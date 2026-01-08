package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// X402Client handles communication with x402 payment facilitators
type X402Client struct {
	facilitatorURL string
	verifyEndpoint string
	settleEndpoint string
	refundEndpoint string
	httpClient     *http.Client
	config         *utils.ConfigManager
	logger         *utils.LogsManager
	maxRetries     int
	retryBackoff   time.Duration
}

// NewX402Client creates a new x402 facilitator client
func NewX402Client(config *utils.ConfigManager, logger *utils.LogsManager) *X402Client {
	timeout := time.Duration(config.GetConfigInt("x402_timeout_seconds", 10, 1, 60)) * time.Second
	maxRetries := config.GetConfigInt("x402_max_retries", 3, 0, 10)
	retryBackoff := time.Duration(config.GetConfigInt("x402_retry_backoff_ms", 1000, 100, 10000)) * time.Millisecond

	facilitatorURL := config.GetConfigWithDefault("x402_facilitator_url", "https://www.x402.org/facilitator")
	verifyEndpoint := config.GetConfigWithDefault("x402_verify_endpoint", "/verify")
	settleEndpoint := config.GetConfigWithDefault("x402_settle_endpoint", "/settle")
	refundEndpoint := config.GetConfigWithDefault("x402_refund_endpoint", "/refund")

	client := &X402Client{
		facilitatorURL: facilitatorURL,
		verifyEndpoint: verifyEndpoint,
		settleEndpoint: settleEndpoint,
		refundEndpoint: refundEndpoint,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		config:       config,
		logger:       logger,
		maxRetries:   maxRetries,
		retryBackoff: retryBackoff,
	}

	logger.Info(fmt.Sprintf("X402 Client initialized: url=%s, verify=%s, settle=%s, refund=%s",
		facilitatorURL, verifyEndpoint, settleEndpoint, refundEndpoint), "x402_client")

	return client
}

// VerifyPayment verifies a payment signature with the facilitator
func (c *X402Client) VerifyPayment(ctx context.Context, req *FacilitatorVerifyRequest) (*FacilitatorResponse, error) {
	if c.facilitatorURL == "" {
		return nil, ErrFacilitatorUnavailable
	}

	url := c.facilitatorURL + c.verifyEndpoint
	var lastErr error

	// Retry logic
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry (exponential backoff)
			backoff := c.retryBackoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}

			c.logger.Info(fmt.Sprintf("Retrying payment verification (attempt %d/%d)", attempt+1, c.maxRetries+1), "x402_client")
		}

		resp, err := c.sendRequest(ctx, "POST", url, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Don't retry on non-retryable errors
		if err == ErrFacilitatorRejected {
			return nil, err
		}
	}

	c.logger.Error(fmt.Sprintf("Payment verification failed after %d attempts: %v", c.maxRetries+1, lastErr), "x402_client")
	return nil, lastErr
}

// SettlePayment settles a payment on-chain
func (c *X402Client) SettlePayment(ctx context.Context, req *FacilitatorSettleRequest) (*FacilitatorSettleResponse, error) {
	if c.facilitatorURL == "" {
		return nil, ErrFacilitatorUnavailable
	}

	url := c.facilitatorURL + c.settleEndpoint

	var lastErr error

	// Retry logic
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.retryBackoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}

			c.logger.Info(fmt.Sprintf("Retrying payment settlement (attempt %d/%d)", attempt+1, c.maxRetries+1), "x402_client")
		}

		settleResp, err := c.sendSettleRequest(ctx, "POST", url, req)
		if err == nil {
			if settleResp.Success {
				c.logger.Info(fmt.Sprintf("Settlement successful: tx=%s, network=%s", settleResp.Transaction, settleResp.Network), "x402_client")
				return settleResp, nil
			}
			return nil, fmt.Errorf("settlement failed: %s - %s", settleResp.ErrorReason, settleResp.ErrorReasonDetail)
		}

		lastErr = err

		// Don't retry on non-retryable errors
		if err == ErrFacilitatorRejected {
			return nil, err
		}
	}

	c.logger.Error(fmt.Sprintf("Payment settlement failed after %d attempts: %v", c.maxRetries+1, lastErr), "x402_client")
	return nil, lastErr
}

// RefundPayment refunds a payment
func (c *X402Client) RefundPayment(ctx context.Context, transactionID string, refundAmount float64, reason string) error {
	if c.facilitatorURL == "" {
		return ErrFacilitatorUnavailable
	}

	url := c.facilitatorURL + c.refundEndpoint

	req := &FacilitatorRefundRequest{
		TransactionID: transactionID,
		RefundAmount:  refundAmount,
		Reason:        reason,
	}

	var lastErr error

	// Retry logic
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := c.retryBackoff * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}

			c.logger.Info(fmt.Sprintf("Retrying payment refund (attempt %d/%d)", attempt+1, c.maxRetries+1), "x402_client")
		}

		resp, err := c.sendRequest(ctx, "POST", url, req)
		if err == nil {
			if resp.Status == "refunded" || resp.Valid {
				return nil
			}
			return fmt.Errorf("refund failed: %s", resp.Error)
		}

		lastErr = err

		// Don't retry on non-retryable errors
		if err == ErrFacilitatorRejected {
			return err
		}
	}

	c.logger.Error(fmt.Sprintf("Payment refund failed after %d attempts: %v", c.maxRetries+1, lastErr), "x402_client")
	return lastErr
}

