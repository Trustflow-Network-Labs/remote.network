package p2p

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// NATType represents the detected NAT type
type NATType int

const (
	NATTypeUnknown NATType = iota
	NATTypeNone           // No NAT - public IP
	NATTypeFullCone       // Full Cone NAT (easiest for hole punching)
	NATTypeRestrictedCone // Restricted Cone NAT (moderate difficulty)
	NATTypePortRestricted // Port Restricted Cone NAT (moderate difficulty)
	NATTypeSymmetric      // Symmetric NAT (hardest, usually requires relay)
)

// String returns the string representation of NAT type
func (n NATType) String() string {
	switch n {
	case NATTypeNone:
		return "None (Public)"
	case NATTypeFullCone:
		return "Full Cone NAT"
	case NATTypeRestrictedCone:
		return "Restricted Cone NAT"
	case NATTypePortRestricted:
		return "Port Restricted Cone NAT"
	case NATTypeSymmetric:
		return "Symmetric NAT"
	default:
		return "Unknown"
	}
}

// HolePunchingDifficulty returns difficulty level for hole punching
func (n NATType) HolePunchingDifficulty() string {
	switch n {
	case NATTypeNone:
		return "none"
	case NATTypeFullCone:
		return "easy"
	case NATTypeRestrictedCone, NATTypePortRestricted:
		return "moderate"
	case NATTypeSymmetric:
		return "hard"
	default:
		return "unknown"
	}
}

// RequiresRelay returns true if this NAT type typically requires relay
func (n NATType) RequiresRelay() bool {
	return n == NATTypeSymmetric
}

// NATDetectionResult contains the full NAT detection results
type NATDetectionResult struct {
	NATType              NATType
	PublicIP             string
	PublicPort           int
	PrivateIP            string
	PrivatePort          int
	MappingBehavior      string // endpoint-independent, address-dependent, address-port-dependent
	FilteringBehavior    string // endpoint-independent, address-dependent, address-port-dependent
	HairpinningSupported bool   // Can connect to own public IP
	PortPredictable      bool   // For symmetric NAT, can we predict port allocation
	DetectionTime        time.Time
	DetectionDuration    time.Duration
}

// NATDetector performs NAT type detection using STUN-like protocol
type NATDetector struct {
	config              *utils.ConfigManager
	logger              *utils.LogsManager
	ctx                 context.Context
	cancel              context.CancelFunc
	nodeTypeManager     *utils.NodeTypeManager
	stunServers         []string
	localPort           int
	result              *NATDetectionResult
	resultMutex         sync.RWMutex
	detectionInProgress bool
}

// NewNATDetector creates a new NAT detector
func NewNATDetector(config *utils.ConfigManager, logger *utils.LogsManager, localPort int) *NATDetector {
	ctx, cancel := context.WithCancel(context.Background())

	// Read STUN servers from config
	stunServers := config.GetConfigSlice("nat_stun_servers", []string{
		"stun.l.google.com:19302",
		"stun1.l.google.com:19302",
		"stun2.l.google.com:19302",
	})

	return &NATDetector{
		config:          config,
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
		nodeTypeManager: utils.NewNodeTypeManager(),
		stunServers:     stunServers,
		localPort:       localPort,
	}
}

