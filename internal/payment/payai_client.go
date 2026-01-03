package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// PayAIClient handles communication with PayAI facilitator
// See: https://docs.payai.network/x402/reference
type PayAIClient struct {
	baseURL     string
	httpClient  *http.Client
	logger      *utils.LogsManager
	mapper      *NetworkMapper
	maxRetries  int
	retryBackoff time.Duration
}

// NewPayAIClient creates a new PayAI facilitator client
func NewPayAIClient(baseURL string, logger *utils.LogsManager) *PayAIClient {
	return &PayAIClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:       logger,
		mapper:       NewNetworkMapper(),
		maxRetries:   3,
		retryBackoff: 2 * time.Second,
	}
}

// VerifyPayment verifies a payment signature with PayAI facilitator
func (c *PayAIClient) VerifyPayment(
	ctx context.Context,
	sig *PaymentSignature,
	requiredAmount float64,
	recipient string,
	description string,
) (*PayAIVerifyResponse, error) {
	// Convert network to PayAI format
	payaiNetwork, err := c.mapper.ToPayAI(sig.Network)
	if err != nil {
		return nil, err
	}

	// Convert amount to wei/lamports
	amountStr := c.convertToSmallestUnit(sig.Amount, sig.Currency)

	// Build PayAI request
	req := &PayAIVerifyRequest{
		X402Version: 1,
		PaymentPayload: &PayAIPaymentPayload{
			X402Version: 1,
			Scheme:      "exact",
			Network:     payaiNetwork,
			Payload: &X402PayloadWrapper{
				Signature: sig.Signature,
				Authorization: &X402Authorization{
					From:        sig.Sender,
					To:          sig.Recipient,
					Value:       amountStr,
					ValidAfter:  fmt.Sprintf("%d", sig.Timestamp-3600),
					ValidBefore: fmt.Sprintf("%d", sig.Timestamp+3600),
					Nonce:       sig.Nonce,
				},
				// NOTE: Domain and Metadata removed for x402 v2 compatibility
				// PayAI may need separate handling if it uses different format
			},
		},
		PaymentRequirements: &PayAIPaymentRequirements{
			Scheme:            "exact",
			Network:           payaiNetwork,
			MaxAmountRequired: amountStr,
			PayTo:             recipient,
			Asset:             c.getAssetAddress(sig.Currency, sig.Network),
			Resource:          fmt.Sprintf("/payment/%s", sig.Nonce),
			Description:       description,
		},
	}

	c.logger.Debug(fmt.Sprintf("PayAI verify request: network=%s, amount=%s, payer=%s",
		payaiNetwork, amountStr, sig.Sender[:8]), "payai_client")

	// Send request with retry
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.retryBackoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			c.logger.Info(fmt.Sprintf("Retrying PayAI verify (attempt %d/%d)", attempt+1, c.maxRetries+1), "payai_client")
		}

		resp, err := c.sendVerifyRequest(ctx, req)
		if err == nil {
			c.logger.Info(fmt.Sprintf("PayAI verify success: payer=%s, valid=%v",
				resp.Payer, resp.IsValid), "payai_client")
			return resp, nil
		}

		lastErr = err

		// Don't retry on client errors (4xx)
		if err == ErrFacilitatorRejected {
			return nil, err
		}
	}

	c.logger.Error(fmt.Sprintf("PayAI verify failed after %d attempts: %v", c.maxRetries+1, lastErr), "payai_client")
	return nil, lastErr
}

// SettlePayment settles a payment on-chain via PayAI facilitator
func (c *PayAIClient) SettlePayment(
	ctx context.Context,
	sig *PaymentSignature,
	actualAmount float64,
	recipient string,
	waitUntil string,
) (*PayAISettleResponse, error) {
	// Convert network to PayAI format
	payaiNetwork, err := c.mapper.ToPayAI(sig.Network)
	if err != nil {
		return nil, err
	}

	// Convert amount to wei/lamports
	amountStr := c.convertToSmallestUnit(actualAmount, sig.Currency)

	// Build PayAI settle request (re-use payment payload)
	req := &PayAISettleRequest{
		X402Version: 1,
		PaymentPayload: &PayAIPaymentPayload{
			X402Version: 1,
			Scheme:      "exact",
			Network:     payaiNetwork,
			Payload: &X402PayloadWrapper{
				Signature: sig.Signature,
				Authorization: &X402Authorization{
					From:        sig.Sender,
					To:          sig.Recipient,
					Value:       amountStr,
					ValidAfter:  fmt.Sprintf("%d", sig.Timestamp-3600),
					ValidBefore: fmt.Sprintf("%d", sig.Timestamp+3600),
					Nonce:       sig.Nonce,
				},
				// NOTE: Domain and Metadata removed for x402 v2 compatibility
				// PayAI may need separate handling if it uses different format
			},
		},
		PaymentRequirements: &PayAIPaymentRequirements{
			Scheme:            "exact",
			Network:           payaiNetwork,
			MaxAmountRequired: amountStr,
			PayTo:             recipient,
			Asset:             c.getAssetAddress(sig.Currency, sig.Network),
			Resource:          fmt.Sprintf("/payment/%s", sig.Nonce),
			Description:       "Payment settlement",
		},
		WaitUntil: waitUntil,
	}

	if waitUntil == "" {
		req.WaitUntil = "submitted" // Default to submitted
	}

	c.logger.Info(fmt.Sprintf("PayAI settle request: network=%s, amount=%s, waitUntil=%s",
		payaiNetwork, amountStr, req.WaitUntil), "payai_client")

	// Send request with retry
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.retryBackoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			c.logger.Info(fmt.Sprintf("Retrying PayAI settle (attempt %d/%d)", attempt+1, c.maxRetries+1), "payai_client")
		}

		resp, err := c.sendSettleRequest(ctx, req)
		if err == nil {
			c.logger.Info(fmt.Sprintf("PayAI settle success: tx=%s, network=%s",
				resp.Transaction[:min(16, len(resp.Transaction))], resp.Network), "payai_client")
			return resp, nil
		}

		lastErr = err

		// Don't retry on client errors (4xx)
		if err == ErrFacilitatorRejected {
			return nil, err
		}
	}

	c.logger.Error(fmt.Sprintf("PayAI settle failed after %d attempts: %v", c.maxRetries+1, lastErr), "payai_client")
	return nil, lastErr
}

