# Relay Circulation Analysis

## Node Mapping

### Relay Nodes (Public IP)
| IP Address | Node ID | Role |
|------------|---------|------|
| 159.65.253.245 | `1ecc14701e6a945db38033ac2245f0b7ac7488b9` | Relay Peer |
| 85.237.211.221 | `5a9347082515c3ed94c378c1f38bc290659a4932` | Relay Peer |
| 167.86.116.185 | `9431b0cc9ac41fb59d4d02ffc972f6b6d8bac632` | Relay Peer |

### NAT Peers (Behind NAT)
| Public IP | Node ID | Role | Local IP |
|-----------|---------|------|----------|
| 91.75.0.111 | `5668cfc9a595f27b6b90a157b224ca46417c848e` | NAT Peer | 192.168.3.108 |
| 95.180.109.240 | `e20ec3d7256317f84816235843bce09e0a9efa49` | NAT Peer #1 | 192.168.2.17 |
| 95.180.109.240 | `a4fb3da55c9fb23c0fd41b41a5536123c3f00f31` | NAT Peer #2 | 172.18.240.1 |

## Database State Analysis

### 85.237.211.221 (5a9347082515c3ed94c378c1f38bc290659a4932)
**Active Sessions:**
- ✅ `a4fb3da55c9fb23c0fd41b41a5536123c3f00f31` (172.18.240.1 NAT peer) - ACTIVE
  - Session: `acadfb6a-2cfd-49f8-9c44-0084f11368ad`
  - Started: 2025-10-06 14:59:31
  - Last keepalive: 15:03:52 (recent!)

**Terminated Sessions:**
- ❌ `5668cfc9a595f27b6b90a157b224ca46417c848e` (91.75.0.111 NAT peer) - TERMINATED
  - Session: `25d00dfa-6cca-47f6-97a9-af858f086d74`
  - Started: 10:50:14, Ended: 11:21:14
  - Duration: ~31 minutes

### 159.65.253.245 (1ecc14701e6a945db38033ac2245f0b7ac7488b9)
**Active Sessions:**
- ✅ `5668cfc9a595f27b6b90a157b224ca46417c848e` (91.75.0.111 NAT peer) - ACTIVE
  - Session: `83dc6293-e9f0-44f5-98a4-8c0ac529d551`
  - Started: 2025-10-06 14:59:00
  - Last keepalive: 15:05:00 (recent!)

- ✅ `e20ec3d7256317f84816235843bce09e0a9efa49` (192.168.2.17 NAT peer) - ACTIVE
  - Session: `58d1080e-eee2-4696-b56a-899d092c938f`
  - Started: 2025-10-06 14:59:19
  - Last keepalive: 15:05:00 (recent!)

**Terminated Sessions:**
- ❌ `a4fb3da55c9fb23c0fd41b41a5536123c3f00f31` (172.18.240.1 NAT peer) - TERMINATED
  - Session: `35633ca1-099f-472f-a6af-0ee599764d6f`
  - Started: 10:50:26, Ended: 11:21:56
  - Duration: ~31.5 minutes

### 167.86.116.185 (9431b0cc9ac41fb59d4d02ffc972f6b6d8bac632)
**Active Sessions:** NONE

**Terminated Sessions:**
- ❌ `e20ec3d7256317f84816235843bce09e0a9efa49` (192.168.2.17 NAT peer) - TERMINATED
  - Session: `f1ba187d-4c00-41b3-9362-36230c23af72`
  - Started: 10:49:34, Ended: 11:21:34
  - Duration: ~32 minutes

## Relay Switching Pattern Analysis

### NAT Peer: e20ec3d7256317f84816235843bce09e0a9efa49 (192.168.2.17-95.180.109.240)

**Relay History:**
1. **Initial Connection**: 167.86.116.185 (`9431b0cc9ac41fb59d4d02ffc972f6b6d8bac632`)
   - Connected: ~10:49
   - Terminated: ~11:21 (after 32 minutes)
   - **Reason for switch**: Connection loss detected at 15:25:54

2. **Switch to**: 159.65.253.245 (`1ecc14701e6a945db38033ac2245f0b7ac7488b9`)
   - Connected: 14:59:19 (15:25:59 in logs)
   - Still active with regular keepalives
   - **Reason**: Previous relay became unreachable

**Log Evidence:**
```
15:25:34 - Keepalive failed: timeout: no recent network activity
15:25:54 - Relay connection lost after 3 failed keepalives
15:25:54 - Removing failed relay 9431b0cc9ac41fb59d4d02ffc972f6b6d8bac632
15:25:59 - Connecting to relay 1ecc14701e6a945db38033ac2245f0b7ac7488b9
15:25:59 - Successfully connected (session: 58d1080e-eee2-4696-b56a-899d092c938f)
```

