package p2p

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/database"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/quic-go/quic-go"
)

// RelayCache stores cached relay connection info
type RelayCache struct {
	RelayAddress   string
	RelaySessionID string
	CachedAt       time.Time
}

// JobMessageHandler handles job-related messages over QUIC
type JobMessageHandler struct {
	logger        *utils.LogsManager
	cm            *utils.ConfigManager
	quicPeer      *QUICPeer
	dbManager     *database.SQLiteManager
	metadataQuery *MetadataQueryService
	ourPeerID     string
	// Callbacks for job operations
	onJobRequest              func(*types.JobExecutionRequest, string) (*types.JobExecutionResponse, error)
	onJobStart                func(*types.JobStartRequest, string) (*types.JobStartResponse, error)
	onJobStatusUpdate         func(*types.JobStatusUpdate, string) error
	onJobStatusRequest        func(*types.JobStatusRequest, string) (*types.JobStatusResponse, error)
	onJobDataTransferRequest  func(*types.JobDataTransferRequest, string) (*types.JobDataTransferResponse, error)
	onJobDataChunk            func(*types.JobDataChunk, string) error
	onJobDataTransferComplete func(*types.JobDataTransferComplete, string) error
	onJobCancel               func(*types.JobCancelRequest, string) (*types.JobCancelResponse, error)
	// Relay connection cache (peerID -> relay info)
	relayCache   map[string]*RelayCache
	relayCacheMu sync.RWMutex
}

// NewJobMessageHandler creates a new job message handler
func NewJobMessageHandler(cm *utils.ConfigManager, quicPeer *QUICPeer) *JobMessageHandler {
	return &JobMessageHandler{
		logger:     utils.NewLogsManager(cm),
		cm:         cm,
		quicPeer:   quicPeer,
		relayCache: make(map[string]*RelayCache),
	}
}

// SetDependencies sets the additional dependencies needed for relay forwarding
func (jmh *JobMessageHandler) SetDependencies(dbManager *database.SQLiteManager, metadataQuery *MetadataQueryService, ourPeerID string) {
	jmh.dbManager = dbManager
	jmh.metadataQuery = metadataQuery
	jmh.ourPeerID = ourPeerID
}

// SetCallbacks sets the callback functions for job operations
func (jmh *JobMessageHandler) SetCallbacks(
	onJobRequest func(*types.JobExecutionRequest, string) (*types.JobExecutionResponse, error),
	onJobStart func(*types.JobStartRequest, string) (*types.JobStartResponse, error),
	onJobStatusUpdate func(*types.JobStatusUpdate, string) error,
	onJobStatusRequest func(*types.JobStatusRequest, string) (*types.JobStatusResponse, error),
	onJobDataTransferRequest func(*types.JobDataTransferRequest, string) (*types.JobDataTransferResponse, error),
	onJobDataChunk func(*types.JobDataChunk, string) error,
	onJobDataTransferComplete func(*types.JobDataTransferComplete, string) error,
	onJobCancel func(*types.JobCancelRequest, string) (*types.JobCancelResponse, error),
) {
	jmh.onJobRequest = onJobRequest
	jmh.onJobStart = onJobStart
	jmh.onJobStatusUpdate = onJobStatusUpdate
	jmh.onJobStatusRequest = onJobStatusRequest
	jmh.onJobDataTransferRequest = onJobDataTransferRequest
	jmh.onJobDataChunk = onJobDataChunk
	jmh.onJobDataTransferComplete = onJobDataTransferComplete
	jmh.onJobCancel = onJobCancel
}

// HandleJobMessage handles incoming job-related messages
func (jmh *JobMessageHandler) HandleJobMessage(msg *QUICMessage, stream *quic.Stream, peerID string) error {
	jmh.logger.Info(fmt.Sprintf("Handling job message type: %s from peer: %s", msg.Type, peerID), "job_handler")

	switch msg.Type {
	case MessageTypeJobRequest:
		return jmh.handleJobRequest(msg, stream, peerID)

	case MessageTypeJobResponse:
		return jmh.handleJobResponse(msg, stream, peerID)

	case MessageTypeJobStart:
		return jmh.handleJobStart(msg, stream, peerID)

	case MessageTypeJobStartResponse:
		return jmh.handleJobStartResponse(msg, stream, peerID)

	case MessageTypeJobStatusUpdate:
		return jmh.handleJobStatusUpdate(msg, stream, peerID)

	case MessageTypeJobStatusRequest:
		return jmh.handleJobStatusRequest(msg, stream, peerID)

	case MessageTypeJobDataTransferRequest:
		return jmh.handleJobDataTransferRequest(msg, stream, peerID)

	case MessageTypeJobDataTransferResponse:
		return jmh.handleJobDataTransferResponse(msg, stream, peerID)

	case MessageTypeJobDataChunk:
		return jmh.handleJobDataChunk(msg, stream, peerID)

	case MessageTypeJobDataTransferComplete:
		return jmh.handleJobDataTransferComplete(msg, stream, peerID)

	case MessageTypeJobDataChunkAck:
		return jmh.handleJobDataChunkAck(msg, stream, peerID)

	case MessageTypeJobDataTransferResume:
		return jmh.handleJobDataTransferResume(msg, stream, peerID)

	case MessageTypeJobDataTransferStall:
		return jmh.handleJobDataTransferStall(msg, stream, peerID)

	case MessageTypeJobCancel:
		return jmh.handleJobCancel(msg, stream, peerID)

	default:
		return fmt.Errorf("unknown job message type: %s", msg.Type)
	}
}

// HandleRelayedJobMessage handles job messages received via relay forwarding
// Returns response message instead of writing to stream (relay-compatible pattern)
func (jmh *JobMessageHandler) HandleRelayedJobMessage(msg *QUICMessage, sourcePeerID string) *QUICMessage {
	// DEBUG: Log entry into handler
	jmh.logger.Info(fmt.Sprintf("DEBUG: HandleRelayedJobMessage called with type=%s from peer=%s", msg.Type, sourcePeerID), "job_handler")
	jmh.logger.Info(fmt.Sprintf("Handling relayed job message type: %s from peer: %s", msg.Type, sourcePeerID), "job_handler")

	switch msg.Type {
	case MessageTypeJobRequest:
		return jmh.handleRelayedJobRequest(msg, sourcePeerID)

	case MessageTypeJobStart:
		return jmh.handleRelayedJobStart(msg, sourcePeerID)

	case MessageTypeJobStatusRequest:
		return jmh.handleRelayedJobStatusRequest(msg, sourcePeerID)

	case MessageTypeJobDataTransferRequest:
		return jmh.handleRelayedDataTransferRequest(msg, sourcePeerID)

	case MessageTypeJobCancel:
		return jmh.handleRelayedJobCancel(msg, sourcePeerID)

	// One-way messages (no response expected) - handle directly
	case MessageTypeJobStatusUpdate:
		if err := jmh.handleJobStatusUpdate(msg, nil, sourcePeerID); err != nil {
			jmh.logger.Error(fmt.Sprintf("Failed to handle relayed status update: %v", err), "job_handler")
		}
		return nil

	case MessageTypeJobDataChunk:
		// DEBUG: Log chunk message receipt
		jmh.logger.Info("DEBUG: Received job_data_chunk message via relay, calling handleJobDataChunk", "job_handler")
		if err := jmh.handleJobDataChunk(msg, nil, sourcePeerID); err != nil {
			jmh.logger.Error(fmt.Sprintf("Failed to handle relayed data chunk: %v", err), "job_handler")
		}
		return nil

	case MessageTypeJobDataTransferComplete:
		if err := jmh.handleJobDataTransferComplete(msg, nil, sourcePeerID); err != nil {
			jmh.logger.Error(fmt.Sprintf("Failed to handle relayed transfer complete: %v", err), "job_handler")
		}
		return nil

	case MessageTypeJobDataChunkAck:
		if err := jmh.handleJobDataChunkAck(msg, nil, sourcePeerID); err != nil {
			jmh.logger.Error(fmt.Sprintf("Failed to handle relayed chunk ack: %v", err), "job_handler")
		}
		return nil

	case MessageTypeJobDataTransferResume:
		if err := jmh.handleJobDataTransferResume(msg, nil, sourcePeerID); err != nil {
			jmh.logger.Error(fmt.Sprintf("Failed to handle relayed transfer resume: %v", err), "job_handler")
		}
		return nil

	case MessageTypeJobDataTransferStall:
		if err := jmh.handleJobDataTransferStall(msg, nil, sourcePeerID); err != nil {
			jmh.logger.Error(fmt.Sprintf("Failed to handle relayed transfer stall: %v", err), "job_handler")
		}
		return nil

	default:
		jmh.logger.Warn(fmt.Sprintf("Unhandled relayed job message type: %s", msg.Type), "job_handler")
		return nil
	}
}

