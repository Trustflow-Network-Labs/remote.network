#!/bin/bash

# Remote Network Node Monitoring Script
# Usage: ./remote-network-monitoring.sh [app_name] [interval] [max_log_size_mb] [pid]
# Special commands:
#   ./remote-network-monitoring.sh --leak-report     - Generate immediate leak report and exit
#   ./remote-network-monitoring.sh --resource-stats  - Query enhanced resource stats from HTTP endpoints
# Environment Variables:
#   ENABLE_GOROUTINE_ANALYSIS=true/false (default: true) - Enable detailed goroutine analysis
#   ENABLE_PERIODIC_REPORTS=true/false (default: true) - Generate stuck goroutine reports every ~5 minutes

set -euo pipefail  # Exit on error, undefined vars, pipe failures

# Handle special commands first before parameter assignment
if [ "${1:-}" = "--leak-report" ]; then
    SPECIAL_COMMAND="leak-report"
    APP_NAME="remote-network"  # Use default for special commands
elif [ "${1:-}" = "--resource-stats" ]; then
    SPECIAL_COMMAND="resource-stats"
    APP_NAME="remote-network"  # Use default for special commands
else
    SPECIAL_COMMAND=""
    # Default values for normal monitoring
    APP_NAME=${1:-"remote-network"}
fi

# Handle special commands immediately before any file creation
if [ "$SPECIAL_COMMAND" = "leak-report" ]; then
    echo "Generating goroutine leak report for $APP_NAME..."
    # Find the process
    if PID=$(pgrep -x "$APP_NAME" || pgrep -f "/$APP_NAME$" || pgrep -f "$APP_NAME" | grep -v "monitoring" | grep -v "$$" | head -1); then
        echo "Found process PID: $PID"
        # Include required functions inline to avoid dependencies
        show_leak_report() {
            local pid=$1
            echo
            echo "=== GOROUTINE LEAK ANALYSIS ==="
            echo "=== CURRENT GOROUTINE ANALYSIS ==="
            echo "Analyzing current goroutine patterns..."

            # Try to get goroutine data from pprof
            if goroutine_data=$(timeout 5 curl -s "http://localhost:6060/debug/pprof/goroutine?debug=1" 2>/dev/null); then
                # Extract total from the profile header
                local total=$(echo "$goroutine_data" | grep "^goroutine profile:" | sed 's/.*total \([0-9]*\).*/\1/')
                echo "Retrieved goroutine profile with total: $total goroutines"
                echo

                # Show top goroutine patterns by counting occurrences
                echo "Top goroutine functions:"
                echo "$goroutine_data" | grep -E "^\s*[a-zA-Z]" | awk '{print $1}' | sort | uniq -c | sort -nr | head -10 | while read count func; do
                    printf "  %-30s: %3d\n" "$func" "$count"
                done

                echo
                echo "Sample goroutine details:"
                echo "$goroutine_data" | head -20
            else
                echo "Could not retrieve goroutine data from pprof endpoint"
            fi
        }
        show_leak_report "$PID"
        exit 0
    else
        echo "Error: $APP_NAME process not found"
        exit 1
    fi
elif [ "$SPECIAL_COMMAND" = "resource-stats" ]; then
    echo "Querying enhanced resource stats for $APP_NAME..."
    # Find the process
    if PID=$(pgrep -x "$APP_NAME" || pgrep -f "/$APP_NAME$" || pgrep -f "$APP_NAME" | grep -v "monitoring" | grep -v "$$" | head -1); then
        echo "Found process PID: $PID"

        # Try different ports to find HTTP server
        for port in 6060 6061 6062 6063 6064 6065; do
            if curl -s --connect-timeout 1 "http://localhost:$port/health" >/dev/null 2>&1; then
                echo "HTTP server found on port: $port"

                echo "=== RESOURCE STATS ==="
                curl -s "http://localhost:$port/stats/resources" 2>/dev/null | jq . 2>/dev/null || curl -s "http://localhost:$port/stats/resources"

                echo ""
                echo "=== HEALTH STATUS ==="
                curl -s "http://localhost:$port/health" 2>/dev/null | jq . 2>/dev/null || curl -s "http://localhost:$port/health"

                echo ""
                echo "=== LEAK REPORT ==="
                curl -s "http://localhost:$port/stats/leaks" 2>/dev/null | head -50

                exit 0
            fi
        done
        echo "Error: HTTP server not found for process PID $PID"
        exit 1
    else
        echo "Error: $APP_NAME process not found"
        exit 1
    fi
