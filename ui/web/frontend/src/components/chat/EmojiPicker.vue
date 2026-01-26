<template>
  <Teleport to="body">
    <div
      v-if="isVisible"
      class="emoji-picker-backdrop"
      @click="hide"
    >
      <div
        class="emoji-picker-container"
        :style="positionStyle"
        @click.stop
      >
        <EmojiPicker
          :native="true"
          :hide-search="false"
          :hide-group-icons="false"
          :hide-group-names="false"
          :disable-skin-tones="false"
          :display-recent="true"
          :theme="theme"
          @select="handleEmojiSelect"
          class="emoji-picker"
        />
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import EmojiPicker from 'vue3-emoji-picker'
import 'vue3-emoji-picker/css'
import { useEmoji } from '../../composables/useEmoji'

interface EmojiSelectEvent {
  i: string // emoji character
  n: string[] // emoji names
  r: string // unicode with skin tone
  t: string // skin tone
  u: string // unicode without tone
}

const emit = defineEmits<{
  select: [emoji: string]
  close: []
}>()

const isVisible = ref(false)
const buttonRect = ref<DOMRect | null>(null)

// Emoji state management
const { addEmoji } = useEmoji()

// Determine theme based on system preference or PrimeVue theme
const theme = computed<'light' | 'dark' | 'auto'>(() => 'light')

// Calculate position style with smart placement
const positionStyle = computed(() => {
  if (!buttonRect.value) {
    return {
      position: 'fixed' as const,
      top: '0px',
      left: '0px',
      zIndex: 1060,
      visibility: 'hidden' as const
    }
  }

  const rect = buttonRect.value
  const pickerWidth = 352
  const pickerHeight = 400
  const gap = 8 // Gap between button and picker

  // Calculate available space in all directions
  const spaceAbove = rect.top
  const spaceBelow = window.innerHeight - rect.bottom

  let top = 0
  let left = 0

  // Vertical positioning: prefer bottom, then top
  if (spaceBelow >= pickerHeight + gap) {
    // Show below button
    top = rect.bottom + gap
  } else if (spaceAbove >= pickerHeight + gap) {
    // Show above button
    top = rect.top - pickerHeight - gap
  } else {
    // Not enough space in either direction, center vertically with scroll
    top = Math.max(gap, (window.innerHeight - pickerHeight) / 2)
  }

  // Horizontal positioning: prefer aligning with button, adjust if needed
  // Try to align left edge with button
  left = rect.left

  // If it would go off the right edge, align right edge with button's right
  if (left + pickerWidth > window.innerWidth - gap) {
    left = rect.right - pickerWidth
  }

  // If still off screen (button is very far right), align with right edge
  if (left + pickerWidth > window.innerWidth - gap) {
    left = window.innerWidth - pickerWidth - gap
  }

  // If off left edge (button is very far left), align with left edge
  if (left < gap) {
    left = gap
  }

  return {
    position: 'fixed' as const,
    top: `${top}px`,
    left: `${left}px`,
    zIndex: 1060 // Use app's z-index-popover
  }
})

// Handle emoji selection
function handleEmojiSelect(emoji: EmojiSelectEvent) {
  const emojiChar = emoji.i
  addEmoji(emojiChar)
  emit('select', emojiChar)
  hide()
}

// Toggle visibility
function toggle(eventOrElement: Event | HTMLElement) {
  if (isVisible.value) {
    hide()
  } else {
    show(eventOrElement)
  }
}

// Show the picker
function show(eventOrElement: Event | HTMLElement) {
  let target: HTMLElement | null = null

  // Check if it's an Event or directly an HTMLElement
  if (eventOrElement instanceof HTMLElement) {
    target = eventOrElement
  } else {
    // It's an Event - use currentTarget or target
    target = (eventOrElement.currentTarget || eventOrElement.target) as HTMLElement
  }

  if (!target) {
    console.warn('EmojiPicker: No target element found')
    return
  }

  buttonRect.value = target.getBoundingClientRect()
  isVisible.value = true
}

