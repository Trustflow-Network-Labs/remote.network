package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// DataTransferCryptoManager handles ECDH key exchange for data transfer encryption.
// Unlike invoices (which encrypt inline), data transfers use ECDH to derive a symmetric key
// that is then used separately for streaming file encryption/decryption.
//
// Security properties:
//   - Forward secrecy: Each transfer uses ephemeral X25519 keys
//   - Authentication: Ed25519 signature verifies sender identity
//   - Replay protection: Timestamp validation (5-minute window)
type DataTransferCryptoManager struct {
	keyPair *KeyPair // Node's Ed25519 identity keys
}

// NewDataTransferCryptoManager creates a new data transfer crypto manager
func NewDataTransferCryptoManager(keyPair *KeyPair) *DataTransferCryptoManager {
	return &DataTransferCryptoManager{
		keyPair: keyPair,
	}
}

// GenerateKeyExchange generates ECDH parameters for the sender.
// This is called by the sender before encrypting a file.
//
// Flow:
//  1. Generate ephemeral X25519 key pair
//  2. Convert recipient's Ed25519 public key to X25519
//  3. Perform ECDH to derive shared secret
//  4. Derive AES-256 key using HKDF with InfoDataTransferKey
//  5. Sign (ephemeralPubKey || timestamp) for authentication
//
// Returns:
//   - ephemeralPubKey: 32-byte X25519 public key to send to recipient
//   - signature: Ed25519 signature for authentication
//   - timestamp: Unix timestamp for replay protection
//   - derivedKey: 32-byte AES-256 key for file encryption
func (dcm *DataTransferCryptoManager) GenerateKeyExchange(
	recipientEd25519PubKey ed25519.PublicKey,
) (ephemeralPubKey [32]byte, signature []byte, timestamp int64, derivedKey [32]byte, err error) {
	// 1. Generate ephemeral X25519 key pair for forward secrecy
	var ephemeralPrivKey [32]byte
	if _, err := rand.Read(ephemeralPrivKey[:]); err != nil {
		return [32]byte{}, nil, 0, [32]byte{}, fmt.Errorf("failed to generate ephemeral private key: %v", err)
	}

	// Clamp the ephemeral private key for X25519
	ephemeralPrivKey[0] &= 248
	ephemeralPrivKey[31] &= 127
	ephemeralPrivKey[31] |= 64

	// Derive ephemeral public key
	curve25519.ScalarBaseMult(&ephemeralPubKey, &ephemeralPrivKey)

	// 2. Convert recipient's Ed25519 public key to X25519
	recipientX25519PubKey, err := ed25519PublicKeyToCurve25519(recipientEd25519PubKey)
	if err != nil {
		return [32]byte{}, nil, 0, [32]byte{}, fmt.Errorf("failed to convert recipient public key: %v", err)
	}

	// 3. Perform ECDH: shared_secret = X25519(ephemeral_priv, recipient_x25519_pub)
	sharedSecret, err := curve25519.X25519(ephemeralPrivKey[:], recipientX25519PubKey[:])
	if err != nil {
		return [32]byte{}, nil, 0, [32]byte{}, fmt.Errorf("ECDH failed: %v", err)
	}

	// 4. Derive encryption key using HKDF-SHA256
	derivedKey, err = deriveDataTransferKey(sharedSecret)
	if err != nil {
		return [32]byte{}, nil, 0, [32]byte{}, fmt.Errorf("failed to derive encryption key: %v", err)
	}

	// 5. Generate timestamp for replay protection
	timestamp = time.Now().Unix()

	// 6. Sign (ephemeralPubKey || timestamp) for authentication
	signaturePayload := make([]byte, 0, 32+8)
	signaturePayload = append(signaturePayload, ephemeralPubKey[:]...)
	signaturePayload = append(signaturePayload, int64ToBytes(timestamp)...)
	signature = ed25519.Sign(dcm.keyPair.PrivateKey, signaturePayload)

	// Clear ephemeral private key from memory
	for i := range ephemeralPrivKey {
		ephemeralPrivKey[i] = 0
	}

	return ephemeralPubKey, signature, timestamp, derivedKey, nil
}

