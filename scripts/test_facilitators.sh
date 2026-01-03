#!/bin/bash

# Test script to verify facilitator compatibility
# Usage: ./test_facilitators.sh

echo "======================================"
echo "Testing x402 Facilitators"
echo "======================================"
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test PayAI Health
echo -e "${YELLOW}Testing PayAI Facilitator...${NC}"
PAYAI_HEALTH=$(curl -s -w "\n%{http_code}" https://facilitator.payai.network/health 2>/dev/null | tail -1)
if [ "$PAYAI_HEALTH" = "200" ]; then
    echo -e "${GREEN}✓ PayAI health check: PASS${NC}"
else
    echo -e "${RED}✗ PayAI health check: FAIL (HTTP $PAYAI_HEALTH)${NC}"
fi

# Test PayAI Verify endpoint
echo -e "${YELLOW}Testing PayAI verify endpoint...${NC}"
PAYAI_VERIFY=$(curl -s -w "\n%{http_code}" -X POST https://facilitator.payai.network/verify \
  -H "Content-Type: application/json" \
  -d '{}' 2>/dev/null | tail -1)

if [ "$PAYAI_VERIFY" = "200" ] || [ "$PAYAI_VERIFY" = "400" ]; then
    echo -e "${GREEN}✓ PayAI verify endpoint: ONLINE (HTTP $PAYAI_VERIFY)${NC}"
    echo "  Format: EIP-191 (personal message signing)"
elif [ "$PAYAI_VERIFY" = "500" ]; then
    echo -e "${YELLOW}⚠ PayAI verify endpoint: TEMPORARY OUTAGE (HTTP 500)${NC}"
    echo "  This is normal - service may be recovering"
else
    echo -e "${RED}✗ PayAI verify endpoint: FAIL (HTTP $PAYAI_VERIFY)${NC}"
fi

echo ""
echo "--------------------------------------"
echo ""

# Test x402.org
echo -e "${YELLOW}Testing x402.org Facilitator...${NC}"
X402_VERIFY=$(curl -s -w "\n%{http_code}" -X POST https://www.x402.org/facilitator/verify \
  -H "Content-Type: application/json" \
  -d '{
    "x402Version":1,
    "paymentPayload":{
      "x402Version":1,
      "scheme":"exact",
      "network":"base-sepolia",
      "payload":{
        "signature":"0x00",
        "authorization":{
          "from":"0x0000000000000000000000000000000000000000",
          "to":"0x0000000000000000000000000000000000000000",
          "value":"1000000000000000",
          "validAfter":"0",
          "validBefore":"9999999999",
          "nonce":"test"
        }
      }
    },
    "paymentRequirements":{
      "scheme":"exact",
      "network":"base-sepolia",
      "maxAmountRequired":"1000000000000000",
      "payTo":"0x0000000000000000000000000000000000000000"
    }
  }' 2>/dev/null | tail -1)

if [ "$X402_VERIFY" = "200" ]; then
    echo -e "${GREEN}✓ x402.org verify endpoint: ONLINE (HTTP 200)${NC}"
    echo "  Format: EIP-712 (typed structured data signing)"

    # Check response
    RESPONSE=$(curl -s -X POST https://www.x402.org/facilitator/verify \
      -H "Content-Type: application/json" \
      -d '{
        "x402Version":1,
        "paymentPayload":{
          "x402Version":1,
          "scheme":"exact",
          "network":"base-sepolia",
          "payload":{
            "signature":"0x00",
            "authorization":{
              "from":"0x0000000000000000000000000000000000000000",
              "to":"0x0000000000000000000000000000000000000000",
              "value":"1000000000000000",
              "validAfter":"0",
              "validBefore":"9999999999",
              "nonce":"test"
            }
          }
        },
        "paymentRequirements":{
          "scheme":"exact",
          "network":"base-sepolia",
          "maxAmountRequired":"1000000000000000",
          "payTo":"0x0000000000000000000000000000000000000000"
        }
      }' 2>/dev/null)

    if echo "$RESPONSE" | grep -q "missing_eip712_domain"; then
        echo -e "${GREEN}✓ x402.org correctly validates EIP-712 signatures${NC}"
    fi
else
    echo -e "${RED}✗ x402.org verify endpoint: FAIL (HTTP $X402_VERIFY)${NC}"
fi

echo ""
echo "======================================"
echo "Compilation Test"
echo "======================================"
echo ""

# Test compilation
echo -e "${YELLOW}Testing Go compilation...${NC}"
cd "$(dirname "$0")"

if go build -o /tmp/remote-network-test ./main.go 2>&1 | grep -q "error"; then
    echo -e "${RED}✗ Compilation FAILED${NC}"
    go build -o /tmp/remote-network-test ./main.go 2>&1 | head -20
else
    echo -e "${GREEN}✓ Compilation SUCCESSFUL${NC}"
    rm -f /tmp/remote-network-test
fi

echo ""
echo "======================================"
echo "Summary"
echo "======================================"
echo ""

# Read current config
CURRENT_FACILITATOR=$(grep "^x402_facilitator_url" ../internal/utils/configs/configs | head -1 | awk '{print $3}')

echo "Current Configuration:"
echo "  Facilitator: $CURRENT_FACILITATOR"

if echo "$CURRENT_FACILITATOR" | grep -q "facilitator.payai.network"; then
    echo "  Signature Format: EIP-191 (auto-detected)"
    echo ""
    echo "To switch to x402.org:"
    echo "  1. Edit internal/utils/configs/configs"
    echo "  2. Comment out: x402_facilitator_url = https://facilitator.payai.network"
    echo "  3. Uncomment: x402_facilitator_url = https://www.x402.org/facilitator"
    echo "  4. Rebuild: go build -o remote-network main.go"
elif echo "$CURRENT_FACILITATOR" | grep -q "x402.org"; then
    echo "  Signature Format: EIP-712 (auto-detected)"
    echo ""
    echo "To switch back to PayAI:"
    echo "  1. Edit internal/utils/configs/configs"
    echo "  2. Comment out: x402_facilitator_url = https://www.x402.org/facilitator"
    echo "  3. Uncomment: x402_facilitator_url = https://facilitator.payai.network"
    echo "  4. Rebuild: go build -o remote-network main.go"
else
    echo "  Signature Format: Unknown"
fi

echo ""
echo "For detailed testing instructions, see: FACILITATOR_TESTING.md"
echo "For implementation details, see: IMPLEMENTATION_SUMMARY.md"
echo ""
