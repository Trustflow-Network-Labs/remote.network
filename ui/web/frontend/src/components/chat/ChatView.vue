<template>
  <AppLayout>
    <div class="chat-view">
      <!-- Header -->
      <div class="chat-header">
        <h2>Messages</h2>
        <div class="actions">
          <Button
            label="New Chat"
            icon="pi pi-plus"
            @click="showNewChatDialog = true"
            severity="success"
          />
          <Button
            label="New Group"
            icon="pi pi-users"
            @click="showGroupDialog = true"
            severity="info"
          />
          <Button
            label="Refresh"
            icon="pi pi-refresh"
            @click="refreshConversations"
            :loading="chatStore.loading"
          />
        </div>
      </div>

      <div class="chat-container">
        <!-- Conversations List (Grouped by peer) -->
        <ConversationList
          :grouped-conversations="chatStore.groupedConversations"
          :active-peer-id="chatStore.activePeerID"
          :total-unread="chatStore.totalUnreadCount"
          @select-peer="selectPeer"
          @delete-peer="handleDeletePeer"
        />

        <!-- Messages Area -->
        <div class="messages-area">
          <div v-if="!chatStore.activePeerID" class="empty-state">
            <i class="pi pi-comments"></i>
            <h3>Select a conversation</h3>
            <p>Choose a conversation from the list or start a new chat</p>
          </div>

          <div v-else class="conversation-view">
            <!-- Conversation Header -->
            <div class="conversation-header">
              <div
                class="conversation-info"
                :class="{ clickable: isGroupConversation }"
                @click="isGroupConversation && (showGroupMembersDialog = true)"
              >
                <h3>
                  {{ conversationTitle }}
                  <i v-if="isGroupConversation" class="pi pi-chevron-right group-chevron"></i>
                </h3>
                <p class="conversation-subtitle">
                  {{ conversationSubtitle }}
                  <span v-if="isGroupConversation" class="view-members-hint">(click to view members)</span>
                </p>
              </div>
              <div class="conversation-actions">
                <Button
                  icon="pi pi-trash"
                  severity="danger"
                  text
                  @click="confirmDelete = true"
                  v-tooltip.left="'Delete Conversation'"
                />
              </div>
            </div>

            <!-- Messages -->
            <MessageList
              :messages="chatStore.activeMessages"
              :local-peer-id="nodeStore.peerId || ''"
              :loading="loadingMessages"
              :is-group="chatStore.activeConversation?.conversation_type === 'group'"
            />

            <!-- Message Input -->
            <MessageInput
              @send="handleSendMessage"
            />
          </div>
        </div>
      </div>

      <!-- New Chat Dialog -->
      <Dialog
        v-model:visible="showNewChatDialog"
        header="New Chat"
        :modal="true"
        :style="{ width: '450px' }"
      >
        <div class="new-chat-form">
          <div class="form-field">
            <label for="peer-id">Peer ID</label>
            <InputText
              id="peer-id"
              v-model="newChatPeerID"
              placeholder="Enter peer ID..."
              class="w-full"
            />
          </div>
          <p class="hint">Enter the peer ID of the person you want to chat with.</p>
        </div>

        <template #footer>
          <Button
            label="Cancel"
            severity="secondary"
            @click="showNewChatDialog = false"
          />
          <Button
            label="Start Chat"
            icon="pi pi-comments"
            @click="createNewChat"
            :disabled="!newChatPeerID"
          />
        </template>
      </Dialog>

      <!-- Delete Confirmation -->
      <Dialog
        v-model:visible="confirmDelete"
        header="Delete Conversation"
        :modal="true"
        :style="{ width: '400px' }"
      >
        <p>Are you sure you want to delete this conversation? All messages will be permanently deleted.</p>

        <template #footer>
          <Button
            label="Cancel"
            severity="secondary"
            @click="confirmDelete = false"
          />
          <Button
            label="Delete"
            severity="danger"
            icon="pi pi-trash"
            @click="deleteActiveConversation"
          />
        </template>
      </Dialog>

      <!-- Group Create Dialog -->
      <GroupCreateDialog
        v-model:visible="showGroupDialog"
        @created="handleGroupCreated"
      />

      <!-- Group Members Dialog -->
      <GroupMembersDialog
        v-model:visible="showGroupMembersDialog"
        :group-id="activeGroupId"
        :group-name="chatStore.activeConversation?.group_name"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useChatStore } from '../../stores/chat'
