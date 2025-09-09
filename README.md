# kubectl-resource-usage

A powerful terminal-based user interface (TUI) for monitoring Kubernetes pod resource usage in real-time. This kubectl plugin provides an intuitive dashboard for observing CPU and memory consumption, resource requests/limits, HPA metrics, and cluster events across multiple namespaces.

## Features

- 🔄 **Real-time monitoring** - Live updates of pod resource usage with configurable intervals
- 📊 **Comprehensive metrics** - CPU and memory usage, requests, and limits
- 🎯 **HPA integration** - Horizontal Pod Autoscaler metrics and thresholds
- 🔍 **Event monitoring** - Recent Kubernetes events with color-coded severity
- 🌐 **Multi-namespace support** - Monitor multiple namespaces simultaneously
- 🎨 **Color-coded alerts** - Visual indicators for resource threshold violations
- ⚡ **Performance optimized** - Efficient API calls with caching and concurrent operations
- 🎛️ **Customizable** - Adjustable refresh intervals and display options

## Installation

### Via Krew (Recommended)

First, install [Krew](https://krew.sigs.k8s.io/docs/user-guide/setup/install/) if you haven't already.

```bash
kubectl krew install resource-usage
```

### Manual Installation

1. Download the latest release for your platform from the [releases page](https://github.com/clglavan/kubectl-resource-usage/releases)
2. Extract the binary and place it in your PATH as `kubectl-resource_usage`
3. Make it executable: `chmod +x kubectl-resource_usage`

### From Source

```bash
git clone https://github.com/clglavan/kubectl-resource-usage.git
cd kubectl-resource-usage
go build -o kubectl-resource_usage .
sudo mv kubectl-resource_usage /usr/local/bin/
```

## Usage

### Basic Usage

```bash
# Monitor the default namespace
kubectl resource-usage

# Monitor a specific namespace
kubectl resource-usage -n kube-system

# Monitor multiple namespaces
kubectl resource-usage -n "default,kube-system,monitoring"
```

### Command Line Options

```bash
kubectl resource-usage [OPTIONS]

Options:
  -n, --namespace string     Kubernetes namespace(s) - comma separated for multiple (default "default")
      --kubeconfig string    Path to the kubeconfig file
      --interval int         Refresh interval in seconds (default 2)
      --small-font          Use smaller font for more compact display (default true)
  -h, --help                Show help message
```

### Examples

```bash
# Monitor production namespaces with 5-second refresh
kubectl resource-usage -n "prod-web,prod-api,prod-db" --interval 5

# Monitor all system components
kubectl resource-usage -n "kube-system,kube-public,kube-node-lease"

# Use custom kubeconfig
kubectl resource-usage --kubeconfig ~/.kube/dev-cluster
```

## Dashboard Layout

The TUI dashboard is organized into three main sections:

### 1. Resource Usage Table
Displays comprehensive information for each pod and container:

| Column | Description |
|--------|-------------|
| Pod | Pod name with namespace |
| Container | Container name within the pod |
| CPU Use | Current CPU usage in cores |
| CPU Req | CPU resource request |
| CPU Lim | CPU resource limit |
| Mem Use | Current memory usage in MB |
| Mem Req | Memory resource request |
| Mem Lim | Memory resource limit |
| HPA | Associated Horizontal Pod Autoscaler name |
| Min/Cur/Max | HPA replica counts |
| CPU Thresh/Use% | HPA CPU threshold and current usage |
| Mem Thresh/Use% | HPA memory threshold and current usage |

### 2. Events Section
Shows recent Kubernetes events with:
- 🟡 **Warning events** in yellow
- 🔴 **Error events** in red
- ⚪ **Normal events** in white
- Timestamps and detailed descriptions

### 3. Status Bar
Displays:
- Last update timestamp
- Total pod count
- Monitored namespaces
- Exit instructions

## Color Coding

The dashboard uses intuitive color coding:

- 🔴 **Red**: Resource usage exceeding thresholds or requests
- 🟡 **Yellow**: Container names and warnings
- 🟢 **Green**: Headers and status information
- 🔵 **Cyan**: Pod names
- 🟠 **Orange**: Resource limits

## Keyboard Controls

- **Tab**: Switch focus between table and events
- **Ctrl+C**: Exit the application
- **Esc**: Alternative exit method

## Requirements

### Cluster Requirements
- Kubernetes cluster with metrics-server installed and running
- Access to the cluster via kubectl

### RBAC Permissions
The plugin requires the following permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: resource-usage-viewer
rules:
- apiGroups: [""]
  resources: ["pods", "events"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["apps"]
  resources: ["replicasets"]
  verbs: ["get", "list"]
- apiGroups: ["autoscaling"]
  resources: ["horizontalpodautoscalers"]
  verbs: ["get", "list"]
- apiGroups: ["metrics.k8s.io"]
  resources: ["pods"]
  verbs: ["get", "list"]
```

### System Requirements
- Terminal with color support and Unicode character rendering
- Minimum terminal size: 120x30 for optimal display

## Troubleshooting

### Common Issues

**"Error getting pod metrics"**
- Ensure metrics-server is installed and running
- Check if metrics-server pods are healthy: `kubectl get pods -n kube-system | grep metrics-server`

**"Failed to create clientset"**
- Verify kubeconfig is valid: `kubectl cluster-info`
- Check cluster connectivity and authentication

**"No data displayed"**
- Verify you have pods running in the specified namespace(s)
- Check RBAC permissions for your user/service account

**Display Issues**
- Try adjusting terminal size (minimum 120x30 recommended)
- Use `--small-font` flag for more compact display
- Ensure your terminal supports Unicode and colors

### Performance Tuning

For large clusters, consider:
- Using specific namespaces instead of monitoring all
- Increasing refresh interval: `--interval 5`
- Monitoring fewer namespaces simultaneously

## Development

### Building from Source

```bash
git clone https://github.com/clglavan/kubectl-resource-usage.git
cd kubectl-resource-usage
go mod download
go build -o kubectl-resource_usage .
```

### Cross-platform Builds

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o kubectl-resource_usage-linux-amd64 .

# macOS AMD64
GOOS=darwin GOARCH=amd64 go build -o kubectl-resource_usage-darwin-amd64 .

# Windows AMD64
GOOS=windows GOARCH=amd64 go build -o kubectl-resource_usage-windows-amd64.exe .
```

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Commit your changes: `git commit -m 'Add amazing feature'`
4. Push to the branch: `git push origin feature/amazing-feature`
5. Open a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Built with [tview](https://github.com/rivo/tview) for the terminal UI
- Uses [client-go](https://github.com/kubernetes/client-go) for Kubernetes API interactions
- Inspired by various Kubernetes monitoring tools in the ecosystem

---

`gh release create v0.1.0 --title "Release v0.1.0" --notes "Initial release of kubectl-resource-usage plugin"`