// sendSettleRequest sends a settlement request to the facilitator
func (c *X402Client) sendSettleRequest(ctx context.Context, method string, url string, body interface{}) (*FacilitatorSettleResponse, error) {
	// Marshal request body
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// DEBUG: Log the request details
	c.logger.Info(fmt.Sprintf("X402 Settle Request: %s %s", method, url), "x402_client")

	// Pretty-print JSON for debugging
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, jsonData, "", "  "); err == nil {
		c.logger.Info(fmt.Sprintf("X402 Settle Request Payload (pretty):\n%s", prettyJSON.String()), "x402_client")
	}

	c.logger.Info(fmt.Sprintf("X402 Settle Request Body (compact): %s", string(jsonData)), "x402_client")
	c.logger.Info(fmt.Sprintf("\n=== CURL COMMAND ===\ncurl -X %s '%s' \\\n  -H 'Content-Type: application/json' \\\n  -d '%s'\n===================", method, url, string(jsonData)), "x402_client")

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "remote-network-node/1.0")

	// Send request
	c.logger.Debug(fmt.Sprintf("Sending X402 settle request to %s", url), "x402_client")
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error(fmt.Sprintf("X402 HTTP settle request failed: %v (ctx.Err=%v)", err, ctx.Err()), "x402_client")
		// Check if it's a timeout
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrFacilitatorTimeout
		}
		return nil, ErrFacilitatorUnavailable
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	c.logger.Debug(fmt.Sprintf("X402 Settle Response: HTTP %d, Body: %s", httpResp.StatusCode, string(respBody)), "x402_client")

	// Check HTTP status code
	if httpResp.StatusCode >= 500 {
		// Server error - retry
		c.logger.Warn(fmt.Sprintf("X402 server error (HTTP %d), will retry", httpResp.StatusCode), "x402_client")
		return nil, ErrFacilitatorUnavailable
	}

	if httpResp.StatusCode >= 400 && httpResp.StatusCode < 500 {
		// Client error - don't retry
		c.logger.Warn(fmt.Sprintf("X402 client error (HTTP %d): %s", httpResp.StatusCode, string(respBody)), "x402_client")
		var resp FacilitatorSettleResponse
		if err := json.Unmarshal(respBody, &resp); err == nil && resp.ErrorReason != "" {
			return nil, fmt.Errorf("%w: %s", ErrFacilitatorRejected, resp.ErrorReason)
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrFacilitatorRejected, httpResp.StatusCode)
	}

	// Parse response
	var resp FacilitatorSettleResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse settle response: %v", err)
	}

	return &resp, nil
}

// sendRequest sends an HTTP request to the facilitator
func (c *X402Client) sendRequest(ctx context.Context, method string, url string, body interface{}) (*FacilitatorResponse, error) {
	// Marshal request body
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// DEBUG: Log the request details
	c.logger.Info(fmt.Sprintf("X402 Request: %s %s", method, url), "x402_client")

	// Pretty-print JSON for debugging
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, jsonData, "", "  "); err == nil {
		c.logger.Info(fmt.Sprintf("X402 Request Payload (pretty):\n%s", prettyJSON.String()), "x402_client")
	}

	c.logger.Info(fmt.Sprintf("X402 Request Body (compact): %s", string(jsonData)), "x402_client")
	c.logger.Info(fmt.Sprintf("\n=== CURL COMMAND ===\ncurl -X %s '%s' \\\n  -H 'Content-Type: application/json' \\\n  -d '%s'\n===================", method, url, string(jsonData)), "x402_client")

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "remote-network-node/1.0")

	// Send request
	c.logger.Debug(fmt.Sprintf("Sending X402 request to %s", url), "x402_client")
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error(fmt.Sprintf("X402 HTTP request failed: %v (ctx.Err=%v)", err, ctx.Err()), "x402_client")
		// Check if it's a timeout
		if ctx.Err() == context.DeadlineExceeded {
			return nil, ErrFacilitatorTimeout
		}
		return nil, ErrFacilitatorUnavailable
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	c.logger.Debug(fmt.Sprintf("X402 Response: HTTP %d, Body: %s", httpResp.StatusCode, string(respBody)), "x402_client")

	// Check HTTP status code
	if httpResp.StatusCode >= 500 {
		// Server error - retry
		c.logger.Warn(fmt.Sprintf("X402 server error (HTTP %d), will retry", httpResp.StatusCode), "x402_client")
		return nil, ErrFacilitatorUnavailable
	}

	if httpResp.StatusCode >= 400 && httpResp.StatusCode < 500 {
		// Client error - don't retry
		c.logger.Warn(fmt.Sprintf("X402 client error (HTTP %d): %s", httpResp.StatusCode, string(respBody)), "x402_client")
		var resp FacilitatorResponse
		if err := json.Unmarshal(respBody, &resp); err == nil && resp.Error != "" {
			return nil, fmt.Errorf("%w: %s", ErrFacilitatorRejected, resp.Error)
		}
		return nil, fmt.Errorf("%w: HTTP %d", ErrFacilitatorRejected, httpResp.StatusCode)
	}

	// Parse response
	var resp FacilitatorResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	return &resp, nil
}

