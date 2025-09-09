package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
)

func main() {
	var kubeconfig string
	var namespace string
	var namespaces []string
	var refreshInterval int
	var smallFont bool

	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	flag.StringVar(&namespace, "namespace", "", "Kubernetes namespace(s) - comma separated for multiple")
	flag.StringVar(&namespace, "n", "", "Kubernetes namespace(s) - comma separated for multiple (shorthand)")
	flag.IntVar(&refreshInterval, "interval", 2, "Refresh interval in seconds")
	flag.BoolVar(&smallFont, "small-font", true, "Use smaller font (more compact display)")
	flag.Parse()

	// Setup k8s clients with proper kubeconfig loading (like kubectl)
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}

	// This follows the same precedence as kubectl:
	// 1. --kubeconfig flag
	// 2. KUBECONFIG environment variable
	// 3. ~/.kube/config
	// 4. In-cluster config
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading kubeconfig: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure you have a valid kubeconfig file or are running inside a Kubernetes cluster\n")
		os.Exit(1)
	}

	// Get the namespace to use - if not specified, use the current context's namespace
	if namespace == "" {
		namespace, _, err = kubeConfig.Namespace()
		if err != nil || namespace == "" {
			namespace = "default"
		}
	}

	// Parse namespaces from comma-delimited string
	namespaces = strings.Split(strings.TrimSpace(namespace), ",")
	for i, ns := range namespaces {
		namespaces[i] = strings.TrimSpace(ns)
	}

	// Set terminal to use smaller font if requested
	if smallFont {
		// Print escape sequence to reduce font size (only works in some terminals)
		fmt.Print("\033[0;10m")
		defer fmt.Print("\033[0;0m") // Reset on exit
	}

	// Add timeouts to prevent hanging
	config.Timeout = 10 * time.Second

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create clientset: %v\n", err)
		os.Exit(1)
	}

	metricsClient, err := metrics.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create metrics client: %v\n", err)
		os.Exit(1)
	}

	// Create tview application
	app := tview.NewApplication()
	table := tview.NewTable().
		SetBorders(false). // Remove borders for more compact display
		SetFixed(1, 0).
		SetSelectable(false, false). // Remove row highlighting
		SetSeparator('│').           // Use more compact separators
		SetEvaluateAllRows(true)     // Force evaluation of all rows for better display

	// Create status bar for footer information
	statusBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)

	// Create events section
	eventsTextView := tview.NewTextView()
	eventsTextView.SetDynamicColors(true)
	eventsTextView.SetScrollable(true)
	eventsTextView.SetWrap(true)
	eventsTextView.SetTitle(" Events ")
	eventsTextView.SetBorder(true)

	// Create flex layout with table, events, and status bar
	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(table, 0, 2, true).
		AddItem(eventsTextView, 0, 1, false).
		AddItem(statusBar, 1, 0, false)

	// Set header row
	headers := []string{"Pod", "Container", "CPU Use", "CPU Req", "CPU Lim", "Mem Use", "Mem Req", "Mem Lim", "HPA", "Min", "Cur", "Max", "CPU Thresh", "CPU Use%", "Mem Thresh", "Mem Use%"}
	for i, header := range headers {
		cell := tview.NewTableCell(header).
			SetSelectable(false).
			SetTextColor(tcell.ColorGreen).
			SetAttributes(tcell.AttrBold | tcell.AttrUnderline)
		table.SetCell(0, i, cell)
	}

	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cache for HPA data to avoid repeated API calls
	type hpaCache struct {
		data      map[string]*autoscalingv2.HorizontalPodAutoscaler
		lastFetch time.Time
		mutex     sync.RWMutex
	}

	hpaData := &hpaCache{
		data: make(map[string]*autoscalingv2.HorizontalPodAutoscaler),
	}

	// Cache for ReplicaSet to Deployment mappings
	type rsCache struct {
		data      map[string]string // rsName -> deploymentName
		lastFetch time.Time
		mutex     sync.RWMutex
	}

	rsData := &rsCache{
		data: make(map[string]string),
	}

	// Handle Ctrl+C
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
		app.Stop()
		os.Exit(0)
	}()

	// Helper function to get HPA data with caching
	getHPAData := func() map[string]*autoscalingv2.HorizontalPodAutoscaler {
		hpaData.mutex.RLock()
		if time.Since(hpaData.lastFetch) < 30*time.Second && len(hpaData.data) > 0 {
			defer hpaData.mutex.RUnlock()
			return hpaData.data
		}
		hpaData.mutex.RUnlock()

		// Need to refresh HPA data
		hpaData.mutex.Lock()
		defer hpaData.mutex.Unlock()

		// Double check in case another goroutine updated it
		if time.Since(hpaData.lastFetch) < 30*time.Second && len(hpaData.data) > 0 {
			return hpaData.data
		}

		// Create context with timeout for this specific call
		hpaCtx, hpaCancel := context.WithTimeout(ctx, 5*time.Second)
		defer hpaCancel()

		// Fetch HPA data from all namespaces
		newData := make(map[string]*autoscalingv2.HorizontalPodAutoscaler)
		var hpaMutex sync.Mutex
		var wg sync.WaitGroup
		wg.Add(len(namespaces))

		for _, ns := range namespaces {
			go func(namespace string) {
				defer wg.Done()
				hpaList, err := clientset.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(hpaCtx, metav1.ListOptions{})
				if err != nil {
					return // Skip this namespace on error
				}
				hpaMutex.Lock()
				for i, hpa := range hpaList.Items {
					if hpa.Spec.ScaleTargetRef.Kind == "Deployment" || hpa.Spec.ScaleTargetRef.Kind == "ReplicaSet" {
						newData[hpa.Spec.ScaleTargetRef.Name] = &hpaList.Items[i]
					}
				}
				hpaMutex.Unlock()
			}(ns)
		}

		wg.Wait()
		hpaData.data = newData
		hpaData.lastFetch = time.Now()
		return newData
	}

	// Helper function to get ReplicaSet to Deployment mapping with caching
	getRSMapping := func(rsName string) string {
		rsData.mutex.RLock()
		if deployment, exists := rsData.data[rsName]; exists {
			rsData.mutex.RUnlock()
			return deployment
		}
		rsData.mutex.RUnlock()

		// Fetch ReplicaSet data
		rsData.mutex.Lock()
		defer rsData.mutex.Unlock()

		// Double check
		if deployment, exists := rsData.data[rsName]; exists {
			return deployment
		}

		// Create context with timeout for this specific call
		rsCtx, rsCancel := context.WithTimeout(ctx, 3*time.Second)
		defer rsCancel()

		// Try to find the ReplicaSet in any of the namespaces
		var deploymentName string
		for _, ns := range namespaces {
			rs, err := clientset.AppsV1().ReplicaSets(ns).Get(rsCtx, rsName, metav1.GetOptions{})
			if err != nil {
				continue // Try next namespace
			}

			for _, rsOwner := range rs.OwnerReferences {
				if rsOwner.Kind == "Deployment" {
					deploymentName = rsOwner.Name
					break
				}
			}
			if deploymentName != "" {
				break
			}
		}

		rsData.data[rsName] = deploymentName // Cache the result (empty string if not found)
		return deploymentName
	}

	// Helper functions for formatting
	formatCPU := func(cpuStr string) string {
		q, err := resource.ParseQuantity(cpuStr)
		if err != nil {
			return "-"
		}
		return fmt.Sprintf("%.3g", float64(q.MilliValue())/1000)
	}

	formatMem := func(memStr string) string {
		q, err := resource.ParseQuantity(memStr)
		if err != nil {
			return "-"
		}
		return fmt.Sprintf("%.0f", float64(q.Value())/1024/1024)
	}

	// Update function with performance optimizations
	updateData := func() {
		// Use channels for concurrent API calls
		type apiResult struct {
			pods       *v1.PodList
			metrics    *metricsv1beta1.PodMetricsList
			events     *v1.EventList
			podsErr    error
			metricsErr error
			eventsErr  error
		}

		resultChan := make(chan apiResult, 1)

		// Start concurrent API calls for all namespaces
		go func() {
			var result apiResult
			var wg sync.WaitGroup
			wg.Add(len(namespaces) * 3) // pods, metrics, events for each namespace

			// Collect all results
			var allPods []*v1.Pod
			var allMetrics []*metricsv1beta1.PodMetrics
			var allEvents []*v1.Event
			var podsMutex, metricsMutex, eventsMutex sync.Mutex

			// Fetch data from each namespace
			for _, ns := range namespaces {
				// Fetch pods for this namespace
				go func(namespace string) {
					defer wg.Done()
					podsCtx, podsCancel := context.WithTimeout(ctx, 5*time.Second)
					defer podsCancel()
					pods, err := clientset.CoreV1().Pods(namespace).List(podsCtx, metav1.ListOptions{})
					if err != nil {
						if result.podsErr == nil {
							result.podsErr = err
						}
						return
					}
					podsMutex.Lock()
					for i := range pods.Items {
						allPods = append(allPods, &pods.Items[i])
					}
					podsMutex.Unlock()
				}(ns)

				// Fetch metrics for this namespace
				go func(namespace string) {
					defer wg.Done()
					metricsCtx, metricsCancel := context.WithTimeout(ctx, 5*time.Second)
					defer metricsCancel()
					metrics, err := metricsClient.MetricsV1beta1().PodMetricses(namespace).List(metricsCtx, metav1.ListOptions{})
					if err != nil {
						if result.metricsErr == nil {
							result.metricsErr = err
						}
						return
					}
					metricsMutex.Lock()
					for i := range metrics.Items {
						allMetrics = append(allMetrics, &metrics.Items[i])
					}
					metricsMutex.Unlock()
				}(ns)

				// Fetch events for this namespace
				go func(namespace string) {
					defer wg.Done()
					eventsCtx, eventsCancel := context.WithTimeout(ctx, 5*time.Second)
					defer eventsCancel()
					events, err := clientset.CoreV1().Events(namespace).List(eventsCtx, metav1.ListOptions{
						Limit: 100,
					})
					if err != nil {
						if result.eventsErr == nil {
							result.eventsErr = err
						}
						return
					}
					eventsMutex.Lock()
					for i := range events.Items {
						allEvents = append(allEvents, &events.Items[i])
					}
					eventsMutex.Unlock()
				}(ns)
			}

			wg.Wait()

			// Create merged results
			result.pods = &v1.PodList{Items: make([]v1.Pod, len(allPods))}
			for i, pod := range allPods {
				result.pods.Items[i] = *pod
			}

			result.metrics = &metricsv1beta1.PodMetricsList{Items: make([]metricsv1beta1.PodMetrics, len(allMetrics))}
			for i, metric := range allMetrics {
				result.metrics.Items[i] = *metric
			}

			result.events = &v1.EventList{Items: make([]v1.Event, len(allEvents))}
			for i, event := range allEvents {
				result.events.Items[i] = *event
			}
			resultChan <- result
		}()

		// Wait for results with timeout
		select {
		case <-ctx.Done():
			return
		case result := <-resultChan:
			if result.podsErr != nil {
				statusBar.SetText(fmt.Sprintf("[red]Error listing pods: %v", result.podsErr))
				return
			}
			if result.metricsErr != nil {
				statusBar.SetText(fmt.Sprintf("[red]Error getting pod metrics: %v", result.metricsErr))
				return
			}

			// Get HPA data (cached)
			hpaMap := getHPAData()

			// Process results
			pods := result.pods
			podMetricsList := result.metrics

			// Sort pods for consistent display order to prevent glitchy switching
			sort.Slice(pods.Items, func(i, j int) bool {
				// Primary sort: by namespace
				if pods.Items[i].Namespace != pods.Items[j].Namespace {
					return pods.Items[i].Namespace < pods.Items[j].Namespace
				}
				// Secondary sort: by pod name within namespace
				return pods.Items[i].Name < pods.Items[j].Name
			})

			// Build metrics map efficiently
			metricsMap := make(map[string]map[string]struct {
				cpu string
				mem string
			}, len(podMetricsList.Items))

			for _, podMetrics := range podMetricsList.Items {
				containerMap := make(map[string]struct {
					cpu string
					mem string
				}, len(podMetrics.Containers))
				for _, c := range podMetrics.Containers {
					containerMap[c.Name] = struct {
						cpu string
						mem string
					}{
						cpu: c.Usage.Cpu().String(),
						mem: c.Usage.Memory().String(),
					}
				}
				metricsMap[podMetrics.Name] = containerMap
			}

			// Optimize table updates - only clear data rows, keep header
			currentRowCount := table.GetRowCount()
			for clearRow := 1; clearRow < currentRowCount; clearRow++ {
				for col := 0; col < len(headers); col++ {
					table.SetCell(clearRow, col, nil)
				}
			}

			// Add data rows with optimized lookups
			row := 1
			for _, pod := range pods.Items {
				// Find HPA for this pod by checking owner references
				var hpa *autoscalingv2.HorizontalPodAutoscaler
				var hpaName string = "-"
				var minReplicas string = "-"
				var currentReplicas string = "-"
				var maxReplicas string = "-"
				var hpaCpuThreshold string = "-"
				var hpaCpuUsage string = "-"
				var hpaMemThreshold string = "-"
				var hpaMemUsage string = "-"

				// Check owner references to find deployment/replicaset
				for _, owner := range pod.OwnerReferences {
					if owner.Kind == "ReplicaSet" {
						// Use cached ReplicaSet lookup
						deploymentName := getRSMapping(owner.Name)
						if deploymentName != "" {
							if foundHPA, exists := hpaMap[deploymentName]; exists {
								hpa = foundHPA
								hpaName = foundHPA.Name
							}
						}
					} else if owner.Kind == "Deployment" {
						if foundHPA, exists := hpaMap[owner.Name]; exists {
							hpa = foundHPA
							hpaName = foundHPA.Name
						}
					}
				}

				// Extract HPA details if found (same as before but more efficient)
				if hpa != nil {
					if hpa.Spec.MinReplicas != nil {
						minReplicas = fmt.Sprintf("%d", *hpa.Spec.MinReplicas)
					}
					currentReplicas = fmt.Sprintf("%d", hpa.Status.CurrentReplicas)
					maxReplicas = fmt.Sprintf("%d", hpa.Spec.MaxReplicas)

					// Extract CPU and Memory metrics from HPA spec
					for _, metric := range hpa.Spec.Metrics {
						if metric.Type == autoscalingv2.ResourceMetricSourceType && metric.Resource != nil {
							if metric.Resource.Name == "cpu" && metric.Resource.Target.Type == autoscalingv2.UtilizationMetricType && metric.Resource.Target.AverageUtilization != nil {
								hpaCpuThreshold = fmt.Sprintf("%d%%", *metric.Resource.Target.AverageUtilization)
							}
							if metric.Resource.Name == "memory" && metric.Resource.Target.Type == autoscalingv2.UtilizationMetricType && metric.Resource.Target.AverageUtilization != nil {
								hpaMemThreshold = fmt.Sprintf("%d%%", *metric.Resource.Target.AverageUtilization)
							}
						}
					}

					// Extract current CPU and Memory usage from HPA status
					for _, currentMetric := range hpa.Status.CurrentMetrics {
						if currentMetric.Type == autoscalingv2.ResourceMetricSourceType && currentMetric.Resource != nil {
							if currentMetric.Resource.Name == "cpu" && currentMetric.Resource.Current.AverageUtilization != nil {
								hpaCpuUsage = fmt.Sprintf("%d%%", *currentMetric.Resource.Current.AverageUtilization)
							}
							if currentMetric.Resource.Name == "memory" && currentMetric.Resource.Current.AverageUtilization != nil {
								hpaMemUsage = fmt.Sprintf("%d%%", *currentMetric.Resource.Current.AverageUtilization)
							}
						}
					}
				}

				for _, c := range pod.Spec.Containers {
					usage, ok := metricsMap[pod.Name][c.Name]
					cpuUsage := "-"
					memUsage := "-"
					if ok {
						cpuUsage = formatCPU(usage.cpu)
						memUsage = formatMem(usage.mem)
					}

					cpuReq := "-"
					memReq := "-"
					cpuLim := "-"
					memLim := "-"

					if !c.Resources.Requests.Cpu().IsZero() {
						cpuReq = formatCPU(c.Resources.Requests.Cpu().String())
					}
					if !c.Resources.Requests.Memory().IsZero() {
						memReq = formatMem(c.Resources.Requests.Memory().String())
					}
					if !c.Resources.Limits.Cpu().IsZero() {
						cpuLim = formatCPU(c.Resources.Limits.Cpu().String())
					}
					if !c.Resources.Limits.Memory().IsZero() {
						memLim = formatMem(c.Resources.Limits.Memory().String())
					}

					// Add cells with conditional styling (reuse existing logic)
					// Pod name with namespace (cyan)
					podNameWithNS := fmt.Sprintf("%s (%s)", pod.Name, pod.Namespace)
					table.SetCell(row, 0, tview.NewTableCell(podNameWithNS).
						SetTextColor(tcell.ColorAqua).
						SetAttributes(tcell.AttrBold))
					// Container name (yellow)
					table.SetCell(row, 1, tview.NewTableCell(c.Name).
						SetTextColor(tcell.ColorYellow))

					// CPU Usage with conditional styling
					cpuUsageCell := tview.NewTableCell(cpuUsage)
					if cpuUsage != "-" && cpuReq != "-" {
						// Parse usage and request to compare
						usageQ, usageErr := resource.ParseQuantity(usage.cpu)
						reqQ, reqErr := resource.ParseQuantity(c.Resources.Requests.Cpu().String())
						if usageErr == nil && reqErr == nil && !reqQ.IsZero() {
							usageMillis := float64(usageQ.MilliValue())
							reqMillis := float64(reqQ.MilliValue())
							if usageMillis >= reqMillis*0.95 {
								cpuUsageCell.SetTextColor(tcell.ColorRed)
							}
						}
					}
					table.SetCell(row, 2, cpuUsageCell)

					// CPU Request
					table.SetCell(row, 3, tview.NewTableCell(cpuReq))

					// CPU Limit with conditional styling
					cpuLimitCell := tview.NewTableCell(cpuLim)
					if !c.Resources.Limits.Cpu().IsZero() {
						cpuLimitCell.SetTextColor(tcell.ColorLightYellow)
					}
					table.SetCell(row, 4, cpuLimitCell)

					// Memory Usage with conditional styling
					memUsageCell := tview.NewTableCell(memUsage)
					if memUsage != "-" && memReq != "-" {
						// Parse usage and request to compare
						usageQ, usageErr := resource.ParseQuantity(usage.mem)
						reqQ, reqErr := resource.ParseQuantity(c.Resources.Requests.Memory().String())
						if usageErr == nil && reqErr == nil && !reqQ.IsZero() {
							usageBytes := float64(usageQ.Value())
							reqBytes := float64(reqQ.Value())
							if usageBytes >= reqBytes*0.95 {
								memUsageCell.SetTextColor(tcell.ColorRed)
							}
						}
					}
					table.SetCell(row, 5, memUsageCell)

					// Memory Request
					table.SetCell(row, 6, tview.NewTableCell(memReq))

					// Memory Limit
					table.SetCell(row, 7, tview.NewTableCell(memLim))

					// HPA columns
					table.SetCell(row, 8, tview.NewTableCell(hpaName))          // HPA Name
					table.SetCell(row, 9, tview.NewTableCell(minReplicas))      // Min Replicas
					table.SetCell(row, 10, tview.NewTableCell(currentReplicas)) // Current Replicas
					table.SetCell(row, 11, tview.NewTableCell(maxReplicas))     // Max Replicas

					// CPU Threshold and Usage with conditional styling
					cpuThresholdCell := tview.NewTableCell(hpaCpuThreshold)
					cpuUsageHPACell := tview.NewTableCell(hpaCpuUsage)

					// Check if CPU usage exceeds threshold
					if hpaCpuThreshold != "-" && hpaCpuUsage != "-" {
						// Parse threshold and usage percentages
						var thresholdVal, usageVal int
						if n, err := fmt.Sscanf(hpaCpuThreshold, "%d%%", &thresholdVal); n == 1 && err == nil {
							if n, err := fmt.Sscanf(hpaCpuUsage, "%d%%", &usageVal); n == 1 && err == nil {
								if usageVal >= thresholdVal {
									cpuThresholdCell.SetTextColor(tcell.ColorRed).SetAttributes(tcell.AttrBold)
									cpuUsageHPACell.SetTextColor(tcell.ColorRed).SetAttributes(tcell.AttrBold)
								}
							}
						}
					}

					// Memory Threshold and Usage with conditional styling
					memThresholdCell := tview.NewTableCell(hpaMemThreshold)
					memUsageHPACell := tview.NewTableCell(hpaMemUsage)

					// Check if Memory usage exceeds threshold
					if hpaMemThreshold != "-" && hpaMemUsage != "-" {
						// Parse threshold and usage percentages
						var thresholdVal, usageVal int
						if n, err := fmt.Sscanf(hpaMemThreshold, "%d%%", &thresholdVal); n == 1 && err == nil {
							if n, err := fmt.Sscanf(hpaMemUsage, "%d%%", &usageVal); n == 1 && err == nil {
								if usageVal >= thresholdVal {
									memThresholdCell.SetTextColor(tcell.ColorRed).SetAttributes(tcell.AttrBold)
									memUsageHPACell.SetTextColor(tcell.ColorRed).SetAttributes(tcell.AttrBold)
								}
							}
						}
					}

					table.SetCell(row, 12, cpuThresholdCell) // CPU Threshold
					table.SetCell(row, 13, cpuUsageHPACell)  // CPU Usage
					table.SetCell(row, 14, memThresholdCell) // Memory Threshold
					table.SetCell(row, 15, memUsageHPACell)  // Memory Usage

					row++
				}
			}

			// Update status bar with timestamp
			namespacesStr := strings.Join(namespaces, ",")
			statusText := fmt.Sprintf("[yellow]Last updated: [green]%s [yellow]| [green]%d [yellow]pods in namespaces [green]%s [yellow]| Press [green]Ctrl+C [yellow]to exit",
				time.Now().Format("15:04:05"), len(pods.Items), namespacesStr)
			statusBar.SetText(statusText)

			// Update events if available
			if result.events != nil && result.eventsErr == nil {
				// Sort events by timestamp (most recent first)
				events := result.events.Items
				sort.Slice(events, func(i, j int) bool {
					// Use LastTimestamp if available, otherwise FirstTimestamp
					timeI := events[i].LastTimestamp.Time
					if timeI.IsZero() {
						timeI = events[i].FirstTimestamp.Time
					}
					timeJ := events[j].LastTimestamp.Time
					if timeJ.IsZero() {
						timeJ = events[j].FirstTimestamp.Time
					}
					return timeI.After(timeJ) // Most recent first
				})

				// Update events section title with count
				eventsTextView.SetTitle(fmt.Sprintf(" Events (%d) ", len(events)))

				var eventsText string
				for _, event := range events {
					// Color based on event type
					color := "[white]"
					switch event.Type {
					case "Warning":
						color = "[yellow]"
					case "Error":
						color = "[red]"
					default:
						color = "[white]"
					}

					// Format: [POD/OBJECT] [TIME] TYPE: REASON - MESSAGE
					objectName := event.InvolvedObject.Name
					if objectName == "" {
						objectName = "unknown"
					}

					// Use LastTimestamp if available, otherwise FirstTimestamp
					eventTime := event.LastTimestamp.Time
					if eventTime.IsZero() {
						eventTime = event.FirstTimestamp.Time
					}

					eventLine := fmt.Sprintf("%s[%s/%s] [%s] %s: %s - %s[white]\n",
						color,
						objectName,
						event.Namespace,
						eventTime.Format("15:04:05"),
						event.Type,
						event.Reason,
						event.Message)
					eventsText += eventLine
				}
				if eventsText == "" {
					eventsText = "[dim]No recent events in these namespaces[white]"
				}
				eventsTextView.SetText(eventsText)
			}
		}
	}

	// First update
	updateData()

	// Schedule regular updates with better error handling
	go func() {
		ticker := time.NewTicker(time.Duration(refreshInterval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Use a separate goroutine for updates to prevent blocking
				go func() {
					// Create a timeout context for this specific update
					updateCtx, updateCancel := context.WithTimeout(ctx, 20*time.Second)
					defer updateCancel()

					// Replace the global context with the update context temporarily
					originalCtx := ctx
					ctx = updateCtx

					app.QueueUpdateDraw(func() {
						updateData()
					})

					// Restore original context
					ctx = originalCtx
				}()
			}
		}
	}()

	// Improved key handling with better signal management
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			cancel()
			app.Stop()
			// Give some time for cleanup
			time.Sleep(100 * time.Millisecond)
			os.Exit(0)
		case tcell.KeyEscape:
			// Alternative exit method
			cancel()
			app.Stop()
			time.Sleep(100 * time.Millisecond)
			os.Exit(0)
		case tcell.KeyTab:
			// Switch focus between table and events
			current := app.GetFocus()
			if current == table {
				app.SetFocus(eventsTextView)
			} else {
				app.SetFocus(table)
			}
			return nil // Consume the event
		}
		return event
	})

	// No initial footer setup - let updateData handle all table content

	// Run the application with the flex layout (table + status bar)
	if err := app.SetRoot(flex, true).EnableMouse(false).Run(); err != nil {
		fmt.Printf("Error running application: %v\n", err)
		os.Exit(1)
	}
}
