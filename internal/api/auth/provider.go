package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// AuthProvider defines the interface for authentication providers
type AuthProvider interface {
	// Authenticate validates credentials and returns peer_id and wallet_address
	Authenticate(credentials interface{}) (peerID string, walletAddress string, err error)

	// ProviderName returns the name of the auth provider
	ProviderName() string
}

// Challenge represents an authentication challenge
type Challenge struct {
	Value     string
	Timestamp time.Time
	ExpiresAt time.Time
}

// ChallengeManager manages authentication challenges
type ChallengeManager struct {
	challenges map[string]*Challenge // key: challenge value
	mutex      sync.RWMutex
	ttl        time.Duration
}

// NewChallengeManager creates a new challenge manager
func NewChallengeManager(ttl time.Duration) *ChallengeManager {
	cm := &ChallengeManager{
		challenges: make(map[string]*Challenge),
		ttl:        ttl,
	}

	// Start cleanup goroutine
	go cm.cleanupExpired()

	return cm
}

// GenerateChallenge creates a new random challenge
func (cm *ChallengeManager) GenerateChallenge() (string, error) {
	// Generate 32 random bytes (256 bits)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random challenge: %v", err)
	}

	challengeValue := hex.EncodeToString(bytes)
	now := time.Now()

	challenge := &Challenge{
		Value:     challengeValue,
		Timestamp: now,
		ExpiresAt: now.Add(cm.ttl),
	}

	cm.mutex.Lock()
	cm.challenges[challengeValue] = challenge
	cm.mutex.Unlock()

	return challengeValue, nil
}

// ValidateChallenge checks if a challenge exists and is not expired
func (cm *ChallengeManager) ValidateChallenge(challengeValue string) bool {
	cm.mutex.RLock()
	challenge, exists := cm.challenges[challengeValue]
	cm.mutex.RUnlock()

	if !exists {
		return false
	}

	if time.Now().After(challenge.ExpiresAt) {
		// Challenge expired, remove it
		cm.mutex.Lock()
		delete(cm.challenges, challengeValue)
		cm.mutex.Unlock()
		return false
	}

	return true
}

// ConsumeChallenge validates and removes a challenge (one-time use)
func (cm *ChallengeManager) ConsumeChallenge(challengeValue string) bool {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	challenge, exists := cm.challenges[challengeValue]
	if !exists {
		return false
	}

	if time.Now().After(challenge.ExpiresAt) {
		delete(cm.challenges, challengeValue)
		return false
	}

	// Remove challenge after successful validation (one-time use)
	delete(cm.challenges, challengeValue)
	return true
}

// cleanupExpired removes expired challenges periodically
func (cm *ChallengeManager) cleanupExpired() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		cm.mutex.Lock()
		for key, challenge := range cm.challenges {
			if now.After(challenge.ExpiresAt) {
				delete(cm.challenges, key)
			}
		}
		cm.mutex.Unlock()
	}
}
