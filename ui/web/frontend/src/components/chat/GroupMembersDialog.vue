<template>
  <Dialog
    v-model:visible="dialogVisible"
    :header="groupName || 'Group Members'"
    :modal="true"
    :style="{ width: '400px' }"
  >
    <div class="group-members-dialog">
      <div v-if="loading" class="loading">
        <ProgressSpinner style="width: 40px; height: 40px" strokeWidth="4" />
      </div>

      <div v-else-if="members.length === 0" class="empty">
        <i class="pi pi-users"></i>
        <p>No members found</p>
      </div>

      <div v-else class="members-list">
        <div
          v-for="member in members"
          :key="member.peer_id"
          class="member-item"
        >
          <div class="member-avatar">
            <i class="pi pi-user"></i>
          </div>
          <div class="member-info">
            <span class="member-id">{{ shortenId(member.peer_id) }}</span>
            <span v-if="member.is_admin" class="admin-badge">Admin</span>
          </div>
          <div class="member-meta">
            <span class="joined-at">Joined {{ formatDate(member.joined_at) }}</span>
          </div>
        </div>
      </div>
    </div>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, type ChatGroupMember } from '../../services/api'
import Dialog from 'primevue/dialog'
import ProgressSpinner from 'primevue/progressspinner'

interface Props {
  visible: boolean
  groupId: string
  groupName?: string
}

const props = defineProps<Props>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
}>()

const dialogVisible = ref(props.visible)
const loading = ref(false)
const members = ref<ChatGroupMember[]>([])

// Sync visible prop
watch(() => props.visible, (newVal) => {
  dialogVisible.value = newVal
  if (newVal && props.groupId) {
    loadMembers()
  }
})

watch(dialogVisible, (newVal) => {
  emit('update:visible', newVal)
})

async function loadMembers() {
  if (!props.groupId) return

  loading.value = true
  try {
    const result = await api.getGroupMembers(props.groupId)
    members.value = result.members || []
  } catch (error) {
    console.error('Failed to load group members:', error)
    members.value = []
  } finally {
    loading.value = false
  }
}

function shortenId(id: string): string {
  if (!id) return ''
  return id.length > 16 ? `${id.slice(0, 8)}...${id.slice(-8)}` : id
}

function formatDate(timestamp: number): string {
  if (!timestamp) return ''
  const date = new Date(timestamp * 1000)
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric'
  })
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.group-members-dialog {
  min-height: 200px;
}

.loading, .empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 2rem;
  color: vars.$color-text-secondary;

  i {
    font-size: 2rem;
    margin-bottom: 0.5rem;
    opacity: 0.5;
  }

  p {
    margin: 0;
    font-size: 0.9rem;
  }
}

.members-list {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.member-item {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem;
  background: vars.$color-surface;
  border: 1px solid vars.$color-border;
  border-radius: 8px;
}

.member-avatar {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: vars.$color-primary;
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;

  i {
    font-size: 1.2rem;
  }
}

.member-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.member-id {
  font-family: monospace;
  font-size: 0.85rem;
}

.admin-badge {
  display: inline-block;
  padding: 0.125rem 0.5rem;
  background: vars.$color-primary;
  color: white;
  border-radius: 12px;
  font-size: 0.7rem;
  font-weight: 600;
  width: fit-content;
}

.member-meta {
  display: flex;
  align-items: center;
}

.joined-at {
  font-size: 0.75rem;
  color: vars.$color-text-secondary;
}
</style>
