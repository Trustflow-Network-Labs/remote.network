package p2p

import (
	"fmt"

	"github.com/Trustflow-Network-Labs/remote-network-node/internal/types"
	"github.com/Trustflow-Network-Labs/remote-network-node/internal/utils"
	"github.com/quic-go/quic-go"
)

// JobMessageHandler handles job-related messages over QUIC
type JobMessageHandler struct {
	logger        *utils.LogsManager
	cm            *utils.ConfigManager
	quicPeer      *QUICPeer
	// Callbacks for job operations
	onJobRequest              func(*types.JobExecutionRequest, string) (*types.JobExecutionResponse, error)
	onJobStatusUpdate         func(*types.JobStatusUpdate, string) error
	onJobDataTransferRequest  func(*types.JobDataTransferRequest, string) (*types.JobDataTransferResponse, error)
	onJobDataChunk            func(*types.JobDataChunk, string) error
	onJobDataTransferComplete func(*types.JobDataTransferComplete, string) error
	onJobCancel               func(*types.JobCancelRequest, string) (*types.JobCancelResponse, error)
}

// NewJobMessageHandler creates a new job message handler
func NewJobMessageHandler(cm *utils.ConfigManager, quicPeer *QUICPeer) *JobMessageHandler {
	return &JobMessageHandler{
		logger:   utils.NewLogsManager(cm),
		cm:       cm,
		quicPeer: quicPeer,
	}
}

// SetCallbacks sets the callback functions for job operations
func (jmh *JobMessageHandler) SetCallbacks(
	onJobRequest func(*types.JobExecutionRequest, string) (*types.JobExecutionResponse, error),
	onJobStatusUpdate func(*types.JobStatusUpdate, string) error,
	onJobDataTransferRequest func(*types.JobDataTransferRequest, string) (*types.JobDataTransferResponse, error),
	onJobDataChunk func(*types.JobDataChunk, string) error,
	onJobDataTransferComplete func(*types.JobDataTransferComplete, string) error,
	onJobCancel func(*types.JobCancelRequest, string) (*types.JobCancelResponse, error),
) {
	jmh.onJobRequest = onJobRequest
	jmh.onJobStatusUpdate = onJobStatusUpdate
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

	case MessageTypeJobStatusUpdate:
		return jmh.handleJobStatusUpdate(msg, stream, peerID)

	case MessageTypeJobDataTransferRequest:
		return jmh.handleJobDataTransferRequest(msg, stream, peerID)

	case MessageTypeJobDataTransferResponse:
		return jmh.handleJobDataTransferResponse(msg, stream, peerID)

	case MessageTypeJobDataChunk:
		return jmh.handleJobDataChunk(msg, stream, peerID)

	case MessageTypeJobDataTransferComplete:
		return jmh.handleJobDataTransferComplete(msg, stream, peerID)

	case MessageTypeJobCancel:
		return jmh.handleJobCancel(msg, stream, peerID)

	default:
		return fmt.Errorf("unknown job message type: %s", msg.Type)
	}
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

	jmh.logger.Info(fmt.Sprintf("Received job data transfer request for job %d from peer %s", request.JobExecutionID, peerID), "job_handler")

	// Call the callback
	response, err := jmh.onJobDataTransferRequest(&request, peerID)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Job data transfer request callback failed: %v", err), "job_handler")
		// Send error response
		response = &types.JobDataTransferResponse{
			JobExecutionID: request.JobExecutionID,
			Accepted:       false,
			Message:        fmt.Sprintf("Data transfer request failed: %v", err),
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

	jmh.logger.Info(fmt.Sprintf("Sent job data transfer response for job %d: accepted=%v", request.JobExecutionID, response.Accepted), "job_handler")
	return nil
}

// handleJobDataTransferResponse handles incoming data transfer responses
func (jmh *JobMessageHandler) handleJobDataTransferResponse(msg *QUICMessage, _ *quic.Stream, peerID string) error {
	var response types.JobDataTransferResponse
	if err := msg.GetDataAs(&response); err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to parse job data transfer response: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Received job data transfer response for job %d from peer %s: accepted=%v", response.JobExecutionID, peerID, response.Accepted), "job_handler")
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

// SendJobRequest sends a job execution request to a peer
func (jmh *JobMessageHandler) SendJobRequest(peerID string, request *types.JobExecutionRequest) (*types.JobExecutionResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Sending job request to peer %s for workflow %d", peerID, request.WorkflowID), "job_handler")

	// Create message
	msg := CreateJobRequest(request)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job request: %v", err), "job_handler")
		return nil, err
	}

	// Send via QUIC
	responseBytes, err := jmh.quicPeer.SendMessageWithResponse(peerID, msgBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job request: %v", err), "job_handler")
		return nil, err
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

	// Send via QUIC (no response expected)
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job status update: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Sent job status update to peer %s for job %d", peerID, update.JobExecutionID), "job_handler")
	return nil
}

// SendJobDataTransferRequest sends a data transfer request to a peer
func (jmh *JobMessageHandler) SendJobDataTransferRequest(peerID string, request *types.JobDataTransferRequest) (*types.JobDataTransferResponse, error) {
	jmh.logger.Info(fmt.Sprintf("Sending job data transfer request to peer %s for job %d", peerID, request.JobExecutionID), "job_handler")

	// Create message
	msg := CreateJobDataTransferRequest(request)
	msgBytes, err := msg.Marshal()
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to marshal job data transfer request: %v", err), "job_handler")
		return nil, err
	}

	// Send via QUIC and wait for response
	responseBytes, err := jmh.quicPeer.SendMessageWithResponse(peerID, msgBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job data transfer request: %v", err), "job_handler")
		return nil, err
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

	// Send via QUIC (no response expected for individual chunks)
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job data chunk: %v", err), "job_handler")
		return err
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
	err = jmh.quicPeer.SendMessageToPeer(peerID, msgBytes)
	if err != nil {
		jmh.logger.Error(fmt.Sprintf("Failed to send job data transfer complete: %v", err), "job_handler")
		return err
	}

	jmh.logger.Info(fmt.Sprintf("Sent job data transfer complete to peer %s for transfer %s", peerID, complete.TransferID), "job_handler")
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

// Close closes the job message handler
func (jmh *JobMessageHandler) Close() {
	jmh.logger.Info("Closing job message handler", "job_handler")
	jmh.logger.Close()
}
