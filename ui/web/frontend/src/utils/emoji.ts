/**
 * Emoji utility functions for chat messaging
 * Handles emoji detection, counting, and normalization
 */

// Comprehensive emoji regex that matches all Unicode emoji
// Includes:
// - \p{Emoji_Presentation}: Characters with emoji presentation by default
// - \p{Emoji}\uFE0F: Characters that need variation selector to be emoji
// - Skin tone modifiers, ZWJ sequences, etc.
const EMOJI_REGEX = /\p{Emoji_Presentation}|\p{Emoji}\uFE0F/gu

/**
 * Checks if the text contains only emojis (and whitespace)
 * @param text The text to check
 * @returns True if the text contains only emojis and whitespace
 */
export function isEmojiOnly(text: string): boolean {
  if (!text || text.trim().length === 0) return false

  // Remove all emojis and check if only whitespace remains
  const textWithoutEmojis = text.replace(EMOJI_REGEX, '').trim()
  return textWithoutEmojis.length === 0
}

/**
 * Counts the number of emojis in the text
 * @param text The text to count emojis in
 * @returns The number of emojis found
 */
export function getEmojiCount(text: string): number {
  const matches = text.match(EMOJI_REGEX)
  return matches ? matches.length : 0
}

/**
 * Determines if emojis in the message should be enlarged
 * Enlarges if: 1-3 emojis only, with no other text
 * @param text The message text
 * @returns True if emojis should be enlarged
 */
export function shouldEnlargeEmojis(text: string): boolean {
  if (!isEmojiOnly(text)) return false

  const count = getEmojiCount(text)
  return count >= 1 && count <= 3
}

/**
 * Normalizes emoji to NFC (Canonical Composition) form
 * Ensures consistent storage and comparison
 * @param emoji The emoji string to normalize
 * @returns The normalized emoji string
 */
export function normalizeEmoji(emoji: string): string {
  return emoji.normalize('NFC')
}

/**
 * Gets a description of the emoji for accessibility
 * This is a simplified version - emoji-mart provides more detailed descriptions
 * @param emoji The emoji to describe
 * @returns A simple description
 */
export function getEmojiDescription(emoji: string): string {
  // For now, return the emoji itself - emoji-mart will provide better descriptions
  // This can be enhanced with a mapping table if needed
  return `Emoji: ${emoji}`
}