// handleRelayedJobRequest handles job requests from relay (returns response instead of writing to stream)
func (jmh *JobMessageHandler) handleRelayedJobRequest(msg *QUICMessage, peerID string) *QUICMessage {
	if jmh.onJobRequest == nil {
		jmh.logger.Error("No callback registered for job requests", "job_handler")
		return CreateJobResponse(&types.JobExecutionResponse{
			Accepted: false,
			Message:  "job request handler not available",
		})
	}

	var request types.JobExecutionRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job request: %v", err), "job_handler")
		return CreateJobResponse(&types.JobExecutionResponse{
			Accepted: false,
			Message:  fmt.Sprintf("invalid request: %v", err),
		})
	}

	jmh.logger.Info(fmt.Sprintf("Received relayed job request from peer %s for workflow job %d", peerID, request.WorkflowJobID), "job_handler")

	// Call the callback
	response, err := jmh.onJobRequest(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job request callback failed: %v", err), "job_handler")
		response = &types.JobExecutionResponse{
			WorkflowJobID: request.WorkflowJobID,
			Accepted:      false,
			Message:       fmt.Sprintf("Job request failed: %v", err),
		}
	}

	jmh.logger.Info(fmt.Sprintf("Sending relayed job response for workflow job %d: accepted=%v", request.WorkflowJobID, response.Accepted), "job_handler")
	return CreateJobResponse(response)
}

// handleRelayedJobStart handles job start requests from relay (returns response instead of writing to stream)
func (jmh *JobMessageHandler) handleRelayedJobStart(msg *QUICMessage, peerID string) *QUICMessage {
	if jmh.onJobStart == nil {
		jmh.logger.Error("No callback registered for job start requests", "job_handler")
		return CreateJobStartResponse(&types.JobStartResponse{
			Started: false,
			Message: "job start handler not available",
		})
	}

	var request types.JobStartRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job start request: %v", err), "job_handler")
		return CreateJobStartResponse(&types.JobStartResponse{
			Started: false,
			Message: fmt.Sprintf("invalid request: %v", err),
		})
	}

	jmh.logger.Info(fmt.Sprintf("Received relayed job start request from peer %s for job execution %d", peerID[:8], request.JobExecutionID), "job_handler")

	// Call the callback
	response, err := jmh.onJobStart(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job start callback failed: %v", err), "job_handler")
		response = &types.JobStartResponse{
			JobExecutionID: request.JobExecutionID,
			WorkflowJobID:  request.WorkflowJobID,
			Started:        false,
			Message:        fmt.Sprintf("Job start failed: %v", err),
		}
	}

	jmh.logger.Info(fmt.Sprintf("Sending relayed job start response for job execution %d: started=%v", request.JobExecutionID, response.Started), "job_handler")
	return CreateJobStartResponse(response)
}

// handleRelayedJobStatusRequest handles status requests from relay
func (jmh *JobMessageHandler) handleRelayedJobStatusRequest(msg *QUICMessage, peerID string) *QUICMessage {
	if jmh.onJobStatusRequest == nil {
		jmh.logger.Error("No callback registered for job status requests", "job_handler")
		return CreateJobStatusResponse(&types.JobStatusResponse{
			Found: false,
		})
	}

	var request types.JobStatusRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job status request: %v", err), "job_handler")
		return CreateJobStatusResponse(&types.JobStatusResponse{
			Found: false,
		})
	}

	jmh.logger.Debug(fmt.Sprintf("Received relayed job status request for job %d from peer %s", request.JobExecutionID, peerID), "job_handler")

	// Call the callback to get job status
	response, err := jmh.onJobStatusRequest(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job status request callback failed: %v", err), "job_handler")
		return CreateJobStatusResponse(&types.JobStatusResponse{
			JobExecutionID: request.JobExecutionID,
			WorkflowJobID:  request.WorkflowJobID,
			Found:          false,
		})
	}

	jmh.logger.Debug(fmt.Sprintf("Sending relayed job status response for job %d: status=%s", request.JobExecutionID, response.Status), "job_handler")
	return CreateJobStatusResponse(response)
}

// handleRelayedDataTransferRequest handles data transfer requests from relay
func (jmh *JobMessageHandler) handleRelayedDataTransferRequest(msg *QUICMessage, peerID string) *QUICMessage {
	if jmh.onJobDataTransferRequest == nil {
		jmh.logger.Error("No callback registered for job data transfer requests", "job_handler")
		return CreateJobDataTransferResponse(&types.JobDataTransferResponse{
			Accepted: false,
			Message:  "data transfer handler not available",
		})
	}

	var request types.JobDataTransferRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job data transfer request: %v", err), "job_handler")
		return CreateJobDataTransferResponse(&types.JobDataTransferResponse{
			Accepted: false,
			Message:  fmt.Sprintf("invalid request: %v", err),
		})
	}

	jmh.logger.Info(fmt.Sprintf("Received relayed data transfer request from peer %s for workflow job %d", peerID, request.WorkflowJobID), "job_handler")

	// Call the callback
	response, err := jmh.onJobDataTransferRequest(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Data transfer request callback failed: %v", err), "job_handler")
		return CreateJobDataTransferResponse(&types.JobDataTransferResponse{
			WorkflowJobID: request.WorkflowJobID,
			Accepted:      false,
			Message:       fmt.Sprintf("transfer request failed: %v", err),
		})
	}

	jmh.logger.Info(fmt.Sprintf("Sending relayed data transfer response for workflow job %d: accepted=%v", request.WorkflowJobID, response.Accepted), "job_handler")
	return CreateJobDataTransferResponse(response)
}

