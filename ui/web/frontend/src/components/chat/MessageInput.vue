<template>
  <div class="message-input-container">
    <div class="input-wrapper">
      <Textarea
        ref="textareaRef"
        v-model="messageContent"
        placeholder="Type a message..."
        :autoResize="true"
        rows="1"
        :maxRows="5"
        :disabled="disabled"
        @keydown.enter.exact="handleEnterKey"
        @keydown="handleKeydown"
        class="message-textarea"
      />

      <Button
        ref="emojiButtonRef"
        icon="pi pi-face-smile"
        text
        rounded
        @click="toggleEmojiPicker($event)"
        class="emoji-button"
        v-tooltip.top="'Insert emoji (Ctrl+E)'"
        aria-label="Insert emoji"
        :aria-haspopup="true"
        :aria-expanded="showEmojiPicker"
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

    <EmojiPicker
      ref="emojiPickerRef"
      @select="insertEmojiAtCursor"
      @close="showEmojiPicker = false"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import Textarea from 'primevue/textarea'
import Button from 'primevue/button'
import EmojiPicker from './EmojiPicker.vue'

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
const textareaRef = ref<InstanceType<typeof Textarea> | null>(null)
const emojiButtonRef = ref<HTMLElement | null>(null)
const emojiPickerRef = ref<InstanceType<typeof EmojiPicker> | null>(null)
const showEmojiPicker = ref(false)

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

function handleKeydown(event: KeyboardEvent) {
  // Ctrl+E (Cmd+E on Mac) toggles emoji picker
  if ((event.ctrlKey || event.metaKey) && event.key === 'e') {
    event.preventDefault()
    // Get the button element for positioning
    if (emojiButtonRef.value) {
      const buttonEl = (emojiButtonRef.value as any).$el || emojiButtonRef.value
      toggleEmojiPickerWithElement(buttonEl)
    }
  }
}

function handleSend() {
  if (!canSend.value) return

  const content = messageContent.value.trim()
  if (content) {
    emit('send', content)
    messageContent.value = ''
  }
}

function toggleEmojiPicker(event: MouseEvent) {
  showEmojiPicker.value = !showEmojiPicker.value

  if (showEmojiPicker.value) {
    emojiPickerRef.value?.toggle(event)
  } else {
    emojiPickerRef.value?.hide()
  }
}

function toggleEmojiPickerWithElement(element: HTMLElement) {
  showEmojiPicker.value = !showEmojiPicker.value

  if (showEmojiPicker.value) {
    emojiPickerRef.value?.toggle(element)
  } else {
    emojiPickerRef.value?.hide()
  }
}

function insertEmojiAtCursor(emoji: string) {
  // Get the textarea element from the DOM
  const textarea = document.querySelector('.message-textarea') as HTMLTextAreaElement
  if (!textarea) {
    // Fallback: just append to the end
    messageContent.value += emoji
    showEmojiPicker.value = false
    return
  }

  const start = textarea.selectionStart ?? messageContent.value.length
  const end = textarea.selectionEnd ?? messageContent.value.length

  // Insert emoji at cursor position
  const before = messageContent.value.substring(0, start)
  const after = messageContent.value.substring(end)
  messageContent.value = before + emoji + after

  // Restore cursor position after the inserted emoji
  const newCursorPos = start + emoji.length

  // Use nextTick to ensure the textarea is updated
  setTimeout(() => {
    textarea.focus()
    textarea.setSelectionRange(newCursorPos, newCursorPos)
  }, 0)

  showEmojiPicker.value = false
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

.emoji-button {
  flex-shrink: 0;
  width: 42px;
  height: 42px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: vars.$color-text-secondary;
  transition: color 0.2s;

  &:hover {
    color: vars.$color-primary;
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
