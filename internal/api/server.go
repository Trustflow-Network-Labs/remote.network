package api

import (
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/api/auth"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/api/events"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/api/middleware"
	ws "github.com/Trustflow-Network-Labs/remote-network-node/internal/api/websocket"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/chat"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/core"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/dependencies"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/payment"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/services"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
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
	httpServer       *http.Server // HTTP server for localhost access
	httpListener     net.Listener // HTTP listener for localhost
	httpPort         string       // HTTP port for localhost
	logger           *utils.LogsManager
	config           *utils.ConfigManager
	peerManager      *core.PeerManager
	dbManager        *database.SQLiteManager
	keyPair          *crypto.KeyPair
	jwtManager       *middleware.JWTManager
	challengeManager *auth.ChallengeManager
	ed25519Provider  *auth.Ed25519Provider
	wsHub            *ws.Hub
	wsLogger         *logrus.Logger
	wsUpgrader       websocket.Upgrader
	eventEmitter       *events.Emitter
	fileProcessor      *services.FileProcessor
	gitService         *services.GitService
	dockerService      *services.DockerService
	standaloneService  *services.StandaloneService
	walletManager      *payment.WalletManager
	invoiceManager     *payment.InvoiceManager
	chatManager        *chat.ChatManager
	localPeerID        string
	startTime          time.Time
}