// handleRelayedJobCancel handles job cancel requests from relay
func (jmh *JobMessageHandler) handleRelayedJobCancel(msg *QUICMessage, peerID string) *QUICMessage {
	if jmh.onJobCancel == nil {
		jmh.logger.Error("No callback registered for job cancel requests", "job_handler")
		return CreateJobCancel(&types.JobCancelResponse{
			Cancelled: false,
			Message:   "cancel handler not available",
		})
	}

	var request types.JobCancelRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job cancel request: %v", err), "job_handler")
		return CreateJobCancel(&types.JobCancelResponse{
			Cancelled: false,
			Message:   fmt.Sprintf("invalid request: %v", err),
		})
	}

	jmh.logger.Info(fmt.Sprintf("Received relayed job cancel request from peer %s for job %d", peerID, request.JobExecutionID), "job_handler")

	// Call the callback
	response, err := jmh.onJobCancel(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job cancel callback failed: %v", err), "job_handler")
		return CreateJobCancel(&types.JobCancelResponse{
			JobExecutionID: request.JobExecutionID,
			Cancelled:      false,
			Message:        fmt.Sprintf("cancel failed: %v", err),
		})
	}

	jmh.logger.Info(fmt.Sprintf("Sending relayed job cancel response for job %d: cancelled=%v", request.JobExecutionID, response.Cancelled), "job_handler")
	return CreateJobCancel(response)
}

// handleJobRequest handles incoming job execution requests
func (jmh *JobMessageHandler) handleJobRequest(msg *QUICMessage, stream *quic.Stream, peerID string) error {
	if jmh.onJobRequest == nil {
		jmh.logger.Error("No callback registered for job requests", "job_handler")
		return fmt.Errorf("no callback registered for job requests")
	}

	var request types.JobExecutionRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job request: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job request for workflow %d from peer %s", request.WorkflowID, peerID), "job_handler")

	// Call the callback
	response, err := jmh.onJobRequest(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job request callback failed: %v", err), "job_handler")
		// Send error response
		response = &types.JobExecutionResponse{
			WorkflowJobID: request.WorkflowJobID,
			Accepted:      false,
			Message:       fmt.Sprintf("Job request failed: %v", err),
		}
	}

	// Send response
	responseMsg := CreateJobResponse(response)
	responseBytes, err := responseMsg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job response: %v", err), "job_handler")
		return err
	}

	_, err = stream.Write(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Sent job response for workflow job %d: accepted=%v", request.WorkflowJobID, response.Accepted), "job_handler")
	return nil
}

// handleJobResponse handles incoming job execution responses
func (jmh *JobMessageHandler) handleJobResponse(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	var response types.JobExecutionResponse
	if err := msg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job response for workflow job %d from peer %s: accepted=%v", response.WorkflowJobID, peerID, response.Accepted), "job_handler")
	// TODO: Handle response (update workflow status, etc.)
	return nil
}

// handleJobStart handles incoming job start requests (Phase 2)
func (jmh *JobMessageHandler) handleJobStart(msg *QUICMessage, stream *quic.Stream, peerID string) error {
	if jmh.onJobStart == nil {
		jmh.logger.Error("No callback registered for job start requests", "job_handler")
		return fmt.Errorf("no callback registered for job start requests")
	}

	var request types.JobStartRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job start request: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job start request for job execution %d from peer %s", request.JobExecutionID, peerID), "job_handler")

	// Call the callback
	response, err := jmh.onJobStart(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job start callback failed: %v", err), "job_handler")
		// Send error response
		response = &types.JobStartResponse{
			JobExecutionID: request.JobExecutionID,
			WorkflowJobID:  request.WorkflowJobID,
			Started:        false,
			Message:        fmt.Sprintf("Job start failed: %v", err),
		}
	}

	// Send response
	responseMsg := CreateJobStartResponse(response)
	responseBytes, err := responseMsg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job start response: %v", err), "job_handler")
		return err
	}

	_, err = stream.Write(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job start response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Sent job start response for job execution %d: started=%v", request.JobExecutionID, response.Started), "job_handler")
	return nil
}

// handleJobStartResponse handles incoming job start responses
func (jmh *JobMessageHandler) handleJobStartResponse(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	var response types.JobStartResponse
	if err := msg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job start response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job start response for job execution %d from peer %s: started=%v", response.JobExecutionID, peerID, response.Started), "job_handler")
	// TODO: Handle response if needed (track which jobs have started)
	return nil
}

// handleJobStatusUpdate handles incoming job status updates
func (jmh *JobMessageHandler) handleJobStatusUpdate(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	if jmh.onJobStatusUpdate == nil {
		jmh.logger.Error("No callback registered for job status updates", "job_handler")
		return fmt.Errorf("no callback registered for job status updates")
	}

	var update types.JobStatusUpdate
	if err := msg.GetDataAs(&update); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job status update: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job status update for job %d from peer %s: status=%s", update.JobExecutionID, peerID, update.Status), "job_handler")

	// Call the callback
	err := jmh.onJobStatusUpdate(&update, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job status update callback failed: %v", err), "job_handler")
		return err
	}

	return nil
}

// handleJobStatusRequest handles incoming job status requests
func (jmh *JobMessageHandler) handleJobStatusRequest(msg *QUICMessage, stream *quic.Stream, peerID string) error {
	if jmh.onJobStatusRequest == nil {
		jmh.logger.Error("No callback registered for job status requests", "job_handler")
		return fmt.Errorf("no callback registered for job status requests")
	}

	var request types.JobStatusRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job status request: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job status request for job %d from peer %s", request.JobExecutionID, peerID), "job_handler")

	// Call the callback to get job status
	response, err := jmh.onJobStatusRequest(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job status request callback failed: %v", err), "job_handler")
		return err
	}

	// Send response back
	responseMsg := CreateJobStatusResponse(response)
	responseBytes, err := responseMsg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal status response: %v", err), "job_handler")
		return err
	}

	if _, err := stream.Write(responseBytes); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to write status response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Sent job status response for job %d to peer %s: status=%s", request.JobExecutionID, peerID, response.Status), "job_handler")
	return nil
}

// handleJobDataTransferRequest handles incoming data transfer requests
func (jmh *JobMessageHandler) handleJobDataTransferRequest(msg *QUICMessage, stream *quic.Stream, peerID string) error {
	if jmh.onJobDataTransferRequest == nil {
		jmh.logger.Error("No callback registered for job data transfer requests", "job_handler")
		return fmt.Errorf("no callback registered for job data transfer requests")
	}

	var request types.JobDataTransferRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job data transfer request: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job data transfer request for workflow job %d from peer %s", request.WorkflowJobID, peerID), "job_handler")

	// Call the callback
	response, err := jmh.onJobDataTransferRequest(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job data transfer request callback failed: %v", err), "job_handler")
		// Send error response
		response = &types.JobDataTransferResponse{
			WorkflowJobID: request.WorkflowJobID,
			Accepted:      false,
			Message:       fmt.Sprintf("Data transfer request failed: %v", err),
		}
	}

	// Send response
	responseMsg := CreateJobDataTransferResponse(response)
	responseBytes, err := responseMsg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job data transfer response: %v", err), "job_handler")
		return err
	}

	_, err = stream.Write(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job data transfer response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Sent job data transfer response for workflow job %d: accepted=%v", request.WorkflowJobID, response.Accepted), "job_handler")
	return nil
}

// handleJobDataTransferResponse handles incoming data transfer responses
func (jmh *JobMessageHandler) handleJobDataTransferResponse(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	var response types.JobDataTransferResponse
	if err := msg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job data transfer response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job data transfer response for workflow job %d from peer %s: accepted=%v", response.WorkflowJobID, peerID, response.Accepted), "job_handler")
	// TODO: Handle response (start transfer, etc.)
	return nil
}

