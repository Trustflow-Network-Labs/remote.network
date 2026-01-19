package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/secretbox"
)

// ChatCryptoManager manages all cryptographic operations for the chat system
// It integrates Double Ratchet for 1-on-1 chats and Sender Keys for group chats
type ChatCryptoManager struct {
	keyPair *KeyPair // Node's Ed25519 identity keys

	// Double Ratchet state per conversation
	ratchets map[string]*DoubleRatchet // conversationID -> DoubleRatchet
	mu       sync.RWMutex               // Protects ratchets map

	// Group sender keys
	groupKeys map[string]*GroupSenderKeys // groupID -> GroupSenderKeys
	groupMu   sync.RWMutex                 // Protects groupKeys map

	// Master encryption key for encrypting ratchet state at rest
	masterKey [32]byte
}

// NewChatCryptoManager creates a new chat crypto manager
func NewChatCryptoManager(keyPair *KeyPair) (*ChatCryptoManager, error) {
	// Derive master encryption key from node's private key
	masterKey, err := DeriveMasterEncryptionKey(keyPair.PrivateKey.Seed())
	if err != nil {
		return nil, fmt.Errorf("failed to derive master key: %v", err)
	}

	return &ChatCryptoManager{
		keyPair:   keyPair,
		ratchets:  make(map[string]*DoubleRatchet),
		groupKeys: make(map[string]*GroupSenderKeys),
		masterKey: masterKey,
	}, nil
}

// InitiateKeyExchange initiates a key exchange with a remote peer
// Returns our ephemeral X25519 public key and a signature for authentication
func (ccm *ChatCryptoManager) InitiateKeyExchange(conversationID string, remotePeerEd25519PubKey ed25519.PublicKey) (ephemeralPubKey [32]byte, signature []byte, err error) {
	// Convert remote Ed25519 public key to X25519 for DH
	remoteX25519PubKey, err := ed25519PublicKeyToCurve25519(remotePeerEd25519PubKey)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("failed to convert remote public key: %v", err)
	}

	// Convert our Ed25519 private key to X25519
	ourX25519PrivKey, err := ed25519PrivateKeyToCurve25519(ccm.keyPair.PrivateKey)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("failed to convert our private key: %v", err)
	}

	// Perform initial DH to derive shared secret
	sharedSecret, err := curve25519.X25519(ourX25519PrivKey[:], remoteX25519PubKey[:])
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("DH failed: %v", err)
	}

	var sharedSecretArray [32]byte
	copy(sharedSecretArray[:], sharedSecret)

	// Create new Double Ratchet as initiator
	dr, err := NewDoubleRatchet(sharedSecretArray, *remoteX25519PubKey)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("failed to create ratchet: %v", err)
	}

	// Store ratchet
	ccm.mu.Lock()
	ccm.ratchets[conversationID] = dr
	ccm.mu.Unlock()

	// Get our ephemeral public key
	ephemeralPubKey = dr.GetCurrentDHPubKey()

	// Sign the ephemeral public key with our Ed25519 identity key for authentication
	signature = ed25519.Sign(ccm.keyPair.PrivateKey, ephemeralPubKey[:])

	return ephemeralPubKey, signature, nil
}

// CompleteKeyExchange completes the key exchange for the initiator when receiving ACK
// This initializes the chains using the receiver's ephemeral public key
func (ccm *ChatCryptoManager) CompleteKeyExchange(
	conversationID string,
	remotePeerEd25519PubKey ed25519.PublicKey,
	remoteEphemeralPubKey [32]byte,
	signature []byte,
) error {
	// Verify signature
	if !ed25519.Verify(remotePeerEd25519PubKey, remoteEphemeralPubKey[:], signature) {
		return errors.New("signature verification failed")
	}

	// Get the ratchet
	ccm.mu.Lock()
	dr, exists := ccm.ratchets[conversationID]
	ccm.mu.Unlock()

	if !exists {
		return errors.New("ratchet not found for conversation")
	}

	// Initialize chains directly WITHOUT performing a full DH ratchet step that generates new keys.
	// The initiator should use its initial DH key pair for the first message.
	// The full DH ratchet step (with new key generation) happens when receiving a message
	// with a different DH public key from the receiver.
	if err := dr.initializeSenderChains(remoteEphemeralPubKey); err != nil {
		return fmt.Errorf("failed to initialize sender chains: %v", err)
	}

	return nil
}