// Hide the picker
function hide() {
  isVisible.value = false
  emit('close')
}

// Expose methods for parent component
defineExpose({
  toggle,
  show,
  hide
})
</script>

<style lang="scss" scoped>
@use '../../scss/variables' as vars;

.emoji-picker-backdrop {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: vars.$z-index-popover - 1;
  background: transparent;
}

.emoji-picker-container {
  width: 352px;
  background: vars.$color-card;
  border: 1px solid vars.$color-border;
  border-radius: vars.$border-radius-md;
  box-shadow: vars.$shadow-lg;
  overflow: hidden;
  transition: opacity vars.$transition-fast, transform vars.$transition-fast;

  .emoji-picker {
    max-height: 400px;
  }
}
</style>

<style lang="scss">
// Global styles for vue3-emoji-picker to match app theme
@use '../../scss/variables' as vars;

.emoji-picker-container {
  // Override emoji picker CSS custom properties for dark theme
  .v3-emoji-picker {
    // CSS custom properties for theming
    --v3-picker-bg: #{vars.$color-card};
    --v3-picker-fg: #{vars.$color-text-primary};
    --v3-picker-border: #{vars.$color-border};
    --v3-picker-input-bg: #{vars.$color-surface-variant};
    --v3-picker-input-border: #{vars.$color-border};
    --v3-picker-input-focus-border: #{vars.$color-primary};
    --v3-group-image-filter: invert(1); // Invert group icons for dark theme
    --v3-picker-emoji-hover: #{vars.$color-surface-hover};

    // Additional styling
    font-family: vars.$font-family-base;
    box-shadow: none; // Remove default shadow (container has it)
    border-radius: 0; // Remove default border radius (container has it)
    width: 100%;
    height: 400px;

    // Header with group icons
    .v3-header {
      padding: vars.$spacing-sm;

      .v3-groups .v3-group {
        border-radius: vars.$border-radius-sm;

        &:hover {
          opacity: 1;
        }
      }
    }

    // Search input styling
    .v3-search {
      padding: 0 vars.$spacing-md vars.$spacing-sm;

      input {
        border-radius: vars.$border-radius-sm;
        font-size: vars.$font-size-sm;
        transition: border-color vars.$transition-fast;

        &::placeholder {
          color: vars.$color-text-secondary;
          opacity: 0.7;
        }
      }
    }

    // Body with emoji groups
    .v3-body {
      padding: 0 vars.$spacing-sm vars.$spacing-md;

      .v3-body-inner {
        // Custom scrollbar
        &::-webkit-scrollbar {
          width: 8px;
        }

        &::-webkit-scrollbar-track {
          background: vars.$color-surface;
          border-radius: 4px;
        }

        &::-webkit-scrollbar-thumb {
          background: vars.$color-border;
          border-radius: 4px;

          &:hover {
            background: vars.$color-accent;
          }
        }

        // Group titles
        .v3-group {
          h5 {
            color: vars.$color-text-secondary;
            font-size: vars.$font-size-sm;
            font-weight: 600;
            text-transform: uppercase;
            letter-spacing: 0.5px;
          }

          // Emoji buttons
          .v3-emojis button {
            border-radius: vars.$border-radius-sm;
            transition: background vars.$transition-fast;
          }
        }
      }
    }

    // Footer with skin tones
    .v3-footer {
      padding: vars.$spacing-sm vars.$spacing-md;

      .v3-tone {
        transition: opacity vars.$transition-fast;

        &:hover {
          opacity: 1;
        }

        .v3-text {
          color: vars.$color-text-secondary;
          font-size: vars.$font-size-sm;
        }
      }
    }

    // Skin tone selector overlay
    .v3-skin-tones {
      background: vars.$color-surface;

      .v3-skin-tone {
        border-radius: vars.$border-radius-sm;
        transition: transform vars.$transition-fast;
      }
    }
  }
}
</style>
