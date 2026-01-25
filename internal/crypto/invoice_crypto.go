package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

// InvoiceCryptoManager handles encryption/decryption for invoice messages
// using one-shot ECDH with ephemeral keys for forward secrecy.
// Unlike chat (which uses Double Ratchet for ongoing conversations),
// invoices are transactional and don't require persistent ratchet state.
type InvoiceCryptoManager struct {
	keyPair *KeyPair // Node's Ed25519 identity keys
}

// NewInvoiceCryptoManager creates a new invoice crypto manager
func NewInvoiceCryptoManager(keyPair *KeyPair) *InvoiceCryptoManager {
	return &InvoiceCryptoManager{
		keyPair: keyPair,
	}
}

// EncryptInvoice encrypts invoice plaintext for a recipient using one-shot ECDH.
// Flow:
//  1. Generate ephemeral X25519 key pair
//  2. Convert recipient's Ed25519 public key to X25519
//  3. Perform ECDH to derive shared secret
//  4. Derive encryption key using HKDF
//  5. Generate random nonce and encrypt with NaCl secretbox
//  6. Sign the message for authentication
//
// Returns:
//   - ciphertext: encrypted payload
//   - nonce: 24-byte random nonce for secretbox
//   - ephemeralPubKey: 32-byte X25519 public key for recipient's DH
//   - signature: Ed25519 signature over (ephemeralPubKey || ciphertext || nonce)
func (icm *InvoiceCryptoManager) EncryptInvoice(
	plaintext []byte,
	recipientEd25519PubKey ed25519.PublicKey,
) (ciphertext []byte, nonce [24]byte, ephemeralPubKey [32]byte, signature []byte, err error) {
	// 1. Generate ephemeral X25519 key pair for forward secrecy
	var ephemeralPrivKey [32]byte
	if _, err := rand.Read(ephemeralPrivKey[:]); err != nil {
		return nil, [24]byte{}, [32]byte{}, nil, fmt.Errorf("failed to generate ephemeral private key: %v", err)
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
		return nil, [24]byte{}, [32]byte{}, nil, fmt.Errorf("failed to convert recipient public key: %v", err)
	}

	// 3. Perform ECDH: shared_secret = X25519(ephemeral_priv, recipient_x25519_pub)
	sharedSecret, err := curve25519.X25519(ephemeralPrivKey[:], recipientX25519PubKey[:])
	if err != nil {
		return nil, [24]byte{}, [32]byte{}, nil, fmt.Errorf("ECDH failed: %v", err)
	}

	// 4. Derive encryption key using HKDF-SHA256
	encryptionKey, err := deriveInvoiceKey(sharedSecret)
	if err != nil {
		return nil, [24]byte{}, [32]byte{}, nil, fmt.Errorf("failed to derive encryption key: %v", err)
	}
	defer ZeroKey(&encryptionKey) // Clear key from memory after use

	// 5. Generate random 24-byte nonce
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, [24]byte{}, [32]byte{}, nil, fmt.Errorf("failed to generate nonce: %v", err)
	}

	// 6. Encrypt with NaCl secretbox (XSalsa20 + Poly1305)
	ciphertext = secretbox.Seal(nil, plaintext, &nonce, &encryptionKey)

	// 7. Sign the message for authentication: Sign(ephemeralPubKey || ciphertext || nonce)
	signaturePayload := make([]byte, 0, len(ephemeralPubKey)+len(ciphertext)+len(nonce))
	signaturePayload = append(signaturePayload, ephemeralPubKey[:]...)
	signaturePayload = append(signaturePayload, ciphertext...)
	signaturePayload = append(signaturePayload, nonce[:]...)
	signature = ed25519.Sign(icm.keyPair.PrivateKey, signaturePayload)

	// Clear ephemeral private key from memory
	for i := range ephemeralPrivKey {
		ephemeralPrivKey[i] = 0
	}

	return ciphertext, nonce, ephemeralPubKey, signature, nil
}

