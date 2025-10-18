package auth

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
)

// Ed25519Provider handles Ed25519 private key authentication
type Ed25519Provider struct {
	nodeKeyPair *crypto.KeyPair
}

// NewEd25519Provider creates a new Ed25519 auth provider
func NewEd25519Provider(keyPair *crypto.KeyPair) *Ed25519Provider {
	return &Ed25519Provider{
		nodeKeyPair: keyPair,
	}
}

// ProviderName returns the provider identifier
func (p *Ed25519Provider) ProviderName() string {
	return "ed25519"
}

// Ed25519Credentials represents Ed25519 authentication credentials
type Ed25519Credentials struct {
	Challenge string `json:"challenge"` // Hex-encoded challenge
	Signature string `json:"signature"` // Hex-encoded signature
	PublicKey string `json:"public_key"` // Hex-encoded public key (optional, can derive from node)
}

// Authenticate validates Ed25519 signature and returns peer_id
// For Ed25519 auth, the user must sign the challenge with the node's private key
// This proves they have access to the node's private key file
func (p *Ed25519Provider) Authenticate(credentials interface{}) (string, string, error) {
	creds, ok := credentials.(*Ed25519Credentials)
	if !ok {
		return "", "", fmt.Errorf("invalid credentials type for Ed25519 provider")
	}

	// Decode challenge
	challengeBytes, err := hex.DecodeString(creds.Challenge)
	if err != nil {
		return "", "", fmt.Errorf("invalid challenge encoding: %v", err)
	}

	// Decode signature
	signatureBytes, err := hex.DecodeString(creds.Signature)
	if err != nil {
		return "", "", fmt.Errorf("invalid signature encoding: %v", err)
	}

	// Use node's public key for verification
	publicKey := p.nodeKeyPair.PublicKey

	// If public key provided in credentials, validate it matches node's key
	if creds.PublicKey != "" {
		providedPubKey, err := hex.DecodeString(creds.PublicKey)
		if err != nil {
			return "", "", fmt.Errorf("invalid public key encoding: %v", err)
		}

		if len(providedPubKey) != ed25519.PublicKeySize {
			return "", "", fmt.Errorf("invalid public key size: expected %d, got %d",
				ed25519.PublicKeySize, len(providedPubKey))
		}

		// Verify provided key matches node's key
		providedKeyStr := hex.EncodeToString(providedPubKey)
		nodeKeyStr := hex.EncodeToString(publicKey)
		if providedKeyStr != nodeKeyStr {
			return "", "", fmt.Errorf("provided public key does not match node's public key")
		}
	}

	// Verify signature
	if !ed25519.Verify(publicKey, challengeBytes, signatureBytes) {
		return "", "", fmt.Errorf("signature verification failed")
	}

	// Derive peer_id from public key
	peerID := crypto.DerivePeerID(publicKey)

	// For Ed25519 auth, no wallet address
	return peerID, "", nil
}

// SignChallenge is a helper method for signing challenges (used in testing/CLI)
func SignChallenge(keyPair *crypto.KeyPair, challenge string) (string, error) {
	challengeBytes, err := hex.DecodeString(challenge)
	if err != nil {
		return "", fmt.Errorf("invalid challenge encoding: %v", err)
	}

	signature := keyPair.Sign(challengeBytes)
	return hex.EncodeToString(signature), nil
}
