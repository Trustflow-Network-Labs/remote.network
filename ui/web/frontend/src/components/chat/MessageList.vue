<template>
  <div class="message-list" ref="messageListRef">
    <div v-if="loading" class="loading-container">
      <ProgressSpinner style="width: 50px; height: 50px" strokeWidth="4" />
    </div>

    <div v-else-if="!messages.length" class="empty-messages">
      <i class="pi pi-comment"></i>
      <p>No messages yet. Start the conversation!</p>
    </div>

    <div v-else class="messages-container">
      <MessageBubble
        v-for="(message, index) in messages"
        :key="message.message_id"
        :message="message"
        :is-sent="message.sender_peer_id === localPeerId"
        :show-timestamp="shouldShowTimestamp(index)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import type { ChatMessage } from '../../services/api'
import MessageBubble from './MessageBubble.vue'
import ProgressSpinner from 'primevue/progressspinner'

interface Props {
  messages: ChatMessage[]
  localPeerId: string
  loading: boolean
}

const props = defineProps<Props>()

const messageListRef = ref<HTMLElement | null>(null)

// Scroll to bottom when messages change
watch(
  () => props.messages.length,
  async () => {
    await nextTick()
    scrollToBottom()
  }
)

function scrollToBottom() {
  if (messageListRef.value) {
    messageListRef.value.scrollTop = messageListRef.value.scrollHeight
  }
}

function shouldShowTimestamp(index: number): boolean {
  // Show timestamp for first message
  if (index === 0) return true

  // Show timestamp if more than 5 minutes since last message
  const current = props.messages[index]
  const previous = props.messages[index - 1]

  const timeDiff = current.timestamp - previous.timestamp
  return timeDiff > 300 // 5 minutes
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.message-list {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  padding: 1rem;
  display: flex;
  flex-direction: column;

  &::-webkit-scrollbar {
    width: 8px;
  }

  &::-webkit-scrollbar-track {
    background: vars.$color-background;
  }

  &::-webkit-scrollbar-thumb {
    background: vars.$color-border;
    border-radius: 4px;

    &:hover {
      background: vars.$color-text-secondary;
    }
  }
}

.loading-container {
  display: flex;
  justify-content: center;
  align-items: center;
  height: 100%;
}

.empty-messages {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: vars.$color-text-secondary;

  i {
    font-size: 3rem;
    margin-bottom: 1rem;
    opacity: 0.5;
  }

  p {
    margin: 0;
    font-size: 0.9rem;
  }
}

.messages-container {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
</style>