// DetectNATType performs full NAT type detection
func (nd *NATDetector) DetectNATType() (*NATDetectionResult, error) {
	nd.resultMutex.Lock()
	if nd.detectionInProgress {
		nd.resultMutex.Unlock()
		return nd.result, fmt.Errorf("detection already in progress")
	}
	nd.detectionInProgress = true
	nd.resultMutex.Unlock()

	defer func() {
		nd.resultMutex.Lock()
		nd.detectionInProgress = false
		nd.resultMutex.Unlock()
	}()

	startTime := time.Now()
	nd.logger.Info("Starting NAT type detection...", "nat-detector")

	result := &NATDetectionResult{
		NATType:       NATTypeUnknown,
		DetectionTime: startTime,
	}

	// Step 1: Get local IP and port
	privateIP, err := nd.nodeTypeManager.GetLocalIP()
	if err != nil {
		nd.logger.Error(fmt.Sprintf("Failed to get local IP: %v", err), "nat-detector")
		return nil, err
	}
	result.PrivateIP = privateIP
	result.PrivatePort = nd.localPort

	nd.logger.Debug(fmt.Sprintf("Local network: %s:%d", privateIP, nd.localPort), "nat-detector")

	// Step 2: Check if we have a public IP (no NAT) using existing utility
	isPublic, err := nd.nodeTypeManager.IsPublicNode()
	if err != nil {
		nd.logger.Warn(fmt.Sprintf("Failed to determine if node is public: %v", err), "nat-detector")
	}

	if isPublic {
		result.NATType = NATTypeNone
		result.PublicIP = privateIP
		result.PublicPort = nd.localPort
		result.MappingBehavior = "none"
		result.FilteringBehavior = "none"
		result.DetectionDuration = time.Since(startTime)

		nd.logger.Info("No NAT detected - public IP address", "nat-detector")
		nd.storeResult(result)
		return result, nil
	}

	// Step 3: Perform STUN-based NAT detection
	publicIP, publicPort, err := nd.performSTUNTest()
	if err != nil {
		nd.logger.Error(fmt.Sprintf("STUN test failed: %v", err), "nat-detector")
		result.NATType = NATTypeUnknown
		result.DetectionDuration = time.Since(startTime)
		nd.storeResult(result)
		return result, err
	}

	result.PublicIP = publicIP
	result.PublicPort = publicPort

	nd.logger.Info(fmt.Sprintf("Public endpoint: %s:%d", publicIP, publicPort), "nat-detector")

	// Step 4: Detect mapping behavior (endpoint independence)
	mappingIndependent, err := nd.testMappingBehavior(publicIP, publicPort)
	if err != nil {
		nd.logger.Warn(fmt.Sprintf("Mapping behavior test failed: %v", err), "nat-detector")
	}

	if !mappingIndependent {
		// Symmetric NAT - different mapping for different destinations
		result.NATType = NATTypeSymmetric
		result.MappingBehavior = "address-port-dependent"
		result.FilteringBehavior = "address-port-dependent"
		result.PortPredictable = nd.testPortPredictability()

		nd.logger.Info("Detected Symmetric NAT", "nat-detector")
	} else {
		// Cone NAT - same mapping for all destinations
		// Step 5: Detect filtering behavior
		filteringType := nd.detectFilteringBehavior()

		switch filteringType {
		case "endpoint-independent":
			result.NATType = NATTypeFullCone
			result.MappingBehavior = "endpoint-independent"
			result.FilteringBehavior = "endpoint-independent"
			nd.logger.Info("Detected Full Cone NAT", "nat-detector")

		case "address-dependent":
			result.NATType = NATTypeRestrictedCone
			result.MappingBehavior = "endpoint-independent"
			result.FilteringBehavior = "address-dependent"
			nd.logger.Info("Detected Restricted Cone NAT", "nat-detector")

		case "address-port-dependent":
			result.NATType = NATTypePortRestricted
			result.MappingBehavior = "endpoint-independent"
			result.FilteringBehavior = "address-port-dependent"
			nd.logger.Info("Detected Port Restricted Cone NAT", "nat-detector")

		default:
			result.NATType = NATTypeUnknown
		}
	}

	// Step 6: Test hairpinning support (optional, can be disabled via config)
	if nd.config.GetConfigBool("nat_test_hairpinning", true) {
		result.HairpinningSupported = nd.testHairpinning(publicIP, publicPort)
	}

	result.DetectionDuration = time.Since(startTime)

	nd.logger.Info(fmt.Sprintf("NAT detection completed in %v: %s (Difficulty: %s, Requires Relay: %v)",
		result.DetectionDuration, result.NATType.String(),
		result.NATType.HolePunchingDifficulty(), result.NATType.RequiresRelay()), "nat-detector")

	nd.storeResult(result)
	return result, nil
}

// performSTUNTest performs a STUN request to discover public IP/port
func (nd *NATDetector) performSTUNTest() (string, int, error) {
	// Try each STUN server until one succeeds
	for _, stunServer := range nd.stunServers {
		publicIP, publicPort, err := nd.stunQuery(stunServer)
		if err == nil {
			return publicIP, publicPort, nil
		}
		nd.logger.Debug(fmt.Sprintf("STUN server %s failed: %v", stunServer, err), "nat-detector")
	}

	return "", 0, fmt.Errorf("all STUN servers failed")
}

