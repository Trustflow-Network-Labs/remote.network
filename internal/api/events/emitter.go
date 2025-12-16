package events

import (
	"context"
	"fmt"
	"time"

	ws "github.com/Trustflow-Network-Labs/remote-network-node/internal/api/websocket"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/core"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/sirupsen/logrus"
)

// Emitter broadcasts real-time events to WebSocket clients
type Emitter struct {
	hub         *ws.Hub
	peerManager *core.PeerManager
	dbManager   *database.SQLiteManager
	logger      *logrus.Logger
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewEmitter creates a new event emitter
func NewEmitter(
	hub *ws.Hub,
	peerManager *core.PeerManager,
	dbManager *database.SQLiteManager,
	logger *logrus.Logger,
) *Emitter {
	ctx, cancel := context.WithCancel(context.Background())

	return &Emitter{
		hub:         hub,
		peerManager: peerManager,
		dbManager:   dbManager,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start begins broadcasting periodic updates
func (e *Emitter) Start() {
	// Start periodic broadcasts
	go e.broadcastNodeStatus()
	go e.broadcastPeers()
	go e.broadcastRelay()
	go e.broadcastServices()
	go e.broadcastWorkflows()
	go e.broadcastBlacklist()

	e.logger.Info("Event emitter started")
}

// Stop stops the event emitter
func (e *Emitter) Stop() {
	e.cancel()
	e.logger.Info("Event emitter stopped")
}

// broadcastNodeStatus sends node status updates every 5 seconds
func (e *Emitter) broadcastNodeStatus() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.hub.ClientCount() == 0 {
				// No clients connected, skip
				continue
			}

			// Get node status
			stats := e.peerManager.GetStats()

			// Get known peers count
			peersCount, err := e.peerManager.GetKnownPeers().GetKnownPeersCount()
			if err != nil {
				e.logger.WithError(err).Error("Failed to get peers count")
				peersCount = 0
			}

			// Extract peer_id and dht_node_id from stats
			peerID := ""
			if val, ok := stats["peer_id"]; ok && val != nil {
				if str, ok := val.(string); ok {
					peerID = str
				}
			}

			dhtNodeID := ""
			if val, ok := stats["dht_node_id"]; ok && val != nil {
				if str, ok := val.(string); ok {
					dhtNodeID = str
				}
			}

			uptime := int64(0)
			if val, ok := stats["uptime_seconds"]; ok && val != nil {
				if floatVal, ok := val.(float64); ok {
					uptime = int64(floatVal)
				}
			}

			payload := ws.NodeStatusPayload{
				PeerID:     peerID,
				DHTNodeID:  dhtNodeID,
				Uptime:     uptime,
				Stats:      stats,
				KnownPeers: peersCount,
			}

			if err := e.hub.BroadcastPayload(ws.MessageTypeNodeStatus, payload); err != nil {
				e.logger.WithError(err).Error("Failed to broadcast node status")
			}
		}
	}
}

// broadcastPeers sends peer list updates every 10 seconds
func (e *Emitter) broadcastPeers() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.hub.ClientCount() == 0 {
				continue
			}

			// Get known peers from database
			peers, err := e.peerManager.GetKnownPeers().GetAllKnownPeers()
			if err != nil {
				e.logger.WithError(err).Error("Failed to get peers")
				continue
			}

			// Convert to payload format
			peerInfos := make([]ws.PeerInfo, 0, len(peers))
			for _, peer := range peers {
				// Convert Addresses field (assuming it's a comma-separated string or similar)
				addresses := []string{}
				// The KnownPeer might not have Addresses field, using empty slice for now

				peerInfos = append(peerInfos, ws.PeerInfo{
					ID:                peer.PeerID,
					Addresses:         addresses,
					LastSeen:          peer.LastSeen.Unix(),
					IsRelay:           peer.IsRelay,
					CanBeRelay:        false, // KnownPeer doesn't have this field
					ConnectionQuality: 0,     // KnownPeer doesn't have this field
					Metadata:          nil,   // KnownPeer doesn't have this field
				})
			}

			payload := ws.PeersPayload{
				Peers: peerInfos,
			}

			if err := e.hub.BroadcastPayload(ws.MessageTypePeersUpdated, payload); err != nil {
				e.logger.WithError(err).Error("Failed to broadcast peers")
			}
		}
	}
}

