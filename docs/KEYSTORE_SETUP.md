# Keystore Setup Guide

## Overview

The Remote Network node uses an **encrypted keystore** to protect your node's cryptographic keys. This guide explains the setup process, passphrase management, and automated deployment options.

---

## What is the Keystore?

The keystore (`keystore.dat`) is an encrypted file containing:
- **Ed25519 Private/Public Keys**: Your node's P2P identity
- **JWT Secret**: For API authentication

**Location:**
- **macOS**: `~/Library/Application Support/remote-network/keystore.dat`
- **Linux**: `~/.local/share/remote-network/keystore.dat`
- **Windows**: `%APPDATA%\remote-network\keystore.dat`

---

## First-Time Setup (Interactive)

### What Happens on First Run

When you start the node for the first time:

```bash
$ ./remote-network start
```

You'll see this prompt:

```
🔑 KEYSTORE PASSPHRASE SETUP
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Your node's cryptographic keys will be encrypted with a passphrase.
This passphrase is required every time the node starts.

⚠️  IMPORTANT:
  • Choose a strong passphrase (minimum 8 characters recommended)
  • You will need this passphrase on every node startup
  • If you lose this passphrase, you will lose access to your node identity

Press Enter to continue...

Create passphrase: ********
Confirm passphrase: ********

✓ New keystore created
  Peer ID: c540bee38ab8a72d354839127bd3fda6b46f2ab5
  Keystore: /Users/you/Library/Application Support/remote-network/keystore.dat
✓ Passphrase saved to OS keyring for future logins
```

### What is the Passphrase?

The passphrase is **your encryption key** for the keystore:
- **Not** a login password for the application
- **Not** stored anywhere in plaintext
- Used to encrypt/decrypt your node's private keys
- **Critical**: Without it, your keystore is useless

### Passphrase Requirements

