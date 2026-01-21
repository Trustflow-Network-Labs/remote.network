<template>
  <Dialog
    v-model:visible="visible"
    header="Create Group"
    :modal="true"
    :style="{ width: '500px' }"
    @hide="resetForm"
  >
    <div class="group-form">
      <div class="form-field">
        <label for="group-name">Group Name</label>
        <InputText
          id="group-name"
          v-model="groupName"
          placeholder="Enter group name..."
          class="w-full"
        />
      </div>

      <div class="form-field">
        <label>Members</label>
        <div class="member-input">
          <InputText
            v-model="newMemberId"
            placeholder="Enter peer ID..."
            class="flex-grow-1"
            @keyup.enter="addMember"
          />
          <Button
            icon="pi pi-plus"
            @click="addMember"
            :disabled="!newMemberId"
          />
        </div>
        <p class="hint">Add at least 2 members to create a group</p>
      </div>

      <div class="members-list" v-if="members.length > 0">
        <div class="member-chip" v-for="(member, index) in members" :key="member">
          <span class="member-id">{{ shortenId(member) }}</span>
          <Button
            icon="pi pi-times"
            text
            rounded
            severity="danger"
            size="small"
            @click="removeMember(index)"
          />
        </div>
      </div>

      <Message v-if="errorMessage" severity="error" :closable="false">
        {{ errorMessage }}
      </Message>
    </div>

    <template #footer>
      <Button
        label="Cancel"
        severity="secondary"
        @click="visible = false"
      />
      <Button
        label="Create Group"
        icon="pi pi-users"
        @click="createGroup"
        :disabled="!isFormValid"
        :loading="creating"
      />
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useChatStore } from '../../stores/chat'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Message from 'primevue/message'

const emit = defineEmits<{
  (e: 'created', groupId: string): void
}>()

const visible = defineModel<boolean>('visible', { default: false })

const chatStore = useChatStore()

const groupName = ref('')
const members = ref<string[]>([])
const newMemberId = ref('')
const creating = ref(false)
const errorMessage = ref('')

const isFormValid = computed(() => {
  return groupName.value.trim().length > 0 && members.value.length >= 2
})

function shortenId(id: string): string {
  if (!id) return ''
  return id.length > 16 ? `${id.slice(0, 8)}...${id.slice(-8)}` : id
}

function addMember() {
  const memberId = newMemberId.value.trim()
  if (!memberId) return

  // Check if already added
  if (members.value.includes(memberId)) {
    errorMessage.value = 'Member already added'
    return
  }

  // Validate peer ID format (40 hex chars - SHA1 of public key)
  if (!/^[a-fA-F0-9]{40}$/.test(memberId)) {
    errorMessage.value = 'Invalid peer ID format (expected 40 hex characters)'
    return
  }

  errorMessage.value = ''
  members.value.push(memberId)
  newMemberId.value = ''
}

function removeMember(index: number) {
  members.value.splice(index, 1)
}

async function createGroup() {
  if (!isFormValid.value) return

  creating.value = true
  errorMessage.value = ''

  try {
    const group = await chatStore.createGroup(groupName.value.trim(), members.value)
    emit('created', group.conversation_id)
    visible.value = false
    resetForm()
  } catch (error: any) {
    errorMessage.value = error.message || 'Failed to create group'
  } finally {
    creating.value = false
  }
}

function resetForm() {
  groupName.value = ''
  members.value = []
  newMemberId.value = ''
  errorMessage.value = ''
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.group-form {
  padding: 1rem 0;
}

.form-field {
  margin-bottom: 1.25rem;

  label {
    display: block;
    margin-bottom: 0.5rem;
    font-weight: 500;
    color: vars.$color-text-primary;
  }
}

.member-input {
  display: flex;
  gap: 0.5rem;
}

.hint {
  margin: 0.5rem 0 0 0;
  font-size: 0.85rem;
  color: vars.$color-text-secondary;
}

.members-list {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.member-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.25rem 0.5rem 0.25rem 0.75rem;
  background: vars.$color-surface-hover;
  border-radius: 1rem;
  font-size: 0.85rem;

  .member-id {
    font-family: monospace;
    color: vars.$color-text-primary;
  }

  :deep(.p-button) {
    width: 1.25rem;
    height: 1.25rem;
    padding: 0;

    .pi {
      font-size: 0.7rem;
    }
  }
}
</style>
