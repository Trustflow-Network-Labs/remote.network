/**
 * Composable for managing emoji state
 * Tracks recent and frequently used emojis, skin tone preferences
 */

import { ref, computed } from 'vue'
import { normalizeEmoji } from '../utils/emoji'

// LocalStorage keys
const RECENT_EMOJIS_KEY = 'chat_recent_emojis'
const FREQUENT_EMOJIS_KEY = 'chat_frequent_emojis'
const SKIN_TONE_KEY = 'chat_emoji_skin_tone'

// Limits
const MAX_RECENT_EMOJIS = 20
const MAX_FREQUENT_EMOJIS = 50

// State (shared across all instances)
const recentEmojisData = ref<string[]>([])
const frequentEmojisData = ref<Record<string, number>>({})
const skinTone = ref<number>(1) // Default: no skin tone modifier (1 = neutral/yellow)

// Initialize from localStorage
function loadFromStorage() {
  try {
    const recent = localStorage.getItem(RECENT_EMOJIS_KEY)
    if (recent) {
      recentEmojisData.value = JSON.parse(recent)
    }

    const frequent = localStorage.getItem(FREQUENT_EMOJIS_KEY)
    if (frequent) {
      frequentEmojisData.value = JSON.parse(frequent)
    }

    const tone = localStorage.getItem(SKIN_TONE_KEY)
    if (tone) {
      skinTone.value = parseInt(tone, 10)
    }
  } catch (error) {
    console.error('Failed to load emoji data from localStorage:', error)
  }
}

// Save to localStorage
function saveToStorage() {
  try {
    localStorage.setItem(RECENT_EMOJIS_KEY, JSON.stringify(recentEmojisData.value))
    localStorage.setItem(FREQUENT_EMOJIS_KEY, JSON.stringify(frequentEmojisData.value))
    localStorage.setItem(SKIN_TONE_KEY, skinTone.value.toString())
  } catch (error) {
    console.error('Failed to save emoji data to localStorage:', error)
  }
}

// Load data on module initialization
loadFromStorage()

/**
 * Composable for emoji state management
 */
export function useEmoji() {
  /**
   * Add an emoji to recent and frequent lists
   * @param emoji The emoji that was used
   */
  function addEmoji(emoji: string) {
    const normalized = normalizeEmoji(emoji)

    // Add to recent (remove if already exists, then add to front)
    const recentIndex = recentEmojisData.value.indexOf(normalized)
    if (recentIndex !== -1) {
      recentEmojisData.value.splice(recentIndex, 1)
    }
    recentEmojisData.value.unshift(normalized)

    // Trim to max size
    if (recentEmojisData.value.length > MAX_RECENT_EMOJIS) {
      recentEmojisData.value = recentEmojisData.value.slice(0, MAX_RECENT_EMOJIS)
    }

    // Update frequent count
    if (!frequentEmojisData.value[normalized]) {
      frequentEmojisData.value[normalized] = 0
    }
    frequentEmojisData.value[normalized]++

    // Trim frequent emojis if exceeds limit
    const frequentEntries = Object.entries(frequentEmojisData.value)
    if (frequentEntries.length > MAX_FREQUENT_EMOJIS) {
      // Sort by count (ascending) and remove the least used
      frequentEntries.sort((a, b) => a[1] - b[1])
      const toRemove = frequentEntries.slice(0, frequentEntries.length - MAX_FREQUENT_EMOJIS)
      toRemove.forEach(([emoji]) => {
        delete frequentEmojisData.value[emoji]
      })
    }

    saveToStorage()
  }

  /**
   * Set the preferred skin tone
   * @param tone Skin tone (1-6, where 1 is neutral/yellow)
   */
  function setSkinTone(tone: number) {
    skinTone.value = tone
    saveToStorage()
  }

  /**
   * Clear all emoji data
   */
  function clearEmojiData() {
    recentEmojisData.value = []
    frequentEmojisData.value = {}
    skinTone.value = 1
    saveToStorage()
  }

  // Computed properties
  const recentEmojis = computed(() => recentEmojisData.value)

  const topFrequentEmojis = computed(() => {
    const entries = Object.entries(frequentEmojisData.value)
    // Sort by count (descending) and take top 10
    return entries
      .sort((a, b) => b[1] - a[1])
      .slice(0, 10)
      .map(([emoji]) => emoji)
  })

  return {
    // State
    recentEmojis,
    topFrequentEmojis,
    skinTone,

    // Actions
    addEmoji,
    setSkinTone,
    clearEmojiData
  }
}
