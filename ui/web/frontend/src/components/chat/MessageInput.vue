<template>
  <div class="message-input-container">
    <div class="input-wrapper">
      <Textarea
        v-model="messageContent"
        placeholder="Type a message..."
        :autoResize="true"
        rows="1"
        :maxRows="5"
        :disabled="disabled"
        @keydown.enter.exact="handleEnterKey"
        class="message-textarea"
      />

      <Button
        icon="pi pi-send"
        :disabled="!canSend"
        :loading="disabled"
        @click="handleSend"
        class="send-button"
        severity="primary"
        rounded
        v-tooltip.top="'Send message'"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import Textarea from 'primevue/textarea'
import Button from 'primevue/button'

interface Props {
  disabled?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  disabled: false
})

const emit = defineEmits<{
  send: [content: string]
}>()

const messageContent = ref('')

const canSend = computed(() => {
  return messageContent.value.trim().length > 0 && !props.disabled
})

function handleEnterKey(event: KeyboardEvent) {
  // Shift+Enter allows new line
  if (event.shiftKey) {
    return
  }

  // Plain Enter sends the message
  event.preventDefault()
  handleSend()
}

function handleSend() {
  if (!canSend.value) return

  const content = messageContent.value.trim()
  if (content) {
    emit('send', content)
    messageContent.value = ''
  }
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.message-input-container {
  border-top: 1px solid vars.$color-border;
  background: vars.$color-surface;
  padding: 1rem;
}

.input-wrapper {
  display: flex;
  align-items: flex-end;
  gap: 0.75rem;
}

.message-textarea {
  flex: 1;
  min-height: 42px;
  max-height: 150px;
  resize: none;
  font-family: inherit;
  font-size: 0.95rem;
  line-height: 1.4;

  &:focus {
    outline: none;
    border-color: vars.$color-primary;
  }

  &:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
}

.send-button {
  flex-shrink: 0;
  width: 42px;
  height: 42px;
  display: flex;
  align-items: center;
  justify-content: center;

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
}
</style>
