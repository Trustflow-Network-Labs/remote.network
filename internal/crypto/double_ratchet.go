package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/secretbox"
)

// DoubleRatchet implements the Signal Protocol Double Ratchet algorithm
// for end-to-end encrypted messaging with forward secrecy
type DoubleRatchet struct {
	// DH Ratchet (X25519 key pair)
	DHRatchetPrivKey [32]byte // Our current X25519 private key
	DHRatchetPubKey  [32]byte // Our current X25519 public key
	RemoteDHPubKey   [32]byte // Remote peer's current X25519 public key

	// Symmetric Ratchets
	RootKey      [32]byte // Root key (KDF-chain root)
	SendChainKey [32]byte // Sending chain key
	RecvChainKey [32]byte // Receiving chain key

	// Message counters
	SendMessageNum int // Number of messages sent in current sending chain
	RecvMessageNum int // Number of messages received in current receiving chain

	// Skip list for out-of-order messages
	SkippedKeys map[int][32]byte // messageNum -> messageKey
}

// NewDoubleRatchet initializes a new Double Ratchet for the initiator (Alice)
// Alice initiates the ratchet by generating an ephemeral X25519 key pair
// The actual DH ratchet step is performed when the ACK is received from Bob
func NewDoubleRatchet(sharedSecret [32]byte, remoteDHPubKey [32]byte) (*DoubleRatchet, error) {
	dr := &DoubleRatchet{
		RemoteDHPubKey: remoteDHPubKey,
		SkippedKeys:    make(map[int][32]byte),
	}

	// Generate our initial DH key pair
	if _, err := rand.Read(dr.DHRatchetPrivKey[:]); err != nil {
		return nil, fmt.Errorf("failed to generate DH key pair: %v", err)
	}
	curve25519.ScalarBaseMult(&dr.DHRatchetPubKey, &dr.DHRatchetPrivKey)

	// Initialize root key from shared secret
	copy(dr.RootKey[:], sharedSecret[:])

	// NOTE: We do NOT perform the initial DH ratchet step here because we don't have
	// Bob's ephemeral public key yet. The DH ratchet will be completed when we receive
	// the key exchange ACK containing Bob's ephemeral public key.

	dr.SendMessageNum = 0
	dr.RecvMessageNum = 0

	return dr, nil
}

// NewDoubleRatchetReceiver initializes a new Double Ratchet for the receiver (Bob)
// Bob waits for Alice's first message to initialize the ratchet
func NewDoubleRatchetReceiver(sharedSecret [32]byte) (*DoubleRatchet, error) {
	dr := &DoubleRatchet{
		SkippedKeys: make(map[int][32]byte),
	}

	// Generate our initial DH key pair
	if _, err := rand.Read(dr.DHRatchetPrivKey[:]); err != nil {
		return nil, fmt.Errorf("failed to generate DH key pair: %v", err)
	}
	curve25519.ScalarBaseMult(&dr.DHRatchetPubKey, &dr.DHRatchetPrivKey)

	// Initialize root key from shared secret
	copy(dr.RootKey[:], sharedSecret[:])

	dr.SendMessageNum = 0
	dr.RecvMessageNum = 0

	return dr, nil
}

// Encrypt encrypts a plaintext message and advances the sending chain
// Returns: (ciphertext, nonce, error)
func (dr *DoubleRatchet) Encrypt(plaintext []byte) ([]byte, [24]byte, error) {
	// Derive message key from current send chain key
	messageKey, nextChainKey, err := DeriveMessageKey(dr.SendChainKey[:])
	if err != nil {
		return nil, [24]byte{}, fmt.Errorf("failed to derive message key: %v", err)
	}

	// Generate random nonce for NaCl secretbox
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, [24]byte{}, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// Encrypt using NaCl secretbox
	ciphertext := secretbox.Seal(nil, plaintext, &nonce, &messageKey)

	// Update send chain key for forward secrecy
	dr.SendChainKey = nextChainKey

	// Increment message counter
	dr.SendMessageNum++

	// Zero out the message key (forward secrecy)
	ZeroKey(&messageKey)

	return ciphertext, nonce, nil
}

