package payment

// PaymentData represents payment request data
type PaymentData struct {
	Amount      float64
	Currency    string
	Network     string
	Recipient   string
	Description string
	Metadata    map[string]interface{}
}

// SignatureFormat defines the signature format used
type SignatureFormat string

const (
	// SignatureFormatEIP191 uses EIP-191 personal message signing
	SignatureFormatEIP191 SignatureFormat = "eip191"
	// SignatureFormatEIP712 uses EIP-712 typed structured data signing (x402.org compatible)
	SignatureFormatEIP712 SignatureFormat = "eip712"
)

// PaymentSignature represents signed payment (x402 format)
type PaymentSignature struct {
	Version         string                 `json:"version"`
	Network         string                 `json:"network"`
	Sender          string                 `json:"sender"`
	Recipient       string                 `json:"recipient"`
	Amount          float64                `json:"amount"`
	Currency        string                 `json:"currency"`
	Nonce           string                 `json:"nonce"`
	Signature       string                 `json:"signature"`
	Timestamp       int64                  `json:"timestamp"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	SignatureFormat SignatureFormat        `json:"signature_format,omitempty"` // Format used for signature
	EIP712Domain    *X402Domain            `json:"eip712_domain,omitempty"`    // EIP-712 domain for EIP-712 signatures
}

// WalletBalance represents a wallet's current balance
type WalletBalance struct {
	WalletID  string  `json:"wallet_id"`
	Network   string  `json:"network"`
	Address   string  `json:"address"`
	Balance   float64 `json:"balance"`
	Currency  string  `json:"currency"`
	UpdatedAt int64   `json:"updated_at"`
}

// FacilitatorVerifyRequest sent to facilitator /verify endpoint (x402 v2 format)
// Uses nested structure with paymentPayload and paymentRequirements
// NOTE: No top-level x402Version field in v2 protocol
type FacilitatorVerifyRequest struct {
	PaymentPayload      *FacilitatorPaymentPayload      `json:"paymentPayload"`
	PaymentRequirements *FacilitatorPaymentRequirements `json:"paymentRequirements"`
}

// FacilitatorPaymentPayload wraps the payment signature and metadata (x402 v2 format)
type FacilitatorPaymentPayload struct {
	X402Version int                             `json:"x402Version"` // Version inside payload (v2)
	Resource    *X402ResourceInfo               `json:"resource"`    // Resource being paid for
	Accepted    *FacilitatorPaymentRequirements `json:"accepted"`    // What payment terms were accepted
	Payload     interface{}                     `json:"payload"`     // Scheme-specific payload: EVM uses X402PayloadWrapper, Solana uses map with "transaction" field
}

// X402ResourceInfo describes the resource being paid for
type X402ResourceInfo struct {
	URL         string `json:"url"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// FacilitatorPaymentRequirements specifies what the payment must satisfy (x402 v2 format)
type FacilitatorPaymentRequirements struct {
	Scheme            string                    `json:"scheme"`            // Payment scheme (e.g., "exact")
	Network           string                    `json:"network"`           // CAIP-2 network ID (e.g., "eip155:84532")
	Asset             string                    `json:"asset"`             // Token contract address (for ERC-20/SPL tokens)
	Amount            string                    `json:"amount"`            // Amount in wei/lamports as string (v2 uses "amount" not "maxAmountRequired")
	PayTo             string                    `json:"payTo"`             // Recipient address
	MaxTimeoutSeconds int                       `json:"maxTimeoutSeconds"` // Maximum timeout in seconds
	Extra             *PaymentRequirementsExtra `json:"extra,omitempty"`   // Extra parameters (e.g., EIP-712 domain info)
}

// PaymentRequirementsExtra contains additional parameters for payment requirements
type PaymentRequirementsExtra struct {
	Name     string `json:"name,omitempty"`     // EIP-712 domain name (for EVM)
	Version  string `json:"version,omitempty"`  // EIP-712 domain version (for EVM)
	FeePayer string `json:"feePayer,omitempty"` // Fee payer address (for Solana)
}

// X402PayloadWrapper wraps the authorization and signature (x402 v2 format)
// NOTE: Domain is NOT included in v2 - facilitator derives it from
// paymentRequirements.asset (verifyingContract), network (chainId), and extra (name, version)
type X402PayloadWrapper struct {
	Signature     string             `json:"signature"`
	Authorization *X402Authorization `json:"authorization"`
}

// X402Domain represents the EIP-712 domain used for signing (ERC-3009 format)
type X402Domain struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	ChainId           string `json:"chainId"`
	VerifyingContract string `json:"verifyingContract"` // Required: token contract address
}