import { useNodeStore } from '../../stores/node'
import { useToast } from 'primevue/usetoast'
import AppLayout from '../layout/AppLayout.vue'
import ConversationList from './ConversationList.vue'
import MessageList from './MessageList.vue'
import MessageInput from './MessageInput.vue'
import GroupCreateDialog from './GroupCreateDialog.vue'
import GroupMembersDialog from './GroupMembersDialog.vue'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'

const chatStore = useChatStore()
const nodeStore = useNodeStore()
const toast = useToast()

const showNewChatDialog = ref(false)
const showGroupDialog = ref(false)
const showGroupMembersDialog = ref(false)
const newChatPeerID = ref('')
const confirmDelete = ref(false)
const loadingMessages = ref(false)

const conversationTitle = computed(() => {
  const conv = chatStore.activeConversation
  if (!conv) return ''

  if (conv.conversation_type === 'group') {
    return conv.group_name || 'Group Chat'
  } else {
    return conv.peer_id ? shortenId(conv.peer_id) : 'Chat'
  }
})

const conversationSubtitle = computed(() => {
  const conv = chatStore.activeConversation
  if (!conv) return ''

  if (conv.conversation_type === 'group') {
    return 'Group conversation'
  } else {
    return `1-on-1 with ${conv.peer_id ? shortenId(conv.peer_id) : 'peer'}`
  }
})

const isGroupConversation = computed(() => {
  return chatStore.activeConversation?.conversation_type === 'group'
})

const activeGroupId = computed(() => {
  const conv = chatStore.activeConversation
  if (!conv || conv.conversation_type !== 'group') return ''
  // For groups, the conversation_id is the group ID
  return conv.conversation_id || ''
})

function shortenId(id: string): string {
  if (!id) return ''
  return id.length > 16 ? `${id.slice(0, 8)}...${id.slice(-8)}` : id
}

async function refreshConversations() {
  try {
    await chatStore.fetchConversations()
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: 'Error',
      detail: error.message || 'Failed to refresh conversations',
      life: 3000
    })
  }
}

async function selectConversation(conversationID: string) {
  chatStore.setActiveConversation(conversationID)

  // Load messages for this conversation
  loadingMessages.value = true
  try {
    await chatStore.fetchMessages(conversationID)
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: 'Error',
      detail: error.message || 'Failed to load messages',
      life: 3000
    })
  } finally {
    loadingMessages.value = false
  }
}

async function selectPeer(peerID: string) {
  // Load all messages for this peer
  loadingMessages.value = true
  try {
    await chatStore.setActivePeer(peerID)
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: 'Error',
      detail: error.message || 'Failed to load messages',
      life: 3000
    })
  } finally {
    loadingMessages.value = false
  }
}

async function handleDeletePeer(peerID: string) {
  // Delete all conversations with this peer
  const convs = chatStore.conversations.filter(c =>
    (c.conversation_type === '1on1' && c.peer_id === peerID) ||
    (c.conversation_type === 'group' && c.conversation_id === peerID)
  )

  try {
    for (const conv of convs) {
      await chatStore.deleteConversation(conv.conversation_id)
    }
    toast.add({
      severity: 'success',
      summary: 'Deleted',
      detail: 'Conversation deleted',
      life: 3000
    })
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: 'Error',
      detail: error.message || 'Failed to delete conversation',
      life: 3000
    })
  }
}

async function createNewChat() {
  if (!newChatPeerID.value) return

  try {
    await chatStore.createConversation(newChatPeerID.value)
    showNewChatDialog.value = false
    const peerID = newChatPeerID.value
    newChatPeerID.value = ''

    // Select the peer (which will load all conversations with this peer)
    await selectPeer(peerID)

    toast.add({
      severity: 'success',
      summary: 'Chat Created',
      detail: 'New conversation started',
      life: 3000
    })
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: 'Error',
      detail: error.message || 'Failed to create conversation',
      life: 3000
    })
  }
}

function handleSendMessage(content: string) {
  if (!chatStore.activeConversationID || !content.trim()) return

  // Fire-and-forget: message appears immediately with 'created' status
  // Status updates will come through WebSocket or when API responds
  chatStore.sendMessage(chatStore.activeConversationID, content)
    .catch((error: any) => {
      // Error is already handled in store (message status set to 'failed')
      // Show toast notification for user feedback
      toast.add({
        severity: 'error',
        summary: 'Message Failed',
        detail: error.message || 'Failed to send message',
        life: 5000
      })
    })
}

