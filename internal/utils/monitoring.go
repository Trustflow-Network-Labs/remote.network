package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type MonitoringServer struct {
	ctx       context.Context
	cancel    context.CancelFunc
	server    *http.Server
	listener  net.Listener
	port      string
	startTime time.Time
	logger    *LogsManager
	config    *ConfigManager

	// Monitoring metrics
	requestCount    int64
	errorCount      int64
	goroutineCount  int64
	memStats        sync.RWMutex
	lastMemStats    runtime.MemStats
	lastStatsUpdate time.Time
}

type ResourceStats struct {
	Timestamp              string  `json:"timestamp"`
	Goroutines             int     `json:"goroutines"`
	GoroutinesActive       int     `json:"goroutines_active"`
	GoroutinesUsagePercent int     `json:"goroutines_usage_percent"`
	HeapAllocBytes         uint64  `json:"heap_alloc_bytes"`
	HeapInuseBytes         uint64  `json:"heap_inuse_bytes"`
	HeapSysBytes           uint64  `json:"heap_sys_bytes"`
	HeapObjects            uint64  `json:"heap_objects"`
	StackInuseBytes        uint64  `json:"stack_inuse_bytes"`
	StackSysBytes          uint64  `json:"stack_sys_bytes"`
	MSpanInuseBytes        uint64  `json:"mspan_inuse_bytes"`
	MCacheInuseBytes       uint64  `json:"mcache_inuse_bytes"`
	GCSys                  uint64  `json:"gc_sys_bytes"`
	NextGC                 uint64  `json:"next_gc_bytes"`
	LastGC                 string  `json:"last_gc"`
	NumGC                  uint32  `json:"num_gc"`
	NumForcedGC            uint32  `json:"num_forced_gc"`
	GCCPUFraction          float64 `json:"gc_cpu_fraction"`
	ActiveStreams          int     `json:"active_streams"`
	ServiceOffers          int     `json:"service_offers"`
	RelayCircuits          int     `json:"relay_circuits"`
	UptimeSeconds          int64   `json:"uptime_seconds"`
	RequestCount           int64   `json:"request_count"`
	ErrorCount             int64   `json:"error_count"`
}

type HealthStatus struct {
	Status      string `json:"status"`
	HostRunning bool   `json:"host_running"`
	Uptime      string `json:"uptime"`
	Port        string `json:"port"`
	Timestamp   string `json:"timestamp"`
	Version     string `json:"version,omitempty"`
}

func NewMonitoringServer(config *ConfigManager, logger *LogsManager) *MonitoringServer {
	ctx, cancel := context.WithCancel(context.Background())

	return &MonitoringServer{
		ctx:             ctx,
		cancel:          cancel,
		startTime:       time.Now(),
		logger:          logger,
		config:          config,
		lastStatsUpdate: time.Now(),
	}
}

// parsePortList parses a comma-separated list of ports
func parsePortList(portList string) []string {
	if portList == "" {
		return []string{}
	}
	ports := strings.Split(portList, ",")
	result := make([]string, 0, len(ports))
	for _, port := range ports {
		port = strings.TrimSpace(port)
		if port != "" {
			result = append(result, port)
		}
	}
	return result
}

