package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/nacl/secretbox"
)

// SenderKey implements the Signal Protocol Sender Keys for group chat encryption
// Each group member maintains a sender key chain for encrypting their messages
// and stores sender keys for all other group members for decryption
type SenderKey struct {
	ChainKey       [32]byte // Current chain key for deriving message keys
	SigningKey     []byte   // Ed25519 signing key (only for our own sender key)
	MessageNumber  int      // Counter for messages sent with this sender key
}

// GroupSenderKeys manages all sender keys for a group conversation
type GroupSenderKeys struct {
	GroupID       string
	SenderKeys    map[string]*SenderKey // peerID -> SenderKey
	OurPeerID     string
	OurSigningKey []byte // Our Ed25519 private key for signing
}

// NewGroupSenderKeys creates a new group sender keys manager
func NewGroupSenderKeys(groupID, ourPeerID string, ourSigningKey []byte) (*GroupSenderKeys, error) {
	gsk := &GroupSenderKeys{
		GroupID:       groupID,
		SenderKeys:    make(map[string]*SenderKey),
		OurPeerID:     ourPeerID,
		OurSigningKey: ourSigningKey,
	}

	// Initialize our own sender key
	ourSenderKey, err := NewSenderKey(ourSigningKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create our sender key: %v", err)
	}

	gsk.SenderKeys[ourPeerID] = ourSenderKey

	return gsk, nil
}

// NewSenderKey creates a new sender key with a random chain key
func NewSenderKey(signingKey []byte) (*SenderKey, error) {
	sk := &SenderKey{
		SigningKey:    signingKey,
		MessageNumber: 0,
	}

	// Generate random initial chain key
	if _, err := rand.Read(sk.ChainKey[:]); err != nil {
		return nil, fmt.Errorf("failed to generate chain key: %v", err)
	}

	return sk, nil
}

// EncryptGroupMessage encrypts a message for a group using our sender key
// Returns: (ciphertext, nonce, messageNumber, signature, error)
func (gsk *GroupSenderKeys) EncryptGroupMessage(plaintext []byte) ([]byte, [24]byte, int, []byte, error) {
	// Get our sender key
	ourSenderKey, exists := gsk.SenderKeys[gsk.OurPeerID]
	if !exists {
		return nil, [24]byte{}, 0, nil, errors.New("our sender key not found")
	}

	// Derive message key from current chain key
	messageKey, nextChainKey, err := DeriveMessageKey(ourSenderKey.ChainKey[:])
	if err != nil {
		return nil, [24]byte{}, 0, nil, fmt.Errorf("failed to derive message key: %v", err)
	}

	// Generate random nonce
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, [24]byte{}, 0, nil, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// Encrypt using NaCl secretbox
	ciphertext := secretbox.Seal(nil, plaintext, &nonce, &messageKey)

	// Get current message number before incrementing
	messageNumber := ourSenderKey.MessageNumber

	// Update sender key state
	ourSenderKey.ChainKey = nextChainKey
	ourSenderKey.MessageNumber++

	// Zero out the message key
	ZeroKey(&messageKey)

	// Sign the message with our Ed25519 key for authentication
	signature, err := signMessage(ciphertext, gsk.OurSigningKey)
	if err != nil {
		return nil, [24]byte{}, 0, nil, fmt.Errorf("failed to sign message: %v", err)
	}

	return ciphertext, nonce, messageNumber, signature, nil
}

// DecryptGroupMessage decrypts a message from a group member
func (gsk *GroupSenderKeys) DecryptGroupMessage(
	senderPeerID string,
	ciphertext []byte,
	nonce [24]byte,
	messageNumber int,
	signature []byte,
	senderPublicKey []byte,
) ([]byte, error) {
	// Verify signature first
	if err := verifyMessageSignature(ciphertext, signature, senderPublicKey); err != nil {
		return nil, fmt.Errorf("signature verification failed: %v", err)
	}

	// Get sender's key
	senderKey, exists := gsk.SenderKeys[senderPeerID]
	if !exists {
		return nil, fmt.Errorf("sender key for peer %s not found", senderPeerID)
	}

	// Derive the correct message key by advancing the chain to the message number
	// This handles out-of-order delivery
	for senderKey.MessageNumber < messageNumber {
		_, nextChainKey, err := DeriveMessageKey(senderKey.ChainKey[:])
		if err != nil {
			return nil, fmt.Errorf("failed to advance chain key: %v", err)
		}
		senderKey.ChainKey = nextChainKey
		senderKey.MessageNumber++
	}

	// If we've already processed this message number, derive from current state
	if senderKey.MessageNumber == messageNumber {
		messageKey, nextChainKey, err := DeriveMessageKey(senderKey.ChainKey[:])
		if err != nil {
			return nil, fmt.Errorf("failed to derive message key: %v", err)
		}

		// Decrypt
		plaintext, ok := secretbox.Open(nil, ciphertext, &nonce, &messageKey)
		if !ok {
			return nil, errors.New("decryption failed")
		}

		// Update sender key state
		senderKey.ChainKey = nextChainKey
		senderKey.MessageNumber++

		// Zero out the message key
		ZeroKey(&messageKey)

		return plaintext, nil
	}

	return nil, fmt.Errorf("message number mismatch: expected %d, got %d", senderKey.MessageNumber, messageNumber)
}

// AddSenderKey adds a sender key for a new group member
// This is called when a new member joins or when we receive a sender key update
func (gsk *GroupSenderKeys) AddSenderKey(peerID string, chainKey [32]byte, messageNumber int) error {
	senderKey := &SenderKey{
		ChainKey:      chainKey,
		MessageNumber: messageNumber,
		SigningKey:    nil, // We don't store other members' signing keys
	}

	gsk.SenderKeys[peerID] = senderKey
	return nil
}

// RemoveSenderKey removes a sender key for a member who left the group
func (gsk *GroupSenderKeys) RemoveSenderKey(peerID string) {
	if senderKey, exists := gsk.SenderKeys[peerID]; exists {
		// Zero out the chain key before removing
		ZeroKey(&senderKey.ChainKey)
		delete(gsk.SenderKeys, peerID)
	}
}

// GetOurSenderKeyState returns our current sender key state for distribution to new members
func (gsk *GroupSenderKeys) GetOurSenderKeyState() ([32]byte, int, error) {
	ourSenderKey, exists := gsk.SenderKeys[gsk.OurPeerID]
	if !exists {
		return [32]byte{}, 0, errors.New("our sender key not found")
	}

	return ourSenderKey.ChainKey, ourSenderKey.MessageNumber, nil
}

// signMessage signs a message using Ed25519
// This is a placeholder - actual implementation should use the Ed25519 key from keys.go
func signMessage(message []byte, signingKey []byte) ([]byte, error) {
	// TODO: Integrate with existing Ed25519 signing in /internal/crypto/keys.go
	// For now, return a placeholder
	// In actual implementation, use: crypto.Sign(message, signingKey)
	signature := make([]byte, 64) // Ed25519 signatures are 64 bytes
	if _, err := rand.Read(signature); err != nil {
		return nil, err
	}
	return signature, nil
}

// verifyMessageSignature verifies an Ed25519 signature
// This is a placeholder - actual implementation should use the Ed25519 key from keys.go
func verifyMessageSignature(message, signature, publicKey []byte) error {
	// TODO: Integrate with existing Ed25519 verification in /internal/crypto/keys.go
	// For now, return success
	// In actual implementation, use: crypto.Verify(message, signature, publicKey)
	if len(signature) != 64 {
		return errors.New("invalid signature length")
	}
	return nil
}