// broadcastRelayOnce performs a single relay broadcast (sessions and candidates)
func (e *Emitter) broadcastRelayOnce() {
	if e.hub.ClientCount() == 0 {
		return
	}

	// Broadcast relay sessions (relay mode)
	relayPeer := e.peerManager.GetRelayPeer()
	if relayPeer != nil {
		sessionMaps := relayPeer.GetSessionDetails()
		sessionInfos := make([]ws.RelaySession, 0, len(sessionMaps))

		for _, session := range sessionMaps {
			sessionID := ""
			remotePeerID := ""
			startTime := int64(0)
			durationSeconds := int64(0)
			bytesSent := int64(0)
			bytesRecv := int64(0)
			earnings := float64(0)

			if val, ok := session["session_id"].(string); ok {
				sessionID = val
			}
			if val, ok := session["client_peer_id"].(string); ok {
				remotePeerID = val
			}
			if val, ok := session["start_time"].(int64); ok {
				startTime = val
			}
			if val, ok := session["duration_seconds"].(int64); ok {
				durationSeconds = val
			}
			if val, ok := session["egress_bytes"].(int64); ok {
				bytesSent = val
			}
			if val, ok := session["ingress_bytes"].(int64); ok {
				bytesRecv = val
			}
			if val, ok := session["earnings"].(float64); ok {
				earnings = val
			}

			sessionInfos = append(sessionInfos, ws.RelaySession{
				SessionID:       sessionID,
				RemotePeerID:    remotePeerID,
				StartTime:       startTime,
				DurationSeconds: durationSeconds,
				BytesSent:       bytesSent,
				BytesRecv:       bytesRecv,
				Earnings:        earnings,
			})
		}

		sessionsPayload := ws.RelaySessionsPayload{
			Sessions: sessionInfos,
		}

		if err := e.hub.BroadcastPayload(ws.MessageTypeRelaySessions, sessionsPayload); err != nil {
			e.logger.WithError(err).Error("Failed to broadcast relay sessions")
		}
	}

	// Broadcast relay candidates (NAT mode)
	relayManager := e.peerManager.GetRelayManager()
	if relayManager != nil {
		// Get current relay and preferred relay
		currentRelay := relayManager.GetCurrentRelay()

		stats := e.peerManager.GetStats()
		myPeerID := ""
		if val, ok := stats["peer_id"]; ok {
			if str, ok := val.(string); ok {
				myPeerID = str
			}
		}

		preferredRelayPeerID, _ := relayManager.GetPreferredRelay(myPeerID)

		// Get all candidates
		candidates := relayManager.GetSelector().GetCandidates()
		candidateInfos := make([]ws.RelayCandidate, 0, len(candidates))

		for _, candidate := range candidates {
			candidateInfos = append(candidateInfos, ws.RelayCandidate{
				NodeID:          candidate.NodeID,
				PeerID:          candidate.PeerID,
				Endpoint:        candidate.Endpoint,
				Latency:         candidate.Latency.String(),
				LatencyMs:       candidate.Latency.Milliseconds(),
				ReputationScore: candidate.ReputationScore,
				PricingPerGB:    candidate.PricingPerGB,
				Capacity:        candidate.Capacity,
				LastSeen:        candidate.LastSeen.Unix(),
				IsConnected:     currentRelay != nil && currentRelay.NodeID == candidate.NodeID,
				IsPreferred:     preferredRelayPeerID != "" && preferredRelayPeerID == candidate.PeerID,
			})
		}

		candidatesPayload := ws.RelayCandidatesPayload{
			Candidates: candidateInfos,
		}

		if err := e.hub.BroadcastPayload(ws.MessageTypeRelayCandidates, candidatesPayload); err != nil {
			e.logger.WithError(err).Error("Failed to broadcast relay candidates")
		}
	}
}

// TriggerRelayBroadcast triggers an immediate relay broadcast (for immediate UI updates)
func (e *Emitter) TriggerRelayBroadcast() {
	e.broadcastRelayOnce()
}

// broadcastRelay sends relay information every 5 seconds
func (e *Emitter) broadcastRelay() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.broadcastRelayOnce()
		}
	}
}

// broadcastServices sends service list updates every 15 seconds
func (e *Emitter) broadcastServices() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.hub.ClientCount() == 0 {
				continue
			}

			// Get services from database
			services, err := e.dbManager.GetAllServices()
			if err != nil {
				e.logger.WithError(err).Error("Failed to get services")
				continue
			}

			// Convert to payload format
			serviceInfos := make([]ws.ServiceInfo, 0, len(services))
			for _, svc := range services {
				serviceInfos = append(serviceInfos, ws.ServiceInfo{
					ID:          fmt.Sprintf("%d", svc.ID),
					Name:        svc.Name,
					Description: svc.Description,
					Type:        svc.Type,
					Config:      svc.Capabilities,
					Status:      svc.Status,
					CreatedAt:   svc.CreatedAt.Unix(),
					UpdatedAt:   svc.UpdatedAt.Unix(),
				})
			}

			payload := ws.ServicesPayload{
				Services: serviceInfos,
			}

			if err := e.hub.BroadcastPayload(ws.MessageTypeServicesUpdated, payload); err != nil {
				e.logger.WithError(err).Error("Failed to broadcast services")
			}
		}
	}
}