// stunQuery sends a STUN binding request and returns the mapped address
func (nd *NATDetector) stunQuery(stunServer string) (string, int, error) {
	// Get timeout from config
	timeout := nd.config.GetConfigDuration("nat_stun_timeout", 5*time.Second)

	// Create UDP connection
	conn, err := net.DialTimeout("udp", stunServer, timeout)
	if err != nil {
		return "", 0, fmt.Errorf("failed to connect to STUN server: %v", err)
	}
	defer conn.Close()

	// Set deadline for response
	conn.SetDeadline(time.Now().Add(timeout))

	// Create STUN binding request (simplified version)
	stunRequest := nd.createSTUNBindingRequest()

	_, err = conn.Write(stunRequest)
	if err != nil {
		return "", 0, fmt.Errorf("failed to send STUN request: %v", err)
	}

	// Read response
	bufferSize := nd.config.GetConfigInt("nat_stun_buffer_size", 1024, 512, 4096)
	buffer := make([]byte, bufferSize)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read STUN response: %v", err)
	}

	// Parse STUN response to extract mapped address
	publicIP, publicPort, err := nd.parseSTUNResponse(buffer[:n])
	if err != nil {
		return "", 0, fmt.Errorf("failed to parse STUN response: %v", err)
	}

	return publicIP, publicPort, nil
}

// createSTUNBindingRequest creates a STUN Binding Request message
func (nd *NATDetector) createSTUNBindingRequest() []byte {
	// STUN Binding Request message format (RFC 5389)
	// Message Type: Binding Request (0x0001)
	// Message Length: 0 (no attributes for simple request)
	// Magic Cookie: 0x2112A442
	// Transaction ID: 96-bit random value

	stunMsg := make([]byte, 20)

	// Message Type: Binding Request (0x0001)
	stunMsg[0] = 0x00
	stunMsg[1] = 0x01

	// Message Length: 0
	stunMsg[2] = 0x00
	stunMsg[3] = 0x00

	// Magic Cookie
	stunMsg[4] = 0x21
	stunMsg[5] = 0x12
	stunMsg[6] = 0xA4
	stunMsg[7] = 0x42

	// Transaction ID (12 bytes) - use timestamp for simplicity
	timestamp := time.Now().UnixNano()
	for i := 0; i < 12; i++ {
		stunMsg[8+i] = byte((timestamp >> (i * 8)) & 0xFF)
	}

	return stunMsg
}

// parseSTUNResponse parses a STUN response and extracts the mapped address
func (nd *NATDetector) parseSTUNResponse(response []byte) (string, int, error) {
	if len(response) < 20 {
		return "", 0, fmt.Errorf("STUN response too short")
	}

	// Verify this is a Binding Response (0x0101)
	if response[0] != 0x01 || response[1] != 0x01 {
		return "", 0, fmt.Errorf("not a STUN Binding Response")
	}

	// Parse attributes to find MAPPED-ADDRESS or XOR-MAPPED-ADDRESS
	offset := 20 // Skip STUN header
	messageLength := int(response[2])<<8 | int(response[3])

	for offset < 20+messageLength && offset+4 <= len(response) {
		attrType := int(response[offset])<<8 | int(response[offset+1])
		attrLength := int(response[offset+2])<<8 | int(response[offset+3])

		if offset+4+attrLength > len(response) {
			break
		}

		// XOR-MAPPED-ADDRESS (0x0020) - preferred
		// MAPPED-ADDRESS (0x0001) - fallback
		if attrType == 0x0020 || attrType == 0x0001 {
			return nd.parseAddressAttribute(response[offset+4:offset+4+attrLength], attrType == 0x0020)
		}

		offset += 4 + attrLength
	}

	return "", 0, fmt.Errorf("no mapped address found in STUN response")
}

// parseAddressAttribute parses MAPPED-ADDRESS or XOR-MAPPED-ADDRESS attribute
func (nd *NATDetector) parseAddressAttribute(data []byte, isXOR bool) (string, int, error) {
	if len(data) < 8 {
		return "", 0, fmt.Errorf("address attribute too short")
	}

	// Skip reserved byte
	family := data[1]

	// Only support IPv4 for now
	if family != 0x01 {
		return "", 0, fmt.Errorf("only IPv4 supported")
	}

	port := int(data[2])<<8 | int(data[3])

	// Parse IP address
	var ip net.IP
	if isXOR {
		// XOR with magic cookie for XOR-MAPPED-ADDRESS
		ip = net.IPv4(
			data[4]^0x21,
			data[5]^0x12,
			data[6]^0xA4,
			data[7]^0x42,
		)
		port ^= 0x2112 // XOR port with first 16 bits of magic cookie
	} else {
		ip = net.IPv4(data[4], data[5], data[6], data[7])
	}

	return ip.String(), port, nil
}

