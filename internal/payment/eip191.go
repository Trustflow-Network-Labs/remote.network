package payment

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// SignEIP191 signs payment data using EIP-191 personal message signing
// This format is used by PayAI facilitator
func SignEIP191(privateKey *ecdsa.PrivateKey, sig *PaymentSignature) (string, error) {
	// Create message hash (EIP-191 format)
	// Format: Network:Sender:Recipient:Amount:Currency:Nonce:Timestamp
	message := fmt.Sprintf("%s:%s:%s:%.6f:%s:%s:%d",
		sig.Network, sig.Sender, sig.Recipient, sig.Amount, sig.Currency, sig.Nonce, sig.Timestamp)

	// Hash message using Keccak256
	messageHash := crypto.Keccak256Hash([]byte(message))

	// Sign hash
	signature, err := crypto.Sign(messageHash.Bytes(), privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign message: %v", err)
	}

	// Convert to hex
	return hexutil.Encode(signature), nil
}
