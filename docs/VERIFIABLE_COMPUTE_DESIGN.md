# Verifiable/Trustless Compute for Non-Deterministic Jobs

This document outlines approaches for making DOCKER and STANDALONE compute jobs verifiable and trustless, despite their non-deterministic nature.

## The Core Challenge

Non-deterministic computations (floating-point operations, random number generation, timestamps, thread scheduling, hardware differences) produce different outputs even with identical inputs. This makes traditional "re-execute and compare" verification impossible.

## Current State

The codebase already has solid **integrity verification**:
- BLAKE3 hashes for file integrity
- AES-256-GCM encryption for data in transit
- Manifest-based attestation in transfer packages

However, this only proves data wasn't tampered with **in transit**—it doesn't prove the computation itself was correct.

---

## Key Insight: Interface Definitions as Contracts

The `JobInterface` definitions with `InterfacePeer` relationships form a **declarative specification** of:
1. **What inputs** the job receives (STDIN, MOUNT with INPUT function)
2. **What outputs** the job produces (STDOUT, STDERR, MOUNT with OUTPUT function)
3. **Who sends/receives** each piece of data (PeerID + path mappings)

This is essentially a **contract** that can be verified!

---

## Verification Approaches

### 1. Hardware-Based: Trusted Execution Environments (TEEs)

Hardware-based attestation using Intel SGX, AMD SEV, or ARM TrustZone:

```
┌─────────────────────────────────────┐
│         TEE Enclave                 │
│  ┌─────────────────────────────┐   │
│  │  Docker/Standalone Job      │   │
│  │  + Hardware Attestation     │   │
│  └─────────────────────────────┘   │
│  Output + Signed Attestation Report │
└─────────────────────────────────────┘
```

