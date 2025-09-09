# Multi-stage build for kubectl-resource-usage
FROM golang:1.21-alpine AS builder

# Install necessary packages
RUN apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o kubectl-resource_usage .

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1001 kubectl && \
    adduser -D -s /bin/sh -u 1001 -G kubectl kubectl

# Set working directory
WORKDIR /home/kubectl

# Copy binary from builder stage
COPY --from=builder /app/kubectl-resource_usage /usr/local/bin/kubectl-resource_usage

# Make binary executable
RUN chmod +x /usr/local/bin/kubectl-resource_usage

# Switch to non-root user
USER kubectl

# Set entrypoint
ENTRYPOINT ["kubectl-resource_usage"]