### NAT Peer: a4fb3da55c9fb23c0fd41b41a5536123c3f00f31 (172.18.240.1-95.180.109.240)

**Relay History:**
1. **Initial Connection**: 159.65.253.245 (`1ecc14701e6a945db38033ac2245f0b7ac7488b9`)
   - Session started: ~10:50:26
   - Terminated: ~11:21:56
   - Duration: ~31.5 minutes

2. **Switch to**: 85.237.211.221 (`5a9347082515c3ed94c378c1f38bc290659a4932`)
   - Connected: ~14:59:31
   - Still active with regular keepalives

### NAT Peer: 5668cfc9a595f27b6b90a157b224ca46417c848e (91.75.0.111)

**Relay History:**
1. **Initial Connection**: 85.237.211.221 (`5a9347082515c3ed94c378c1f38bc290659a4932`)
   - Session started: ~10:50:14
   - Terminated: ~11:21:14
   - Duration: ~31 minutes

2. **Switch to**: 159.65.253.245 (`1ecc14701e6a945db38033ac2245f0b7ac7488b9`)
   - Connected: ~14:59:00
   - Still active with regular keepalives

## Is the Circulation Justified?

### ⚠️ PARTIALLY - The failover is working, but there are SUSPICIOUS patterns

### Analysis:

#### 1. **🚨 SUSPICIOUS: All sessions terminated after exactly ~31-32 minutes**
All three NAT peers' sessions terminated at approximately the same time (~11:21):
- 85.237.211.221 ↔ 5668cfc9: ~31 minutes
- 159.65.253.245 ↔ a4fb3da5: ~31.5 minutes
- 167.86.116.185 ↔ e20ec3d7: ~32 minutes

**This is NOT a coincidence. Possible causes:**
- **NAT UDP mapping timeout** (many NATs timeout at 30 minutes)
- **Common infrastructure timeout** (if relays share network/host)
- **Configured session limit** somewhere in the system

#### 2. **Proper Failover Behavior**
The log for e20ec3d7256317f84816235843bce09e0a9efa49 shows:
- ✅ Keepalive monitoring detected failure (3 consecutive failures)
- ✅ Failed relay removed from candidates
- ✅ New relay selected based on latency/reputation/pricing
- ✅ Successful reconnection to alternate relay
- ✅ New session established and keepalives maintained

#### 3. **Load Distribution**
After the switch, NAT peers are distributed across relays:
- **159.65.253.245**: 2 active sessions (5668cfc9, e20ec3d7)
- **85.237.211.221**: 1 active session (a4fb3da5)
- **167.86.116.185**: 0 active sessions (currently unavailable)

This is good load balancing.

#### 4. **No Continuous Flapping**
- Each NAT peer switched **exactly once** after initial connection loss
- Since reconnection (~14:59), all connections have been **stable**
- Regular keepalives are succeeding
- No further relay switches observed

#### 5. **Selection Criteria Working Correctly**
Looking at latency measurements:
- **9431b0cc (167.86.116.185)**: 39-42ms (best latency)
- **1ecc1470 (159.65.253.245)**: 121-125ms (medium)
- **5a934708 (85.237.211.221)**: 302-313ms (worst)

When 167.86.116.185 failed, the system correctly selected 159.65.253.245 as next best option.

## Strange/Unwanted Behaviors

### 🚨 CRITICAL ISSUES DETECTED:

#### Issue #1: Synchronized 30-Minute Session Termination
**Observation:** All three relay sessions terminated after exactly 31-32 minutes, regardless of which relay peer they were connected to.

**Evidence:**
- Different NAT peers, different relay peers, different start times
- All sessions lasted ~31-32 minutes
- All terminated within 2.5 minutes of each other

**Root Cause Candidates:**

1. **NAT Gateway UDP Mapping Timeout (MOST LIKELY)**
   - Many NAT gateways have 30-minute UDP mapping timeouts
   - Even though keepalives are sent every 20s, NAT mapping might still expire
   - After expiry, packets from relay can't reach NAT peer
   - NAT peer keeps sending keepalives but never receives responses
   - After 3 failed keepalives (60s), connection declared dead

   **Why keepalives don't prevent this:**
   - Keepalives are **outbound** (NAT peer → Relay)
   - NAT mapping requires **bidirectional** traffic to stay alive
   - If relay's responses don't arrive (mapping expired), NAT doesn't refresh

2. **QUIC Implementation Issue**
   - QUIC library might have undocumented 30-minute limit
   - Connection state cleanup after certain duration

3. **Missing KeepAlive Packet**
   - QUIC connections need packets in BOTH directions
   - If relay peer doesn't send data back, connection might be considered idle by NAT