// AcceptKeyExchange accepts a key exchange from a remote peer
// Verifies the signature and initializes the Double Ratchet
func (ccm *ChatCryptoManager) AcceptKeyExchange(
	conversationID string,
	remotePeerEd25519PubKey ed25519.PublicKey,
	remoteEphemeralPubKey [32]byte,
	signature []byte,
) (ourEphemeralPubKey [32]byte, ourSignature []byte, err error) {
	// Verify signature
	if !ed25519.Verify(remotePeerEd25519PubKey, remoteEphemeralPubKey[:], signature) {
		return [32]byte{}, nil, errors.New("signature verification failed")
	}

	// Convert our Ed25519 private key to X25519
	ourX25519PrivKey, err := ed25519PrivateKeyToCurve25519(ccm.keyPair.PrivateKey)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("failed to convert our private key: %v", err)
	}

	// Convert remote Ed25519 public key to X25519
	remoteX25519PubKey, err := ed25519PublicKeyToCurve25519(remotePeerEd25519PubKey)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("failed to convert remote public key: %v", err)
	}

	// Perform DH to derive shared secret
	sharedSecret, err := curve25519.X25519(ourX25519PrivKey[:], remoteX25519PubKey[:])
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("DH failed: %v", err)
	}

	var sharedSecretArray [32]byte
	copy(sharedSecretArray[:], sharedSecret)

	// Create new Double Ratchet as receiver
	dr, err := NewDoubleRatchetReceiver(sharedSecretArray)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("failed to create ratchet: %v", err)
	}

	// Set remote's ephemeral public key (from the initiator)
	dr.RemoteDHPubKey = remoteEphemeralPubKey

	// Initialize chains directly WITHOUT performing a full DH ratchet step.
	// The receiver should only derive initial chains and wait for the first message.
	// When the first message arrives with the sender's (possibly new) DH pub key,
	// THEN the DH ratchet step will be performed.
	if err := dr.initializeReceiverChains(remoteEphemeralPubKey); err != nil {
		return [32]byte{}, nil, fmt.Errorf("failed to initialize receiver chains: %v", err)
	}

	// Store ratchet
	ccm.mu.Lock()
	ccm.ratchets[conversationID] = dr
	ccm.mu.Unlock()

	// Get our ephemeral public key (the initial one, NOT a newly generated one)
	ourEphemeralPubKey = dr.GetCurrentDHPubKey()

	// Sign our ephemeral public key
	ourSignature = ed25519.Sign(ccm.keyPair.PrivateKey, ourEphemeralPubKey[:])

	return ourEphemeralPubKey, ourSignature, nil
}

// Encrypt1on1Message encrypts a message for 1-on-1 chat using Double Ratchet
func (ccm *ChatCryptoManager) Encrypt1on1Message(conversationID string, plaintext []byte) (
	ciphertext []byte,
	nonce [24]byte,
	messageNum int,
	dhPubKey [32]byte,
	err error,
) {
	ccm.mu.RLock()
	dr, exists := ccm.ratchets[conversationID]
	ccm.mu.RUnlock()

	if !exists {
		return nil, [24]byte{}, 0, [32]byte{}, errors.New("ratchet not found for conversation")
	}

	// Encrypt with Double Ratchet
	ciphertext, nonce, err = dr.Encrypt(plaintext)
	if err != nil {
		return nil, [24]byte{}, 0, [32]byte{}, err
	}

	messageNum = dr.SendMessageNum - 1 // -1 because Encrypt() already incremented
	dhPubKey = dr.GetCurrentDHPubKey()

	return ciphertext, nonce, messageNum, dhPubKey, nil
}