// sendVerifyRequest sends HTTP request to PayAI /verify endpoint
func (c *PayAIClient) sendVerifyRequest(ctx context.Context, req *PayAIVerifyRequest) (*PayAIVerifyResponse, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	c.logger.Debug(fmt.Sprintf("PayAI verify request body: %s", string(jsonData)), "payai_client")

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/verify", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "remote-network-node/1.0")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrFacilitatorTimeout
		}
		return nil, ErrFacilitatorUnavailable
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	c.logger.Debug(fmt.Sprintf("PayAI verify response: HTTP %d, Body: %s", httpResp.StatusCode, string(respBody)), "payai_client")

	// Check HTTP status code
	if httpResp.StatusCode >= 500 {
		c.logger.Warn(fmt.Sprintf("PayAI server error (HTTP %d), will retry", httpResp.StatusCode), "payai_client")
		return nil, ErrFacilitatorUnavailable
	}

	if httpResp.StatusCode >= 400 && httpResp.StatusCode < 500 {
		c.logger.Warn(fmt.Sprintf("PayAI client error (HTTP %d): %s", httpResp.StatusCode, string(respBody)), "payai_client")
		var resp PayAIVerifyResponse
		if err := json.Unmarshal(respBody, &resp); err == nil && resp.InvalidReason != "" {
			return nil, fmt.Errorf("%w: %s", ErrFacilitatorRejected, resp.InvalidReason)
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrFacilitatorRejected, httpResp.StatusCode)
	}

	// Parse response
	var resp PayAIVerifyResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	return &resp, nil
}

// sendSettleRequest sends HTTP request to PayAI /settle endpoint
func (c *PayAIClient) sendSettleRequest(ctx context.Context, req *PayAISettleRequest) (*PayAISettleResponse, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	c.logger.Debug(fmt.Sprintf("PayAI settle request body: %s", string(jsonData)), "payai_client")

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/settle", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "remote-network-node/1.0")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrFacilitatorTimeout
		}
		return nil, ErrFacilitatorUnavailable
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	c.logger.Debug(fmt.Sprintf("PayAI settle response: HTTP %d, Body: %s", httpResp.StatusCode, string(respBody)), "payai_client")

	// Check HTTP status code
	if httpResp.StatusCode >= 500 {
		c.logger.Warn(fmt.Sprintf("PayAI server error (HTTP %d), will retry", httpResp.StatusCode), "payai_client")
		return nil, ErrFacilitatorUnavailable
	}

	if httpResp.StatusCode >= 400 && httpResp.StatusCode < 500 {
		c.logger.Warn(fmt.Sprintf("PayAI client error (HTTP %d): %s", httpResp.StatusCode, string(respBody)), "payai_client")
		var resp PayAISettleResponse
		if err := json.Unmarshal(respBody, &resp); err == nil && resp.ErrorReason != "" {
			return nil, fmt.Errorf("%w: %s", ErrFacilitatorRejected, resp.ErrorReason)
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrFacilitatorRejected, httpResp.StatusCode)
	}

	// Parse response
	var resp PayAISettleResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	return &resp, nil
}

// convertToSmallestUnit converts amount to wei (EVM) or lamports (Solana)
func (c *PayAIClient) convertToSmallestUnit(amount float64, currency string) string {
	var multiplier *big.Float

	switch currency {
	case "ETH":
		// 10^18 wei per ETH
		multiplier = new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	case "USDC", "USDT":
		// 10^6 base units per USDC/USDT
		multiplier = new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(6), nil))
	case "SOL":
		// 10^9 lamports per SOL
		multiplier = new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(9), nil))
	default:
		// Default to 18 decimals
		multiplier = new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	}

	// Multiply amount by multiplier
	result := new(big.Float).Mul(big.NewFloat(amount), multiplier)

	// Convert to integer
	intResult, _ := result.Int(nil)

	return intResult.String()
}

// getAssetAddress returns the token contract address or empty for native currency
func (c *PayAIClient) getAssetAddress(currency string, network string) string {
	// Map of token contract addresses per network
	assetMap := map[string]map[string]string{
		"eip155:84532": { // Base Sepolia
			"USDC": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			"ETH":  "", // Native token
		},
		"eip155:8453": { // Base Mainnet
			"USDC": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
			"ETH":  "", // Native token
		},
		"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1": { // Solana Devnet
			"USDC": "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
			"SOL":  "", // Native token
		},
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": { // Solana Mainnet
			"USDC": "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			"SOL":  "", // Native token
		},
	}

	if networkAssets, ok := assetMap[network]; ok {
		if asset, ok := networkAssets[currency]; ok {
			return asset
		}
	}

	// Return empty for native tokens or unknown
	return ""
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
