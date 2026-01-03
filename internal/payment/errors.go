package payment

import "errors"

var (
	// Payment verification errors
	ErrPaymentRequired        = errors.New("payment signature required")
	ErrPaymentVerificationFailed = errors.New("payment verification failed")
	ErrInvalidPaymentSignature = errors.New("invalid payment signature")
	ErrInsufficientPayment    = errors.New("insufficient payment amount")
	ErrPaymentExpired         = errors.New("payment signature expired")
	ErrInvalidNetwork         = errors.New("invalid payment network")
	ErrCurrencyMismatch       = errors.New("payment currency does not match service pricing")
	ErrNetworkMismatch        = errors.New("payment network does not match service network")

	// Facilitator errors
	ErrFacilitatorUnavailable = errors.New("payment facilitator unavailable")
	ErrFacilitatorTimeout     = errors.New("payment facilitator timeout")
	ErrFacilitatorRejected    = errors.New("payment rejected by facilitator")

	// Wallet errors
	ErrWalletNotFound         = errors.New("wallet not found")
	ErrWalletLocked           = errors.New("wallet is locked")
	ErrInvalidPassphrase      = errors.New("invalid wallet passphrase")
	ErrWalletExists           = errors.New("wallet already exists")
	ErrInsufficientBalance    = errors.New("insufficient wallet balance")

	// Escrow errors
	ErrEscrowNotFound         = errors.New("escrow not found")
	ErrEscrowAlreadySettled   = errors.New("escrow already settled")
	ErrEscrowAlreadyRefunded  = errors.New("escrow already refunded")
	ErrEscrowInvalidState     = errors.New("invalid escrow state")
)
