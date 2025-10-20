package api

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/api/auth"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/api/middleware"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/core"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// Frontend dist files will be embedded here when built
// For development, serve from Vite dev server
var frontendDist embed.FS

// APIServer provides HTTP REST/WebSocket API for the node
// Extends the monitoring server functionality
type APIServer struct {
	ctx              context.Context
	cancel           context.CancelFunc
	server           *http.Server
	listener         net.Listener
	port             string
	logger           *utils.LogsManager
	config           *utils.ConfigManager
	peerManager      *core.PeerManager
	dbManager        *database.SQLiteManager
	keyPair          *crypto.KeyPair
	jwtManager       *middleware.JWTManager
	challengeManager *auth.ChallengeManager
	ed25519Provider  *auth.Ed25519Provider
	startTime        time.Time
	mutex            sync.RWMutex
}

// NewAPIServer creates a new API server instance
func NewAPIServer(
	config *utils.ConfigManager,
	logger *utils.LogsManager,
	peerManager *core.PeerManager,
	dbManager *database.SQLiteManager,
) *APIServer {
	ctx, cancel := context.WithCancel(context.Background())

	// Load node's Ed25519 keypair using centralized path management
	paths := utils.GetAppPaths("")
	keysDir := filepath.Join(paths.DataDir, "keys")
	keyPair, err := crypto.LoadKeys(keysDir)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to load keys for API server: %v", err), "api")
		// Continue without auth capabilities
	}

	// Initialize JWT manager with a secret key from config or generate one
	jwtSecret := config.GetConfigWithDefault("jwt_secret", "change-this-secret-key-in-production")
	jwtManager := middleware.NewJWTManager(jwtSecret, "remote-network-node")

	// Initialize challenge manager (challenges valid for 5 minutes)
	challengeManager := auth.NewChallengeManager(5 * time.Minute)

	// Initialize auth providers
	var ed25519Provider *auth.Ed25519Provider
	if keyPair != nil {
		ed25519Provider = auth.NewEd25519Provider(keyPair)
	}

	return &APIServer{
		ctx:              ctx,
		cancel:           cancel,
		logger:           logger,
		config:           config,
		peerManager:      peerManager,
		dbManager:        dbManager,
		keyPair:          keyPair,
		jwtManager:       jwtManager,
		challengeManager: challengeManager,
		ed25519Provider:  ed25519Provider,
		startTime:        time.Now(),
	}
}

// Start initializes and starts the API server
func (s *APIServer) Start() error {
	// Get API port from config (use dedicated api_port, fallback to 8080)
	apiPort := s.config.GetConfigWithDefault("api_port", "8080")
	s.port = apiPort

	s.logger.Info(fmt.Sprintf("Starting API server on port %s", apiPort), "api")

	// Get fallback ports from config
	fallbackPortsStr := s.config.GetConfigWithDefault("api_fallback_ports", "8081,8082")
	fallbackPorts := parsePortList(fallbackPortsStr)

	// Build ports list: primary port + fallbacks
	ports := append([]string{apiPort}, fallbackPorts...)
	var err error

	for _, port := range ports {
		addr := fmt.Sprintf(":%s", port)
		s.listener, err = net.Listen("tcp", addr)
		if err == nil {
			s.port = port
			s.logger.Info(fmt.Sprintf("API server bound to port %s", port), "api")
			break
		}
	}

	if s.listener == nil {
		return fmt.Errorf("failed to bind API server to any port: %v", err)
	}

	// Create HTTP mux
	mux := http.NewServeMux()

	// Register routes
	s.registerRoutes(mux)

	// Wrap mux with CORS middleware
	handler := middleware.CORSMiddleware(mux)

	// Create HTTP server
	s.server = &http.Server{
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error(fmt.Sprintf("API server error: %v", err), "api")
		}
	}()

	s.logger.Info("API server started successfully", "api")
	return nil
}

// registerRoutes sets up all HTTP routes
func (s *APIServer) registerRoutes(mux *http.ServeMux) {
	// Serve frontend static files (production only)
	// In development, use Vite dev server at http://localhost:5173
	frontendFS, err := fs.Sub(frontendDist, "dist")
	if err == nil {
		mux.Handle("/", http.FileServer(http.FS(frontendFS)))
		s.logger.Info("Serving frontend from embedded files", "api")
	} else {
		s.logger.Info("Frontend not embedded - use Vite dev server at http://localhost:5173", "api")
	}

	// API routes
	mux.HandleFunc("/api/health", s.handleHealth)

	// Auth routes
	mux.HandleFunc("/api/auth/challenge", s.handleGetChallenge) // GET - Request new challenge
	mux.HandleFunc("/api/auth/ed25519", s.handleAuthEd25519)     // POST - Ed25519 authentication

	// Node routes
	mux.HandleFunc("/api/node/status", s.handleNodeStatus)
	mux.HandleFunc("/api/node/restart", s.handleNodeRestart)

	// Peer routes
	mux.HandleFunc("/api/peers", s.handlePeers)

	// Services routes
	mux.HandleFunc("/api/services", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleGetServices(w, r)
		} else if r.Method == http.MethodPost {
			s.handleAddService(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/services/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleGetService(w, r)
		} else if r.Method == http.MethodPut {
			s.handleUpdateService(w, r)
		} else if r.Method == http.MethodDelete {
			s.handleDeleteService(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Blacklist routes
	mux.HandleFunc("/api/blacklist", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleGetBlacklist(w, r)
		} else if r.Method == http.MethodPost {
			s.handleAddToBlacklist(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/blacklist/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.handleRemoveFromBlacklist(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/blacklist/check", s.handleCheckBlacklist)

	// Workflows routes
	mux.HandleFunc("/api/workflows", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleGetWorkflows(w, r)
		} else if r.Method == http.MethodPost {
			s.handleCreateWorkflow(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/workflows/", func(w http.ResponseWriter, r *http.Request) {
		// Check if it's an execute request
		if strings.HasSuffix(r.URL.Path, "/execute") {
			s.handleExecuteWorkflow(w, r)
			return
		}
		// Check if it's a jobs request
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			s.handleGetWorkflowJobs(w, r)
			return
		}

		// Otherwise, handle as CRUD operations
		if r.Method == http.MethodGet {
			s.handleGetWorkflow(w, r)
		} else if r.Method == http.MethodPut {
			s.handleUpdateWorkflow(w, r)
		} else if r.Method == http.MethodDelete {
			s.handleDeleteWorkflow(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// WebSocket endpoint (to be implemented)
	// mux.HandleFunc("/api/ws", s.handleWebSocket)

	s.logger.Debug("API routes registered", "api")
}

// handleHealth returns API health status
func (s *APIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","uptime":%d}`, int64(time.Since(s.startTime).Seconds()))
}

// Stop gracefully shuts down the API server
func (s *APIServer) Stop() error {
	s.logger.Info("Stopping API server", "api")
	s.cancel()

	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}

	return nil
}

// GetPort returns the port the server is listening on
func (s *APIServer) GetPort() string {
	return s.port
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