fi

# Continue with normal monitoring setup only if not a special command
INTERVAL=${2:-30}  # seconds
MAX_LOG_SIZE_MB=${3:-100}  # MB, rotate logs when exceeded
ENABLE_GOROUTINE_ANALYSIS=${ENABLE_GOROUTINE_ANALYSIS:-"true"}  # Set to "false" to disable
ENABLE_PERIODIC_REPORTS=${ENABLE_PERIODIC_REPORTS:-"true"}  # Set to "false" to disable periodic stuck goroutine reports
LOG_FILE="remote-network-metrics-$(date +%Y%m%d_%H%M%S).csv"
ERROR_LOG="remote-network-monitoring-errors.log"
SCRIPT_PID=$$
MAX_RETRIES=3
CURL_TIMEOUT=3

# Logging functions
log_info() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] INFO: $1" >> "$ERROR_LOG"
    # Only show startup messages on console
    case "$1" in
        *"Starting enhanced monitoring"*|*"Logging to:"*|*"Error log:"*|*"Interval:"*|*"Max log size:"*|*"Goroutine analysis:"*|*"Periodic reports:"*|*"Script PID:"*|*"Press Ctrl+C"*|*"pprof_port"*|*"Monitoring stopped"*|*"Total runtime"*)
            echo "[$(date '+%Y-%m-%d %H:%M:%S')] INFO: $1"
            ;;
    esac
}

log_error() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] ERROR: $1" >> "$ERROR_LOG"
}

log_warn() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] WARN: $1" >> "$ERROR_LOG"
}

