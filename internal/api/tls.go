package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// generateECDSACertificate creates a self-signed X.509 certificate using ECDSA P-256
// This is specifically for the API server to ensure browser compatibility
// (browsers don't support Ed25519 for HTTPS yet)
func generateECDSACertificate() ([]byte, *ecdsa.PrivateKey, error) {
	// Generate ECDSA P-256 private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate ECDSA key: %v", err)
	}

	// Certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			Organization: []string{"Remote Network Node"},
			CommonName:   "Remote Network API",
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour), // Valid for 1 year
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// Allow any IP/DNS for flexibility in deployment
		IPAddresses: []net.IP{net.IPv4zero, net.IPv6zero, net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:    []string{"localhost", "*"},
	}

	// Create self-signed certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %v", err)
	}

	return certDER, privateKey, nil
}

// saveECDSACertificateToPEM saves an ECDSA certificate and private key to PEM files
func saveECDSACertificateToPEM(certDER []byte, privateKey *ecdsa.PrivateKey, certPath, keyPath string) error {
	// Encode certificate to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})
	if certPEM == nil {
		return fmt.Errorf("failed to encode certificate to PEM")
	}

	// Save certificate file (readable by all)
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write certificate file: %v", err)
	}

	// Encode private key to PEM (PKCS#8 format)
	privKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %v", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privKeyBytes,
	})
	if keyPEM == nil {
		return fmt.Errorf("failed to encode private key to PEM")
	}

	// Save private key file (readable only by owner)
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key file: %v", err)
	}

	return nil
}

// loadECDSACertificateFromPEM loads an ECDSA certificate from PEM files
// Returns error if certificate doesn't exist, is invalid, or is expiring soon (<30 days)
func loadECDSACertificateFromPEM(certPath, keyPath string) (tls.Certificate, error) {
	// Load certificate
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to read certificate file: %v", err)
	}

	// Load private key
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to read private key file: %v", err)
	}

	// Parse PEM blocks
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return tls.Certificate{}, fmt.Errorf("failed to decode certificate PEM")
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return tls.Certificate{}, fmt.Errorf("failed to decode private key PEM")
	}

	// Accept multiple private key formats (PKCS#8, EC, RSA)
	// This allows using Let's Encrypt certificates directly without conversion
	validKeyTypes := []string{"PRIVATE KEY", "EC PRIVATE KEY", "RSA PRIVATE KEY"}
	validType := false
	for _, t := range validKeyTypes {
		if keyBlock.Type == t {
			validType = true
			break
		}
	}
	if !validType {
		return tls.Certificate{}, fmt.Errorf("unsupported private key type: %s (expected PRIVATE KEY, EC PRIVATE KEY, or RSA PRIVATE KEY)", keyBlock.Type)
	}

	// Parse certificate
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to parse certificate: %v", err)
	}

	// Check if certificate is expired or expiring soon (< 30 days)
	now := time.Now()
	if now.After(cert.NotAfter) {
		return tls.Certificate{}, fmt.Errorf("certificate expired on %v", cert.NotAfter)
	}
	if now.Add(30 * 24 * time.Hour).After(cert.NotAfter) {
		return tls.Certificate{}, fmt.Errorf("certificate expiring soon (expires %v)", cert.NotAfter)
	}

	// Load as tls.Certificate
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to load X509 key pair: %v", err)
	}

	return tlsCert, nil
}

// loadOrGenerateAPICertificates loads existing ECDSA certificates or generates new ones
// This is used by the API server for HTTPS (browser compatibility)
func loadOrGenerateAPICertificates(paths *utils.AppPaths, logger *utils.LogsManager) (tls.Certificate, error) {
	certPath := paths.GetDataPath("api-cert.pem")
	keyPath := paths.GetDataPath("api-key.pem")

	// Try to load existing certificate
	tlsCert, err := loadECDSACertificateFromPEM(certPath, keyPath)
	if err == nil {
		logger.Info(fmt.Sprintf("Loaded existing ECDSA certificate for API server from %s", certPath), "api")
		return tlsCert, nil
	}

	// Certificate doesn't exist or is expired, generate new one
	logger.Info(fmt.Sprintf("Generating new ECDSA P-256 certificate for API server (reason: %v)", err), "api")

	certDER, privateKey, err := generateECDSACertificate()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to generate ECDSA certificate: %v", err)
	}

	// Save certificate to disk
	if err := saveECDSACertificateToPEM(certDER, privateKey, certPath, keyPath); err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to save ECDSA certificate: %v", err)
	}

	logger.Info(fmt.Sprintf("ECDSA certificate saved to %s", certPath), "api")

	// Load the newly created certificate
	tlsCert, err = loadECDSACertificateFromPEM(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("failed to load newly created certificate: %v", err)
	}

	return tlsCert, nil
}