// Decrypt decrypts a ciphertext message and advances the receiving chain
// Handles out-of-order messages by storing skipped keys
func (dr *DoubleRatchet) Decrypt(ciphertext []byte, nonce [24]byte, messageNum int, senderDHPubKey [32]byte) ([]byte, error) {
	// Check if we need to perform a DH ratchet step
	if senderDHPubKey != dr.RemoteDHPubKey {
		if err := dr.performDHRatchet(senderDHPubKey); err != nil {
			return nil, fmt.Errorf("DH ratchet failed: %v", err)
		}
	}

	// Check if this is an out-of-order message (use skipped key)
	if messageNum < dr.RecvMessageNum {
		if messageKey, exists := dr.SkippedKeys[messageNum]; exists {
			plaintext, ok := secretbox.Open(nil, ciphertext, &nonce, &messageKey)
			if !ok {
				return nil, errors.New("decryption failed with skipped key")
			}
			// Remove used skipped key
			delete(dr.SkippedKeys, messageNum)
			ZeroKey(&messageKey)
			return plaintext, nil
		}
		return nil, errors.New("skipped key not found for out-of-order message")
	}

	// Skip ahead and store keys for missing messages
	for dr.RecvMessageNum < messageNum {
		messageKey, nextChainKey, err := DeriveMessageKey(dr.RecvChainKey[:])
		if err != nil {
			return nil, fmt.Errorf("failed to derive skipped key: %v", err)
		}
		dr.SkippedKeys[dr.RecvMessageNum] = messageKey
		dr.RecvChainKey = nextChainKey
		dr.RecvMessageNum++
	}

	// Derive message key for current message
	messageKey, nextChainKey, err := DeriveMessageKey(dr.RecvChainKey[:])
	if err != nil {
		return nil, fmt.Errorf("failed to derive message key: %v", err)
	}

	// Decrypt
	plaintext, ok := secretbox.Open(nil, ciphertext, &nonce, &messageKey)
	if !ok {
		return nil, errors.New("decryption failed")
	}

	// Update receive chain key
	dr.RecvChainKey = nextChainKey
	dr.RecvMessageNum++

	// Zero out the message key
	ZeroKey(&messageKey)

	return plaintext, nil
}

// performDHRatchet performs a Diffie-Hellman ratchet step when receiving a new DH public key
func (dr *DoubleRatchet) performDHRatchet(newRemoteDHPubKey [32]byte) error {
	// Store the new remote DH public key
	dr.RemoteDHPubKey = newRemoteDHPubKey

	// Perform DH with our current private key and new remote public key
	dhOutput, err := curve25519.X25519(dr.DHRatchetPrivKey[:], dr.RemoteDHPubKey[:])
	if err != nil {
		return fmt.Errorf("DH computation failed: %v", err)
	}

	var dhOutputArray [32]byte
	copy(dhOutputArray[:], dhOutput)

	// Derive new root key and receiving chain key
	newRootKey, recvChainKey, err := DeriveRootKey(dr.RootKey[:], dhOutputArray[:])
	if err != nil {
		return fmt.Errorf("root key derivation failed: %v", err)
	}

	dr.RootKey = newRootKey
	dr.RecvChainKey = recvChainKey
	dr.RecvMessageNum = 0 // Reset receive counter

	// Generate new DH key pair for sending
	if _, err := rand.Read(dr.DHRatchetPrivKey[:]); err != nil {
		return fmt.Errorf("failed to generate new DH key pair: %v", err)
	}
	curve25519.ScalarBaseMult(&dr.DHRatchetPubKey, &dr.DHRatchetPrivKey)

	// Perform DH with new private key and remote public key
	dhOutput2, err := curve25519.X25519(dr.DHRatchetPrivKey[:], dr.RemoteDHPubKey[:])
	if err != nil {
		return fmt.Errorf("second DH computation failed: %v", err)
	}

	var dhOutputArray2 [32]byte
	copy(dhOutputArray2[:], dhOutput2)

	// Derive new root key and sending chain key
	newRootKey2, sendChainKey, err := DeriveRootKey(dr.RootKey[:], dhOutputArray2[:])
	if err != nil {
		return fmt.Errorf("second root key derivation failed: %v", err)
	}

	dr.RootKey = newRootKey2
	dr.SendChainKey = sendChainKey
	dr.SendMessageNum = 0 // Reset send counter

	return nil
}

