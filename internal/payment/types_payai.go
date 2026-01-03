package payment

// PayAI facilitator uses a nested request structure different from standard x402
// See: https://docs.payai.network/x402/reference

// PayAIVerifyRequest is sent to PayAI facilitator /verify endpoint
type PayAIVerifyRequest struct {
	X402Version         int                       `json:"x402Version"`
	PaymentPayload      *PayAIPaymentPayload      `json:"paymentPayload"`
	PaymentRequirements *PayAIPaymentRequirements `json:"paymentRequirements"`
}

// PayAIPaymentPayload wraps the payment signature and metadata
type PayAIPaymentPayload struct {
	X402Version int                 `json:"x402Version"`
	Scheme      string              `json:"scheme"`
	Network     string              `json:"network"`
	Payload     *X402PayloadWrapper `json:"payload"`
}

// PayAIPaymentRequirements specifies what the payment must satisfy
type PayAIPaymentRequirements struct {
	Scheme            string `json:"scheme"`
	Network           string `json:"network"`
	MaxAmountRequired string `json:"maxAmountRequired"` // Wei/lamports as string
	PayTo             string `json:"payTo"`             // Recipient address
	Asset             string `json:"asset,omitempty"`   // Token contract address (empty or 0x0 for native)
	Resource          string `json:"resource"`          // API endpoint or resource identifier
	Description       string `json:"description"`       // Human-readable payment description
	MimeType          string `json:"mimeType,omitempty"`
	MaxTimeoutSeconds int    `json:"maxTimeoutSeconds,omitempty"`
}

// PayAIVerifyResponse is returned from PayAI facilitator /verify endpoint
type PayAIVerifyResponse struct {
	IsValid       bool   `json:"isValid"`
	InvalidReason string `json:"invalidReason,omitempty"`
	Payer         string `json:"payer,omitempty"`
}

// PayAISettleRequest is sent to PayAI facilitator /settle endpoint
type PayAISettleRequest struct {
	X402Version         int                       `json:"x402Version"`
	PaymentPayload      *PayAIPaymentPayload      `json:"paymentPayload"`
	PaymentRequirements *PayAIPaymentRequirements `json:"paymentRequirements"`
	WaitUntil           string                    `json:"waitUntil,omitempty"` // "submitted" or "confirmed"
}

// PayAISettleResponse is returned from PayAI facilitator /settle endpoint
type PayAISettleResponse struct {
	Success     bool   `json:"success"`
	ErrorReason string `json:"errorReason,omitempty"`
	Payer       string `json:"payer"`
	Transaction string `json:"transaction"` // Note: "transaction" not "transactionId"
	Network     string `json:"network"`
}