// NewAPIServer creates a new API server instance
func NewAPIServer(
	config *utils.ConfigManager,
	logger *utils.LogsManager,
	peerManager *core.PeerManager,
	dbManager *database.SQLiteManager,
	keyPair *crypto.KeyPair,
) *APIServer {
	ctx, cancel := context.WithCancel(context.Background())

	// Use keyPair loaded from encrypted keystore (passed from start command)
	logger.Info(fmt.Sprintf("API server using keyPair for peer_id: %s", keyPair.PeerID()), "api")

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

	// Initialize WebSocket hub (use separate logrus logger for WebSocket)
	wsLogger := logrus.New()
	// Use configured log level instead of hardcoded InfoLevel
	logLevelStr := config.GetConfigWithDefault("log_level", "info")
	logLevel, err := logrus.ParseLevel(logLevelStr)
	if err != nil {
		logger.Warn(fmt.Sprintf("Invalid log level '%s', defaulting to 'info'", logLevelStr), "server")
		logLevel = logrus.InfoLevel
	}
	wsLogger.SetLevel(logLevel)
	wsLogger.SetFormatter(&logrus.JSONFormatter{})
	// Write WebSocket logs to the main log file instead of CLI
	wsLogger.SetOutput(logger.File)
	wsHub := ws.NewHub(wsLogger)

	// Configure WebSocket upgrader
	wsUpgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			// Allow all origins for development
			// TODO: Restrict in production based on config
			return true
		},
	}

	// Initialize event emitter
	eventEmitter := events.NewEmitter(wsHub, peerManager, dbManager, wsLogger)

	// Set event emitter on job and workflow managers for real-time updates
	if jobManager := peerManager.GetJobManager(); jobManager != nil {
		jobManager.SetEventEmitter(eventEmitter)
		logger.Info("Event emitter set for JobManager", "api")
	}
	if workflowManager := peerManager.GetWorkflowManager(); workflowManager != nil {
		workflowManager.SetEventEmitter(eventEmitter)
		logger.Info("Event emitter set for WorkflowManager", "api")
	}

	// Get app paths for proper storage locations
	appPaths := utils.GetAppPaths("")

	// Initialize file processor
	fileProcessor := services.NewFileProcessor(dbManager, logger, config, appPaths)

	// Initialize Git service for Docker service creation from Git repos
	gitService := services.NewGitService(logger, config, appPaths)
	logger.Info("Git service initialized for Docker service creation", "api")

	// Initialize dependency manager for Docker service
	depManager := dependencies.NewDependencyManager(config, logger)
	logger.Info("Dependency manager initialized for Docker operations", "api")

	// Initialize Docker service
	dockerService := services.NewDockerService(dbManager, logger, config, appPaths, depManager, gitService)
	logger.Info("Docker service initialized", "api")

	// Set event broadcaster for real-time Docker operation updates
	dockerService.SetEventBroadcaster(eventEmitter)
	logger.Info("Event broadcaster set for Docker service", "api")

	// Initialize Standalone service
	standaloneService := services.NewStandaloneService(dbManager, logger, config, appPaths, gitService)
	logger.Info("Standalone service initialized", "api")

	// Set event broadcaster for real-time Standalone operation updates
	standaloneService.SetEventBroadcaster(eventEmitter)
	logger.Info("Event broadcaster set for Standalone service", "api")

	// Initialize file upload handler
	fileUploadHandler := ws.NewFileUploadHandler(dbManager, logger, config, appPaths)

	// Set file upload handler in hub
	wsHub.SetFileUploadHandler(fileUploadHandler)

	// Set callback for upload completion to trigger file processing
	fileUploadHandler.SetOnUploadCompleteCallback(func(uploadGroupID string, serviceID int64) {
		logger.Info(fmt.Sprintf("Triggering file processing for upload group %s, service %d", uploadGroupID, serviceID), "api")
		if err := fileProcessor.ProcessUploadedFile(uploadGroupID, serviceID); err != nil {
			logger.Error(fmt.Sprintf("Failed to process uploaded files: %v", err), "api")
		} else {
			// Broadcast service update after successful processing
			eventEmitter.BroadcastServiceUpdate()
			logger.Info(fmt.Sprintf("File processing completed for service %d", serviceID), "api")
		}
	})

	// Initialize wallet manager with database support (for default wallet + payment compatibility checks)
	walletManager, err := payment.NewWalletManagerWithDB(config, dbManager)
	if err != nil {
		logger.Warn(fmt.Sprintf("Failed to initialize wallet manager: %v", err), "api_server")
	} else {
		logger.Info("Wallet manager initialized with database support", "api")
	}

	// Initialize service search handler for remote service discovery (with wallet manager for payment compatibility)
	serviceSearchHandler := ws.NewServiceSearchHandler(wsLogger, dbManager, peerManager.GetKnownPeers(), peerManager.GetQUIC(), peerManager.GetMetadataQuery(), peerManager.GetDHT(), peerManager.GetRelayPeer(), walletManager, peerManager.GetPeerID())
	wsHub.SetServiceSearchHandler(serviceSearchHandler)
	logger.Info("Service search handler initialized for remote service discovery", "api")

	// Initialize X402 client for escrow payments
	x402Client := payment.NewX402Client(config, logger)
	logger.Info("X402 client initialized", "api")

	// Initialize escrow manager (required for invoice manager)
	escrowManager := payment.NewEscrowManager(dbManager, x402Client, config, logger)
	logger.Info("Escrow manager initialized", "api")

	// Initialize invoice manager
	invoiceManager := payment.NewInvoiceManager(
		dbManager,
		walletManager,
		escrowManager,
		logger,
		config,
	)
	logger.Info("Invoice manager initialized", "api")

	// Setup invoice handler and connect to QUIC peer
	if err := peerManager.SetupInvoiceHandler(invoiceManager, eventEmitter); err != nil {
		logger.Error(fmt.Sprintf("Failed to setup invoice handler: %v", err), "api")
	} else {
		logger.Info("Invoice handler setup complete", "api")
	}

	// Initialize chat manager
	chatManager := chat.NewChatManager(
		dbManager,
		logger,
		config,
		keyPair.PeerID(),
	)
	logger.Info("Chat manager initialized", "api")

	// Setup chat handler and connect to QUIC peer
	// Pass event emitter adapter to bridge events to WebSocket
	chatEventAdapter := events.NewChatEmitterAdapter(eventEmitter)
	if err := peerManager.SetupChatHandler(chatManager, chatEventAdapter); err != nil {
		logger.Error(fmt.Sprintf("Failed to setup chat handler: %v", err), "api")
	} else {
		logger.Info("Chat handler setup complete", "api")
	}

	return &APIServer{
		ctx:               ctx,
		cancel:            cancel,
		logger:            logger,
		config:            config,
		peerManager:       peerManager,
		dbManager:         dbManager,
		keyPair:           keyPair,
		jwtManager:        jwtManager,
		challengeManager:  challengeManager,
		ed25519Provider:   ed25519Provider,
		wsHub:             wsHub,
		wsLogger:          wsLogger,
		wsUpgrader:        wsUpgrader,
		eventEmitter:      eventEmitter,
		fileProcessor:     fileProcessor,
		gitService:        gitService,
		dockerService:     dockerService,
		standaloneService: standaloneService,
		walletManager:     walletManager,
		invoiceManager:    invoiceManager,
		chatManager:       chatManager,
		localPeerID:       keyPair.PeerID(),
		startTime:         time.Now(),
	}
}

