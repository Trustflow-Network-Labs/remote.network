import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import router from './router'
import App from './App.vue'
import { useAuthStore } from './stores/auth'
import { useConnectionStore } from './stores/connection'
import { initializeWebSocket } from './services/websocket'
import { api } from './services/api'

// PrimeVue imports
import PrimeVue from 'primevue/config'
import Aura from '@primeuix/themes/aura'
import ConfirmationService from 'primevue/confirmationservice'
import ToastService from 'primevue/toastservice'
import Tooltip from 'primevue/tooltip'
import 'primeicons/primeicons.css'

// Locale imports
import Locale_en_GB from './locales/en_GB'

// Styles
import './scss/main.scss'

// Setup i18n
const messages = {
  en_GB: Locale_en_GB
}

const i18n = createI18n({
  legacy: false,          // Use Composition API mode (modern approach)
  locale: 'en_GB',
  fallbackLocale: 'en_GB',
  messages
})

// Create app
const app = createApp(App)
const pinia = createPinia()

// Configure PrimeVue with custom theme
app.use(PrimeVue, {
  theme: {
    preset: Aura,
    options: {
      prefix: 'p',
      darkModeSelector: false,
      cssLayer: false
    }
  }
})

app.use(pinia)
app.use(router)
app.use(i18n)
app.use(ConfirmationService)
app.use(ToastService)
app.directive('tooltip', Tooltip)

// Initialize WebSocket if user is already authenticated (page refresh scenario)
const authStore = useAuthStore()
const connectionStore = useConnectionStore()

// Sync API client with stored endpoint (page refresh scenario)
api.setBaseURL(connectionStore.nodeEndpoint)

if (authStore.isAuthenticated && authStore.token) {
  initializeWebSocket(connectionStore.nodeEndpoint, authStore.token)
}

app.mount('#app')
