# Remote Network Scripts

This directory contains utility scripts for debugging and testing the remote network node.

## Running Scripts

To run these scripts, use the following command from the root of the project:

```bash
cd scripts && go run *.go <command> [args...]
```

### Available Commands

1. **Query DHT Script**
   
   Query and decode peer metadata from the DHT using BEP44.

   ```bash
   cd scripts && go run *.go query-dht <peer_id>
   ```

   ### Parameters

   - `peer_id`: The node ID or peer ID (first 8 characters work)

   ### Example

   ```bash
   cd scripts && go run *.go query-dht 0c225d04
   ```

   This will:
   1. Look up the peer in your known_peers database
   2. Bootstrap a DHT connection
   3. Query DHT store nodes for the peer's metadata
   4. Decode and display the full metadata including:
      - Network information (IPs, ports, node type)
      - Relay information (for NAT peers)
      - Service counts (files, apps)
      - Sequence number
      - Timestamps
   5. Validate the metadata for common issues

   ### Output Example

   ```
   === DHT Metadata Query ===
   Node ID: 38966ec5c613cb399ffc386cbd0771417f0d2e30
   Peer ID: 0c225d04246c8d1a318f7a4a4ee2a822aa469c07
   Public Key: 0c76e3bd69d1fdf7f62bb0e925be635ee3b96e5e42b8e818a6a5b8e59c08ba4a
   Is Store Peer: false

   Bootstrapping DHT (waiting 5 seconds)...
   Querying DHT with 3 priority store nodes...

   ✅ Found metadata: seq=3, size=607 bytes

   === Decoded Metadata (seq=3) ===
   Node ID: 38966ec5c613cb399ffc386cbd0771417f0d2e30
   Peer ID: 0c225d04246c8d1a318f7a4a4ee2a822aa469c07
   ...
   ```

   ### Debugging Use Cases

   - **Verify NAT peer relay info**: Check if NAT peers have correct relay_endpoint
   - **Inspect metadata versions**: See which sequence number is stored in DHT
   - **Diagnose connection issues**: Validate peer metadata before attempting connections
   - **Monitor metadata updates**: Track how metadata changes over time

2. **Decrypt Service Script**

   This script allows you to decrypt and decompress encrypted data services for testing and verification purposes.

   ```bash
   cd scripts && go run *.go decrypt-service <service_id> <output_dir>
   ```

   ### Parameters

   - `service_id`: The ID of the service in the database (e.g., 1, 2, 3)
   - `output_dir`: Directory where decrypted and extracted files will be saved

   ### Example

   ```bash
   cd scripts && go run *.go decrypt-service 1 /tmp/test_decrypt
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
