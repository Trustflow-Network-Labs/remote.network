<template>
  <div class="conversation-list">
    <div class="list-header">
      <h3>Conversations</h3>
      <Badge v-if="totalUnread > 0" :value="totalUnread" severity="danger" />
    </div>

    <div class="search-box">
      <div class="search-input-wrapper">
        <i class="pi pi-search search-icon"></i>
        <InputText
          v-model="searchQuery"
          placeholder="Search conversations..."
          class="search-input"
        />
      </div>
    </div>

    <div class="conversations-scroll">
      <div v-if="!groupedConversations.length" class="empty-list">
        <i class="pi pi-inbox"></i>
        <p>No conversations yet</p>
      </div>

      <div v-else class="conversations">
        <GroupedConversationItem
          v-for="grouped in filteredConversations"
          :key="grouped.peer_id"
          :grouped="grouped"
          :is-active="grouped.peer_id === activePeerId"
          @select="$emit('select-peer', grouped.peer_id)"
          @delete="$emit('delete-peer', grouped.peer_id)"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import type { GroupedConversation } from '../../stores/chat'
import GroupedConversationItem from './GroupedConversationItem.vue'
import InputText from 'primevue/inputtext'
import Badge from 'primevue/badge'

interface Props {
  groupedConversations: GroupedConversation[]
  activePeerId: string | null
  totalUnread: number
}

const props = defineProps<Props>()

defineEmits<{
  'select-peer': [peerId: string]
  'delete-peer': [peerId: string]
}>()

const searchQuery = ref('')

const filteredConversations = computed(() => {
  if (!searchQuery.value) {
    return props.groupedConversations
  }

  const query = searchQuery.value.toLowerCase()
  return props.groupedConversations.filter(grouped => {
    if (grouped.conversation_type === 'group') {
      return grouped.group_name?.toLowerCase().includes(query)
    } else {
      return grouped.peer_id?.toLowerCase().includes(query)
    }
  })
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.conversation-list {
  width: 350px;
  display: flex;
  flex-direction: column;
  background: vars.$color-surface;
  border-right: 1px solid vars.$color-border;
}

.list-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 1rem 1.5rem;
  border-bottom: 1px solid vars.$color-border;

  h3 {
    margin: 0;
    font-size: 1.1rem;
    color: vars.$color-text-primary;
  }
}

.search-box {
  padding: 1rem;
  border-bottom: 1px solid vars.$color-border;
}

.search-input-wrapper {
  position: relative;
  width: 100%;
}

.search-icon {
  position: absolute;
  left: 0.75rem;
  top: 50%;
  transform: translateY(-50%);
  color: vars.$color-text-secondary;
  pointer-events: none;
  z-index: 1;
}

.search-input {
  width: 100%;
  padding-left: 2.5rem;
}

.conversations-scroll {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;

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

.empty-list {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 3rem 1rem;
  color: vars.$color-text-secondary;

  i {
    font-size: 2.5rem;
    margin-bottom: 1rem;
    opacity: 0.5;
  }

  p {
    margin: 0;
    font-size: 0.9rem;
  }
}

.conversations {
  display: flex;
  flex-direction: column;
}
</style>