// handleJobDataChunk handles incoming data chunks
func (jmh *JobMessageHandler) handleJobDataChunk(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	if jmh.onJobDataChunk == nil {
		jmh.logger.Error("No callback registered for job data chunks", "job_handler")
		return fmt.Errorf("no callback registered for job data chunks")
	}

	var chunk types.JobDataChunk
	if err := msg.GetDataAs(&chunk); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job data chunk: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job data chunk %d/%d for transfer %s from peer %s", chunk.ChunkIndex+1, chunk.TotalChunks, chunk.TransferID, peerID), "job_handler")

	// Call the callback
	err := jmh.onJobDataChunk(&chunk, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job data chunk callback failed: %v", err), "job_handler")
		return err
	}

	return nil
}

// handleJobDataTransferComplete handles incoming transfer complete notifications
func (jmh *JobMessageHandler) handleJobDataTransferComplete(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	if jmh.onJobDataTransferComplete == nil {
		jmh.logger.Error("No callback registered for job data transfer complete", "job_handler")
		return fmt.Errorf("no callback registered for job data transfer complete")
	}

	var complete types.JobDataTransferComplete
	if err := msg.GetDataAs(&complete); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job data transfer complete: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job data transfer complete for transfer %s from peer %s: success=%v", complete.TransferID, peerID, complete.Success), "job_handler")

	// Call the callback
	err := jmh.onJobDataTransferComplete(&complete, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job data transfer complete callback failed: %v", err), "job_handler")
		return err
	}

	return nil
}

// handleJobDataChunkAck handles incoming chunk acknowledgments from receiver
func (jmh *JobMessageHandler) handleJobDataChunkAck(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	var ack types.JobDataChunkAck
	if err := msg.GetDataAs(&ack); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse chunk ACK: %v", err), "job_handler")
		return err
	}

	jmh.logger.Debug(fmt.Sprintf("Received chunk ACK from peer %s for transfer %s (%d chunks)",
		peerID, ack.TransferID, len(ack.ChunkIndexes)), "job_handler")

	// Get current transfer state from database
	transfer, err := jmh.dbManager.GetTransferByID(ack.TransferID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to get transfer %s: %v", ack.TransferID, err), "job_handler")
		return err
	}

	// Merge new ACKed chunks with existing ones
	ackedMap := make(map[int]bool)
	for _, idx := range transfer.ChunksAcked {
		ackedMap[idx] = true
	}
	for _, idx := range ack.ChunkIndexes {
		ackedMap[idx] = true
	}

	// Convert back to slice
	ackedList := make([]int, 0, len(ackedMap))
	for idx := range ackedMap {
		ackedList = append(ackedList, idx)
	}

	// Update database with new ACKed chunks
	err = jmh.dbManager.UpdateTransferProgress(ack.TransferID, transfer.ChunksSent, transfer.ChunksReceived, ackedList, transfer.BytesTransferred)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to update transfer progress: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Updated transfer %s: %d chunks acked (total: %d/%d)",
		ack.TransferID, len(ack.ChunkIndexes), len(ackedList), transfer.TotalChunks), "job_handler")

	return nil
}

// handleJobDataTransferResume handles incoming transfer resume requests from receiver
func (jmh *JobMessageHandler) handleJobDataTransferResume(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	var resume types.JobDataTransferResume
	if err := msg.GetDataAs(&resume); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse transfer resume request: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received transfer resume request from peer %s for transfer %s (last known chunk: %d, %d chunks received)",
		peerID, resume.TransferID, resume.LastKnownChunk, len(resume.ReceivedChunks)), "job_handler")

	// Get transfer from database
	transfer, err := jmh.dbManager.GetTransferByID(resume.TransferID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to get transfer %s: %v", resume.TransferID, err), "job_handler")
		return err
	}

	// Verify this is our transfer (we're the sender)
	if transfer.Direction != "sender" || transfer.SourcePeerID != jmh.ourPeerID {
		jmh.logger.Warn(fmt.Sprintf("Resume request for transfer %s, but we're not the sender", resume.TransferID), "job_handler")
		return fmt.Errorf("not the sender for this transfer")
	}

	// Calculate missing chunks (chunks receiver hasn't received yet)
	receivedMap := make(map[int]bool)
	for _, idx := range resume.ReceivedChunks {
		receivedMap[idx] = true
	}

	missingChunks := make([]int, 0)
	for i := 0; i < transfer.TotalChunks; i++ {
		if !receivedMap[i] {
			missingChunks = append(missingChunks, i)
		}
	}

	if len(missingChunks) == 0 {
		jmh.logger.Info(fmt.Sprintf("Transfer %s: receiver has all chunks, no resume needed", resume.TransferID), "job_handler")
		return nil
	}

	jmh.logger.Info(fmt.Sprintf("Transfer %s: receiver is missing %d/%d chunks, will resend them",
		resume.TransferID, len(missingChunks), transfer.TotalChunks), "job_handler")

	// Note: We log the resume request but don't resend chunks here directly
	// The actual resumption will be handled by the periodic checkAndResumeActiveTransfers()
	// which already has the proper logic for reading files and resending chunks

	// Update the transfer's received chunks based on what the receiver told us
	// This helps the automatic resumption logic know what to skip
	err = jmh.dbManager.UpdateTransferProgress(resume.TransferID, transfer.ChunksSent, resume.ReceivedChunks, transfer.ChunksAcked, transfer.BytesTransferred)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to update transfer progress with receiver state: %v", err), "job_handler")
	}

	jmh.logger.Info(fmt.Sprintf("Updated transfer %s state with receiver's chunk list, automatic resumption will handle resending", resume.TransferID), "job_handler")

	return nil
}

// handleJobDataTransferStall handles incoming transfer stall notifications from receiver
func (jmh *JobMessageHandler) handleJobDataTransferStall(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	var stall types.JobDataTransferStall
	if err := msg.GetDataAs(&stall); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse transfer stall notification: %v", err), "job_handler")
		return err
	}

	jmh.logger.Warn(fmt.Sprintf("Received transfer stall notification from peer %s for transfer %s: %s (last chunk sent: %d)",
		peerID, stall.TransferID, stall.Reason, stall.LastChunkSent), "job_handler")

	// TODO: Phase 3 - Implement sender-side stall handling
	// This will trigger retry mechanism to resume sending
	jmh.logger.Warn(fmt.Sprintf("Transfer stall handler not yet implemented (transfer %s)", stall.TransferID), "job_handler")

	return nil
}

// handleJobCancel handles incoming job cancellation requests
func (jmh *JobMessageHandler) handleJobCancel(msg *QUICMessage, stream *quic.Stream, peerID string) error {
	if jmh.onJobCancel == nil {
		jmh.logger.Error("No callback registered for job cancellation", "job_handler")
		return fmt.Errorf("no callback registered for job cancellation")
	}

	var request types.JobCancelRequest
	if err := msg.GetDataAs(&request); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job cancel request: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job cancel request for job %d from peer %s", request.JobExecutionID, peerID), "job_handler")

	// Call the callback
	response, err := jmh.onJobCancel(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job cancel callback failed: %v", err), "job_handler")
		// Send error response
		response = &types.JobCancelResponse{
			JobExecutionID: request.JobExecutionID,
			Cancelled:      false,
			Message:        fmt.Sprintf("Job cancellation failed: %v", err),
		}
	}

	// Send response
	responseMsg := CreateJobCancel(response)
	responseBytes, err := responseMsg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job cancel response: %v", err), "job_handler")
		return err
	}

	_, err = stream.Write(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job cancel response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Sent job cancel response for job %d: cancelled=%v", request.JobExecutionID, response.Cancelled), "job_handler")
	return nil
}

