<template>
  <div
    class="conversation-item"
    :class="{ active: isActive, unread: conversation.unread_count > 0 }"
    @click="$emit('select')"
  >
    <div class="avatar">
      <i :class="avatarIcon"></i>
    </div>

    <div class="conversation-details">
      <div class="header-row">
        <span class="name">{{ conversationName }}</span>
        <span v-if="conversation.last_message_at" class="time">
          {{ formatTime(conversation.last_message_at) }}
        </span>
      </div>

      <div class="message-row">
        <span class="last-message">{{ lastMessagePreview }}</span>
        <Badge
          v-if="conversation.unread_count > 0"
          :value="conversation.unread_count"
          severity="danger"
          class="unread-badge"
        />
      </div>
    </div>

    <div class="conversation-actions" @click.stop>
      <Button
        icon="pi pi-trash"
        severity="danger"
        text
        rounded
        size="small"
        @click="$emit('delete')"
        v-tooltip.left="'Delete'"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ChatConversation } from '../../services/api'
import Button from 'primevue/button'
import Badge from 'primevue/badge'

interface Props {
  conversation: ChatConversation
  isActive: boolean
}

const props = defineProps<Props>()

defineEmits<{
  select: []
  delete: []
}>()

const conversationName = computed(() => {
  if (props.conversation.conversation_type === 'group') {
    return props.conversation.group_name || 'Group Chat'
  } else {
    return shortenId(props.conversation.peer_id || 'Unknown')
  }
})

const avatarIcon = computed(() => {
  return props.conversation.conversation_type === 'group'
    ? 'pi pi-users'
    : 'pi pi-user'
})

const lastMessagePreview = computed(() => {
  if (props.conversation.last_message && props.conversation.last_message.content) {
    const content = props.conversation.last_message.content
    return content.length > 50 ? `${content.slice(0, 50)}...` : content
  }
  return 'No messages yet'
})

function shortenId(id: string): string {
  if (!id) return ''
  return id.length > 16 ? `${id.slice(0, 8)}...${id.slice(-8)}` : id
}

function formatTime(timestamp: number): string {
  const date = new Date(timestamp * 1000)
  const now = new Date()
  const diff = now.getTime() - date.getTime()

  // Less than 24 hours - show time
  if (diff < 24 * 60 * 60 * 1000) {
    return date.toLocaleTimeString('en-US', {
      hour: 'numeric',
      minute: '2-digit',
      hour12: true
    })
  }

  // Less than 7 days - show day
  if (diff < 7 * 24 * 60 * 60 * 1000) {
    return date.toLocaleDateString('en-US', { weekday: 'short' })
  }

  // Older - show date
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric'
  })
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.conversation-item {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 1rem;
  cursor: pointer;
  transition: background-color 0.2s;
  border-bottom: 1px solid vars.$color-border;
  position: relative;

  &:hover {
    background: vars.$color-background;

    .conversation-actions {
      opacity: 1;
    }
  }

  &.active {
    background: rgba(205, 81, 36, 0.15);
    border-left: 3px solid vars.$color-primary;

    &::before {
      opacity: 1;
    }
  }

  &.unread {
    .name {
      font-weight: 600;
    }

    .last-message {
      font-weight: 500;
      color: vars.$color-text-primary;
    }
  }
}

.avatar {
  flex-shrink: 0;
  width: 48px;
  height: 48px;
  border-radius: 50%;
  background: rgba(205, 81, 36, 0.15);
  display: flex;
  align-items: center;
  justify-content: center;
  color: vars.$color-primary;
  font-size: 1.5rem;
}

.conversation-details {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.header-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.5rem;

  .name {
    font-size: 0.95rem;
    color: vars.$color-text-primary;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .time {
    font-size: 0.75rem;
    color: vars.$color-text-secondary;
    flex-shrink: 0;
  }
}

.message-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.5rem;

  .last-message {
    font-size: 0.85rem;
    color: vars.$color-text-secondary;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
  }

  .unread-badge {
    flex-shrink: 0;
  }
}

.conversation-actions {
  opacity: 0;
  transition: opacity 0.2s;
  display: flex;
  gap: 0.25rem;
}
</style>