// GetNetwork returns the configured network (testnet/mainnet)
func (c *X402Client) GetNetwork() string {
	return c.config.GetConfigWithDefault("x402_network", "testnet")
}

// GetChainID returns the configured chain ID
func (c *X402Client) GetChainID() string {
	return c.config.GetConfigWithDefault("x402_chain_id", "eip155:84532")
}

// GetSupported queries the facilitator's /supported endpoint to get supported payment schemes
func (c *X402Client) GetSupported(ctx context.Context) (*FacilitatorSupportedResponse, error) {
	if c.facilitatorURL == "" {
		return nil, ErrFacilitatorUnavailable
	}

	url := c.facilitatorURL + "/supported"

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	// Send request
	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	// Check status code
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("facilitator returned HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	// Parse response
	var resp FacilitatorSupportedResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	c.logger.Debug(fmt.Sprintf("Facilitator supports %d payment schemes", len(resp.Kinds)), "x402_client")
	return &resp, nil
}

// GetSolanaFeePayer queries the facilitator and returns the feePayer address for Solana network
func (c *X402Client) GetSolanaFeePayer(ctx context.Context, network string) (string, error) {
	supported, err := c.GetSupported(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to query facilitator: %v", err)
	}

	// Find matching Solana network in supported kinds
	for _, kind := range supported.Kinds {
		if kind.Network == network && kind.Extra != nil {
			if feePayer, ok := kind.Extra["feePayer"].(string); ok {
				c.logger.Debug(fmt.Sprintf("Found feePayer for %s: %s", network, feePayer), "x402_client")
				return feePayer, nil
			}
		}
	}

	return "", fmt.Errorf("facilitator does not provide feePayer for network %s", network)
}

// CreateTokenAccount requests the facilitator to create a Solana token account for a recipient
// The facilitator signs and broadcasts the transaction, paying for the account creation
// Returns the transaction signature
func (c *X402Client) CreateTokenAccount(
	ctx context.Context,
	network string,
	recipientAddress string,
	tokenSymbol string,
	unsignedTransaction string,
) (string, error) {
	if c.facilitatorURL == "" {
		return "", ErrFacilitatorUnavailable
	}

	// Use a custom endpoint for token account creation
	// This is not part of the standard x402 protocol, but some facilitators may support it
	url := c.facilitatorURL + "/create-token-account"

	type CreateTokenAccountRequest struct {
		Network             string `json:"network"`
		RecipientAddress    string `json:"recipient_address"`
		TokenSymbol         string `json:"token_symbol"`
		UnsignedTransaction string `json:"unsigned_transaction"`
	}

	req := &CreateTokenAccountRequest{
		Network:             network,
		RecipientAddress:    recipientAddress,
		TokenSymbol:         tokenSymbol,
		UnsignedTransaction: unsignedTransaction,
	}

	// Marshal request body
	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %v", err)
	}

	c.logger.Info(fmt.Sprintf("X402 CreateTokenAccount Request: POST %s", url), "x402_client")
	c.logger.Debug(fmt.Sprintf("Request body: %s", string(jsonData)), "x402_client")

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "remote-network-node/1.0")

	// Send request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.logger.Error(fmt.Sprintf("X402 HTTP request failed: %v", err), "x402_client")
		return "", ErrFacilitatorUnavailable
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}

	c.logger.Debug(fmt.Sprintf("X402 CreateTokenAccount Response: HTTP %d, Body: %s", httpResp.StatusCode, string(respBody)), "x402_client")

	// Check HTTP status code
	if httpResp.StatusCode >= 400 {
		return "", fmt.Errorf("facilitator returned error (HTTP %d): %s", httpResp.StatusCode, string(respBody))
	}

	// Parse response
	type CreateTokenAccountResponse struct {
		Success     bool   `json:"success"`
		Transaction string `json:"transaction"`
		Message     string `json:"message"`
		Error       string `json:"error"`
	}

	var resp CreateTokenAccountResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("failed to parse response: %v", err)
	}

	if !resp.Success {
		return "", fmt.Errorf("facilitator failed to create token account: %s", resp.Error)
	}

	c.logger.Info(fmt.Sprintf("Token account created successfully: %s", resp.Transaction), "x402_client")
	return resp.Transaction, nil
}