// broadcastWorkflows sends workflow list updates every 15 seconds
func (e *Emitter) broadcastWorkflows() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.hub.ClientCount() == 0 {
				continue
			}

			// Get workflows from database
			workflows, err := e.dbManager.GetAllWorkflows()
			if err != nil {
				e.logger.WithError(err).Error("Failed to get workflows")
				continue
			}

			// Convert to payload format
			workflowInfos := make([]ws.WorkflowInfo, 0, len(workflows))
			for _, wf := range workflows {
				// Count jobs for this workflow
				jobs, _ := e.dbManager.GetWorkflowJobs(wf.ID)
				jobCount := len(jobs)

				// Determine overall status based on most recent job
				status := "idle"
				if len(jobs) > 0 {
					status = jobs[0].Status // Most recent job
				}

				workflowInfos = append(workflowInfos, ws.WorkflowInfo{
					ID:          fmt.Sprintf("%d", wf.ID),
					Name:        wf.Name,
					Description: wf.Description,
					Status:      status,
					JobCount:    jobCount,
					Config:      wf.Definition,
					CreatedAt:   wf.CreatedAt.Unix(),
					UpdatedAt:   wf.UpdatedAt.Unix(),
				})
			}

			payload := ws.WorkflowsPayload{
				Workflows: workflowInfos,
			}

			if err := e.hub.BroadcastPayload(ws.MessageTypeWorkflowsUpdated, payload); err != nil {
				e.logger.WithError(err).Error("Failed to broadcast workflows")
			}
		}
	}
}

// broadcastBlacklist sends blacklist updates every 30 seconds
func (e *Emitter) broadcastBlacklist() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.hub.ClientCount() == 0 {
				continue
			}

			// Get blacklist from database
			blacklist, err := e.dbManager.GetBlacklist()
			if err != nil {
				e.logger.WithError(err).Error("Failed to get blacklist")
				continue
			}

			// Convert to payload format
			blacklistEntries := make([]ws.BlacklistEntry, 0, len(blacklist))
			for _, entry := range blacklist {
				blacklistEntries = append(blacklistEntries, ws.BlacklistEntry{
					PeerID:    entry.PeerID,
					Reason:    entry.Reason,
					AddedAt:   entry.BlacklistedAt.Unix(),
					ExpiresAt: 0, // Not supported in current blacklist implementation
				})
			}

			payload := ws.BlacklistPayload{
				Blacklist: blacklistEntries,
			}

			if err := e.hub.BroadcastPayload(ws.MessageTypeBlacklistUpdated, payload); err != nil {
				e.logger.WithError(err).Error("Failed to broadcast blacklist")
			}
		}
	}
}

// BroadcastServiceUpdate sends an immediate service update
func (e *Emitter) BroadcastServiceUpdate() {
	services, err := e.dbManager.GetAllServices()
	if err != nil {
		e.logger.WithError(err).Error("Failed to get services for broadcast")
		return
	}

	serviceInfos := make([]ws.ServiceInfo, 0, len(services))
	for _, svc := range services {
		serviceInfos = append(serviceInfos, ws.ServiceInfo{
			ID:          fmt.Sprintf("%d", svc.ID),
			Name:        svc.Name,
			Description: svc.Description,
			Type:        svc.Type,
			Config:      svc.Capabilities,
			Status:      svc.Status,
			CreatedAt:   svc.CreatedAt.Unix(),
			UpdatedAt:   svc.UpdatedAt.Unix(),
		})
	}

	payload := ws.ServicesPayload{
		Services: serviceInfos,
	}

	if err := e.hub.BroadcastPayload(ws.MessageTypeServicesUpdated, payload); err != nil {
		e.logger.WithError(err).Error("Failed to broadcast service update")
	}
}