func (ms *MonitoringServer) Start() error {
	// Get pprof port from config or use default
	pprofPort := ms.config.GetConfigWithDefault("pprof_port", "6060")
	ms.port = pprofPort

	ms.logger.Info(fmt.Sprintf("Starting monitoring server on port %s", pprofPort), "monitoring")

	// Get fallback ports from config
	fallbackPortsStr := ms.config.GetConfigWithDefault("pprof_fallback_ports", "6061,6062")
	fallbackPorts := parsePortList(fallbackPortsStr)

	// Build ports list: primary port + fallbacks
	ports := append([]string{pprofPort}, fallbackPorts...)
	var err error

	// Create custom mux for our endpoints
	mux := http.NewServeMux()

	// Add pprof endpoints manually since we're using a custom mux
	mux.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&ms.requestCount, 1)
		http.DefaultServeMux.ServeHTTP(w, r)
	})

	// Add resource stats endpoint
	mux.HandleFunc("/stats/resources", ms.handleResourceStats)

	// Add goroutine leak report endpoint
	mux.HandleFunc("/stats/leaks", ms.handleLeakReport)

	// Add health check endpoint
	mux.HandleFunc("/health", ms.handleHealth)

	// Add metrics endpoint
	mux.HandleFunc("/metrics", ms.handleMetrics)

	// Try ports in sequence
	for i, port := range ports {
		ms.listener, err = net.Listen("tcp", ":"+port)
		if err != nil {
			if i < len(ports)-1 {
				ms.logger.Warn(fmt.Sprintf("monitoring port %s unavailable, trying next port: %v", port, err), "monitoring")
				continue
			} else {
				ms.logger.Error(fmt.Sprintf("All monitoring ports failed, last error: %v", err), "monitoring")
				return fmt.Errorf("failed to bind to any monitoring port: %v", err)
			}
		}

		ms.port = port
		ms.logger.Info(fmt.Sprintf("HTTP monitoring server successfully bound to port %s", port), "monitoring")
		ms.logger.Info(fmt.Sprintf("Available endpoints: /debug/pprof/, /stats/resources, /stats/leaks, /health, /metrics on port %s", port), "monitoring")
		break
	}

	// Create HTTP server
	ms.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	// Start server in goroutine
	go func() {
		if err := ms.server.Serve(ms.listener); err != nil && err != http.ErrServerClosed {
			ms.logger.Error(fmt.Sprintf("Monitoring server error: %v", err), "monitoring")
			atomic.AddInt64(&ms.errorCount, 1)
		}
	}()

	// Start background metrics collection
	go ms.collectMetrics()

	ms.logger.Info(fmt.Sprintf("Monitoring server started successfully on port %s", ms.port), "monitoring")
	return nil
}

func (ms *MonitoringServer) Stop() error {
	ms.logger.Info("Stopping monitoring server...", "monitoring")

	// Cancel context
	ms.cancel()

	// Shutdown HTTP server with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if ms.server != nil {
		if err := ms.server.Shutdown(ctx); err != nil {
			ms.logger.Warn(fmt.Sprintf("Error shutting down monitoring server: %v", err), "monitoring")
			return err
		}
	}

	ms.logger.Info("Monitoring server stopped successfully", "monitoring")
	return nil
}

func (ms *MonitoringServer) GetPort() string {
	return ms.port
}

func (ms *MonitoringServer) collectMetrics() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ms.ctx.Done():
			return
		case <-ticker.C:
			ms.updateMemStats()
		}
	}
}

func (ms *MonitoringServer) updateMemStats() {
	ms.memStats.Lock()
	defer ms.memStats.Unlock()

	runtime.ReadMemStats(&ms.lastMemStats)
	ms.lastStatsUpdate = time.Now()
	atomic.StoreInt64(&ms.goroutineCount, int64(runtime.NumGoroutine()))
}