**Pros:** True trustlessness via hardware guarantees, works with non-deterministic code
**Cons:** Requires specific hardware (Intel SGX, AMD SEV-SNP), limited enclave memory
**Implementation:** Integrate with [Gramine](https://gramine.readthedocs.io/) or [EGo](https://www.edgeless.systems/products/ego/) for SGX-based Docker execution

### 2. Economic: Optimistic Verification with Fraud Proofs

Assume results are correct; allow disputes with economic stakes:

```
┌──────────────┐     ┌──────────────┐
│  Executor 1  │     │  Executor 2  │
│  (Primary)   │     │  (Challenger)│
└──────┬───────┘     └──────┬───────┘
       │                    │
       ▼                    ▼
   Result A            Result B (if disputed)
       │                    │
       └────────┬───────────┘
                ▼
        ┌───────────────┐
        │ Dispute Game  │
        │ (bisection)   │
        └───────────────┘
```

### 3. Redundancy: Replicated Execution with Threshold Agreement

Run the job on N nodes, accept result if M agree:

```go
type ReplicatedJobConfig struct {
    Replicas       int     // N executors
    Threshold      float64 // e.g., 0.67 for 2/3 agreement
    OutputCompare  string  // "EXACT", "SEMANTIC", "HASH"
}
```

For non-deterministic outputs, use semantic comparison rather than exact matching.

### 4. Cryptographic: zkVM/zkProof (Emerging)

Zero-knowledge proofs for computation verification using RISC Zero, SP1, or Jolt.

**Current limitations:** ~10-100x slower than native execution
**Best for:** High-value computations where trustlessness is critical

---

## Contract-Based Verification (Recommended Approach)

### Extended Type Definitions

```go
type JobInterface struct {
    Type           string            `json:"type"`
    Path           string            `json:"path"`
    InterfacePeers []*InterfacePeer  `json:"interface_peers"`

    // NEW: Output contract specification
    OutputContract *OutputContract   `json:"output_contract,omitempty"`
}

type OutputContract struct {
    // Structural validation
    FileType        string            `json:"file_type,omitempty"`        // "json", "csv", "binary", "text"
    Schema          *JSONSchema       `json:"schema,omitempty"`           // JSON schema for structured outputs
    MimeType        string            `json:"mime_type,omitempty"`        // Expected MIME type

    // Size bounds
    MinSizeBytes    *int64            `json:"min_size_bytes,omitempty"`
    MaxSizeBytes    *int64            `json:"max_size_bytes,omitempty"`

    // Content validation
    MustContain     []string          `json:"must_contain,omitempty"`     // Required strings/patterns
    MustNotContain  []string          `json:"must_not_contain,omitempty"` // Forbidden patterns

    // Deterministic subset verification
    DeterministicFields []string      `json:"deterministic_fields,omitempty"` // Fields that MUST match on re-execution

    // Relationship to inputs (causal verification)
    DerivedFrom     []InputDerivation `json:"derived_from,omitempty"`
}

type InputDerivation struct {
    InputInterface  string `json:"input_interface"`   // Which input this output derives from
    Relationship    string `json:"relationship"`      // "transform", "filter", "aggregate", "subset"
    Verifiable      bool   `json:"verifiable"`        // Can we verify the relationship?
}
```

### Causal/Relational Verification

Since interfaces define which inputs map to which outputs, we can verify **relationships**:

```
┌─────────────────────────────────────────────────────────────┐
│                    Job Interface Contract                    │
├─────────────────────────────────────────────────────────────┤
│ INPUTS:                                                      │
│   MOUNT /input/data.csv (from PeerA, job_123)               │
│   STDIN (from PeerB, job_456)                               │
├─────────────────────────────────────────────────────────────┤
│ OUTPUTS:                                                     │
│   STDOUT → must be valid JSON                               │
│   MOUNT /output/result.csv:                                 │
│     - derived_from: /input/data.csv                         │
│     - relationship: "filter" (rows subset of input)         │
│     - row_count: <= input row count                         │
│     - columns: same as input                                │
└─────────────────────────────────────────────────────────────┘
```

### Verification Logic Example

```go
func VerifyOutputContract(input []byte, output []byte, contract *OutputContract) error {
    // 1. Structural validation
    if contract.FileType == "json" {
        if !json.Valid(output) {
            return errors.New("output is not valid JSON")
        }
    }

    // 2. Schema validation
    if contract.Schema != nil {
        if err := validateJSONSchema(output, contract.Schema); err != nil {
            return fmt.Errorf("schema validation failed: %w", err)
        }
    }

    // 3. Relational verification
    for _, derivation := range contract.DerivedFrom {
        if derivation.Relationship == "subset" {
            if !isSubset(input, output) {
                return errors.New("output is not a subset of input")
            }
        }
        if derivation.Relationship == "transform" {
            if countRecords(input) != countRecords(output) {
                return errors.New("output cardinality mismatch")
            }
        }
    }

    return nil
}
```

### Input-Output Hash Commitment

```go
type ExecutionCommitment struct {
    JobExecutionID    int64             `json:"job_execution_id"`

    // Input commitments (known before execution)
    InputHashes       map[string]string `json:"input_hashes"`  // interface_path -> BLAKE3 hash
    InputSizes        map[string]int64  `json:"input_sizes"`

    // Expected output structure (from interface definition)
    ExpectedOutputs   []ExpectedOutput  `json:"expected_outputs"`

    // Filled after execution
    ActualOutputs     map[string]string `json:"actual_outputs,omitempty"` // path -> hash
}

type ExpectedOutput struct {
    InterfaceType     string          `json:"interface_type"`
    Path              string          `json:"path"`
    Contract          *OutputContract `json:"contract"`
    DestinationPeers  []string        `json:"destination_peers"`
}
```

### Workflow-Level Verification (DAG Consistency)

Since interfaces define a DAG of data flow, verify **consistency across the entire workflow**:

```
Job A (DATA) ──STDOUT──▶ Job B (DOCKER) ──MOUNT/output──▶ Job C (STANDALONE)
     │                        │                               │
     ▼                        ▼                               ▼
  hash_A                   hash_B                          hash_C
     │                        │                               │
     └────────────────────────┴───────────────────────────────┘
                              │
                    Workflow Attestation:
                    {
                      "workflow_id": 123,
                      "chain": [
                        {"job": "A", "input_hash": null, "output_hash": "hash_A"},
                        {"job": "B", "input_hash": "hash_A", "output_hash": "hash_B"},
                        {"job": "C", "input_hash": "hash_B", "output_hash": "hash_C"}
                      ]
                    }
```

**Key verification:** Each job's `input_hash` must match the previous job's `output_hash` per the interface routing.

---

## Complete Type Definitions for Implementation

```go
// verification_types.go

type VerificationMode string

const (
    VerificationNone       VerificationMode = "NONE"
    VerificationOptimistic VerificationMode = "OPTIMISTIC"
    VerificationTEE        VerificationMode = "TEE"
    VerificationReplicated VerificationMode = "REPLICATED"
)

type JobExecutionAttestation struct {
    ExecutionID    string
    ExecutorPeerID string
    InputHash      string   // Hash of all inputs
    OutputHash     string   // Hash of all outputs
    ExecutionLog   string   // Deterministic execution trace
    Signature      []byte   // Executor's signature
    Stake          uint64   // Economic stake for optimistic
}

type VerifiableJobInterface struct {
    JobInterface
    Contract *InterfaceContract `json:"contract,omitempty"`
}

type InterfaceContract struct {
    OutputSpec *OutputSpec `json:"output_spec,omitempty"` // For OUTPUT interfaces
    InputSpec  *InputSpec  `json:"input_spec,omitempty"`  // For INPUT interfaces
}

type OutputSpec struct {
    // Structural
    Format          string   `json:"format"`                   // json, csv, binary, text, image/*
    SchemaRef       string   `json:"schema_ref,omitempty"`     // Reference to JSON schema

    // Bounds
    MinSize         *int64   `json:"min_size,omitempty"`
    MaxSize         *int64   `json:"max_size,omitempty"`

    // For structured data
    RequiredFields  []string `json:"required_fields,omitempty"`

    // Determinism level
    Determinism     string   `json:"determinism"`                    // "full", "partial", "none"
    DeterministicPaths []string `json:"deterministic_paths,omitempty"` // JSON paths that must be deterministic

    // Relationship to inputs
    InputRelations  []InputRelation `json:"input_relations,omitempty"`
}

type InputRelation struct {
    InputPath   string `json:"input_path"`   // Which input interface
    Relation    string `json:"relation"`     // subset, superset, transform, aggregate, independent
    Cardinality string `json:"cardinality"`  // 1:1, 1:N, N:1, N:M
}

type InputSpec struct {
    Format    string `json:"format"`
    SchemaRef string `json:"schema_ref,omitempty"`
    Required  bool   `json:"required"`
}
```

---

## Verification Flow

```
┌──────────────────────────────────────────────────────────────────┐
│                     VERIFICATION FLOW                             │
├──────────────────────────────────────────────────────────────────┤
│                                                                   │
│  1. PRE-EXECUTION (Orchestrator)                                 │
│     ├─ Hash all inputs per interface definitions                 │
│     ├─ Create ExecutionCommitment                                │
│     └─ Record expected output contracts                          │
│                                                                   │
│  2. EXECUTION (Executor Peer)                                    │
│     ├─ Receive inputs with hashes                                │
│     ├─ Verify received input hashes match commitment             │
│     ├─ Execute job                                               │
│     └─ Generate outputs with hashes                              │
│                                                                   │
│  3. POST-EXECUTION VERIFICATION                                  │
│     ├─ Verify output structure matches contract                  │
│     ├─ Verify input-output relationships (if defined)            │
│     ├─ Verify deterministic fields match (if re-executed)        │
│     └─ Chain: next job's input_hash == this job's output_hash    │
│                                                                   │
│  4. DISPUTE (Optional - Optimistic Verification)                 │
│     ├─ Challenger re-executes with same inputs                   │
│     ├─ Compares deterministic portions                           │
│     └─ Validates contract compliance                             │
│                                                                   │
└──────────────────────────────────────────────────────────────────┘
```

---

## Example: Defining a Verifiable Job

```json
{
  "job_name": "process_csv",
  "service_type": "DOCKER",
  "interfaces": [
    {
      "type": "MOUNT",
      "path": "/input",
      "interface_peers": [{"peer_id": "QmA...", "peer_mount_function": "INPUT"}],
      "contract": {
        "input_spec": {
          "format": "csv",
          "required": true
        }
      }
    },
    {
      "type": "MOUNT",
      "path": "/output",
      "interface_peers": [{"peer_id": "QmB...", "peer_mount_function": "OUTPUT"}],
      "contract": {
        "output_spec": {
          "format": "csv",
          "determinism": "partial",
          "deterministic_paths": ["$.columns", "$.row_count"],
          "input_relations": [{
            "input_path": "/input",
            "relation": "transform",
            "cardinality": "1:1"
          }],
          "required_fields": ["id", "result"]
        }
      }
    },
    {
      "type": "STDOUT",
      "path": "",
      "contract": {
        "output_spec": {
          "format": "json",
          "determinism": "partial",
          "deterministic_paths": ["$.status", "$.record_count"],
          "required_fields": ["status", "record_count", "processing_time"]
        }
      }
    }
  ]
}
```

---

## Summary: How Interfaces Enable Verification

| Aspect | How Interfaces Enable Verification |
|--------|-----------------------------------|
| **Structure** | Define expected output format, schema, size bounds |
| **Relationships** | Link inputs to outputs (transform, filter, aggregate) |
| **Partial Determinism** | Specify which fields/paths MUST be deterministic |
| **Chain Integrity** | Verify hash chain across workflow DAG |
| **Dispute Resolution** | Re-execute and compare deterministic portions |

This approach works **with** non-determinism rather than against it—verifying what **can** be verified (structure, relationships, deterministic subsets) while accepting what cannot (timestamps, random IDs, floating-point precision).

---

## Implementation Priority

1. **Immediate:** Add execution attestation signatures (non-repudiation)
2. **Short-term:** Implement output contract validation for interfaces
3. **Medium-term:** Add replicated execution for critical jobs
4. **Long-term:** TEE integration (SGX/SEV) for hardware-backed attestation
5. **Future:** zkVM integration for cryptographic proofs