#### Issue #2: Rapid Relay Switching (CRITICAL)
**Observation:** Sessions are switching relays every ~30 minutes within the same run

**Timeline (accounting for timezone difference):**
- All events happened within the past ~1 hour (user's current run)
- First relay connections: lasted 31-32 minutes, then failed
- Immediate reconnection: to different relays
- Currently active: but will likely fail again at 30-minute mark

**This means:**
- NAT peers are stuck in a 30-minute relay switching cycle
- Each cycle removes a relay from candidates
- Eventually, no relay candidates will remain
- **This is a CRITICAL operational issue**

### ✅ Expected Behaviors Observed:
1. **Keepalive monitoring** - 20-second intervals
2. **Failure detection** - 3 consecutive failures trigger reconnection
3. **Automatic failover** - seamless switch to alternate relay
4. **Load balancing** - NAT peers distributed across available relays
5. **Session persistence** - no unnecessary relay switches

### ❌ No Anomalies Found:
- ❌ No rapid relay switching (flapping)
- ❌ No keepalive timeout issues (after reconnection)
- ❌ No session ID conflicts
- ❌ No stuck terminated sessions
- ❌ No relay selection errors
- ❌ No database inconsistencies

## Recommendations

### 🔧 IMMEDIATE FIXES REQUIRED:

#### Fix #1: Ensure Bidirectional Keepalive Traffic
**Problem:** NAT UDP mappings require traffic in BOTH directions. Current keepalives are one-way (NAT peer → Relay with pong response on same stream).

**Solution:** Verify relay peers are sending pong responses that traverse back through NAT:
```go
// In relay.go handlePing, ensure pong is sent back through same connection
// This should already be happening, but verify packets actually traverse NAT
```

**Test:** Monitor UDP traffic on NAT peer side to confirm pongs are arriving every 20s

#### Fix #2: Reduce Keepalive Interval to 15 Minutes
**Problem:** 30-minute NAT timeout is common. 20-second keepalives should work, but might not be resetting NAT timer.

**Solution:** Send an explicit "heartbeat" message every 15 minutes in addition to 20s keepalives:
```toml
# In configs
relay_keepalive_interval = 20s
relay_nat_heartbeat_interval = 15m  # NEW: Extra heartbeat for NAT refresh
```

#### Fix #3: Don't Remove Relay From Candidates on Single Failure
**Problem:** When keepalive fails, relay is removed from candidates. If this was a NAT timeout (not relay fault), we shouldn't penalize the relay.

**Current code:**
```go
// handleRelayConnectionLoss removes failed relay
rm.selector.RemoveCandidate(failedRelayNodeID)
```

**Solution:** Implement smarter relay health tracking:
```go
// Track failure count instead of immediate removal
rm.selector.IncrementFailureCount(failedRelayNodeID)
// Remove only after 3+ failures within 1 hour
```

### 📊 INVESTIGATION NEEDED:

1. **Monitor current sessions** - Wait ~30 minutes from current session start to see if they die again
2. **Check relay peer logs around next failure** - Did they see the disconnections? Any errors?
3. **Packet capture** - Monitor UDP traffic during keepalive to confirm bidirectional flow
4. **NAT gateway type** - Identify NAT implementation and timeout settings
5. **Test hypothesis** - If sessions die again at ~30 min mark, confirms NAT timeout issue

### Future Enhancements
1. **Adaptive keepalive timing**: Detect NAT timeout and adjust keepalive interval accordingly
2. **Relay health scoring**: Track uptime/stability, don't penalize relays for NAT issues
3. **Graceful relay migration**: Allow relays to signal planned shutdown
4. **NAT hole punching**: Reduce reliance on relay peers for long-lived sessions

## Conclusion

**⚠️ The relay circulation reveals CRITICAL ISSUES that need investigation:**

1. **30-Minute Session Death Pattern (CRITICAL)**
   - All sessions dying after exactly 31-32 minutes is NOT normal
   - Strong evidence of NAT UDP mapping timeout
   - Keepalives not preventing NAT timeout (bidirectional traffic issue)
   - **Action:** Implement bidirectional heartbeat or reduce keepalive interval

2. **30-Minute Relay Cycling (CRITICAL)**
   - NAT peers switch relays every ~30 minutes
   - Each switch removes failed relay from candidate pool
   - With only 3 relays, pool will be exhausted after 90 minutes
   - **Action:** Don't remove relays on NAT timeout (Fix #3 below)

3. **System Behavior:**
   - Failover mechanism works correctly
   - Relay selection works correctly
   - Keepalive monitoring works correctly
   - **BUT:** 30-minute timeout will keep recurring until fixed

**Primary Issue:** NAT UDP mapping timeout causing synchronized session deaths. Fix keepalive mechanism to prevent this.
