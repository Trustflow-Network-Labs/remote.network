import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import router from './router'
import App from './App.vue'

// PrimeVue imports
import PrimeVue from 'primevue/config'
import Aura from '@primeuix/themes/aura'
import ConfirmationService from 'primevue/confirmationservice'
import ToastService from 'primevue/toastservice'
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

app.mount('#app')