// Decrypt1on1Message decrypts a message from 1-on-1 chat using Double Ratchet
func (ccm *ChatCryptoManager) Decrypt1on1Message(
	conversationID string,
	ciphertext []byte,
	nonce [24]byte,
	messageNum int,
	senderDHPubKey [32]byte,
) (plaintext []byte, err error) {
	ccm.mu.RLock()
	dr, exists := ccm.ratchets[conversationID]
	ccm.mu.RUnlock()

	if !exists {
		return nil, errors.New("ratchet not found for conversation")
	}

	// Decrypt with Double Ratchet
	plaintext, err = dr.Decrypt(ciphertext, nonce, messageNum, senderDHPubKey)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// GetRatchet returns the ratchet for a conversation (for persistence)
func (ccm *ChatCryptoManager) GetRatchet(conversationID string) (*DoubleRatchet, error) {
	ccm.mu.RLock()
	defer ccm.mu.RUnlock()

	dr, exists := ccm.ratchets[conversationID]
	if !exists {
		return nil, errors.New("ratchet not found")
	}

	return dr, nil
}

// SetRatchet sets the ratchet for a conversation (when loading from DB)
func (ccm *ChatCryptoManager) SetRatchet(conversationID string, dr *DoubleRatchet) {
	ccm.mu.Lock()
	defer ccm.mu.Unlock()

	ccm.ratchets[conversationID] = dr
}

// EncryptRatchetForStorage encrypts a ratchet state for storage in DB
func (ccm *ChatCryptoManager) EncryptRatchetForStorage(dr *DoubleRatchet) ([]byte, [24]byte, error) {
	// Serialize ratchet to bytes (simplified - in production use proper serialization)
	// For now, just concatenate all fields
	plaintext := make([]byte, 0, 256)
	plaintext = append(plaintext, dr.DHRatchetPrivKey[:]...)
	plaintext = append(plaintext, dr.DHRatchetPubKey[:]...)
	plaintext = append(plaintext, dr.RemoteDHPubKey[:]...)
	plaintext = append(plaintext, dr.RootKey[:]...)
	plaintext = append(plaintext, dr.SendChainKey[:]...)
	plaintext = append(plaintext, dr.RecvChainKey[:]...)

	// Generate nonce
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, [24]byte{}, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// Encrypt with master key
	ciphertext := secretbox.Seal(nil, plaintext, &nonce, &ccm.masterKey)

	return ciphertext, nonce, nil
}

// DecryptRatchetFromStorage decrypts a ratchet state from DB storage
func (ccm *ChatCryptoManager) DecryptRatchetFromStorage(ciphertext []byte, nonce [24]byte) (*DoubleRatchet, error) {
	// Decrypt with master key
	plaintext, ok := secretbox.Open(nil, ciphertext, &nonce, &ccm.masterKey)
	if !ok {
		return nil, errors.New("failed to decrypt ratchet state")
	}

	// Deserialize (simplified - in production use proper deserialization)
	if len(plaintext) < 192 { // 6 * 32 bytes
		return nil, errors.New("invalid ratchet state size")
	}

	dr := &DoubleRatchet{
		SkippedKeys: make(map[int][32]byte),
	}

	offset := 0
	copy(dr.DHRatchetPrivKey[:], plaintext[offset:offset+32])
	offset += 32
	copy(dr.DHRatchetPubKey[:], plaintext[offset:offset+32])
	offset += 32
	copy(dr.RemoteDHPubKey[:], plaintext[offset:offset+32])
	offset += 32
	copy(dr.RootKey[:], plaintext[offset:offset+32])
	offset += 32
	copy(dr.SendChainKey[:], plaintext[offset:offset+32])
	offset += 32
	copy(dr.RecvChainKey[:], plaintext[offset:offset+32])

	return dr, nil
}

// EncryptMessageKeyForStorage encrypts a single message key for storage in DB
func (ccm *ChatCryptoManager) EncryptMessageKeyForStorage(messageKey [32]byte) ([]byte, [24]byte, error) {
	// Generate nonce
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, [24]byte{}, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// Encrypt message key with master key
	ciphertext := secretbox.Seal(nil, messageKey[:], &nonce, &ccm.masterKey)

	return ciphertext, nonce, nil
}

// DecryptMessageKeyFromStorage decrypts a single message key from DB storage
func (ccm *ChatCryptoManager) DecryptMessageKeyFromStorage(ciphertext []byte, nonce [24]byte) ([32]byte, error) {
	// Decrypt with master key
	plaintext, ok := secretbox.Open(nil, ciphertext, &nonce, &ccm.masterKey)
	if !ok {
		return [32]byte{}, errors.New("failed to decrypt message key")
	}

	if len(plaintext) != 32 {
		return [32]byte{}, errors.New("invalid message key size")
	}

	var messageKey [32]byte
	copy(messageKey[:], plaintext)

	return messageKey, nil
}

// CreateGroupSenderKeys creates sender keys for a new group
func (ccm *ChatCryptoManager) CreateGroupSenderKeys(groupID, ourPeerID string) error {
	ccm.groupMu.Lock()
	defer ccm.groupMu.Unlock()

	// Use our Ed25519 private key for signing
	gsk, err := NewGroupSenderKeys(groupID, ourPeerID, ccm.keyPair.PrivateKey)
	if err != nil {
		return err
	}

	ccm.groupKeys[groupID] = gsk
	return nil
}

// EncryptGroupMessage encrypts a message for a group
func (ccm *ChatCryptoManager) EncryptGroupMessage(groupID string, plaintext []byte) (
	ciphertext []byte,
	nonce [24]byte,
	messageNum int,
	signature []byte,
	err error,
) {
	ccm.groupMu.RLock()
	gsk, exists := ccm.groupKeys[groupID]
	ccm.groupMu.RUnlock()

	if !exists {
		return nil, [24]byte{}, 0, nil, errors.New("group sender keys not found")
	}

	return gsk.EncryptGroupMessage(plaintext)
}

// DecryptGroupMessage decrypts a message from a group member
func (ccm *ChatCryptoManager) DecryptGroupMessage(
	groupID, senderPeerID string,
	ciphertext []byte,
	nonce [24]byte,
	messageNum int,
	signature []byte,
	senderPublicKey ed25519.PublicKey,
) (plaintext []byte, err error) {
	ccm.groupMu.RLock()
	gsk, exists := ccm.groupKeys[groupID]
	ccm.groupMu.RUnlock()

	if !exists {
		return nil, errors.New("group sender keys not found")
	}

	return gsk.DecryptGroupMessage(senderPeerID, ciphertext, nonce, messageNum, signature, senderPublicKey)
}

// ed25519PublicKeyToCurve25519 converts an Ed25519 public key to X25519/Curve25519
// using the proper birational map between Edwards and Montgomery curves.
// This is the mathematically correct conversion that preserves the key relationship.
func ed25519PublicKeyToCurve25519(ed25519Pub ed25519.PublicKey) (*[32]byte, error) {
	// Verify Ed25519 public key size
	if len(ed25519Pub) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Ed25519 public key size")
	}

	// Ed25519 public keys are points on the Edwards curve in compressed form.
	// The 32-byte encoding is the y-coordinate with the sign of x in the top bit.
	// We need to convert to the Montgomery u-coordinate using: u = (1 + y) / (1 - y)

	// Extract the y-coordinate (clear the sign bit)
	var yBytes [32]byte
	copy(yBytes[:], ed25519Pub)
	yBytes[31] &= 0x7F // Clear sign bit

	// Convert y to field element
	// We need to compute u = (1 + y) / (1 - y) mod p where p = 2^255 - 19

	// Use filippo.io/edwards25519 for proper field arithmetic
	// The edwards25519 package represents points internally and can convert

	// Alternative: Use the relationship directly with big integers
	// For simplicity and correctness, we'll use the standard conversion formula

	// The Ed25519 public key bytes represent a point on the curve.
	// We use the montgomery u-coordinate formula: u = (1+y)/(1-y)

	// Import the point using edwards25519 and extract Montgomery u-coordinate
	var curve25519Pub [32]byte

	// Use the direct formula with field arithmetic
	// p = 2^255 - 19
	// y is encoded in little-endian as the Ed25519 public key (with sign bit cleared)

	// For a simpler and more reliable approach, use the low-level conversion
	// that Go's x/crypto provides via the internal structure of Ed25519 keys

	// The correct conversion uses the birational map:
	// Given Edwards point (x, y), Montgomery u = (1 + y) / (1 - y)

	// We'll implement using big.Int for clarity
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19)) // 2^255 - 19

	// y is the Ed25519 public key with sign bit cleared, in little-endian
	y := new(big.Int).SetBytes(reverseBytes(yBytes[:]))

	// Compute (1 + y) mod p
	one := big.NewInt(1)
	numerator := new(big.Int).Add(one, y)
	numerator.Mod(numerator, p)

	// Compute (1 - y) mod p
	denominator := new(big.Int).Sub(one, y)
	denominator.Mod(denominator, p)
	if denominator.Sign() < 0 {
		denominator.Add(denominator, p)
	}

	// Compute modular inverse of denominator
	denominatorInv := new(big.Int).ModInverse(denominator, p)
	if denominatorInv == nil {
		return nil, errors.New("failed to compute modular inverse - invalid point")
	}

	// u = numerator * denominatorInv mod p
	u := new(big.Int).Mul(numerator, denominatorInv)
	u.Mod(u, p)

	// Convert u to 32 bytes in little-endian
	uBytes := u.Bytes()
	// Pad to 32 bytes and reverse for little-endian
	uPadded := make([]byte, 32)
	copy(uPadded[32-len(uBytes):], uBytes)
	copy(curve25519Pub[:], reverseBytes(uPadded))

	return &curve25519Pub, nil
}

