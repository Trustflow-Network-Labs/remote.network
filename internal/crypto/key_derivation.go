package crypto

import (
	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
)

// Key derivation constants
const (
	// Key sizes
	KeySize          = 32 // 256 bits for symmetric keys
	NonceSize        = 24 // NaCl secretbox nonce size

	// HKDF info strings (domain separation)
	InfoRootKey      = "RemoteNetworkChatRootKey"
	InfoChainKey     = "RemoteNetworkChatChainKey"
	InfoMessageKey   = "RemoteNetworkChatMessageKey"
	InfoMasterKey    = "RemoteNetworkChatMasterKey"
	InfoSenderKey    = "RemoteNetworkChatSenderKey"
)

// DeriveRootKey derives a new root key and chain key from the current root key and DH output
// using HKDF with SHA-256
// Returns: (newRootKey [32]byte, newChainKey [32]byte)
func DeriveRootKey(currentRootKey, dhOutput []byte) ([32]byte, [32]byte, error) {
	var newRootKey, newChainKey [32]byte

	// HKDF-SHA256 with root key as salt and DH output as input key material
	hkdfReader := hkdf.New(sha256.New, dhOutput, currentRootKey, []byte(InfoRootKey))

	// Derive 64 bytes total: 32 for new root key + 32 for new chain key
	output := make([]byte, 64)
	if _, err := io.ReadFull(hkdfReader, output); err != nil {
		return newRootKey, newChainKey, err
	}

	copy(newRootKey[:], output[:32])
	copy(newChainKey[:], output[32:])

	return newRootKey, newChainKey, nil
}

// DeriveChainKey derives the next chain key from the current chain key
// using HKDF with SHA-256
// Returns: (nextChainKey [32]byte)
func DeriveChainKey(currentChainKey []byte) ([32]byte, error) {
	var nextChainKey [32]byte

	// HKDF-SHA256 with chain key as input key material
	hkdfReader := hkdf.New(sha256.New, currentChainKey, nil, []byte(InfoChainKey))

	if _, err := io.ReadFull(hkdfReader, nextChainKey[:]); err != nil {
		return nextChainKey, err
	}

	return nextChainKey, nil
}

// DeriveMessageKey derives a message key from the chain key
// This is used for encrypting/decrypting a single message
// Returns: (messageKey [32]byte, nextChainKey [32]byte)
func DeriveMessageKey(currentChainKey []byte) ([32]byte, [32]byte, error) {
	var messageKey, nextChainKey [32]byte

	// First derive the message key (with different info string)
	hkdfMsgReader := hkdf.New(sha256.New, currentChainKey, nil, []byte(InfoMessageKey))
	if _, err := io.ReadFull(hkdfMsgReader, messageKey[:]); err != nil {
		return messageKey, nextChainKey, err
	}

	// Then derive the next chain key for forward secrecy
	var err error
	nextChainKey, err = DeriveChainKey(currentChainKey)
	if err != nil {
		return messageKey, nextChainKey, err
	}

	return messageKey, nextChainKey, nil
}

// DeriveMasterEncryptionKey derives a master key for encrypting ratchet state at rest
// from the node's Ed25519 private key
// This ensures that ratchet keys are encrypted before being stored in the database
func DeriveMasterEncryptionKey(nodePrivateKey []byte) ([32]byte, error) {
	var masterKey [32]byte

	// HKDF-SHA256 with node's private key as input key material
	hkdfReader := hkdf.New(sha256.New, nodePrivateKey, nil, []byte(InfoMasterKey))

	if _, err := io.ReadFull(hkdfReader, masterKey[:]); err != nil {
		return masterKey, err
	}

	return masterKey, nil
}

// DeriveSenderKey derives a sender key for group chat encryption
// from the group ID and member's identity key
func DeriveSenderKey(groupID string, memberPrivateKey []byte) ([32]byte, error) {
	var senderKey [32]byte

	// HKDF-SHA256 with member's private key as input, group ID as salt
	hkdfReader := hkdf.New(sha256.New, memberPrivateKey, []byte(groupID), []byte(InfoSenderKey))

	if _, err := io.ReadFull(hkdfReader, senderKey[:]); err != nil {
		return senderKey, err
	}

	return senderKey, nil
}

// ZeroKey securely zeros out a key from memory
// This should be called immediately after a message key is used to ensure forward secrecy
func ZeroKey(key *[32]byte) {
	if key == nil {
		return
	}
	for i := range key {
		key[i] = 0
	}
}
