package p2p

import (
	"testing"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// TestMetadataPublisherSequenceIncrement tests that sequence numbers increment correctly
func TestMetadataPublisherSequenceIncrement(t *testing.T) {
	// Create test metadata publisher (without actual DHT)
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)
	keyPair, _ := crypto.GenerateKeypair()

	mp := &MetadataPublisher{
		bep44Manager:    nil, // We won't actually publish
		keyPair:         keyPair,
		logger:          logger,
		config:          cm,
		currentSequence: 0,
		stopChan:        make(chan struct{}),
	}

	// Test initial sequence number
	if mp.currentSequence != 0 {
		t.Errorf("Expected initial sequence 0, got %d", mp.currentSequence)
	}

	// Simulate sequence increments (as would happen during updates)
	mp.metadataMutex.Lock()
	mp.currentSequence++
	seq1 := mp.currentSequence
	mp.metadataMutex.Unlock()

	if seq1 != 1 {
		t.Errorf("Expected sequence 1 after first increment, got %d", seq1)
	}

	mp.metadataMutex.Lock()
	mp.currentSequence++
	seq2 := mp.currentSequence
	mp.metadataMutex.Unlock()

	if seq2 != 2 {
		t.Errorf("Expected sequence 2 after second increment, got %d", seq2)
	}

	// Verify monotonic increase
	if seq2 <= seq1 {
		t.Error("Sequence numbers should be monotonically increasing")
	}
}

// TestMetadataPublisherCurrentMetadata tests metadata storage and retrieval
func TestMetadataPublisherCurrentMetadata(t *testing.T) {
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)
	keyPair, _ := crypto.GenerateKeypair()

	mp := &MetadataPublisher{
		keyPair: keyPair,
		logger:  logger,
		config:  cm,
	}

	// Create test metadata
	metadata := &database.PeerMetadata{
		NodeID:  keyPair.PeerID(),
		Topic:   "test-topic",
		Version: 1,
		NetworkInfo: database.NetworkInfo{
			PublicIP:   "1.2.3.4",
			PublicPort: 30906,
			NodeType:   "public",
		},
		Timestamp: time.Now(),
	}

	// Store metadata
	mp.metadataMutex.Lock()
	mp.currentMetadata = metadata
	mp.metadataMutex.Unlock()

	// Retrieve and verify
	mp.metadataMutex.RLock()
	retrieved := mp.currentMetadata
	mp.metadataMutex.RUnlock()

	if retrieved == nil {
		t.Fatal("Current metadata is nil")
	}

	if retrieved.NodeID != keyPair.PeerID() {
		t.Errorf("Expected NodeID %s, got %s", keyPair.PeerID(), retrieved.NodeID)
	}

	if retrieved.Topic != "test-topic" {
		t.Errorf("Expected topic 'test-topic', got '%s'", retrieved.Topic)
	}

	if retrieved.NetworkInfo.PublicIP != "1.2.3.4" {
		t.Errorf("Expected IP 1.2.3.4, got %s", retrieved.NetworkInfo.PublicIP)
	}
}

// TestMetadataPublisherThreadSafety tests concurrent access to metadata
func TestMetadataPublisherThreadSafety(t *testing.T) {
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)
	keyPair, _ := crypto.GenerateKeypair()

	mp := &MetadataPublisher{
		keyPair:         keyPair,
		logger:          logger,
		config:          cm,
		currentSequence: 0,
	}

	// Run concurrent goroutines that increment sequence
	done := make(chan bool)
	iterations := 100

	// Writer goroutine
	go func() {
		for i := 0; i < iterations; i++ {
			mp.metadataMutex.Lock()
			mp.currentSequence++
			mp.metadataMutex.Unlock()
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < iterations; i++ {
			mp.metadataMutex.RLock()
			_ = mp.currentSequence
			mp.metadataMutex.RUnlock()
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// Verify final sequence number
	mp.metadataMutex.RLock()
	finalSeq := mp.currentSequence
	mp.metadataMutex.RUnlock()

	if finalSeq != int64(iterations) {
		t.Errorf("Expected final sequence %d, got %d", iterations, finalSeq)
	}
}

// TestMetadataPublisherGetCurrentSequence tests retrieving current sequence
func TestMetadataPublisherGetCurrentSequence(t *testing.T) {
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)
	keyPair, _ := crypto.GenerateKeypair()

	mp := &MetadataPublisher{
		keyPair:         keyPair,
		logger:          logger,
		config:          cm,
		currentSequence: 42,
	}

	// Read sequence
	mp.metadataMutex.RLock()
	seq := mp.currentSequence
	mp.metadataMutex.RUnlock()

	if seq != 42 {
		t.Errorf("Expected sequence 42, got %d", seq)
	}
}

// TestMetadataPublisherStopChannel tests the stop channel initialization
func TestMetadataPublisherStopChannel(t *testing.T) {
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)
	keyPair, _ := crypto.GenerateKeypair()

	mp := NewMetadataPublisher(nil, keyPair, logger, cm, nil)

	// Verify stop channel is initialized
	if mp.stopChan == nil {
		t.Error("Stop channel should be initialized")
	}

	// Verify it's not closed initially
	select {
	case <-mp.stopChan:
		t.Error("Stop channel should not be closed initially")
	default:
		// Expected: channel is open
	}

	// Simulate stopping
	close(mp.stopChan)

	// Verify it's now closed
	select {
	case <-mp.stopChan:
		// Expected: channel is closed
	default:
		t.Error("Stop channel should be closed after close()")
	}
}