- **Minimum**: 8 characters
- **Recommended**: 16+ characters with mix of:
  - Uppercase letters (A-Z)
  - Lowercase letters (a-z)
  - Numbers (0-9)
  - Special characters (!@#$%^&*)

**Example good passphrases:**
- `MyN0de!Secur3Pass2024`
- `correct-horse-battery-staple-87!`
- `P@ssw0rd_F0r_Rem0teN3tw0rk`

---

## OS Keyring Storage

### Where is the Passphrase Stored?

After you enter your passphrase **once**, it's automatically saved to your **operating system's secure keyring**:

| OS | Keyring | Access Method |
|----|---------|---------------|
| **macOS** | Keychain Access | Applications → Utilities → Keychain Access |
| **Windows** | Credential Manager | Control Panel → Credential Manager |
| **Linux** | Secret Service | gnome-keyring, kwallet, or pass |

**Service Name**: `remote-network-keystore`
**Key Name**: `remote-network-keystore-passphrase`

### Why Does It Ask for My OS Password?

When the application saves the passphrase to the OS keyring, **your operating system may prompt you for your user password**:

- **macOS**: "remote-network wants to access Keychain" → Enter your Mac password
- **Windows**: User Account Control (UAC) prompt → Click "Yes"
- **Linux**: "Authentication required" → Enter your user password

**This is normal and expected!** Your OS is protecting access to the secure keyring.

### Is This Secure?

✅ **Yes!** OS keyrings are designed for exactly this purpose:
- Encrypted with your OS user credentials
- Protected by OS-level security
- Used by browsers, email clients, SSH clients, etc.
- Much more secure than plaintext `.env` files

### Subsequent Runs

After the first setup, starting the node is seamless:

```bash
$ ./remote-network start

🔒 Encrypted keystore found
✓ Passphrase loaded from OS keyring
✓ Keystore unlocked successfully
✓ Node started successfully
```

No password prompt! The passphrase is loaded automatically using this priority order:
1. **Custom passphrase file** (if you provided `--passphrase-file` flag)
2. **Auto-created passphrase file** (if keyring save failed - see Troubleshooting section)
3. **OS keyring** (if it was saved successfully)
4. **Interactive prompt** (as last resort)

---

## Automated/Headless Deployments

For servers, Docker containers, or CI/CD pipelines where you can't enter passwords interactively.

### Option 1: Passphrase File

#### Create the Passphrase File

```bash
# Create a file with your passphrase
echo "YourStrongPassphraseHere" > /secure/path/keystore-passphrase.txt

# Set strict permissions (owner read-only)
chmod 400 /secure/path/keystore-passphrase.txt
```

#### Use the Passphrase File

```bash
# Start node with passphrase file
./remote-network start --passphrase-file /secure/path/keystore-passphrase.txt
```

**Short flag:**
```bash
./remote-network start -p /secure/path/keystore-passphrase.txt
```

### Option 2: Docker Secrets

For Docker deployments:

```dockerfile
# docker-compose.yml
services:
  remote-network:
    image: remote-network:latest
    command: start --passphrase-file /run/secrets/keystore_passphrase
    secrets:
      - keystore_passphrase

secrets:
  keystore_passphrase:
    file: ./keystore-passphrase.txt
```

### Option 3: SystemD Service

For Linux systemd services:

```ini
# /etc/systemd/system/remote-network.service
[Unit]
Description=Remote Network P2P Node
After=network.target

[Service]
Type=simple
User=remote-network
WorkingDirectory=/opt/remote-network
ExecStart=/opt/remote-network/remote-network start --passphrase-file /etc/remote-network/keystore-passphrase.txt
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

**Security Note:** Ensure passphrase file has `0400` or `0600` permissions!

---

## Security Best Practices

### ✅ DO

- **Use strong, unique passphrases** (16+ characters)
- **Backup your keystore file** separately from the passphrase
- **Store passphrase in a password manager** (1Password, Bitwarden, etc.)
- **Use different passphrases** for dev/staging/production
- **Set strict file permissions** on passphrase files (chmod 400/600)
- **Use OS keyring** for desktop/interactive deployments
- **Use passphrase file** for servers/automated deployments

### ❌ DON'T

- **Don't commit passphrases** to git repositories
- **Don't share passphrases** via email/Slack/etc.
- **Don't store passphrases** in application logs
- **Don't use weak passphrases** (dictionary words, personal info)
- **Don't store keystore + passphrase** in the same backup
- **Don't use environment variables** for passphrases (visible in process lists)

---

## Troubleshooting

### Automatic Passphrase File Creation

**New in v2.0+**: If the OS keyring save fails, the node will **automatically create a passphrase file** for you!

When you see:
```
⚠️  Warning: Could not save passphrase to OS keyring: <error>

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✓ Passphrase saved to file for automatic node restarts
  Location: ~/.config/remote-network/keystore-passphrase.txt

Security notes:
  • File permissions set to 0600 (owner read/write only)
  • Node will auto-load passphrase on restart (including UI restarts)
  • To use manual passphrase file instead:
    ./remote-network start --passphrase-file /path/to/your/passphrase.txt
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```

**What this means:**
- Your passphrase was saved to a secure file (0600 permissions - owner only)
- **Node restarts will work automatically** - no more passphrase prompts!
- The **UI "Restart peer" button will work seamlessly**
- The passphrase file path is saved to config, so restart commands use it automatically

**File locations:**
- **Linux**: `~/.config/remote-network/keystore-passphrase.txt`
- **macOS**: `~/Library/Application Support/remote-network/keystore-passphrase.txt`
- **Windows**: `%APPDATA%\remote-network\keystore-passphrase.txt`

**Common causes for keyring failure:**
- OS keyring service not running (Linux)
- Permissions issue
- Headless/SSH session without keyring access
- Running inside Docker/container

**Alternative solutions:**
1. **Accept auto-created file** (recommended): Works automatically, secure permissions
2. **Use custom passphrase file**: `./remote-network start --passphrase-file /custom/path.txt`
3. **Fix keyring** (Linux):
   ```bash
   # Install keyring service
   sudo apt install gnome-keyring  # Ubuntu/Debian
   sudo dnf install gnome-keyring  # Fedora
   ```

### "Failed to unlock keystore: invalid passphrase"

You entered the wrong passphrase.

**Solutions:**
1. Try entering it again carefully
2. If using passphrase file, check the file contents
3. **If you forgot it**: You'll need to create a new keystore (new node identity)

### Resetting/Creating New Keystore

**⚠️ Warning**: This creates a **new node identity**. Your peer ID will change.

```bash
# Backup old keystore (optional)
mv ~/Library/Application\ Support/remote-network/keystore.dat ~/keystore.dat.backup

# Start node - it will create a new keystore
./remote-network start
```

### Migrating to New Machine

**Copy both the keystore AND passphrase:**

1. **Copy keystore file** to new machine (same location)
2. **Either**:
   - Enter passphrase on first run (saves to new OS keyring)
   - **Or** copy passphrase file and use `--passphrase-file`

---

## FAQ

### Can I change my passphrase?

Not directly. You need to:
1. Export your keys (future feature)
2. Create new keystore with new passphrase
3. Import keys

### Where can I view my saved passphrase?

- **macOS**: Keychain Access app → Search "remote-network-keystore"
- **Windows**: Credential Manager → Windows Credentials → "remote-network-keystore"
- **Linux**: `secret-tool lookup service remote-network-keystore user remote-network-keystore-passphrase`

### What happens if I lose my passphrase?

**Your keystore becomes permanently inaccessible.** You'll need to:
1. Delete the old keystore
2. Create a new one (new node identity)
3. Re-establish connections with peers

**Backup recommendations:**
- Store passphrase in password manager
- Keep encrypted backup of passphrase separately
- Document your passphrase recovery procedure

### Is the OS keyring really secure?

Yes! OS keyrings are:
- Used by major applications (browsers, email clients, etc.)
- Encrypted with your OS user credentials
- Protected by OS-level permissions
- Battle-tested and audited

**More secure than:**
- Plaintext `.env` files
- Environment variables (visible in `ps` output)
- Config files with 644 permissions

---

## Support

If you encounter issues with keystore setup:

1. Check this guide's [Troubleshooting](#troubleshooting) section
2. View logs: `~/.cache/remote-network/logs/` (Linux) or `~/Library/Logs/remote-network/` (macOS)
3. Report issues: https://github.com/Trustflow-Network-Labs/remote-network-node/issues

---

**Last Updated**: 2025-11-17
