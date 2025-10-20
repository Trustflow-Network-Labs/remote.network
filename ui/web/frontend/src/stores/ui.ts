import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useUIStore = defineStore('ui', () => {
  // Navigation menu expanded state
  const expandedMenuKeys = ref<Record<string, boolean>>({})

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
}, {
  persist: {
    storage: localStorage,
    paths: ['expandedMenuKeys']
  }
})
