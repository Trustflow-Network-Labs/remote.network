package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTClaims represents the claims stored in JWT tokens
type JWTClaims struct {
	WalletAddress string `json:"wallet_address,omitempty"` // For wallet auth (MetaMask/Keplr)
	PeerID        string `json:"peer_id"`                   // Node's peer_id
	AuthProvider  string `json:"auth_provider"`             // "ed25519", "metamask", "keplr", "email"
	jwt.RegisteredClaims
}

// JWTManager handles JWT token generation and validation
type JWTManager struct {
	secretKey []byte
	issuer    string
}

// NewJWTManager creates a new JWT manager with a secret key
func NewJWTManager(secretKey string, issuer string) *JWTManager {
	return &JWTManager{
		secretKey: []byte(secretKey),
		issuer:    issuer,
	}
}

// GenerateToken creates a new JWT token with the given claims
func (jm *JWTManager) GenerateToken(peerID, authProvider, walletAddress string, duration time.Duration) (string, error) {
	now := time.Now()
	expiresAt := now.Add(duration)

	claims := JWTClaims{
		WalletAddress: walletAddress,
		PeerID:        peerID,
		AuthProvider:  authProvider,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    jm.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jm.secretKey)
}

// ValidateToken validates a JWT token and returns the claims
func (jm *JWTManager) ValidateToken(tokenString string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jm.secretKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %v", err)
	}

	claims, ok := token.Claims.(*JWTClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}

// AuthMiddleware is an HTTP middleware that validates JWT tokens
func (jm *JWTManager) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Missing Authorization header", http.StatusUnauthorized)
			return
		}

		// Expected format: "Bearer <token>"
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]
		claims, err := jm.ValidateToken(tokenString)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid token: %v", err), http.StatusUnauthorized)
			return
		}

		// Add claims to request context
		ctx := context.WithValue(r.Context(), "claims", claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetClaims retrieves JWT claims from the request context
func GetClaims(r *http.Request) (*JWTClaims, error) {
	claims, ok := r.Context().Value("claims").(*JWTClaims)
	if !ok {
		return nil, fmt.Errorf("no claims found in context")
	}
	return claims, nil
}