# Check dependencies
check_dependencies() {
    local missing_deps=()
    for cmd in ps curl awk; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing_deps+=("$cmd")
        fi
    done

    # netstat or ss (one is sufficient)
    if ! command -v netstat >/dev/null 2>&1 && ! command -v ss >/dev/null 2>&1; then
        missing_deps+=("netstat or ss")
    fi

    if [ ${#missing_deps[@]} -ne 0 ]; then
        log_error "Missing dependencies: ${missing_deps[*]}"
        log_info "Please install missing dependencies and retry"
        exit 1
    fi
}

# Create CSV header
create_csv_header() {
    echo "timestamp,pid,cpu_percent,mem_percent,rss_mb,vsz_mb,threads,file_descriptors,tcp_connections,goroutines,external_goroutines,app_goroutines,dht_goroutines,quic_goroutines,p2p_goroutines,net_goroutines,runtime_goroutines,heap_allocs,heap_inuse_mb,active_connections,service_cache,relay_circuits,script_memory_mb,goroutine_issues,errors" > "$LOG_FILE"
}

# Rotate log if too large
rotate_log_if_needed() {
    if [ -f "$LOG_FILE" ]; then
        local size_mb=$(( $(stat -c%s "$LOG_FILE" 2>/dev/null || stat -f%z "$LOG_FILE" 2>/dev/null || echo 0) / 1048576 ))
        if [ "$size_mb" -gt "$MAX_LOG_SIZE_MB" ]; then
            local backup_file="${LOG_FILE%.csv}_$(date +%H%M%S).csv"
            mv "$LOG_FILE" "$backup_file"
            log_info "Log rotated: $backup_file (${size_mb}MB)"
            create_csv_header
        fi
    fi
}

log_info "Starting enhanced monitoring for $APP_NAME"
log_info "Logging to: $LOG_FILE"
log_info "Error log: $ERROR_LOG"
log_info "Interval: $INTERVAL seconds"
log_info "Max log size: $MAX_LOG_SIZE_MB MB"
log_info "Goroutine analysis: $ENABLE_GOROUTINE_ANALYSIS"
log_info "Periodic reports: $ENABLE_PERIODIC_REPORTS"
log_info "Script PID: $SCRIPT_PID"
log_info "Press Ctrl+C to stop"

# Check dependencies before starting
check_dependencies

# Create initial CSV header
create_csv_header

# Enhanced trap to handle cleanup
cleanup() {
    log_info "Monitoring stopped. Logs saved to $LOG_FILE"
    log_info "Total runtime: $SECONDS seconds"
    # Clean up temp files
    rm -f /tmp/pprof_*_logged_* 2>/dev/null
    exit 0
}

trap cleanup INT TERM

# Enhanced functions with retry logic and error handling
safe_curl() {
    local url=$1
    local retries=0
    local result=""

    while [ $retries -lt $MAX_RETRIES ]; do
        if result=$(timeout $CURL_TIMEOUT curl -s --connect-timeout $CURL_TIMEOUT "$url" 2>/dev/null); then
            echo "$result"
            return 0
        fi
        retries=$((retries + 1))
        [ $retries -lt $MAX_RETRIES ] && sleep 1
    done
    return 1
}

# Analyze external package goroutines for remote-network specifics
analyze_external_goroutines() {
    local stacks="$1"
    local pid="$2"
    local external_file="/tmp/external_goroutines_$pid"
    local current_time=$(date '+%Y-%m-%d %H:%M:%S')

    # Count goroutines by counting unique "goroutine N [state]:" headers that have external packages in their stack
    local dht_count=$(echo "$stacks" | awk '/^goroutine [0-9]+ \[/ { gr=$0; getline; stack=$0; while(getline && !/^goroutine [0-9]+ \[/) stack=stack"\n"$0; if(stack ~ /dht|kad-dht/) print gr }' | wc -l)
    local quic_count=$(echo "$stacks" | awk '/^goroutine [0-9]+ \[/ { gr=$0; getline; stack=$0; while(getline && !/^goroutine [0-9]+ \[/) stack=stack"\n"$0; if(stack ~ /quic|yamux/) print gr }' | wc -l)
    local p2p_count=$(echo "$stacks" | awk '/^goroutine [0-9]+ \[/ { gr=$0; getline; stack=$0; while(getline && !/^goroutine [0-9]+ \[/) stack=stack"\n"$0; if(stack ~ /p2p|peer/) print gr }' | wc -l)
    local net_count=$(echo "$stacks" | awk '/^goroutine [0-9]+ \[/ { gr=$0; getline; stack=$0; while(getline && !/^goroutine [0-9]+ \[/) stack=stack"\n"$0; if(stack ~ /net\/|http|tls/) print gr }' | wc -l)
    local runtime_count=$(echo "$stacks" | awk '/^goroutine [0-9]+ \[/ { gr=$0; getline; stack=$0; while(getline && !/^goroutine [0-9]+ \[/) stack=stack"\n"$0; if(stack ~ /runtime\.|internal\/poll|sync\./) print gr }' | wc -l)

    # Calculate total external goroutines
    local total_external=$((dht_count + quic_count + p2p_count + net_count + runtime_count))

    # Use the already-parsed goroutine count from the main function
    local total_goroutines=$GOROUTINES

    # Calculate app goroutines (estimated)
    local app_goroutines=$((total_goroutines - total_external))
    [ "$app_goroutines" -lt 0 ] && app_goroutines=0

    # Log detailed breakdown
    local breakdown="dht:$dht_count quic:$quic_count p2p:$p2p_count net:$net_count runtime:$runtime_count"
    log_info "Goroutine breakdown for PID $pid - Total:$total_goroutines External:$total_external App:$app_goroutines | $breakdown"

    # Store for trend analysis
    echo "$current_time|$total_goroutines|$total_external|$app_goroutines|$dht_count|$quic_count|$p2p_count|$net_count|$runtime_count" >> "$external_file"

    # Keep last 20 entries for trend analysis
    tail -20 "$external_file" > "${external_file}.tmp" && mv "${external_file}.tmp" "$external_file"

    # Alert on high external goroutine counts
    if [ "$dht_count" -gt 100 ]; then
        log_warn "High DHT goroutine count: $dht_count (PID: $pid)"
    fi

    if [ "$total_external" -gt 200 ]; then
        log_warn "High total external goroutines: $total_external (PID: $pid)"
    fi

    # Set global variables for CSV output
    EXTERNAL_GOROUTINES="$total_external"
    DHT_GOROUTINES="$dht_count"
    QUIC_GOROUTINES="$quic_count"
    P2P_GOROUTINES="$p2p_count"
    NET_GOROUTINES="$net_count"
    RUNTIME_GOROUTINES="$runtime_count"
    APP_GOROUTINES="$app_goroutines"
}

get_process_stats() {
    local pid=$1
    local errors=""

    # Basic process stats with error handling (cross-platform)
    if command -v ps >/dev/null 2>&1; then
        # Try different ps formats for different platforms
        if PS_STATS=$(ps -p "$pid" -o %cpu,%mem,rss,vsz 2>/dev/null | tail -n +2); then
            # Success with macOS/BSD format
            :
        elif PS_STATS=$(ps -p "$pid" -o pcpu,pmem,rss,vsz 2>/dev/null | tail -n +2); then
            # Linux format
            :
        else
            errors="ps_failed"
            echo "0,0,0,0,0,0,0,0,0,0,0,0,0,0,none,${errors}"
            return 1
        fi
    else
        errors="ps_not_found"
        echo "0,0,0,0,0,0,0,0,0,0,0,0,0,0,none,${errors}"
        return 1
    fi

    CPU=$(echo "$PS_STATS" | awk '{print $1}')
    MEM=$(echo "$PS_STATS" | awk '{print $2}')
    RSS=$(echo "$PS_STATS" | awk '{printf "%.2f", $3/1024}')
    VSZ=$(echo "$PS_STATS" | awk '{printf "%.2f", $4/1024}')

    # Thread count with cross-platform fallback
    if [ -f "/proc/$pid/status" ]; then
        # Linux: Use /proc filesystem
        THREADS=$(grep "^Threads:" "/proc/$pid/status" 2>/dev/null | awk '{print $2}' || echo "0")
    elif command -v ps >/dev/null 2>&1; then
        # macOS: Try ps with different options
        if ps -M -p "$pid" >/dev/null 2>&1; then
            THREADS=$(ps -M -p "$pid" 2>/dev/null | wc -l | awk '{print $1-1}') # Subtract 1 for header
        elif ps -p "$pid" -o nlwp >/dev/null 2>&1; then
            THREADS=$(ps -p "$pid" -o nlwp 2>/dev/null | tail -n +2 || echo "1")
        else
            # Final fallback - estimate using ps aux
            THREADS=1
        fi
    else
        THREADS=1
        errors="${errors}:threads_failed"
    fi

    # Ensure THREADS is a valid number
    [[ "$THREADS" =~ ^[0-9]+$ ]] || THREADS=1

    # File descriptor count with cross-platform fallback
    if [ -d "/proc/$pid/fd" ]; then
        # Linux: Use /proc filesystem
        FD_COUNT=$(find "/proc/$pid/fd" -type l 2>/dev/null | wc -l)
    elif command -v lsof >/dev/null 2>&1; then
        # macOS/BSD: Use lsof
        FD_COUNT=$(lsof -p "$pid" 2>/dev/null | wc -l || echo "0")
        # lsof includes headers, subtract 1
        [ "$FD_COUNT" -gt 0 ] && FD_COUNT=$((FD_COUNT - 1))
    else
        FD_COUNT=0
        errors="${errors}:fd_failed"
    fi

    # Ensure FD_COUNT is a valid number
    [[ "$FD_COUNT" =~ ^[0-9]+$ ]] || FD_COUNT=0

    # TCP connections with fallback
    if command -v ss >/dev/null 2>&1; then
        TCP_CONN=$(ss -tulpn 2>/dev/null | grep -c "$pid" || echo "0")
    else
        TCP_CONN=$(netstat -antp 2>/dev/null | grep -c "$pid" || echo "0")
    fi

    # Enhanced pprof data collection with retry logic
    GOROUTINES=0
    EXTERNAL_GOROUTINES=0
    APP_GOROUTINES=0
    DHT_GOROUTINES=0
    QUIC_GOROUTINES=0
    P2P_GOROUTINES=0
    NET_GOROUTINES=0
    RUNTIME_GOROUTINES=0
    HEAP_ALLOCS=0
    HEAP_INUSE=0
    GOROUTINE_ISSUES="none"
    local pprof_success=false
    local pprof_port_found=""

    # Try to find actual pprof port by checking what the process is listening on (cross-platform)
    local listening_ports=""
    if command -v lsof >/dev/null 2>&1; then
        # macOS/BSD: Use lsof
        listening_ports=$(lsof -nP -iTCP -sTCP:LISTEN -p "$pid" 2>/dev/null | awk 'NR>1 {print $9}' | cut -d: -f2 | tr '\n' ' ' || echo "")
    elif command -v ss >/dev/null 2>&1; then
        # Linux: Use ss
        listening_ports=$(ss -tulpn 2>/dev/null | grep "$pid" | grep LISTEN | awk '{print $5}' | cut -d: -f2 | tr '\n' ' ' || echo "")
    elif command -v netstat >/dev/null 2>&1; then
        # Fallback: Use netstat
        listening_ports=$(netstat -tulpn 2>/dev/null | grep "$pid" | grep LISTEN | awk '{print $4}' | cut -d: -f2 | tr '\n' ' ' || echo "")
    fi

    # Clean up listening ports (remove duplicates, ensure single line)
    listening_ports=$(echo "$listening_ports" | tr '\n' ' ' | tr -s ' ' | sed 's/^ *//;s/ *$//')

    # Add common pprof ports to the list to check (including fallback ports)
    local ports_to_check="6060 6061 6062"
    if [ -n "$listening_ports" ]; then
        ports_to_check="$listening_ports $ports_to_check"
    fi

    # Remove duplicates from port list
    ports_to_check=$(echo "$ports_to_check" | tr ' ' '\n' | sort -nu | tr '\n' ' ' | sed 's/ $//')

    for port in $ports_to_check; do
        # Skip if not a valid port number
        [[ "$port" =~ ^[0-9]+$ ]] || continue

        # Test both IPv4 and IPv6 endpoints
        hosts_to_try="localhost [::1]"
        pprof_host_found=""
        test_successful=false

        for host in $hosts_to_try; do
            # First test if pprof is available at all
            if safe_curl "http://$host:$port/debug/pprof/" >/dev/null; then
                pprof_host_found="$host"
                test_successful=true
                break
            fi
        done

        if [ "$test_successful" = false ]; then
            continue
        fi

        # Try goroutine endpoint with the working host
        if goroutine_data=$(safe_curl "http://$pprof_host_found:$port/debug/pprof/goroutine?debug=1"); then
            # Extract just the number after "total" using awk
            GOROUTINES=$(echo "$goroutine_data" | head -1 | awk '{print $4}')
            # Validate it's a proper integer
            if [[ "$GOROUTINES" =~ ^[0-9]+$ ]] && [ "$GOROUTINES" -gt 0 ]; then
                pprof_success=true
                pprof_port_found="$port"

                # Try heap endpoint with the working host
                if heap_data=$(safe_curl "http://$pprof_host_found:$port/debug/pprof/heap?debug=1"); then
                    HEAP_ALLOCS=$(echo "$heap_data" | grep "# heap profile:" | grep -o '[0-9]\+ objects' | grep -o '[0-9]\+' | head -1 || echo "0")
                    HEAP_INUSE=$(echo "$heap_data" | grep -E "# HeapInuse = [0-9]+" | grep -o '[0-9]\+' | head -1 || echo "0")

                    # Fallback heap parsing
                    if [ "$HEAP_INUSE" = "0" ] || ! [[ "$HEAP_INUSE" =~ ^[0-9]+$ ]]; then
                        HEAP_INUSE=$(echo "$heap_data" | grep "# heap profile:" | grep -o '[0-9]\+ \[.*MB\]' | grep -o '[0-9]\+' | tail -1 || echo "0")
                    fi

                    # Ensure we have valid integers
                    [[ "$HEAP_ALLOCS" =~ ^[0-9]+$ ]] || HEAP_ALLOCS=0
                    [[ "$HEAP_INUSE" =~ ^[0-9]+$ ]] || HEAP_INUSE=0
                fi

                # Always get goroutine breakdown for CSV output (lightweight)
                if goroutine_stacks=$(safe_curl "http://$pprof_host_found:$port/debug/pprof/goroutine?debug=2"); then
                    analyze_external_goroutines "$goroutine_stacks" "$1"
                fi

                break
            fi
        fi
    done

    # Fallback if pprof unavailable
    if [ "$pprof_success" = false ]; then
        GOROUTINES=$THREADS
        errors="${errors}:pprof_failed"

        # Log pprof diagnostic info on first failure and periodically (avoid spam)
        diag_file="/tmp/pprof_diag_logged_$1"
        if [ ! -f "$diag_file" ] || [ $(($(date +%s) - $(stat -c %Y "$diag_file" 2>/dev/null || echo 0))) -gt 300 ]; then
            log_warn "pprof unavailable for PID $1. Checked ports: $ports_to_check. Process listening on: ${listening_ports:-none}"
            touch "$diag_file"
        fi
    else
        # Log successful pprof discovery
        if [ ! -f "/tmp/pprof_success_logged_$1" ]; then
            log_info "pprof found on port $pprof_port_found for PID $1"
            touch "/tmp/pprof_success_logged_$1"
        fi
    fi

    # Convert heap to MB
    HEAP_INUSE_MB=0
    if [[ "$HEAP_INUSE" =~ ^[0-9]+$ ]] && [ "$HEAP_INUSE" -gt 0 ]; then
        # Use awk for floating point arithmetic instead of bc
        HEAP_INUSE_MB=$(awk -v heap="$HEAP_INUSE" 'BEGIN { printf "%.2f", heap / 1048576 }')
    fi

    # Initialize detailed goroutine breakdown variables
    DHT_GOROUTINES=${DHT_GOROUTINES:-0}
    QUIC_GOROUTINES=${QUIC_GOROUTINES:-0}
    P2P_GOROUTINES=${P2P_GOROUTINES:-0}
    NET_GOROUTINES=${NET_GOROUTINES:-0}
    RUNTIME_GOROUTINES=${RUNTIME_GOROUTINES:-0}

    # Placeholder for future enhancements
    ACTIVE_CONNECTIONS=0
    SERVICE_CACHE=0
    RELAY_CIRCUITS=0

    # Script memory monitoring
    SCRIPT_MEMORY_MB=0
    if SCRIPT_RSS=$(ps -p $SCRIPT_PID -o rss --no-headers 2>/dev/null); then
        SCRIPT_MEMORY_MB=$(echo "$SCRIPT_RSS" | awk '{printf "%.2f", $1/1024}')
    fi

    # Clean up error string
    errors=${errors#:}

    echo "$CPU,$MEM,$RSS,$VSZ,$THREADS,$FD_COUNT,$TCP_CONN,$GOROUTINES,$EXTERNAL_GOROUTINES,$APP_GOROUTINES,$DHT_GOROUTINES,$QUIC_GOROUTINES,$P2P_GOROUTINES,$NET_GOROUTINES,$RUNTIME_GOROUTINES,$HEAP_ALLOCS,$HEAP_INUSE_MB,$ACTIVE_CONNECTIONS,$SERVICE_CACHE,$RELAY_CIRCUITS,$SCRIPT_MEMORY_MB,$GOROUTINE_ISSUES,${errors:-none}"
}

# Resource monitoring and alerting
check_resource_alerts() {
    local pid=$1
    local cpu=$2
    local mem=$3
    local rss=$4
    local goroutines=$5

    # CPU alert (>80%) - using awk for floating point comparison
    if awk -v cpu="$cpu" 'BEGIN { exit !(cpu > 80) }'; then
        log_warn "High CPU usage: ${cpu}% (PID: $pid)"
    fi

    # Memory alert (>90%) - using awk for floating point comparison
    if awk -v mem="$mem" 'BEGIN { exit !(mem > 90) }'; then
        log_warn "High memory usage: ${mem}% (${rss}MB RSS) (PID: $pid)"
    fi

    # Goroutine alert (>500 for remote-network)
    if [[ "$goroutines" =~ ^[0-9]+$ ]] && [ "$goroutines" -gt 500 ]; then
        log_warn "High goroutine count: $goroutines (PID: $pid)"
    fi
}

# Main monitoring loop with enhanced error handling
while true; do
    # Wrap entire loop iteration in error handling to prevent script death
    {
        rotate_log_if_needed

        TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')

        # Find the actual remote-network binary process, not scripts
        if ! PID=$(pgrep -x "$APP_NAME" || pgrep -f "/$APP_NAME$" || pgrep -f "$APP_NAME" | grep -v "monitoring" | grep -v "$$" | head -1); then
            echo "$TIMESTAMP,not_found,0,0,0,0,0,0,0,0,0,0,0,0,0,0,process_not_found" >> "$LOG_FILE"
            printf "\r%s | Process not found" "$(date '+%H:%M:%S')"
            sleep "$INTERVAL"
            continue
        fi

        if [ -z "$PID" ]; then
            echo "$TIMESTAMP,not_found,0,0,0,0,0,0,0,0,0,0,0,0,0,0,pid_empty" >> "$LOG_FILE"
            printf "\r%s | Process not found" "$(date '+%H:%M:%S')"
            sleep "$INTERVAL"
            continue
        fi

        # Check if process still exists
        if ! kill -0 "$PID" 2>/dev/null; then
            echo "$TIMESTAMP,process_died,0,0,0,0,0,0,0,0,0,0,0,0,0,0,process_died" >> "$LOG_FILE"
            printf "\r%s | Process died (PID: %s)" "$(date '+%H:%M:%S')" "$PID"
            sleep "$INTERVAL"
            continue
        fi

        # Get process statistics
        if STATS=$(get_process_stats "$PID"); then
            echo "$TIMESTAMP,$PID,$STATS" >> "$LOG_FILE"

            # Parse stats for display and alerts and clean up any newlines
            CPU=$(echo "$STATS" | cut -d, -f1 | tr -d '\n\r')
            MEM=$(echo "$STATS" | cut -d, -f2 | tr -d '\n\r')
            RSS=$(echo "$STATS" | cut -d, -f3 | tr -d '\n\r')
            THREADS=$(echo "$STATS" | cut -d, -f5 | tr -d '\n\r')
            FD_COUNT=$(echo "$STATS" | cut -d, -f6 | tr -d '\n\r')
            TCP_CONN=$(echo "$STATS" | cut -d, -f7 | tr -d '\n\r')
            GOROUTINES=$(echo "$STATS" | cut -d, -f8 | tr -d '\n\r')
            EXTERNAL_GR=$(echo "$STATS" | cut -d, -f9 | tr -d '\n\r')
            APP_GR=$(echo "$STATS" | cut -d, -f10 | tr -d '\n\r')
            DHT_GR=$(echo "$STATS" | cut -d, -f11 | tr -d '\n\r')
            QUIC_GR=$(echo "$STATS" | cut -d, -f12 | tr -d '\n\r')
            P2P_GR=$(echo "$STATS" | cut -d, -f13 | tr -d '\n\r')
            NET_GR=$(echo "$STATS" | cut -d, -f14 | tr -d '\n\r')
            RUNTIME_GR=$(echo "$STATS" | cut -d, -f15 | tr -d '\n\r')
            ERRORS=$(echo "$STATS" | cut -d, -f23 | tr -d '\n\r')

            # Resource alerts
            check_resource_alerts "$PID" "$CPU" "$MEM" "$RSS" "$GOROUTINES"

            # Enhanced display with detailed external package breakdown
            printf "\r%s | PID:%s | CPU:%s%% | MEM:%s%% | RSS:%sMB | T:%s | FD:%s | TCP:%s | GR:%s(E:%s/A:%s|D:%s/Q:%s/P:%s/N:%s/R:%s)" \
                "$(date '+%H:%M:%S')" "$PID" "$CPU" "$MEM" "$RSS" "$THREADS" "$FD_COUNT" "$TCP_CONN" "$GOROUTINES" "$EXTERNAL_GR" "$APP_GR" "$DHT_GR" "$QUIC_GR" "$P2P_GR" "$NET_GR" "$RUNTIME_GR"

            if [ "$ERRORS" != "none" ]; then
                printf " | ERR:%s" "$ERRORS"
            fi

            # Force output and ensure line completion
            printf "\n"; sleep 0.1

        else
            log_error "Failed to get process stats for PID $PID"
            echo "$TIMESTAMP,$PID,error_getting_stats,0,0,0,0,0,0,0,0,0,0,0,0,0,stats_failed" >> "$LOG_FILE"
        fi

    } || {
        # Catch any errors that escape the individual error handlers
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] ERROR: Main loop iteration failed, but continuing monitoring..." >> "$ERROR_LOG" 2>/dev/null || true
        printf "\r%s | ERROR in monitoring iteration, continuing...\n" "$(date '+%H:%M:%S')"
    }

    sleep "$INTERVAL"
done