// BroadcastWorkflowUpdate sends an immediate workflow update
func (e *Emitter) BroadcastWorkflowUpdate() {
	workflows, err := e.dbManager.GetAllWorkflows()
	if err != nil {
		e.logger.WithError(err).Error("Failed to get workflows for broadcast")
		return
	}

	workflowInfos := make([]ws.WorkflowInfo, 0, len(workflows))
	for _, wf := range workflows {
		jobs, _ := e.dbManager.GetWorkflowJobs(wf.ID)
		jobCount := len(jobs)

		// Determine overall status based on most recent job
		status := "idle"
		if len(jobs) > 0 {
			status = jobs[0].Status // Most recent job
		}

		workflowInfos = append(workflowInfos, ws.WorkflowInfo{
			ID:          fmt.Sprintf("%d", wf.ID),
			Name:        wf.Name,
			Description: wf.Description,
			Status:      status,
			JobCount:    jobCount,
			Config:      wf.Definition,
			CreatedAt:   wf.CreatedAt.Unix(),
			UpdatedAt:   wf.UpdatedAt.Unix(),
		})
	}

	payload := ws.WorkflowsPayload{
		Workflows: workflowInfos,
	}

	if err := e.hub.BroadcastPayload(ws.MessageTypeWorkflowsUpdated, payload); err != nil {
		e.logger.WithError(err).Error("Failed to broadcast workflow update")
	}
}

// BroadcastExecutionUpdate sends a workflow execution status update
func (e *Emitter) BroadcastExecutionUpdate(executionID int64) {
	execution, err := e.dbManager.GetWorkflowExecutionByID(executionID)
	if err != nil {
		e.logger.WithError(err).Error("Failed to get execution for broadcast")
		return
	}

	payload := ws.ExecutionUpdatedPayload{
		ExecutionID: execution.ID,
		WorkflowID:  execution.WorkflowID,
		Status:      execution.Status,
		Error:       execution.Error,
		StartedAt:   execution.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	if !execution.CompletedAt.IsZero() {
		payload.CompletedAt = execution.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	if err := e.hub.BroadcastPayload(ws.MessageTypeExecutionUpdated, payload); err != nil {
		e.logger.WithError(err).Error("Failed to broadcast execution update")
	}
}

// BroadcastJobStatusUpdate sends a job status update
func (e *Emitter) BroadcastJobStatusUpdate(jobExecutionID, workflowJobID, executionID int64, jobName, status, errorMsg string) {
	payload := ws.JobStatusUpdatedPayload{
		JobExecutionID: jobExecutionID,
		WorkflowJobID:  workflowJobID,
		ExecutionID:    executionID,
		JobName:        jobName,
		Status:         status,
		Error:          errorMsg,
	}

	if err := e.hub.BroadcastPayload(ws.MessageTypeJobStatusUpdated, payload); err != nil {
		e.logger.WithError(err).Error("Failed to broadcast job status update")
	}
}

// BroadcastBlacklistUpdate sends an immediate blacklist update
func (e *Emitter) BroadcastBlacklistUpdate() {
	blacklist, err := e.dbManager.GetBlacklist()
	if err != nil {
		e.logger.WithError(err).Error("Failed to get blacklist for broadcast")
		return
	}

	blacklistEntries := make([]ws.BlacklistEntry, 0, len(blacklist))
	for _, entry := range blacklist {
		blacklistEntries = append(blacklistEntries, ws.BlacklistEntry{
			PeerID:    entry.PeerID,
			Reason:    entry.Reason,
			AddedAt:   entry.BlacklistedAt.Unix(),
			ExpiresAt: 0, // Not supported in current blacklist implementation
		})
	}

	payload := ws.BlacklistPayload{
		Blacklist: blacklistEntries,
	}

	if err := e.hub.BroadcastPayload(ws.MessageTypeBlacklistUpdated, payload); err != nil {
		e.logger.WithError(err).Error("Failed to broadcast blacklist update")
	}
}

// BroadcastDockerPullProgress sends Docker image pull progress update
func (e *Emitter) BroadcastDockerPullProgress(serviceName, imageName, status, progress string, progressDetail map[string]interface{}) {
	payload := ws.DockerPullProgressPayload{
		ServiceName:    serviceName,
		ImageName:      imageName,
		Status:         status,
		Progress:       progress,
		ProgressDetail: progressDetail,
	}

	if err := e.hub.BroadcastPayload(ws.MessageTypeDockerPullProgress, payload); err != nil {
		e.logger.WithError(err).Error("Failed to broadcast Docker pull progress")
	}
}

// BroadcastDockerBuildOutput sends Docker image build output update
func (e *Emitter) BroadcastDockerBuildOutput(serviceName, imageName, stream, errorMsg string, errorDetail map[string]interface{}) {
	payload := ws.DockerBuildOutputPayload{
		ServiceName: serviceName,
		ImageName:   imageName,
		Stream:      stream,
		Error:       errorMsg,
		ErrorDetail: errorDetail,
	}

	if err := e.hub.BroadcastPayload(ws.MessageTypeDockerBuildOutput, payload); err != nil {
		e.logger.WithError(err).Error("Failed to broadcast Docker build output")
	}
}
