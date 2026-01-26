<template>
  <div class="message-bubble-container" :class="{ sent: isSent }">
    <div v-if="showTimestamp" class="timestamp-divider">
      <span>{{ formatTimestamp(message.timestamp) }}</span>
    </div>

    <div class="message-bubble" :class="{ sent: isSent, failed: message.status === 'failed' }">
      <!-- Show sender name for received messages in group chats -->
      <div v-if="isGroup && !isSent" class="sender-name">
        {{ senderDisplayName }}
      </div>
      <div class="message-content" :class="{ 'emoji-large': isEmojiOnlyMessage }">
        <span v-if="isEmojiOnlyMessage" role="img" :aria-label="message.content">
          {{ message.content }}
        </span>
        <template v-else>
          {{ message.content }}
        </template>
      </div>

      <div class="message-meta">
        <span class="time">{{ formatTime(message.timestamp) }}</span>
        <span v-if="isSent" class="status-indicator">
          <!-- Created: clock icon (message stored, sending in progress) -->
          <i v-if="message.status === 'created'" class="pi pi-clock" v-tooltip.left="'Sending...'" />

          <!-- Sent: single check (relay confirmed for NAT peers) -->
          <i v-else-if="message.status === 'sent'" class="pi pi-check" v-tooltip.left="'Sent to relay'" />

          <!-- Delivered: double check (recipient confirmed) -->
          <i
            v-else-if="message.status === 'delivered'"
            class="pi pi-check-circle"
            v-tooltip.left="'Delivered'"
          />

          <!-- Read: double check (blue) -->
          <i
            v-else-if="message.status === 'read'"
            class="pi pi-check-circle read"
            v-tooltip.left="'Read'"
          />

          <!-- Failed: error icon -->
          <i
            v-else-if="message.status === 'failed'"
            class="pi pi-exclamation-triangle"
            v-tooltip.left="'Failed to send'"
            style="color: var(--red-500)"
          />
        </span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ChatMessage } from '../../services/api'
import { shouldEnlargeEmojis } from '../../utils/emoji'

interface Props {
  message: ChatMessage
  isSent: boolean
  showTimestamp: boolean
  isGroup?: boolean
}

const props = defineProps<Props>()

// Check if the message contains only emojis (1-3) and should be enlarged
const isEmojiOnlyMessage = computed(() => {
  return shouldEnlargeEmojis(props.message.content)
})

// Shorten peer ID for display
const senderDisplayName = computed(() => {
  const id = props.message.sender_peer_id
  if (!id) return 'Unknown'
  return id.length > 12 ? `${id.slice(0, 6)}...${id.slice(-4)}` : id
})

function formatTime(timestamp: number): string {
  const date = new Date(timestamp * 1000)
  return date.toLocaleTimeString('en-US', {
    hour: 'numeric',
    minute: '2-digit',
    hour12: true
  })
}

function formatTimestamp(timestamp: number): string {
  const date = new Date(timestamp * 1000)
  const now = new Date()

  // Today
  const isToday =
    date.getDate() === now.getDate() &&
    date.getMonth() === now.getMonth() &&
    date.getFullYear() === now.getFullYear()

  if (isToday) {
    return 'Today'
  }

  // Yesterday
  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  const isYesterday =
    date.getDate() === yesterday.getDate() &&
    date.getMonth() === yesterday.getMonth() &&
    date.getFullYear() === yesterday.getFullYear()

  if (isYesterday) {
    return 'Yesterday'
  }

  // Older
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: date.getFullYear() !== now.getFullYear() ? 'numeric' : undefined
  })
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.message-bubble-container {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  margin-bottom: 0.25rem;

  &.sent {
    align-items: flex-end;
  }
}

.timestamp-divider {
  width: 100%;
  text-align: center;
  margin: 1rem 0;
  position: relative;

  &::before {
    content: '';
    position: absolute;
    top: 50%;
    left: 0;
    right: 0;
    height: 1px;
    background: vars.$color-border;
    z-index: 0;
  }

  span {
    background: vars.$color-background;
    padding: 0.25rem 0.75rem;
    border-radius: 12px;
    font-size: 0.75rem;
    color: vars.$color-text-secondary;
    position: relative;
    z-index: 1;
  }
}

.message-bubble {
  max-width: 70%;
  min-width: 100px;
  padding: 0.75rem 1rem;
  border-radius: 12px;
  background: vars.$color-surface;
  border: 1px solid vars.$color-border;
  word-wrap: break-word;
  word-break: break-word;

  &.sent {
    background: vars.$color-primary;
    color: white;
    border-color: vars.$color-primary;

    .message-meta {
      .time {
        color: rgba(255, 255, 255, 0.8);
      }

      .status-indicator {
        color: rgba(255, 255, 255, 0.8);

        i.read {
          color: #4fc3f7; // Light blue for read receipts
        }
      }
    }
  }

  &.failed {
    background: rgba(239, 68, 68, 0.1);
    border-color: var(--red-500);
  }
}

.sender-name {
  font-size: 0.75rem;
  font-weight: 600;
  color: vars.$color-primary;
  margin-bottom: 0.25rem;
}

.message-content {
  font-size: 0.95rem;
  line-height: 1.4;
  white-space: pre-wrap;
  margin-bottom: 0.25rem;

  &.emoji-large {
    font-size: 2.5rem;
    line-height: 1.2;
  }
}

.message-meta {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  justify-content: flex-end;
  margin-top: 0.25rem;

  .time {
    font-size: 0.7rem;
    color: vars.$color-text-secondary;
  }

  .status-indicator {
    display: flex;
    align-items: center;
    font-size: 0.9rem;

    i {
      &.read {
        color: vars.$color-primary;
      }
    }
  }
}
</style>