// X402Authorization represents the payment authorization (x402 exact scheme)
type X402Authorization struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
	FeePayer    string `json:"feePayer,omitempty"` // For Solana transactions (who pays transaction fees)
}

// FacilitatorSettleRequest is the same as VerifyRequest per x402 protocol
// The /settle endpoint expects the full paymentPayload + paymentRequirements structure
type FacilitatorSettleRequest = FacilitatorVerifyRequest

// FacilitatorSupportedResponse is the response from /supported endpoint
type FacilitatorSupportedResponse struct {
	Kinds      []SupportedKind            `json:"kinds"`      // Supported payment schemes
	Extensions []string                   `json:"extensions"` // Supported extensions
	Signers    map[string][]string        `json:"signers"`    // Available signers by network
}

// SupportedKind represents a supported payment scheme
type SupportedKind struct {
	X402Version int                    `json:"x402Version"` // Protocol version (1 or 2)
	Scheme      string                 `json:"scheme"`      // Payment scheme (e.g., "exact")
	Network     string                 `json:"network"`     // Network identifier
	Extra       map[string]interface{} `json:"extra,omitempty"` // Extra data (e.g., feePayer for Solana)
}

// FacilitatorRefundRequest sent to facilitator /refund endpoint
type FacilitatorRefundRequest struct {
	TransactionID string  `json:"transactionId"`
	RefundAmount  float64 `json:"refundAmount,omitempty"` // For partial refunds
	Reason        string  `json:"reason,omitempty"`
}

// FacilitatorVerifyResponse from facilitator /verify endpoint
type FacilitatorVerifyResponse struct {
	IsValid       bool   `json:"isValid"`
	Valid         bool   `json:"valid"` // Backward compatibility
	InvalidReason string `json:"invalidReason,omitempty"`
	Payer         string `json:"payer,omitempty"`
	Error         string `json:"error,omitempty"`
}

// FacilitatorSettleResponse from facilitator /settle endpoint
type FacilitatorSettleResponse struct {
	Success           bool   `json:"success"`
	Transaction       string `json:"transaction,omitempty"`       // Blockchain transaction hash
	Network           string `json:"network,omitempty"`           // Network identifier
	Payer             string `json:"payer,omitempty"`             // Payer address
	ErrorReason       string `json:"errorReason,omitempty"`       // Error reason if failed
	ErrorReasonDetail string `json:"errorReasonDetail,omitempty"` // Detailed error message
}

// FacilitatorResponse is a unified response type (deprecated, use specific types)
type FacilitatorResponse struct {
	IsValid       bool    `json:"isValid"`
	Valid         bool    `json:"valid"` // Backward compatibility
	InvalidReason string  `json:"invalidReason,omitempty"`
	TransactionID string  `json:"transactionId,omitempty"`
	Error         string  `json:"error,omitempty"`
	Amount        float64 `json:"amount,omitempty"`
	Status        string  `json:"status,omitempty"` // "verified", "settled", "refunded", "failed"
	// Settlement-specific fields
	Success     bool   `json:"success,omitempty"`
	Transaction string `json:"transaction,omitempty"`
	Network     string `json:"network,omitempty"`
	Payer       string `json:"payer,omitempty"`
}