// DecryptInvoice decrypts an encrypted invoice using one-shot ECDH.
// Flow:
//  1. Verify Ed25519 signature over (ephemeralPubKey || ciphertext || nonce)
//  2. Convert our Ed25519 private key to X25519
//  3. Perform ECDH with sender's ephemeral public key
//  4. Derive decryption key using HKDF
//  5. Decrypt with NaCl secretbox
//
// Parameters:
//   - ciphertext: encrypted payload
//   - nonce: 24-byte nonce used for encryption
//   - senderEphemeralPubKey: 32-byte X25519 public key from sender
//   - signature: Ed25519 signature to verify
//   - senderEd25519PubKey: sender's Ed25519 identity public key for verification
func (icm *InvoiceCryptoManager) DecryptInvoice(
	ciphertext []byte,
	nonce [24]byte,
	senderEphemeralPubKey [32]byte,
	signature []byte,
	senderEd25519PubKey ed25519.PublicKey,
) (plaintext []byte, err error) {
	// 1. Verify signature: Verify(sender_pub, ephemeral_pub || ciphertext || nonce, signature)
	signaturePayload := make([]byte, 0, len(senderEphemeralPubKey)+len(ciphertext)+len(nonce))
	signaturePayload = append(signaturePayload, senderEphemeralPubKey[:]...)
	signaturePayload = append(signaturePayload, ciphertext...)
	signaturePayload = append(signaturePayload, nonce[:]...)

	if !ed25519.Verify(senderEd25519PubKey, signaturePayload, signature) {
		return nil, errors.New("signature verification failed")
	}

	// 2. Convert our Ed25519 private key to X25519
	ourX25519PrivKey, err := ed25519PrivateKeyToCurve25519(icm.keyPair.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert our private key: %v", err)
	}
	defer ZeroKey(ourX25519PrivKey) // Clear key from memory after use

	// 3. Perform ECDH: shared_secret = X25519(our_x25519_priv, sender_ephemeral_pub)
	sharedSecret, err := curve25519.X25519(ourX25519PrivKey[:], senderEphemeralPubKey[:])
	if err != nil {
		return nil, fmt.Errorf("ECDH failed: %v", err)
	}

	// 4. Derive decryption key using HKDF-SHA256
	decryptionKey, err := deriveInvoiceKey(sharedSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to derive decryption key: %v", err)
	}
	defer ZeroKey(&decryptionKey) // Clear key from memory after use

	// 5. Decrypt with NaCl secretbox
	plaintext, ok := secretbox.Open(nil, ciphertext, &nonce, &decryptionKey)
	if !ok {
		return nil, errors.New("decryption failed - message integrity check failed")
	}

	return plaintext, nil
}

// deriveInvoiceKey derives a 32-byte encryption key from a shared secret
// using HKDF-SHA256 with InfoInvoiceKey as the info parameter
func deriveInvoiceKey(sharedSecret []byte) ([32]byte, error) {
	var key [32]byte

	// HKDF-SHA256 with shared secret as input key material
	// No salt (nil), use InfoInvoiceKey for domain separation
	hkdfReader := hkdf.New(sha256.New, sharedSecret, nil, []byte(InfoInvoiceKey))

	if _, err := io.ReadFull(hkdfReader, key[:]); err != nil {
		return key, fmt.Errorf("HKDF failed: %v", err)
	}

	return key, nil
}

// GetPublicKey returns the public key for this invoice crypto manager
// Used when the sender needs to include their identity for the recipient to look up
func (icm *InvoiceCryptoManager) GetPublicKey() ed25519.PublicKey {
	return icm.keyPair.PublicKey
}

// GetPeerID returns the peer ID for this invoice crypto manager
func (icm *InvoiceCryptoManager) GetPeerID() string {
	return icm.keyPair.PeerID()
}