// ed25519PrivateKeyToCurve25519 converts an Ed25519 private key to X25519/Curve25519
// using the standard scalar derivation with proper clamping.
func ed25519PrivateKeyToCurve25519(ed25519Priv ed25519.PrivateKey) (*[32]byte, error) {
	// Ed25519 private keys are 64 bytes: 32-byte seed + 32-byte public key
	if len(ed25519Priv) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key size")
	}

	// The standard way to derive the X25519 private key from Ed25519:
	// 1. Hash the seed with SHA-512
	// 2. Take the first 32 bytes
	// 3. Apply clamping: clear bits 0,1,2 and 255, set bit 254
	var curve25519Priv [32]byte
	seed := ed25519Priv.Seed()
	hash := sha512.Sum512(seed)
	copy(curve25519Priv[:], hash[:32])

	// Apply X25519 clamping
	curve25519Priv[0] &= 248  // Clear bits 0, 1, 2
	curve25519Priv[31] &= 127 // Clear bit 255
	curve25519Priv[31] |= 64  // Set bit 254

	return &curve25519Priv, nil
}

// reverseBytes reverses a byte slice (for little-endian conversion)
func reverseBytes(b []byte) []byte {
	result := make([]byte, len(b))
	for i := 0; i < len(b); i++ {
		result[i] = b[len(b)-1-i]
	}
	return result
}