// SendJobRequest sends a job execution request to a peer (supports both direct and relay connections)
func (jmh *JobMessageHandler) SendJobRequest(peerID string, request *types.JobExecutionRequest) (*types.JobExecutionResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Sending job request to peer %s for workflow %d", peerID, request.WorkflowID), "job_handler")

	// Create message
	msg := CreateJobRequest(request)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job request: %v", err), "job_handler")
		return nil, err
	}

	// Try to send via direct connection first
	responseBytes, err := jmh.quicPeer.SendMessageWithResponse(peerID, msgBytes)
	if err != nil {
		jmh.logger.Info(fmt.Sprintf("Direct send failed, will try relay: %v", err), "job_handler")
		// Fallback to relay if direct connection fails
		return jmh.sendJobRequestViaRelay(peerID, msgBytes)
	}

	// Parse response
	responseMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job response: %v", err), "job_handler")
		return nil, err
	}

	var response types.JobExecutionResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job response data: %v", err), "job_handler")
		return nil, err
	}

	jmh.logger.Info(fmt.Sprintf("Received job response from peer %s: accepted=%v", peerID, response.Accepted), "job_handler")
	return &response, nil
}

// SendJobStart sends a job start request to a peer (Phase 2)
func (jmh *JobMessageHandler) SendJobStart(peerID string, request *types.JobStartRequest) (*types.JobStartResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Sending job start to peer %s for job execution %d", peerID[:8], request.JobExecutionID), "job_handler")

	// Create message
	msg := CreateJobStart(request)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job start: %v", err), "job_handler")
		return nil, err
	}

	// Try to send via direct connection first
	responseBytes, err := jmh.quicPeer.SendMessageWithResponse(peerID, msgBytes)
	if err != nil {
		jmh.logger.Info(fmt.Sprintf("Direct send failed, will try relay: %v", err), "job_handler")
		// Fallback to relay if direct connection fails
		return jmh.sendJobStartViaRelay(peerID, msgBytes)
	}

	// Parse response
	responseMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job start response: %v", err), "job_handler")
		return nil, err
	}

	var response types.JobStartResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job start response data: %v", err), "job_handler")
		return nil, err
	}

	jmh.logger.Info(fmt.Sprintf("Received job start response from peer %s: started=%v", peerID[:8], response.Started), "job_handler")
	return &response, nil
}

// sendJobStartViaRelay sends a job start via relay
func (jmh *JobMessageHandler) sendJobStartViaRelay(peerID string, jobStartBytes []byte) (*types.JobStartResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Attempting to send job start via relay to peer %s", peerID[:8]), "job_handler")

	// Use the same relay mechanism as other requests (JobRequest, JobStatusRequest, etc.)
	// This includes proper relay address lookup and automatic retry logic
	responseBytes, err := jmh.sendMessageWithResponseViaRelay(peerID, jobStartBytes, "job_start")
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job start via relay: %v", err), "job_handler")
		return nil, err
	}

	// Parse response
	responseMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job start response: %v", err), "job_handler")
		return nil, err
	}

	var response types.JobStartResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job start response data: %v", err), "job_handler")
		return nil, err
	}

	jmh.logger.Info(fmt.Sprintf("Received job start response from peer %s via relay: started=%v", peerID[:8], response.Started), "job_handler")
	return &response, nil
}

// SendJobStatusUpdate sends a job status update to a peer
func (jmh *JobMessageHandler) SendJobStatusUpdate(peerID string, update *types.JobStatusUpdate) error {
	jmh.logger.Info(fmt.Sprintf("Sending job status update to peer %s for job %d: status=%s", peerID, update.JobExecutionID, update.Status), "job_handler")

	// Create message
	msg := CreateJobStatusUpdate(update)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job status update: %v", err), "job_handler")
		return err
	}

	// Try direct connection first, fallback to relay
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		// Fallback to relay for NAT peers
		return jmh.sendMessageViaRelay(peerID, msgBytes, "job_status_update")
	}

	jmh.logger.Info(fmt.Sprintf("Sent job status update to peer %s for job %d", peerID, update.JobExecutionID), "job_handler")
	return nil
}

// SendJobStatusRequest requests job status from an executor peer
func (jmh *JobMessageHandler) SendJobStatusRequest(peerID string, request *types.JobStatusRequest) (*types.JobStatusResponse, error) {
	jmh.logger.Debug(fmt.Sprintf("Requesting job status from peer %s for job %d", peerID, request.JobExecutionID), "job_handler")

	// Create message
	msg := CreateJobStatusRequest(request)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job status request: %v", err), "job_handler")
		return nil, err
	}

	// Try direct connection first, fallback to relay
	responseBytes, err := jmh.quicPeer.SendMessageWithResponse(peerID, msgBytes)
	if err != nil {
		// Fallback to relay for NAT peers
		return jmh.sendStatusRequestViaRelay(peerID, msgBytes)
	}

	// Parse response
	responseMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to unmarshal status response: %v", err), "job_handler")
		return nil, err
	}

	var response types.JobStatusResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse status response: %v", err), "job_handler")
		return nil, err
	}

	jmh.logger.Debug(fmt.Sprintf("Received job status from peer %s for job %d: status=%s", peerID, request.JobExecutionID, response.Status), "job_handler")
	return &response, nil
}

// SendJobDataTransferRequest sends a data transfer request to a peer
func (jmh *JobMessageHandler) SendJobDataTransferRequest(peerID string, request *types.JobDataTransferRequest) (*types.JobDataTransferResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Sending job data transfer request to peer %s for workflow job %d (transfer: %s)", peerID, request.WorkflowJobID, request.TransferID), "job_handler")

	// Create message
	msg := CreateJobDataTransferRequest(request)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job data transfer request: %v", err), "job_handler")
		return nil, err
	}

	// Try direct connection first, fallback to relay
	responseBytes, err := jmh.quicPeer.SendMessageWithResponse(peerID, msgBytes)
	if err != nil {
		jmh.logger.Info(fmt.Sprintf("Direct data transfer request failed, trying relay: %v", err), "job_handler")
		return jmh.sendDataTransferRequestViaRelay(peerID, msgBytes)
	}

	// Parse response
	responseMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job data transfer response: %v", err), "job_handler")
		return nil, err
	}

	var response types.JobDataTransferResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job data transfer response data: %v", err), "job_handler")
		return nil, err
	}

	jmh.logger.Info(fmt.Sprintf("Received job data transfer response from peer %s: accepted=%v", peerID, response.Accepted), "job_handler")
	return &response, nil
}

// SendJobDataChunk sends a data chunk to a peer
func (jmh *JobMessageHandler) SendJobDataChunk(peerID string, chunk *types.JobDataChunk) error {
	jmh.logger.Debug(fmt.Sprintf("Sending job data chunk %d/%d to peer %s for transfer %s",
		chunk.ChunkIndex+1, chunk.TotalChunks, peerID, chunk.TransferID), "job_handler")

	// Create message
	msg := CreateJobDataChunk(chunk)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job data chunk: %v", err), "job_handler")
		return err
	}

	// DEBUG: Log marshaled message size
	jmh.logger.Info(fmt.Sprintf("DEBUG: Marshaled chunk message size: %d bytes (data payload: %d bytes)", len(msgBytes), len(chunk.Data)), "job_handler")

	// Try direct connection first, fallback to relay
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		// Fallback to relay for NAT peers
		return jmh.sendMessageViaRelay(peerID, msgBytes, "job_data_chunk")
	}

	return nil
}

