package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// handleListConversations lists chat conversations
func (s *APIServer) handleListConversations(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	conversations, err := s.chatManager.ListConversations(limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"conversations": conversations,
		"limit":         limit,
		"offset":        offset,
	})
}

// handleCreateConversation creates or gets a 1-on-1 conversation
func (s *APIServer) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID string `json:"peer_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.PeerID == "" {
		http.Error(w, "peer_id is required", http.StatusBadRequest)
		return
	}

	conversationID, err := s.chatManager.CreateOrGetConversation(req.PeerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the created/existing conversation
	conversation, err := s.chatManager.GetConversation(conversationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(conversation)
}

// handleGetConversation retrieves a specific conversation
func (s *APIServer) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimPrefix(r.URL.Path, "/api/chat/conversations/")

	conversation, err := s.chatManager.GetConversation(conversationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if conversation == nil {
		http.Error(w, "Conversation not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(conversation)
}

// handleDeleteConversation deletes a conversation
func (s *APIServer) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimPrefix(r.URL.Path, "/api/chat/conversations/")

	if err := s.chatManager.DeleteConversation(conversationID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Conversation deleted successfully",
	})
}

// handleListMessages lists messages in a conversation
func (s *APIServer) handleListMessages(w http.ResponseWriter, r *http.Request) {
	// Extract conversation ID from path: /api/chat/conversations/:id/messages
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/conversations/")
	conversationID := strings.TrimSuffix(path, "/messages")

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	messages, err := s.chatManager.ListMessages(conversationID, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"messages": messages,
		"limit":    limit,
		"offset":   offset,
	})
}

// handleSendMessage sends a message in a conversation
func (s *APIServer) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	// Extract conversation ID from path: /api/chat/conversations/:id/messages
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/conversations/")
	conversationID := strings.TrimSuffix(path, "/messages")

	var req struct {
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	messageID, err := s.chatManager.SendMessage(conversationID, req.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the sent message from database
	message, err := s.dbManager.GetMessage(messageID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message_id": messageID,
		"message":    message,
	})
}

// handleMarkMessageRead marks a message as read
func (s *APIServer) handleMarkMessageRead(w http.ResponseWriter, r *http.Request) {
	// Extract message ID from path: /api/chat/messages/:id/read
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/messages/")
	messageID := strings.TrimSuffix(path, "/read")

	if err := s.chatManager.MarkMessageAsRead(messageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Message marked as read",
	})
}

// handleMarkConversationRead marks all messages in a conversation as read
func (s *APIServer) handleMarkConversationRead(w http.ResponseWriter, r *http.Request) {
	// Extract conversation ID from path: /api/chat/conversations/:id/read
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/conversations/")
	conversationID := strings.TrimSuffix(path, "/read")

	if err := s.chatManager.MarkConversationAsRead(conversationID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Conversation marked as read",
	})
}

// handleCreateGroup creates a new group chat
func (s *APIServer) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupName string   `json:"group_name"`
		Members   []string `json:"members"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.GroupName == "" {
		http.Error(w, "group_name is required", http.StatusBadRequest)
		return
	}

	if len(req.Members) < 2 {
		http.Error(w, "at least 2 members required", http.StatusBadRequest)
		return
	}

	groupID, err := s.chatManager.CreateGroup(req.GroupName, req.Members)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the created group
	group, err := s.chatManager.GetConversation(groupID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(group)
}

// handleSendGroupMessage sends a message to a group
func (s *APIServer) handleSendGroupMessage(w http.ResponseWriter, r *http.Request) {
	// Extract group ID from path: /api/chat/groups/:id/messages
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/groups/")
	groupID := strings.TrimSuffix(path, "/messages")

	var req struct {
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	messageID, err := s.chatManager.SendGroupMessage(groupID, req.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message_id": messageID,
	})
}

// handleInitiateKeyExchange manually initiates key exchange with a peer
func (s *APIServer) handleInitiateKeyExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID string `json:"peer_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.PeerID == "" {
		http.Error(w, "peer_id is required", http.StatusBadRequest)
		return
	}

	if err := s.chatManager.InitiateKeyExchange(req.PeerID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Key exchange initiated",
	})
}

// handleGetUnreadCount returns total unread message count
func (s *APIServer) handleGetUnreadCount(w http.ResponseWriter, r *http.Request) {
	count, err := s.chatManager.GetUnreadCount()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{
		"unread_count": count,
	})
}

// handleInviteToGroup invites a peer to a group
func (s *APIServer) handleInviteToGroup(w http.ResponseWriter, r *http.Request) {
	// Extract group ID from path: /api/chat/groups/:id/invite
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/groups/")
	groupID := strings.TrimSuffix(path, "/invite")

	var req struct {
		PeerID string `json:"peer_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.PeerID == "" {
		http.Error(w, "peer_id is required", http.StatusBadRequest)
		return
	}

	if err := s.chatManager.InviteToGroup(groupID, req.PeerID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Peer invited to group successfully",
	})
}

// handleLeaveGroup removes the current peer from a group
func (s *APIServer) handleLeaveGroup(w http.ResponseWriter, r *http.Request) {
	// Extract group ID from path: /api/chat/groups/:id/leave
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/groups/")
	groupID := strings.TrimSuffix(path, "/leave")

	if err := s.chatManager.LeaveGroup(groupID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Left group successfully",
	})
}

// handleGetGroupMembers returns members of a group
func (s *APIServer) handleGetGroupMembers(w http.ResponseWriter, r *http.Request) {
	// Extract group ID from path: /api/chat/groups/:id/members
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/groups/")
	groupID := strings.TrimSuffix(path, "/members")

	members, err := s.chatManager.GetGroupMembers(groupID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"members": members,
	})
}
