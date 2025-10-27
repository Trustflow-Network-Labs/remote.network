# Decrypt Service Script

This script allows you to decrypt and decompress encrypted data services for testing and verification purposes.

## Usage

```bash
go run scripts/decrypt_service.go <service_id> <output_dir>
```

### Parameters

- `service_id`: The ID of the service in the database (e.g., 1, 2, 3)
- `output_dir`: Directory where decrypted and extracted files will be saved

### Example

```bash
go run scripts/decrypt_service.go 1 /tmp/test_decrypt
```

This will:
1. Read service details from the database
2. Retrieve the encryption passphrase
3. Decrypt `data/services/service_1_encrypted.dat`
4. Decompress the tar.gz archive
5. Extract all files to `/tmp/test_decrypt/extracted/`

## Output

The script provides detailed information about:
- Service details (file count, sizes, hash)
- Encryption information (passphrase, hash)
- Decryption process (file sizes)
- List of all extracted files with their sizes

## Directory Structure

After running, you'll find:
- `/tmp/test_decrypt/decrypted.tar.gz` - The decrypted tar.gz archive
- `/tmp/test_decrypt/extracted/` - All extracted files with preserved directory structure

## Requirements

- Go 1.24.6 or higher
- Access to the Remote Network database at `~/Library/Application Support/remote-network/remote-network.db`
- The encrypted service file must exist in `data/services/`

## Example Output

```
=== Service Details ===
Service ID: 1
File(s): 11 files
Encrypted Size: 17839 bytes (17.42 KB)
Original Size: 62178 bytes (60.72 KB)
Compression: tar.gz
Hash: 3b6be1369f8ac26c168fb24ddeadff45a37cab06508b76fc524afaa57bc7ae4d

=== Decryption Info ===
Passphrase: 76860828d0969f8b51f5cc5a91e54b560b5b097b0eb7b5bd01a3c61b0ee5372a
Passphrase Hash: 65e4dc919ce23232832064ea1d2d052707fbb95da28388dde991f7d28b66dddf

=== Decryption Process ===
Reading encrypted file: data/services/service_1_encrypted.dat
Decrypted to: /tmp/test_decrypt/decrypted.tar.gz
Decrypted size: 17811 bytes (17.39 KB)

=== Decompression Process ===
Extracted to: /tmp/test_decrypt/extracted

=== Extracted Files ===
  ARCHITECTURE.md (21138 bytes)
  Data Service Implementation Plan.html (14771 bytes)
  diagrams/README.md (4292 bytes)
  diagrams/hp-1-successful-flow.puml (2069 bytes)
  diagrams/hp-2-lan-detection.puml (1640 bytes)
  diagrams/hp-3-direct-dial.puml (1714 bytes)
  diagrams/hp-4-failure-fallback.puml (2084 bytes)
  diagrams/hp-5-architecture.puml (1519 bytes)
  diagrams/hp-6-decision-tree.puml (1504 bytes)
  diagrams/hp-7-rtt-timing.puml (2024 bytes)
  hole-punching-protocol.md (9423 bytes)

✓ Decryption and decompression completed successfully!
```

## Technical Details

### Encryption
- Algorithm: AES-256-GCM
- Key derivation: PBKDF2 with SHA-256 (100,000 iterations)
- Nonce: 12 bytes (prepended to ciphertext)

### Compression
- Format: tar.gz
- Preserves directory structure
- Typical compression ratio: 28-30%

### Security Notes
- The passphrase is stored in the database for demonstration purposes
- In production, passphrases should be securely managed (e.g., key management service)
- The script displays the passphrase for verification - handle with care