// SendJobDataTransferComplete sends a transfer complete notification to a peer
func (jmh *JobMessageHandler) SendJobDataTransferComplete(peerID string, complete *types.JobDataTransferComplete) error {
	jmh.logger.Info(fmt.Sprintf("Sending job data transfer complete to peer %s for transfer %s", peerID, complete.TransferID), "job_handler")

	// Create message
	msg := CreateJobDataTransferComplete(complete)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job data transfer complete: %v", err), "job_handler")
		return err
	}

	// Send via QUIC (no response expected)
	// Try direct connection first, fallback to relay if it fails
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		jmh.logger.Info(fmt.Sprintf("Direct connection failed for job data transfer complete, trying relay: %v", err), "job_handler")
		return jmh.sendMessageViaRelay(peerID, msgBytes, "job_data_transfer_complete")
	}

	jmh.logger.Info(fmt.Sprintf("Sent job data transfer complete to peer %s for transfer %s", peerID, complete.TransferID), "job_handler")
	return nil
}

// SendJobDataChunkAck sends a chunk acknowledgment to a peer
func (jmh *JobMessageHandler) SendJobDataChunkAck(peerID string, ack *types.JobDataChunkAck) error {
	jmh.logger.Debug(fmt.Sprintf("Sending chunk ACK to peer %s for transfer %s (%d chunks)",
		peerID, ack.TransferID, len(ack.ChunkIndexes)), "job_handler")

	// Create message
	msg := CreateJobDataChunkAck(ack)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal chunk ACK: %v", err), "job_handler")
		return err
	}

	// Try direct connection first, fallback to relay
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		return jmh.sendMessageViaRelay(peerID, msgBytes, "job_data_chunk_ack")
	}

	return nil
}

// SendJobDataTransferResume sends a transfer resume request to a peer
func (jmh *JobMessageHandler) SendJobDataTransferResume(peerID string, resume *types.JobDataTransferResume) error {
	jmh.logger.Info(fmt.Sprintf("Sending transfer resume request to peer %s for transfer %s (last known chunk: %d)",
		peerID, resume.TransferID, resume.LastKnownChunk), "job_handler")

	// Create message
	msg := CreateJobDataTransferResume(resume)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal transfer resume request: %v", err), "job_handler")
		return err
	}

	// Try direct connection first, fallback to relay
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		return jmh.sendMessageViaRelay(peerID, msgBytes, "job_data_transfer_resume")
	}

	jmh.logger.Info(fmt.Sprintf("Sent transfer resume request to peer %s for transfer %s", peerID, resume.TransferID), "job_handler")
	return nil
}

// SendJobDataTransferStall sends a transfer stall notification to a peer
func (jmh *JobMessageHandler) SendJobDataTransferStall(peerID string, stall *types.JobDataTransferStall) error {
	jmh.logger.Warn(fmt.Sprintf("Sending transfer stall notification to peer %s for transfer %s: %s",
		peerID, stall.TransferID, stall.Reason), "job_handler")

	// Create message
	msg := CreateJobDataTransferStall(stall)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal transfer stall notification: %v", err), "job_handler")
		return err
	}

	// Try direct connection first, fallback to relay
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		return jmh.sendMessageViaRelay(peerID, msgBytes, "job_data_transfer_stall")
	}

	return nil
}

// SendJobCancel sends a job cancellation request to a peer
func (jmh *JobMessageHandler) SendJobCancel(peerID string, request *types.JobCancelRequest) (*types.JobCancelResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Sending job cancel request to peer %s for job %d", peerID, request.JobExecutionID), "job_handler")

	// Create message
	msg := CreateJobCancel(request)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job cancel request: %v", err), "job_handler")
		return nil, err
	}

	// Send via QUIC and wait for response
	responseBytes, err := jmh.quicPeer.SendMessageWithResponse(peerID, msgBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job cancel request: %v", err), "job_handler")
		return nil, err
	}

	// Parse response
	responseMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job cancel response: %v", err), "job_handler")
		return nil, err
	}

	var response types.JobCancelResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job cancel response data: %v", err), "job_handler")
		return nil, err
	}

	jmh.logger.Info(fmt.Sprintf("Received job cancel response from peer %s: cancelled=%v", peerID, response.Cancelled), "job_handler")
	return &response, nil
}

// sendDataTransferRequestViaRelay sends a data transfer request via relay
func (jmh *JobMessageHandler) sendStatusRequestViaRelay(peerID string, requestBytes []byte) (*types.JobStatusResponse, error) {
	jmh.logger.Debug(fmt.Sprintf("Sending status request to peer %s via relay", peerID[:8]), "job_handler")

	// Use the same relay mechanism as other requests
	responseBytes, err := jmh.sendMessageWithResponseViaRelay(peerID, requestBytes, "job_status_request")
	if err != nil {
		return nil, err
	}

	// Parse response
	responseMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse status response: %v", err)
	}

	var response types.JobStatusResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		return nil, fmt.Errorf("failed to parse status response data: %v", err)
	}

	jmh.logger.Debug(fmt.Sprintf("Received status response from peer %s via relay: status=%s", peerID[:8], response.Status), "job_handler")
	return &response, nil
}

func (jmh *JobMessageHandler) sendDataTransferRequestViaRelay(peerID string, requestBytes []byte) (*types.JobDataTransferResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Sending data transfer request to peer %s via relay", peerID[:8]), "job_handler")

	// Use the same relay mechanism as job requests
	responseBytes, err := jmh.sendMessageWithResponseViaRelay(peerID, requestBytes, "job_data_transfer_request")
	if err != nil {
		return nil, err
	}

	// Parse response
	responseMsg, err := UnmarshalQUICMessage(responseBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse data transfer response: %v", err)
	}

	var response types.JobDataTransferResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		return nil, fmt.Errorf("failed to parse data transfer response data: %v", err)
	}

	jmh.logger.Info(fmt.Sprintf("Received data transfer response from peer %s via relay: accepted=%v", peerID[:8], response.Accepted), "job_handler")
	return &response, nil
}

// getRelayInfo retrieves relay connection info for a peer with caching
// Cache expires after 1 minute to quickly detect relay/session changes
func (jmh *JobMessageHandler) getRelayInfo(peerID string) (relayAddress string, relaySessionID string, err error) {
	// Check cache first
	jmh.relayCacheMu.RLock()
	cached, exists := jmh.relayCache[peerID]
	jmh.relayCacheMu.RUnlock()

	// Use cached data if it exists and is fresh (< 1 minute old)
	if exists && time.Since(cached.CachedAt) < 1*time.Minute {
		jmh.logger.Debug(fmt.Sprintf("Using cached relay info for peer %s (age: %v)", peerID[:8], time.Since(cached.CachedAt)), "job_handler")
		return cached.RelayAddress, cached.RelaySessionID, nil
	}

	// Cache miss or expired - query metadata
	jmh.logger.Debug(fmt.Sprintf("Querying relay metadata for peer %s (cache miss or expired)", peerID[:8]), "job_handler")

	// Get peer from database
	peer, err := jmh.dbManager.KnownPeers.GetKnownPeer(peerID, "remote-network-mesh")
	if err != nil || peer == nil || len(peer.PublicKey) == 0 {
		return "", "", fmt.Errorf("peer %s not found or has no public key", peerID[:8])
	}

	// Query DHT for metadata
	metadata, err := jmh.metadataQuery.QueryMetadata(peerID, peer.PublicKey)
	if err != nil {
		// Invalidate cache on error
		jmh.relayCacheMu.Lock()
		delete(jmh.relayCache, peerID)
		jmh.relayCacheMu.Unlock()
		return "", "", fmt.Errorf("failed to query peer metadata: %v", err)
	}

	// Validate relay info
	if !metadata.NetworkInfo.UsingRelay || metadata.NetworkInfo.RelayAddress == "" || metadata.NetworkInfo.RelaySessionID == "" {
		// Invalidate cache if peer is not using relay
		jmh.relayCacheMu.Lock()
		delete(jmh.relayCache, peerID)
		jmh.relayCacheMu.Unlock()
		return "", "", fmt.Errorf("peer not accessible via relay")
	}

	// Update cache
	jmh.relayCacheMu.Lock()
	jmh.relayCache[peerID] = &RelayCache{
		RelayAddress:   metadata.NetworkInfo.RelayAddress,
		RelaySessionID: metadata.NetworkInfo.RelaySessionID,
		CachedAt:       time.Now(),
	}
	jmh.relayCacheMu.Unlock()

	jmh.logger.Debug(fmt.Sprintf("Cached relay info for peer %s: relay=%s, session=%s", peerID[:8], metadata.NetworkInfo.RelayAddress, metadata.NetworkInfo.RelaySessionID[:8]), "job_handler")

	return metadata.NetworkInfo.RelayAddress, metadata.NetworkInfo.RelaySessionID, nil
}

