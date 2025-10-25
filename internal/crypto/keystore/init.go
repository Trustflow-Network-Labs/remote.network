package keystore

import (
	"bufio"
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/crypto"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
)

// InitOrLoadKeystore initializes or loads the encrypted keystore
// Handles migration from unencrypted keys and passphrase prompts
func InitOrLoadKeystore(dataDir string, passphraseFile string, config *utils.ConfigManager) (*KeystoreData, error) {
	keystorePath := filepath.Join(dataDir, "keystore.dat")
	keysDir := filepath.Join(dataDir, "keys")

	// Check if keystore exists
	if _, err := os.Stat(keystorePath); err == nil {
		// Keystore exists, unlock it
		return unlockExistingKeystore(keystorePath, passphraseFile, config)
	}

	// Keystore doesn't exist - check if we have unencrypted keys to migrate
	if _, err := os.Stat(filepath.Join(keysDir, "ed25519_private.key")); err == nil {
		// Migrate existing unencrypted keys
		return migrateUnencryptedKeys(dataDir, keystorePath, keysDir, passphraseFile, config)
	}

	// No keystore and no existing keys - create fresh keystore
	return createFreshKeystore(keystorePath, passphraseFile, config)
}

// unlockExistingKeystore prompts for passphrase and unlocks the keystore
func unlockExistingKeystore(keystorePath string, passphraseFile string, config *utils.ConfigManager) (*KeystoreData, error) {
	fmt.Println("\n🔒 Encrypted keystore found")

	// Load keystore
	ks, err := LoadKeystore(keystorePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load keystore: %v", err)
	}

	// Get passphrase
	passphrase, err := getPassphrase(passphraseFile, false, config)
	if err != nil {
		return nil, err
	}

	// Unlock keystore
	data, err := UnlockKeystore(ks, passphrase)
	if err != nil {
		return nil, fmt.Errorf("failed to unlock keystore: %v", err)
	}

	fmt.Println("✓ Keystore unlocked successfully")
	return data, nil
}

// migrateUnencryptedKeys migrates existing unencrypted keys to encrypted keystore
func migrateUnencryptedKeys(dataDir, keystorePath, keysDir string, passphraseFile string, config *utils.ConfigManager) (*KeystoreData, error) {
	fmt.Println("\n📦 Unencrypted keys found - migrating to encrypted keystore")

	// Load existing keys
	keyPair, err := crypto.LoadKeys(keysDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing keys: %v", err)
	}

	// Generate JWT secret
	jwtSecret, err := GenerateJWTSecret()
	if err != nil {
		return nil, fmt.Errorf("failed to generate JWT secret: %v", err)
	}

	// Get passphrase
	passphrase, err := getPassphrase(passphraseFile, true, config)
	if err != nil {
		return nil, err
	}

	// Create encrypted keystore
	ks, err := CreateKeystore(passphrase, keyPair.PrivateKey, keyPair.PublicKey, jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create keystore: %v", err)
	}

	// Save keystore
	if err := SaveKeystore(ks, keystorePath); err != nil {
		return nil, fmt.Errorf("failed to save keystore: %v", err)
	}

	// Backup old keys before removing
	backupDir := filepath.Join(dataDir, "keys_backup_unencrypted")
	if err := os.MkdirAll(backupDir, 0700); err == nil {
		os.Rename(filepath.Join(keysDir, "ed25519_private.key"), filepath.Join(backupDir, "ed25519_private.key"))
		os.Rename(filepath.Join(keysDir, "ed25519_public.key"), filepath.Join(backupDir, "ed25519_public.key"))
		fmt.Printf("✓ Old unencrypted keys backed up to: %s\n", backupDir)
		fmt.Println("  You can safely delete this directory after verifying the node works correctly")
	}

	fmt.Printf("✓ Keystore created and saved to: %s\n", keystorePath)

	return &KeystoreData{
		Ed25519PrivateKey: keyPair.PrivateKey,
		Ed25519PublicKey:  keyPair.PublicKey,
		JWTSecret:         jwtSecret,
	}, nil
}