// DeriveDecryptionKey derives the decryption key on the receiver side.
// This is called by the receiver after receiving ECDH parameters.
//
// Flow:
//  1. Verify timestamp (reject if > 5 minutes old for replay protection)
//  2. Verify Ed25519 signature over (ephemeralPubKey || timestamp)
//  3. Convert our Ed25519 private key to X25519
//  4. Perform ECDH with sender's ephemeral public key
//  5. Derive same AES-256 key using HKDF
//
// Parameters:
//   - ephemeralPubKey: 32-byte X25519 public key from sender
//   - signature: Ed25519 signature to verify
//   - timestamp: Unix timestamp for replay protection
//   - senderEd25519PubKey: sender's Ed25519 identity public key
//
// Returns:
//   - derivedKey: 32-byte AES-256 key for file decryption
func (dcm *DataTransferCryptoManager) DeriveDecryptionKey(
	ephemeralPubKey [32]byte,
	signature []byte,
	timestamp int64,
	senderEd25519PubKey ed25519.PublicKey,
) (derivedKey [32]byte, err error) {
	// 1. Verify timestamp for replay protection (5-minute window)
	now := time.Now().Unix()
	if now-timestamp > 300 || timestamp > now+60 {
		return [32]byte{}, errors.New("timestamp outside valid window (replay protection)")
	}

	// 2. Verify signature: Verify(sender_pub, ephemeral_pub || timestamp, signature)
	signaturePayload := make([]byte, 0, 32+8)
	signaturePayload = append(signaturePayload, ephemeralPubKey[:]...)
	signaturePayload = append(signaturePayload, int64ToBytes(timestamp)...)

	if !ed25519.Verify(senderEd25519PubKey, signaturePayload, signature) {
		return [32]byte{}, errors.New("signature verification failed")
	}

	// 3. Convert our Ed25519 private key to X25519
	ourX25519PrivKey, err := ed25519PrivateKeyToCurve25519(dcm.keyPair.PrivateKey)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to convert our private key: %v", err)
	}
	defer ZeroKey(ourX25519PrivKey) // Clear key from memory after use

	// 4. Perform ECDH: shared_secret = X25519(our_x25519_priv, sender_ephemeral_pub)
	sharedSecret, err := curve25519.X25519(ourX25519PrivKey[:], ephemeralPubKey[:])
	if err != nil {
		return [32]byte{}, fmt.Errorf("ECDH failed: %v", err)
	}

	// 5. Derive decryption key using HKDF-SHA256
	derivedKey, err = deriveDataTransferKey(sharedSecret)
	if err != nil {
		return [32]byte{}, fmt.Errorf("failed to derive decryption key: %v", err)
	}

	return derivedKey, nil
}

// deriveDataTransferKey derives a 32-byte AES-256 key from a shared secret
// using HKDF-SHA256 with InfoDataTransferKey as the info parameter
func deriveDataTransferKey(sharedSecret []byte) ([32]byte, error) {
	var key [32]byte

	// HKDF-SHA256 with shared secret as input key material
	// No salt (nil), use InfoDataTransferKey for domain separation
	hkdfReader := hkdf.New(sha256.New, sharedSecret, nil, []byte(InfoDataTransferKey))

	if _, err := io.ReadFull(hkdfReader, key[:]); err != nil {
		return key, fmt.Errorf("HKDF failed: %v", err)
	}

	return key, nil
}

// int64ToBytes converts an int64 to a byte slice (big-endian)
func int64ToBytes(n int64) []byte {
	b := make([]byte, 8)
	b[0] = byte(n >> 56)
	b[1] = byte(n >> 48)
	b[2] = byte(n >> 40)
	b[3] = byte(n >> 32)
	b[4] = byte(n >> 24)
	b[5] = byte(n >> 16)
	b[6] = byte(n >> 8)
	b[7] = byte(n)
	return b
}

// GetPublicKey returns the public key for this data transfer crypto manager
func (dcm *DataTransferCryptoManager) GetPublicKey() ed25519.PublicKey {
	return dcm.keyPair.PublicKey
}

// GetPeerID returns the peer ID for this data transfer crypto manager
func (dcm *DataTransferCryptoManager) GetPeerID() string {
	return dcm.keyPair.PeerID()
}