func (ms *MonitoringServer) handleResourceStats(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&ms.requestCount, 1)
	w.Header().Set("Content-Type", "application/json")

	// Get current memory stats
	ms.memStats.RLock()
	memStats := ms.lastMemStats
	ms.memStats.RUnlock()

	// Calculate uptime
	uptime := time.Since(ms.startTime)

	// Get current goroutine count
	goroutines := runtime.NumGoroutine()

	// Calculate goroutine usage percentage (relative to a reasonable max)
	maxGoroutines := 1000 // Configurable threshold
	goroutineUsagePercent := (goroutines * 100) / maxGoroutines
	if goroutineUsagePercent > 100 {
		goroutineUsagePercent = 100
	}

	stats := ResourceStats{
		Timestamp:              time.Now().Format(time.RFC3339),
		Goroutines:             goroutines,
		GoroutinesActive:       goroutines, // For our purposes, all are considered active
		GoroutinesUsagePercent: goroutineUsagePercent,
		HeapAllocBytes:         memStats.HeapAlloc,
		HeapInuseBytes:         memStats.HeapInuse,
		HeapSysBytes:           memStats.HeapSys,
		HeapObjects:            memStats.HeapObjects,
		StackInuseBytes:        memStats.StackInuse,
		StackSysBytes:          memStats.StackSys,
		MSpanInuseBytes:        memStats.MSpanInuse,
		MCacheInuseBytes:       memStats.MCacheInuse,
		GCSys:                  memStats.GCSys,
		NextGC:                 memStats.NextGC,
		LastGC:                 time.Unix(0, int64(memStats.LastGC)).Format(time.RFC3339),
		NumGC:                  memStats.NumGC,
		NumForcedGC:            memStats.NumForcedGC,
		GCCPUFraction:          memStats.GCCPUFraction,
		ActiveStreams:          0, // Will be updated when we implement P2P manager integration
		ServiceOffers:          0, // Will be updated when we implement P2P manager integration
		RelayCircuits:          0, // Will be updated when we implement P2P manager integration
		UptimeSeconds:          int64(uptime.Seconds()),
		RequestCount:           atomic.LoadInt64(&ms.requestCount),
		ErrorCount:             atomic.LoadInt64(&ms.errorCount),
	}

	if err := json.NewEncoder(w).Encode(stats); err != nil {
		atomic.AddInt64(&ms.errorCount, 1)
		ms.logger.Error(fmt.Sprintf("Failed to encode resource stats: %v", err), "monitoring")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (ms *MonitoringServer) handleLeakReport(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&ms.requestCount, 1)
	w.Header().Set("Content-Type", "text/plain")

	report := ms.generateGoroutineLeakReport()
	w.Write([]byte(report))
}

func (ms *MonitoringServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&ms.requestCount, 1)
	w.Header().Set("Content-Type", "application/json")

	uptime := time.Since(ms.startTime)

	health := HealthStatus{
		Status:      "ok",
		HostRunning: true,
		Uptime:      uptime.String(),
		Port:        ms.port,
		Timestamp:   time.Now().Format(time.RFC3339),
		Version:     "remote-network-v1.0", // Could be made configurable
	}

	if err := json.NewEncoder(w).Encode(health); err != nil {
		atomic.AddInt64(&ms.errorCount, 1)
		ms.logger.Error(fmt.Sprintf("Failed to encode health status: %v", err), "monitoring")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (ms *MonitoringServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&ms.requestCount, 1)
	w.Header().Set("Content-Type", "text/plain")

	// Simple Prometheus-style metrics
	metrics := fmt.Sprintf(`# HELP remote_network_goroutines Current number of goroutines
# TYPE remote_network_goroutines gauge
remote_network_goroutines %d

# HELP remote_network_heap_bytes Current heap memory in bytes
# TYPE remote_network_heap_bytes gauge
remote_network_heap_bytes %d

# HELP remote_network_requests_total Total number of HTTP requests
# TYPE remote_network_requests_total counter
remote_network_requests_total %d

# HELP remote_network_errors_total Total number of errors
# TYPE remote_network_errors_total counter
remote_network_errors_total %d

# HELP remote_network_uptime_seconds Uptime in seconds
# TYPE remote_network_uptime_seconds gauge
remote_network_uptime_seconds %d
`,
		runtime.NumGoroutine(),
		ms.lastMemStats.HeapAlloc,
		atomic.LoadInt64(&ms.requestCount),
		atomic.LoadInt64(&ms.errorCount),
		int64(time.Since(ms.startTime).Seconds()),
	)

	w.Write([]byte(metrics))
}

func (ms *MonitoringServer) generateGoroutineLeakReport() string {
	// Get goroutine stack dump
	buf := make([]byte, 1<<16)
	stackSize := runtime.Stack(buf, true)

	report := "=== GOROUTINE LEAK REPORT ===\n"
	report += fmt.Sprintf("Generated at: %s\n", time.Now().Format(time.RFC3339))
	report += fmt.Sprintf("Total goroutines: %d\n", runtime.NumGoroutine())
	report += fmt.Sprintf("Uptime: %s\n\n", time.Since(ms.startTime).String())

	report += "=== GOROUTINE STACK TRACES ===\n"
	report += string(buf[:stackSize])

	return report
}