async function handleDeleteConversation(conversationID: string) {
  try {
    await chatStore.deleteConversation(conversationID)
    toast.add({
      severity: 'success',
      summary: 'Deleted',
      detail: 'Conversation deleted',
      life: 3000
    })
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: 'Error',
      detail: error.message || 'Failed to delete conversation',
      life: 3000
    })
  }
}

async function deleteActiveConversation() {
  if (!chatStore.activeConversationID) return

  await handleDeleteConversation(chatStore.activeConversationID)
  confirmDelete.value = false
}

async function handleGroupCreated(groupId: string) {
  toast.add({
    severity: 'success',
    summary: 'Group Created',
    detail: 'New group conversation created',
    life: 3000
  })

  // Refresh conversations and select the new group
  await refreshConversations()
  await selectConversation(groupId)
}

onMounted(async () => {
  // Ensure node status is loaded first (needed for localPeerId to determine sent vs received)
  if (!nodeStore.peerId) {
    await nodeStore.fetchNodeStatus()
  }

  // Fetch conversations
  await refreshConversations()

  // Subscribe to WebSocket updates
  chatStore.subscribeToUpdates()

  // Fetch unread count
  await chatStore.fetchUnreadCount()

  // Restore previously active peer (if any)
  const restoredPeerID = chatStore.restoreActivePeer()
  if (restoredPeerID) {
    // Load messages for restored peer
    loadingMessages.value = true
    try {
      await chatStore.fetchMessagesForPeer(restoredPeerID)
    } catch (error: any) {
      console.error('Failed to load messages for restored peer:', error)
      // Clear the invalid peer
      chatStore.setActivePeer(null)
    } finally {
      loadingMessages.value = false
    }
  }
})

onUnmounted(() => {
  chatStore.unsubscribeFromUpdates()
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.chat-view {
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
}

.chat-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1.5rem;
  border-bottom: 1px solid vars.$color-border;
  background: vars.$color-surface;

  h2 {
    margin: 0;
    font-size: 1.5rem;
    color: vars.$color-text-primary;
  }

  .actions {
    display: flex;
    gap: 0.5rem;
  }
}

.chat-container {
  display: flex;
  flex: 1;
  overflow: hidden;
}

.messages-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: vars.$color-background;
  border-left: 1px solid vars.$color-border;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: vars.$color-text-secondary;

  i {
    font-size: 4rem;
    margin-bottom: 1rem;
    opacity: 0.5;
  }

  h3 {
    margin: 0 0 0.5rem 0;
    color: vars.$color-text-primary;
  }

  p {
    margin: 0;
    font-size: 0.9rem;
  }
}

.conversation-view {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.conversation-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1rem 1.5rem;
  border-bottom: 1px solid vars.$color-border;
  background: vars.$color-surface;

  .conversation-info {
    h3 {
      margin: 0 0 0.25rem 0;
      font-size: 1.1rem;
      color: vars.$color-text-primary;
      display: flex;
      align-items: center;
      gap: 0.5rem;

      .group-chevron {
        font-size: 0.8rem;
        opacity: 0.6;
        transition: transform 0.2s ease;
      }
    }

    .conversation-subtitle {
      margin: 0;
      font-size: 0.85rem;
      color: vars.$color-text-secondary;

      .view-members-hint {
        font-size: 0.75rem;
        opacity: 0.7;
      }
    }

    &.clickable {
      cursor: pointer;
      padding: 0.5rem;
      margin: -0.5rem;
      border-radius: 8px;
      transition: background-color 0.2s ease;

      &:hover {
        background-color: rgba(0, 0, 0, 0.05);

        h3 .group-chevron {
          transform: translateX(3px);
        }
      }
    }
  }
}

.new-chat-form {
  padding: 1rem 0;

  .form-field {
    margin-bottom: 1rem;

    label {
      display: block;
      margin-bottom: 0.5rem;
      font-weight: 500;
      color: vars.$color-text-primary;
    }
  }

  .hint {
    margin: 0.5rem 0 0 0;
    font-size: 0.85rem;
    color: vars.$color-text-secondary;
  }
}
</style>
