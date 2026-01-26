/**
 * Type declarations for vue3-emoji-picker
 * Extends the existing type definitions
 */

declare module 'vue3-emoji-picker/css' {
  const content: any
  export default content
}

declare module 'vue3-emoji-picker' {
  import { DefineComponent } from 'vue'

  export interface EmojiSelectEvent {
    i: string // emoji character
    n: string[] // emoji names/keywords
    r: string // unicode with skin tone
    t: string // skin tone type
    u: string // unicode without tone
  }

  export interface StaticTexts {
    placeholder?: string
    skinTone?: string
  }

  export interface EmojiPickerProps {
    native?: boolean
    hideSearch?: boolean
    hideGroupIcons?: boolean
    hideGroupNames?: boolean
    disableStickyGroupNames?: boolean
    disableSkinTones?: boolean
    disabledGroups?: string[]
    groupNames?: Record<string, string>
    staticTexts?: StaticTexts
    pickerType?: 'input' | 'textarea' | ''
    mode?: 'prepend' | 'insert' | 'append'
    offset?: number
    additionalGroups?: Record<string, any[]>
    groupOrder?: string[]
    groupIcons?: Record<string, any>
    displayRecent?: boolean
    theme?: 'light' | 'dark' | 'auto'
  }

  const EmojiPicker: DefineComponent<EmojiPickerProps>
  export default EmojiPicker
}