// invalidateRelayCache invalidates cached relay info for a peer
// This should be called when relay communication fails to force a fresh metadata query
func (jmh *JobMessageHandler) invalidateRelayCache(peerID string) {
	jmh.relayCacheMu.Lock()
	defer jmh.relayCacheMu.Unlock()

	if _, exists := jmh.relayCache[peerID]; exists {
		delete(jmh.relayCache, peerID)
		jmh.logger.Debug(fmt.Sprintf("Invalidated relay cache for peer %s", peerID[:8]), "job_handler")
	}
}

// sendMessageViaRelay sends a one-way message via relay (no response expected)
func (jmh *JobMessageHandler) sendMessageViaRelay(peerID string, messageBytes []byte, messageType string) error {
	jmh.logger.Debug(fmt.Sprintf("Sending %s to peer %s via relay", messageType, peerID[:8]), "job_handler")

	// Check dependencies
	if jmh.dbManager == nil || jmh.metadataQuery == nil || jmh.ourPeerID == "" {
		return fmt.Errorf("relay dependencies not set")
	}

	// Get relay info with caching (avoids slow DHT queries for every chunk)
	relayAddress, relaySessionID, err := jmh.getRelayInfo(peerID)
	if err != nil {
		return err
	}

	// Wrap message in relay forward
	forwardMsg := NewQUICMessage(MessageTypeRelayForward, &RelayForwardData{
		SessionID:    relaySessionID,
		SourcePeerID: jmh.ourPeerID,
		TargetPeerID: peerID,
		MessageType:  messageType,
		Payload:      messageBytes,
		PayloadSize:  int64(len(messageBytes)),
	})
	forwardMsgBytes, err := forwardMsg.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal relay forward: %v", err)
	}

	// DEBUG: Log relay forward message size
	jmh.logger.Info(fmt.Sprintf("DEBUG: Relay forward message size: %d bytes (original payload: %d bytes, type: %s)", len(forwardMsgBytes), len(messageBytes), messageType), "job_handler")

	// Connect to relay
	conn, err := jmh.quicPeer.ConnectToPeer(relayAddress)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %v", err)
	}

	// Open stream
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		// Stream open failure often indicates a stale connection (connection exists locally
		// but remote side has closed it). Clean it up to force fresh connection on next attempt.
		relayAddr := conn.RemoteAddr().String()
		jmh.logger.Debug(fmt.Sprintf("Cleaning up potentially stale relay connection %s after stream open failure", relayAddr), "job_handler")

		// Clean up stale connection to force fresh reconnection on retry
		if cleanupErr := jmh.quicPeer.DisconnectFromPeer(relayAddr); cleanupErr != nil {
			jmh.logger.Debug(fmt.Sprintf("Relay connection cleanup completed for %s (connection may have already been closed)", relayAddr), "job_handler")
		}

		return fmt.Errorf("failed to open stream to relay: %v", err)
	}
	defer stream.Close()

	// Send message (no response expected for one-way messages)
	if _, err := stream.Write(forwardMsgBytes); err != nil {
		return fmt.Errorf("failed to send via relay: %v", err)
	}

	jmh.logger.Debug(fmt.Sprintf("Sent %s to peer %s via relay successfully", messageType, peerID[:8]), "job_handler")
	return nil
}

// sendMessageWithResponseViaRelay sends a message via relay and waits for response
// Automatically retries once with fresh relay metadata if the first attempt fails with timeout
func (jmh *JobMessageHandler) sendMessageWithResponseViaRelay(peerID string, messageBytes []byte, messageType string) ([]byte, error) {
	jmh.logger.Info(fmt.Sprintf("Sending %s to peer %s via relay", messageType, peerID[:8]), "job_handler")

	// Check dependencies
	if jmh.dbManager == nil || jmh.metadataQuery == nil || jmh.ourPeerID == "" {
		return nil, fmt.Errorf("relay dependencies not set")
	}

	// Try sending with retry logic
	response, err := jmh.trySendWithRetry(peerID, messageBytes, messageType)
	return response, err
}

// trySendWithRetry attempts to send a message via relay with one retry on cache-related failures
func (jmh *JobMessageHandler) trySendWithRetry(peerID string, messageBytes []byte, messageType string) ([]byte, error) {
	// First attempt
	response, err := jmh.doSendMessageWithResponse(peerID, messageBytes, messageType)
	if err == nil {
		return response, nil
	}

	// Check if error is related to stale relay info (deadline exceeded or connection issues)
	if isRelayCacheError(err) {
		jmh.logger.Info(fmt.Sprintf("Relay communication failed, invalidating cache and retrying for peer %s: %v", peerID[:8], err), "job_handler")

		// Invalidate cache to force fresh metadata query
		jmh.invalidateRelayCache(peerID)

		// Retry once with fresh relay info
		response, retryErr := jmh.doSendMessageWithResponse(peerID, messageBytes, messageType)
		if retryErr == nil {
			jmh.logger.Info(fmt.Sprintf("Retry successful for peer %s after cache invalidation", peerID[:8]), "job_handler")
			return response, nil
		}

		// Both attempts failed
		return nil, fmt.Errorf("failed after retry: %v", retryErr)
	}

	// Non-cache-related error, don't retry
	return nil, err
}

// isRelayCacheError determines if an error is likely due to stale relay cache
func isRelayCacheError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "deadline exceeded") ||
	       strings.Contains(errStr, "no data received") ||
	       strings.Contains(errStr, "connection reset") ||
	       strings.Contains(errStr, "Application error")
}