// GetCurrentDHPubKey returns the current DH public key for this ratchet
// This should be sent with each message so the receiver can perform DH ratchet if needed
func (dr *DoubleRatchet) GetCurrentDHPubKey() [32]byte {
	return dr.DHRatchetPubKey
}

// initializeSenderChains initializes the chain keys for the initiator (sender) after receiving
// the receiver's ephemeral public key in the key exchange ACK.
// This derives both SendChainKey (for sending) and RecvChainKey (for receiving) from the same
// DH operation, ensuring symmetric derivation with the receiver.
// IMPORTANT: Does NOT generate new DH key pair - the initial key pair is used for the first message.
func (dr *DoubleRatchet) initializeSenderChains(remoteDHPubKey [32]byte) error {
	// Store the remote DH public key
	dr.RemoteDHPubKey = remoteDHPubKey

	// Perform DH with our initial private key and remote's public key
	dhOutput, err := curve25519.X25519(dr.DHRatchetPrivKey[:], remoteDHPubKey[:])
	if err != nil {
		return fmt.Errorf("DH computation failed: %v", err)
	}

	var dhOutputArray [32]byte
	copy(dhOutputArray[:], dhOutput)

	// Derive new root key and chain key from the DH output
	// The sender uses this as SendChainKey
	newRootKey, chainKey, err := DeriveRootKey(dr.RootKey[:], dhOutputArray[:])
	if err != nil {
		return fmt.Errorf("root key derivation failed: %v", err)
	}

	dr.RootKey = newRootKey
	dr.SendChainKey = chainKey
	dr.SendMessageNum = 0

	// For the receiver's messages back to us, we'll derive RecvChainKey when we
	// receive a message with a different DH pub key from the receiver.
	// For now, set RecvChainKey to the same value so we can receive replies
	// (the receiver will use the symmetric derivation).
	dr.RecvChainKey = chainKey
	dr.RecvMessageNum = 0

	return nil
}

// initializeReceiverChains initializes the chain keys for the receiver after accepting
// the initiator's key exchange request.
// This derives RecvChainKey (for receiving) from the DH operation.
// IMPORTANT: Does NOT generate new DH key pair - the initial key pair is used.
// The full DH ratchet step (with new key generation) happens when receiving a message
// with a different DH public key.
func (dr *DoubleRatchet) initializeReceiverChains(remoteDHPubKey [32]byte) error {
	// Store the remote DH public key
	dr.RemoteDHPubKey = remoteDHPubKey

	// Perform DH with our initial private key and remote's public key
	dhOutput, err := curve25519.X25519(dr.DHRatchetPrivKey[:], remoteDHPubKey[:])
	if err != nil {
		return fmt.Errorf("DH computation failed: %v", err)
	}

	var dhOutputArray [32]byte
	copy(dhOutputArray[:], dhOutput)

	// Derive new root key and chain key from the DH output
	// The receiver uses this as RecvChainKey (symmetric with sender's SendChainKey)
	newRootKey, chainKey, err := DeriveRootKey(dr.RootKey[:], dhOutputArray[:])
	if err != nil {
		return fmt.Errorf("root key derivation failed: %v", err)
	}

	dr.RootKey = newRootKey
	dr.RecvChainKey = chainKey
	dr.RecvMessageNum = 0

	// For sending messages back, we'll derive SendChainKey when we send our first reply.
	// For now, set SendChainKey to the same value so we can reply immediately.
	dr.SendChainKey = chainKey
	dr.SendMessageNum = 0

	return nil
}

// CleanupOldSkippedKeys removes skipped keys older than a certain age
// This prevents unbounded memory growth from missing messages
func (dr *DoubleRatchet) CleanupOldSkippedKeys(maxSkip int) {
	// Keep only the most recent maxSkip keys
	if len(dr.SkippedKeys) <= maxSkip {
		return
	}

	// Find the minimum message number we want to keep
	minKeepNum := dr.RecvMessageNum - maxSkip

	for msgNum, key := range dr.SkippedKeys {
		if msgNum < minKeepNum {
			ZeroKey(&key)
			delete(dr.SkippedKeys, msgNum)
		}
	}
}