// Start initializes and starts the API server
func (s *APIServer) Start() error {
	// Get API port from config (use dedicated api_port, fallback to 30069)
	apiPort := s.config.GetConfigWithDefault("api_port", "30069")
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

	// Check if HTTPS is enabled
	useHTTPS := s.config.GetConfigBool("api_use_https", true)

	// Create HTTP server with TLS configuration
	// Note: WriteTimeout is set to 1 hour to accommodate long-running operations like Docker builds
	// This is a single-user application, so we can be generous with timeouts
	s.server = &http.Server{
		Handler:      handler,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 1 * time.Hour, // Allow for very long Docker builds
		IdleTimeout:  10 * time.Minute,
		ErrorLog:     log.New(s.logger.File, "", 0), // Redirect errors to log file, not stdout
	}

	// Configure TLS if HTTPS is enabled
	if useHTTPS {
		paths := utils.GetAppPaths("")

		// Load or generate ECDSA certificates for API server
		// (browsers don't support Ed25519, so we use ECDSA P-256)
		cert, err := loadOrGenerateAPICertificates(paths, s.logger)
		if err != nil {
			return fmt.Errorf("failed to load/generate API TLS certificates: %v", err)
		}

		// Configure TLS with modern settings
		s.server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,           // Minimum TLS 1.2 for browser compatibility
			NextProtos:   []string{"h2", "http/1.1"}, // Enable HTTP/2
		}

		s.logger.Info("Configured HTTPS with ECDSA P-256 TLS 1.2+", "api")
	}

	// Start WebSocket hub
	go s.wsHub.Run()
	s.logger.Info("WebSocket hub started", "api")

	// Start event emitter
	s.eventEmitter.Start()
	s.logger.Info("Event emitter started", "api")

	// Start server in goroutine
	go func() {
		var serveErr error
		if useHTTPS {
			// Use HTTPS (certificates already loaded in TLSConfig)
			s.logger.Info("Starting HTTPS server with ECDSA P-256 TLS 1.2+", "api")
			serveErr = s.server.ServeTLS(s.listener, "", "") // Empty strings - certs in TLSConfig
		} else {
			// Use HTTP (not recommended for production)
			s.logger.Warn("Starting HTTP server (HTTPS disabled - not recommended for production)", "api")
			serveErr = s.server.Serve(s.listener)
		}

		if serveErr != nil && serveErr != http.ErrServerClosed {
			s.logger.Error(fmt.Sprintf("API server error: %v", serveErr), "api")
		}
	}()

	protocol := "HTTP"
	if useHTTPS {
		protocol = "HTTPS (ECDSA P-256)"
	}
	s.logger.Info(fmt.Sprintf("API server started successfully (%s)", protocol), "api")

	// Start HTTP localhost server if enabled
	if s.config.GetConfigBool("api_http_localhost", true) {
		httpPort := s.config.GetConfigWithDefault("api_http_port", "8081")
		httpAddr := fmt.Sprintf("127.0.0.1:%s", httpPort)

		s.httpListener, err = net.Listen("tcp", httpAddr)
		if err != nil {
			s.logger.Warn(fmt.Sprintf("Failed to bind HTTP localhost server to %s: %v", httpAddr, err), "api")
		} else {
			s.httpPort = httpPort

			// Create HTTP server (same handler as HTTPS server)
			s.httpServer = &http.Server{
				Handler:      handler,
				ReadTimeout:  5 * time.Minute,
				WriteTimeout: 1 * time.Hour, // Allow for very long Docker builds
				IdleTimeout:  10 * time.Minute,
				ErrorLog:     log.New(s.logger.File, "", 0),
			}

			// Start HTTP server in goroutine
			go func() {
				s.logger.Info(fmt.Sprintf("Starting HTTP localhost server on %s", httpAddr), "api")
				serveErr := s.httpServer.Serve(s.httpListener)
				if serveErr != nil && serveErr != http.ErrServerClosed {
					s.logger.Error(fmt.Sprintf("HTTP localhost server error: %v", serveErr), "api")
				}
			}()

			s.logger.Info(fmt.Sprintf("HTTP localhost server started successfully on %s", httpAddr), "api")
		}
	}

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
	mux.HandleFunc("/api/auth/ed25519", s.handleAuthEd25519)    // POST - Ed25519 authentication

	// Node routes (protected with JWT authentication)
	mux.Handle("/api/node/status", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleNodeStatus)))
	mux.Handle("/api/node/restart", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleNodeRestart)))
	mux.Handle("/api/node/capabilities", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleNodeCapabilities)))

	// Peer routes (protected with JWT authentication)
	mux.Handle("/api/peers", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handlePeers)))
	// Pattern for /api/peers/{peer_id}/capabilities
	mux.Handle("/api/peers/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/capabilities") {
			s.handlePeerCapabilities(w, r)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})))

	// Services routes (protected with JWT authentication)
	mux.Handle("/api/services", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleGetServices(w, r)
		case http.MethodPost:
			s.handleAddService(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/services/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for special endpoints
		if strings.HasSuffix(r.URL.Path, "/status") {
			s.handleUpdateServiceStatus(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pricing") {
			s.handleUpdateServicePricing(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/passphrase") {
			s.handleGetServicePassphrase(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/interfaces") {
			s.handleGetServiceInterfaces(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/payment-networks") {
			// Payment network management endpoints
			switch r.Method {
			case http.MethodGet:
				s.handleGetServicePaymentNetworks(w, r)
			case http.MethodPut:
				s.handleSetServicePaymentNetworks(w, r)
			case http.MethodDelete:
				s.handleClearServicePaymentNetworks(w, r)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		// Default service CRUD operations
		switch r.Method {
		case http.MethodGet:
			s.handleGetService(w, r)
		case http.MethodPut:
			s.handleUpdateService(w, r)
		case http.MethodDelete:
			s.handleDeleteService(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))

	// File processing route (protected with JWT authentication)
	mux.Handle("/api/services/process-upload", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleProcessUploadedFile)))

	// Docker service routes (protected with JWT authentication)
	mux.Handle("/api/services/docker/from-registry", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleCreateDockerFromRegistry)))
	mux.Handle("/api/services/docker/from-git", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleCreateDockerFromGit)))
	mux.Handle("/api/services/docker/from-local", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleCreateDockerFromLocal)))
	mux.Handle("/api/services/docker/validate-git", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleValidateGitRepo)))
	mux.Handle("/api/services/docker/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/details") {
			s.handleGetDockerServiceDetails(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/interfaces/suggested") {
			s.handleGetSuggestedInterfaces(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/interfaces") {
			s.handleUpdateServiceInterfaces(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/config") {
			s.handleUpdateDockerServiceConfig(w, r)
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	})))

	// Standalone service routes (protected with JWT authentication)
	mux.Handle("/api/services/standalone/from-local", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleCreateStandaloneFromLocal)))
	mux.Handle("/api/services/standalone/from-git", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleCreateStandaloneFromGit)))
	// Note: from-upload is handled via WebSocket chunked upload (see file_upload_handler.go)
	mux.Handle("/api/services/standalone/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/details") {
			s.handleGetStandaloneDetails(w, r)
			return
		}
		// POST /api/services/standalone/:id/finalize (after upload)
		if strings.HasSuffix(r.URL.Path, "/finalize") && r.Method == http.MethodPost {
			s.handleFinalizeStandaloneUpload(w, r)
			return
		}
		// DELETE /api/services/standalone/:id
		if r.Method == http.MethodDelete {
			s.handleDeleteStandaloneService(w, r)
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	})))

	// Blacklist routes (protected with JWT authentication)
	mux.Handle("/api/blacklist", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleGetBlacklist(w, r)
		case http.MethodPost:
			s.handleAddToBlacklist(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/blacklist/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.handleRemoveFromBlacklist(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/blacklist/check", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleCheckBlacklist)))

	// Relay routes (protected with JWT authentication)
	mux.Handle("/api/relay/sessions", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleRelayGetSessions)))
	mux.Handle("/api/relay/sessions/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse path to check for session actions
		if strings.Contains(r.URL.Path, "/disconnect") {
			s.handleRelayDisconnectSession(w, r)
		} else if strings.Contains(r.URL.Path, "/blacklist") {
			s.handleRelayBlacklistSession(w, r)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})))
	mux.Handle("/api/relay/candidates", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleRelayGetCandidates)))
	mux.Handle("/api/relay/connect", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleRelayConnect)))
	mux.Handle("/api/relay/disconnect", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleRelayDisconnect)))
	mux.Handle("/api/relay/prefer", s.jwtManager.AuthMiddleware(http.HandlerFunc(s.handleRelayPrefer)))

	// Workflows routes (protected with JWT authentication)
	mux.Handle("/api/workflows", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleGetWorkflows(w, r)
		case http.MethodPost:
			s.handleCreateWorkflow(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/workflows/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if it's an execute request
		if strings.HasSuffix(r.URL.Path, "/execute") {
			s.handleExecuteWorkflow(w, r)
			return
		}
		// Check if it's a jobs request (legacy)
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			s.handleGetWorkflowJobs(w, r)
			return
		}
		// Check if it's an execution-instances request (new workflow_executions table)
		if strings.HasSuffix(r.URL.Path, "/execution-instances") {
			s.handleGetWorkflowExecutionInstances(w, r)
			return
		}
		// Check if it's an executions request (legacy - job_executions)
		if strings.HasSuffix(r.URL.Path, "/executions") {
			s.handleGetWorkflowExecutions(w, r)
			return
		}
		// Check if it's a nodes request
		if strings.Contains(r.URL.Path, "/nodes") {
			if strings.HasSuffix(r.URL.Path, "/gui-props") {
				s.handleUpdateWorkflowNodeGUIProps(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/config") {
				s.handleUpdateWorkflowNodeConfig(w, r)
			} else if strings.Contains(r.URL.Path, "/nodes/") {
				// DELETE /api/workflows/:id/nodes/:nodeId
				s.handleDeleteWorkflowNode(w, r)
			} else if r.Method == http.MethodGet {
				s.handleGetWorkflowNodes(w, r)
			} else if r.Method == http.MethodPost {
				s.handleAddWorkflowNode(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		// Check if it's a connections request
		if strings.Contains(r.URL.Path, "/connections") {
			if strings.Contains(r.URL.Path, "/connections/") {
				// DELETE or PUT /api/workflows/:id/connections/:connectionId
				switch r.Method {
				case http.MethodDelete:
					s.handleDeleteWorkflowConnection(w, r)
				case http.MethodPut:
					s.handleUpdateWorkflowConnection(w, r)
				default:
					http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				}
			} else {
				switch r.Method {
				case http.MethodGet:
					s.handleGetWorkflowConnections(w, r)
				case http.MethodPost:
					s.handleAddWorkflowConnection(w, r)
				default:
					http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				}
			}
			return
		}
		// Check if it's a UI state request
		if strings.HasSuffix(r.URL.Path, "/ui-state") {
			switch r.Method {
			case http.MethodGet:
				s.handleGetWorkflowUIState(w, r)
			case http.MethodPut:
				s.handleUpdateWorkflowUIState(w, r)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		// Otherwise, handle as CRUD operations
		switch r.Method {
		case http.MethodGet:
			s.handleGetWorkflow(w, r)
		case http.MethodPut:
			s.handleUpdateWorkflow(w, r)
		case http.MethodDelete:
			s.handleDeleteWorkflow(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))

	// Job executions routes (protected with JWT authentication)
	mux.Handle("/api/job-executions/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if it's an interfaces request
		if strings.HasSuffix(r.URL.Path, "/interfaces") {
			s.handleGetJobExecutionInterfaces(w, r)
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	})))

	// Workflow executions routes (protected with JWT authentication)
	mux.Handle("/api/workflow-executions/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if it's a jobs request
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			s.handleGetWorkflowExecutionJobs(w, r)
			return
		}
		// Check if it's a status request
		if strings.HasSuffix(r.URL.Path, "/status") {
			s.handleGetWorkflowExecutionStatus(w, r)
			return
		}
		http.Error(w, "Not found", http.StatusNotFound)
	})))

	// Wallet management routes (protected with JWT authentication)
	mux.Handle("/api/wallets", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleListWallets(w, r)
		case http.MethodPost:
			s.handleCreateWallet(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/wallets/import", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handleImportWallet(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/wallets/set-default", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handleSetDefaultWallet(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/wallets/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Count(r.URL.Path, "/") == 3 {
			s.handleDeleteWallet(w, r)
		} else if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/balance") {
			s.handleGetWalletBalance(w, r)
		} else if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/export") {
			s.handleExportWallet(w, r)
		} else if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/check-token-account") {
			s.handleCheckTokenAccount(w, r)
		} else if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/initialize-token-account") {
			s.handleInitializeTokenAccount(w, r)
		} else if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/token-account-address") {
			s.handleGetTokenAccountAddress(w, r)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})))

	// Invoice management routes (protected with JWT authentication)
	mux.Handle("/api/invoices", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.handleCreateInvoice(w, r)
		case http.MethodGet:
			s.handleListInvoices(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/invoices/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Count(r.URL.Path, "/") == 3 {
			s.handleGetInvoice(w, r)
		} else if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/accept") {
			s.handleAcceptInvoice(w, r)
		} else if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reject") {
			s.handleRejectInvoice(w, r)
		} else if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/delete") {
			s.handleDeleteInvoice(w, r)
		} else if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/resend") {
			s.handleResendInvoice(w, r)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})))

	// Allowed networks endpoint (protected with JWT authentication)
	mux.Handle("/api/allowed-networks", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleGetAllowedNetworks(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))

	// Chat routes (protected with JWT authentication)
	mux.Handle("/api/chat/conversations", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleListConversations(w, r)
		case http.MethodPost:
			s.handleCreateConversation(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/chat/conversations/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for message-related endpoints
		if strings.Contains(r.URL.Path, "/messages") {
			switch r.Method {
			case http.MethodGet:
				s.handleListMessages(w, r)
			case http.MethodPost:
				s.handleSendMessage(w, r)
			default:
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		// Check for read endpoint
		if strings.HasSuffix(r.URL.Path, "/read") {
			if r.Method == http.MethodPost {
				s.handleMarkConversationRead(w, r)
			} else {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		// Default conversation CRUD operations
		switch r.Method {
		case http.MethodGet:
			s.handleGetConversation(w, r)
		case http.MethodDelete:
			s.handleDeleteConversation(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/chat/messages/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/read") && r.Method == http.MethodPost {
			s.handleMarkMessageRead(w, r)
		} else {
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})))
	mux.Handle("/api/chat/groups", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handleCreateGroup(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/chat/groups/", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/messages") && r.Method == http.MethodPost:
			s.handleSendGroupMessage(w, r)
		case strings.HasSuffix(r.URL.Path, "/invite") && r.Method == http.MethodPost:
			s.handleInviteToGroup(w, r)
		case strings.HasSuffix(r.URL.Path, "/leave") && r.Method == http.MethodPost:
			s.handleLeaveGroup(w, r)
		case strings.HasSuffix(r.URL.Path, "/members") && r.Method == http.MethodGet:
			s.handleGetGroupMembers(w, r)
		default:
			http.Error(w, "Not found", http.StatusNotFound)
		}
	})))
	mux.Handle("/api/chat/key-exchange", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handleInitiateKeyExchange(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/chat/unread-count", s.jwtManager.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleGetUnreadCount(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})))

	// WebSocket endpoint
	mux.HandleFunc("/api/ws", s.handleWebSocket)

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

	// Stop event emitter
	if s.eventEmitter != nil {
		s.eventEmitter.Stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown main HTTPS server
	var mainErr error
	if s.server != nil {
		mainErr = s.server.Shutdown(ctx)
	}

	// Shutdown HTTP localhost server
	if s.httpServer != nil {
		httpErr := s.httpServer.Shutdown(ctx)
		if httpErr != nil {
			s.logger.Warn(fmt.Sprintf("Error shutting down HTTP localhost server: %v", httpErr), "api")
		}
	}

	return mainErr
}

// GetPort returns the port the server is listening on
func (s *APIServer) GetPort() string {
	return s.port
}

// SetChatManager sets the chat manager after construction
func (s *APIServer) SetChatManager(chatManager *chat.ChatManager) {
	s.chatManager = chatManager
	s.logger.Info("Chat manager set for API server", "api")
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