// createFreshKeystore creates a new keystore with new keys
func createFreshKeystore(keystorePath string, passphraseFile string, config *utils.ConfigManager) (*KeystoreData, error) {
	fmt.Println("\n🔑 No existing keys found - creating new encrypted keystore")

	// Generate new Ed25519 keypair
	keyPair, err := crypto.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %v", err)
	}

	// Generate JWT secret
	jwtSecret, err := GenerateJWTSecret()
	if err != nil {
		return nil, fmt.Errorf("failed to generate JWT secret: %v", err)
	}

	// Get passphrase
	passphrase, err := getPassphrase(passphraseFile, true, config)
	if err != nil {
		return nil, err
	}

	// Create encrypted keystore
	ks, err := CreateKeystore(passphrase, keyPair.PrivateKey, keyPair.PublicKey, jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create keystore: %v", err)
	}

	// Save keystore
	if err := SaveKeystore(ks, keystorePath); err != nil {
		return nil, fmt.Errorf("failed to save keystore: %v", err)
	}

	peerID := crypto.DerivePeerID(keyPair.PublicKey)
	fmt.Printf("✓ New keystore created\n")
	fmt.Printf("  Peer ID: %s\n", peerID)
	fmt.Printf("  Keystore: %s\n", keystorePath)

	return &KeystoreData{
		Ed25519PrivateKey: keyPair.PrivateKey,
		Ed25519PublicKey:  keyPair.PublicKey,
		JWTSecret:         jwtSecret,
	}, nil
}

// getPassphrase prompts the user for a passphrase or reads from file/config
func getPassphrase(passphraseFile string, isNewKeystore bool, config *utils.ConfigManager) (string, error) {
	// Priority 1: Check config for keystore_passphrase
	if config != nil {
		if configPassphrase, exists := config.GetConfig("keystore_passphrase"); exists && configPassphrase != "" {
			return configPassphrase, nil
		}
	}

	// Priority 2: Check if passphrase file is provided
	if passphraseFile != "" {
		passphrase, err := os.ReadFile(passphraseFile)
		if err != nil {
			return "", fmt.Errorf("failed to read passphrase file: %v", err)
		}
		return strings.TrimSpace(string(passphrase)), nil
	}

	// Priority 3: Interactive passphrase prompt
	if isNewKeystore {
		return promptNewPassphrase()
	}
	return promptPassphrase()
}

// promptPassphrase prompts for a passphrase (for unlocking)
func promptPassphrase() (string, error) {
	fmt.Print("Enter keystore passphrase: ")
	passphrase, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("failed to read passphrase: %v", err)
	}

	if len(passphrase) == 0 {
		return "", fmt.Errorf("passphrase cannot be empty")
	}

	return string(passphrase), nil
}

// promptNewPassphrase prompts for a new passphrase with confirmation
func promptNewPassphrase() (string, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🔐 KEYSTORE PASSPHRASE SETUP")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("")
	fmt.Println("Your node's cryptographic keys will be encrypted with a passphrase.")
	fmt.Println("This passphrase is required every time the node starts.")
	fmt.Println("")
	fmt.Println("⚠️  IMPORTANT:")
	fmt.Println("  • Choose a strong passphrase (minimum 8 characters recommended)")
	fmt.Println("  • You will need this passphrase on every node startup")
	fmt.Println("  • If you lose this passphrase, you will lose access to your node identity")
	fmt.Println("")
	fmt.Print("Press Enter to continue...")
	reader.ReadString('\n')

	for {
		fmt.Print("\nCreate passphrase: ")
		passphrase1, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("failed to read passphrase: %v", err)
		}

		if len(passphrase1) == 0 {
			fmt.Println("❌ Passphrase cannot be empty. Please try again.")
			continue
		}

		if len(passphrase1) < 8 {
			fmt.Println("⚠️  Warning: Passphrase is shorter than 8 characters")
			fmt.Print("Continue with this passphrase? (yes/no): ")
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))
			if response != "yes" && response != "y" {
				continue
			}
		}

		fmt.Print("Confirm passphrase: ")
		passphrase2, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("failed to read passphrase confirmation: %v", err)
		}

		if string(passphrase1) != string(passphrase2) {
			fmt.Println("❌ Passphrases do not match. Please try again.")
			continue
		}

		return string(passphrase1), nil
	}
}

// LoadKeysFromKeystore is a helper that converts KeystoreData to crypto.KeyPair
func LoadKeysFromKeystore(data *KeystoreData) (*crypto.KeyPair, error) {
	if len(data.Ed25519PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size in keystore")
	}
	if len(data.Ed25519PublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size in keystore")
	}

	return &crypto.KeyPair{
		PublicKey:  ed25519.PublicKey(data.Ed25519PublicKey),
		PrivateKey: ed25519.PrivateKey(data.Ed25519PrivateKey),
	}, nil
}
