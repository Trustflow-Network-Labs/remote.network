import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const STORAGE_KEY = 'ui-expanded-menu-keys'

function loadFromStorage(): Record<string, boolean> {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    return stored ? JSON.parse(stored) : {}
  } catch {
    return {}
  }
}

export const useUIStore = defineStore('ui', () => {
  // Navigation menu expanded state - load from localStorage on init
  const expandedMenuKeys = ref<Record<string, boolean>>(loadFromStorage())

  // Persist to localStorage whenever expandedMenuKeys changes
  watch(expandedMenuKeys, (newValue) => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(newValue))
    } catch {
      // Ignore storage errors
    }
  }, { deep: true })

  function setExpandedMenuKeys(keys: Record<string, boolean>) {
    expandedMenuKeys.value = keys
  }

  function toggleMenuKey(key: string) {
    expandedMenuKeys.value[key] = !expandedMenuKeys.value[key]
  }

  return {
    expandedMenuKeys,
    setExpandedMenuKeys,
    toggleMenuKey
  }
})