// testMappingBehavior tests if NAT uses endpoint-independent mapping
func (nd *NATDetector) testMappingBehavior(publicIP string, publicPort int) (bool, error) {
	// Test if we get the same public port when connecting to different STUN servers
	if len(nd.stunServers) < 2 {
		return true, nil // Assume endpoint-independent if we can't test
	}

	// Query second STUN server
	_, secondPort, err := nd.stunQuery(nd.stunServers[1])
	if err != nil {
		return true, err // Assume endpoint-independent on error
	}

	// If ports match, mapping is endpoint-independent (Cone NAT)
	// If ports differ, mapping is address/port-dependent (Symmetric NAT)
	return secondPort == publicPort, nil
}

// detectFilteringBehavior detects NAT filtering behavior
func (nd *NATDetector) detectFilteringBehavior() string {
	// Read default filtering assumption from config
	defaultFiltering := nd.config.GetConfigWithDefault("nat_default_filtering", "address-port-dependent")

	// This requires cooperation from STUN servers with different IPs
	// For now, we'll return the configured default (most restrictive for safety)
	// TODO: Implement proper filtering detection with multiple test servers

	return defaultFiltering
}

// testPortPredictability tests if symmetric NAT uses predictable port allocation
func (nd *NATDetector) testPortPredictability() bool {
	// Get number of samples from config
	samples := nd.config.GetConfigInt("nat_port_prediction_samples", 3, 2, 10)
	maxPortDiff := nd.config.GetConfigInt("nat_port_prediction_max_diff", 10, 1, 100)

	ports := make([]int, 0, samples)

	for i := 0; i < samples; i++ {
		_, port, err := nd.performSTUNTest()
		if err == nil {
			ports = append(ports, port)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if len(ports) < 2 {
		return false
	}

	// Check if ports are sequential or follow a pattern
	sequential := true
	for i := 1; i < len(ports); i++ {
		diff := ports[i] - ports[i-1]
		if diff < 0 || diff > maxPortDiff {
			sequential = false
			break
		}
	}

	return sequential
}

// testHairpinning tests if NAT supports hairpinning (NAT loopback)
func (nd *NATDetector) testHairpinning(publicIP string, publicPort int) bool {
	timeout := nd.config.GetConfigDuration("nat_hairpinning_timeout", 2*time.Second)

	// Try to connect to our own public endpoint
	conn, err := net.DialTimeout("udp", fmt.Sprintf("%s:%d", publicIP, publicPort), timeout)
	if err != nil {
		return false
	}
	conn.Close()

	return true
}

// GetLastResult returns the last detection result
func (nd *NATDetector) GetLastResult() *NATDetectionResult {
	nd.resultMutex.RLock()
	defer nd.resultMutex.RUnlock()
	return nd.result
}

// storeResult stores the detection result
func (nd *NATDetector) storeResult(result *NATDetectionResult) {
	nd.resultMutex.Lock()
	defer nd.resultMutex.Unlock()
	nd.result = result
}

// Stop stops the NAT detector
func (nd *NATDetector) Stop() {
	nd.cancel()
}

// GetNATType returns the detected NAT type (convenience method)
func (nd *NATDetector) GetNATType() NATType {
	nd.resultMutex.RLock()
	defer nd.resultMutex.RUnlock()

	if nd.result != nil {
		return nd.result.NATType
	}
	return NATTypeUnknown
}

// IsSymmetricNAT returns true if behind symmetric NAT
func (nd *NATDetector) IsSymmetricNAT() bool {
	return nd.GetNATType() == NATTypeSymmetric
}

// CanHolePunch returns true if hole punching is feasible
func (nd *NATDetector) CanHolePunch() bool {
	natType := nd.GetNATType()
	return natType != NATTypeUnknown && natType != NATTypeSymmetric
}

// ShouldUseRelay returns true if this peer should use relay by default
func (nd *NATDetector) ShouldUseRelay() bool {
	result := nd.GetLastResult()
	if result == nil {
		return false
	}

	// Use relay for symmetric NAT or if hole punching is unlikely to succeed
	return result.NATType.RequiresRelay()
}