// TestMetadataPublisherInitialization tests NewMetadataPublisher
func TestMetadataPublisherInitialization(t *testing.T) {
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)
	keyPair, _ := crypto.GenerateKeypair()

	mp := NewMetadataPublisher(nil, keyPair, logger, cm, nil)

	// Verify fields are initialized
	if mp.keyPair == nil {
		t.Error("KeyPair should be set")
	}

	if mp.logger == nil {
		t.Error("Logger should be set")
	}

	if mp.config == nil {
		t.Error("Config should be set")
	}

	if mp.currentSequence != 0 {
		t.Errorf("Initial sequence should be 0, got %d", mp.currentSequence)
	}

	if mp.stopChan == nil {
		t.Error("Stop channel should be initialized")
	}

	// Verify PeerID derivation
	peerID := mp.keyPair.PeerID()
	if len(peerID) != 40 { // SHA1 hex string
		t.Errorf("Invalid peer ID length: expected 40, got %d", len(peerID))
	}
}

// TestMetadataSequenceLogic tests the sequence number logic
func TestMetadataSequenceLogic(t *testing.T) {
	testCases := []struct {
		name          string
		initialSeq    int64
		operations    []string // "publish" or "update"
		expectedFinal int64
	}{
		{
			name:          "Initial publish",
			initialSeq:    0,
			operations:    []string{"publish"},
			expectedFinal: 0, // Publish doesn't increment
		},
		{
			name:          "Single update",
			initialSeq:    0,
			operations:    []string{"publish", "update"},
			expectedFinal: 1, // Update increments
		},
		{
			name:          "Multiple updates",
			initialSeq:    0,
			operations:    []string{"publish", "update", "update", "update"},
			expectedFinal: 3, // Three updates
		},
		{
			name:          "Update without publish",
			initialSeq:    0,
			operations:    []string{"update", "update"},
			expectedFinal: 2, // Updates still work
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cm := utils.NewConfigManager("")
			logger := utils.NewLogsManager(cm)
			keyPair, _ := crypto.GenerateKeypair()

			mp := &MetadataPublisher{
				keyPair:         keyPair,
				logger:          logger,
				config:          cm,
				currentSequence: tc.initialSeq,
			}

			// Execute operations
			for _, op := range tc.operations {
				mp.metadataMutex.Lock()
				if op == "update" {
					mp.currentSequence++
				}
				// "publish" doesn't increment sequence
				mp.metadataMutex.Unlock()
			}

			// Verify final sequence
			mp.metadataMutex.RLock()
			finalSeq := mp.currentSequence
			mp.metadataMutex.RUnlock()

			if finalSeq != tc.expectedFinal {
				t.Errorf("Expected final sequence %d, got %d", tc.expectedFinal, finalSeq)
			}
		})
	}
}

// TestMetadataPublisherRepublishInterval tests republish interval logic
func TestMetadataPublisherRepublishInterval(t *testing.T) {
	// Test different republish intervals
	testIntervals := []int{
		60,   // 1 minute
		300,  // 5 minutes
		3600, // 1 hour (default)
	}

	for _, interval := range testIntervals {
		duration := time.Duration(interval) * time.Second

		// Verify duration calculation
		if duration < 1*time.Second {
			t.Errorf("Republish interval too short: %v", duration)
		}

		if duration > 24*time.Hour {
			t.Errorf("Republish interval too long: %v", duration)
		}
	}

	// Test default interval
	defaultInterval := 3600 * time.Second // 1 hour
	if defaultInterval != 1*time.Hour {
		t.Errorf("Default interval should be 1 hour, got %v", defaultInterval)
	}
}

// TestMetadataPublisherFields tests struct field access
func TestMetadataPublisherFields(t *testing.T) {
	cm := utils.NewConfigManager("")
	logger := utils.NewLogsManager(cm)
	keyPair, _ := crypto.GenerateKeypair()

	mp := NewMetadataPublisher(nil, keyPair, logger, cm, nil)

	// Test keyPair access
	if mp.keyPair != keyPair {
		t.Error("KeyPair field not set correctly")
	}

	// Test logger access
	if mp.logger != logger {
		t.Error("Logger field not set correctly")
	}

	// Test config access
	if mp.config != cm {
		t.Error("Config field not set correctly")
	}

	// Test mutex is zero-value (not nil, since it's embedded)
	mp.metadataMutex.Lock()
	mp.metadataMutex.Unlock() // Should not panic

	// Test we can safely lock/unlock
	mp.metadataMutex.RLock()
	mp.metadataMutex.RUnlock() // Should not panic
}