// doSendMessageWithResponse performs the actual relay forward without retry logic
func (jmh *JobMessageHandler) doSendMessageWithResponse(peerID string, messageBytes []byte, messageType string) ([]byte, error) {
	// Get relay info with caching (avoids slow DHT queries)
	relayAddress, relaySessionID, err := jmh.getRelayInfo(peerID)
	if err != nil {
		return nil, err
	}

	// Wrap message in relay forward
	forwardMsg := NewQUICMessage(MessageTypeRelayForward, &RelayForwardData{
		SessionID:    relaySessionID,
		SourcePeerID: jmh.ourPeerID,
		TargetPeerID: peerID,
		MessageType:  messageType,
		Payload:      messageBytes,
		PayloadSize:  int64(len(messageBytes)),
	})
	forwardMsgBytes, err := forwardMsg.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal relay forward: %v", err)
	}

	// Connect to relay
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := jmh.quicPeer.ConnectToPeer(relayAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to relay: %v", err)
	}

	// Open stream
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		// Stream open failure often indicates a stale connection (connection exists locally
		// but remote side has closed it). Clean it up to force fresh connection on next attempt.
		relayAddr := conn.RemoteAddr().String()
		jmh.logger.Debug(fmt.Sprintf("Cleaning up potentially stale relay connection %s after stream open failure", relayAddr), "job_handler")

		// Clean up stale connection to force fresh reconnection on retry
		if cleanupErr := jmh.quicPeer.DisconnectFromPeer(relayAddr); cleanupErr != nil {
			jmh.logger.Debug(fmt.Sprintf("Relay connection cleanup completed for %s (connection may have already been closed)", relayAddr), "job_handler")
		}

		return nil, fmt.Errorf("failed to open stream to relay: %v", err)
	}
	defer stream.Close()

	// Send message
	if _, err := stream.Write(forwardMsgBytes); err != nil {
		return nil, fmt.Errorf("failed to send via relay: %v", err)
	}

	// Read response
	stream.SetReadDeadline(time.Now().Add(30 * time.Second))
	buffer := make([]byte, 1024*1024)
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read relay response: %v", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("no data received from relay")
	}

	// Parse response - might be wrapped
	responseMsg, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to parse relay response: %v", err)
	}

	// Unwrap if needed
	if responseMsg.Type == MessageTypeRelayForward {
		var forwardData RelayForwardData
		if err := responseMsg.GetDataAs(&forwardData); err != nil {
			return nil, fmt.Errorf("failed to parse relay forward data: %v", err)
		}
		return forwardData.Payload, nil
	}

	// Return raw message bytes
	return buffer[:n], nil
}

// sendJobRequestViaRelay sends a job execution request via relay forwarding
// Automatically retries once with fresh relay metadata if the first attempt fails with timeout
func (jmh *JobMessageHandler) sendJobRequestViaRelay(peerID string, jobRequestBytes []byte) (*types.JobExecutionResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Attempting to send job request to peer %s via relay", peerID[:8]), "job_handler")

	// Check if dependencies are available
	if jmh.dbManager == nil || jmh.metadataQuery == nil || jmh.ourPeerID == "" {
		return nil, fmt.Errorf("relay dependencies not set - call SetDependencies first")
	}

	// Try sending with retry logic
	response, err := jmh.trySendJobRequestWithRetry(peerID, jobRequestBytes)
	return response, err
}

// trySendJobRequestWithRetry attempts to send a job request with one retry on cache-related failures
func (jmh *JobMessageHandler) trySendJobRequestWithRetry(peerID string, jobRequestBytes []byte) (*types.JobExecutionResponse, error) {
	// First attempt
	response, err := jmh.doSendJobRequest(peerID, jobRequestBytes)
	if err == nil {
		return response, nil
	}

	// Check if error is related to stale relay info
	if isRelayCacheError(err) {
		jmh.logger.Info(fmt.Sprintf("Job request failed, invalidating cache and retrying for peer %s: %v", peerID[:8], err), "job_handler")

		// Invalidate cache to force fresh metadata query
		jmh.invalidateRelayCache(peerID)

		// Retry once with fresh relay info
		response, retryErr := jmh.doSendJobRequest(peerID, jobRequestBytes)
		if retryErr == nil {
			jmh.logger.Info(fmt.Sprintf("Job request retry successful for peer %s after cache invalidation", peerID[:8]), "job_handler")
			return response, nil
		}

		// Both attempts failed
		return nil, fmt.Errorf("failed after retry: %v", retryErr)
	}

	// Non-cache-related error, don't retry
	return nil, err
}

// doSendJobRequest performs the actual job request send without retry logic
func (jmh *JobMessageHandler) doSendJobRequest(peerID string, jobRequestBytes []byte) (*types.JobExecutionResponse, error) {
	// Get relay info with caching (avoids slow DHT queries)
	relayAddress, relaySessionID, err := jmh.getRelayInfo(peerID)
	if err != nil {
		return nil, err
	}

	jmh.logger.Info(fmt.Sprintf("Sending job request to peer %s via relay %s (session: %s)",
		peerID[:8], relayAddress, relaySessionID[:8]), "job_handler")

	// Create relay forward message wrapping the job request
	forwardMsg := NewQUICMessage(MessageTypeRelayForward, &RelayForwardData{
		SessionID:    relaySessionID,
		SourcePeerID: jmh.ourPeerID,
		TargetPeerID: peerID,
		MessageType:  "job_request",
		Payload:      jobRequestBytes,
		PayloadSize:  int64(len(jobRequestBytes)),
	})
	forwardMsgBytes, err := forwardMsg.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal relay forward: %v", err)
	}

	// Connect to relay
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := jmh.quicPeer.ConnectToPeer(relayAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to relay: %v", err)
	}

	// Open stream
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		// Stream open failure often indicates a stale connection (connection exists locally
		// but remote side has closed it). Clean it up to force fresh connection on next attempt.
		relayAddr := conn.RemoteAddr().String()
		jmh.logger.Debug(fmt.Sprintf("Cleaning up potentially stale relay connection %s after stream open failure", relayAddr), "job_handler")

		// Clean up stale connection to force fresh reconnection on retry
		if cleanupErr := jmh.quicPeer.DisconnectFromPeer(relayAddr); cleanupErr != nil {
			jmh.logger.Debug(fmt.Sprintf("Relay connection cleanup completed for %s (connection may have already been closed)", relayAddr), "job_handler")
		}

		return nil, fmt.Errorf("failed to open stream to relay: %v", err)
	}
	defer stream.Close()

	// Send relay forward request
	if _, err := stream.Write(forwardMsgBytes); err != nil {
		return nil, fmt.Errorf("failed to send relay forward: %v", err)
	}

	// Read response with deadline
	stream.SetReadDeadline(time.Now().Add(30 * time.Second))
	buffer := make([]byte, 1024*1024) // 1MB buffer
	n, err := stream.Read(buffer)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read relay response: %v", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("no data received from relay")
	}

	// Parse response - might be wrapped in RELAY_FORWARD or direct JOB_RESPONSE
	responseMsg, err := UnmarshalQUICMessage(buffer[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to parse relay response: %v", err)
	}

	// Handle different response types
	if responseMsg.Type == MessageTypeRelayForward {
		// Unwrap relay forward message
		var forwardData RelayForwardData
		if err := responseMsg.GetDataAs(&forwardData); err != nil {
			return nil, fmt.Errorf("failed to parse relay forward data: %v", err)
		}

		// Parse the wrapped job response
		innerMsg, err := UnmarshalQUICMessage(forwardData.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to parse wrapped job response: %v", err)
		}
		responseMsg = innerMsg
	}

	// Parse job execution response
	var response types.JobExecutionResponse
	if err := responseMsg.GetDataAs(&response); err != nil {
		return nil, fmt.Errorf("failed to parse job response data: %v", err)
	}

	jmh.logger.Info(fmt.Sprintf("Received job response from peer %s via relay: accepted=%v", peerID[:8], response.Accepted), "job_handler")
	return &response, nil
}

// Close closes the job message handler
func (jmh *JobMessageHandler) Close() {
	jmh.logger.Info("Closing job message handler", "job_handler")
	jmh.logger.Close()
}
