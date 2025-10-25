<template>
  <div class="navigation-sidebar">
    <div class="sidebar-header">
      <img class="logo" alt="Remote Network logo" src="../../assets/images/logo.png" />
    </div>

    <nav class="sidebar-nav">
      <PanelMenu :model="menuItems" :multiple="true" v-model:expandedKeys="uiStore.expandedMenuKeys" />
    </nav>

    <div class="sidebar-footer">
      <button class="action-btn logout-btn" @click="logout">
        <i class="pi pi-sign-out"></i>
        {{ $t('message.auth.logout') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import PanelMenu from 'primevue/panelmenu'
import { useAuthStore } from '../../stores/auth'
import { useUIStore } from '../../stores/ui'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const authStore = useAuthStore()
const uiStore = useUIStore()

const menuItems = computed(() => {
  const currentPath = route.path

  return [
    {
      icon: 'pi pi-home',
      label: t('message.navigation.dashboard'),
      key: 'dashboard',
      command: () => router.push('/dashboard'),
      class: currentPath === '/dashboard' ? 'active-menu-item' : ''
    },
    {
      icon: 'pi pi-users',
      label: t('message.navigation.peers'),
      key: 'peers',
      command: () => router.push('/peers'),
      class: currentPath === '/peers' ? 'active-menu-item' : ''
    },
    {
      icon: 'pi pi-box',
      label: t('message.navigation.services'),
      key: 'services',
      command: () => router.push('/services'),
      class: currentPath === '/services' ? 'active-menu-item' : ''
    },
    {
      icon: 'pi pi-sitemap',
      label: t('message.navigation.workflows'),
      key: 'workflows',
      command: () => router.push('/workflows'),
      class: currentPath.startsWith('/workflows') ? 'active-menu-item' : ''
    }
  ]
})

function logout() {
  authStore.clearAuth()
  router.push('/login')
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.navigation-sidebar {
  width: 100%;
  min-height: 100vh;
  background: vars.$color-surface;
  display: flex;
  flex-direction: column;
  border-right: none; // Border handled by resizer

  // PanelMenu CSS variables
  --p-panelmenu-gap: 0;
  --p-panelmenu-item-color: rgba(240, 240, 240, 1);
  --p-panelmenu-item-focus-color: rgba(240, 240, 240, 1);
  --p-panelmenu-panel-background: transparent;
  --p-panelmenu-panel-border-width: 0;
  --p-panelmenu-panel-border-color: transparent;
  --p-panelmenu-panel-padding: 0 0.5rem;
  --p-panelmenu-item-padding: 0.5rem;
  --p-panelmenu-item-focus-background: rgb(205, 81, 36);
  --p-panelmenu-item-active-background: #4060c3;
  --p-panelmenu-item-icon-color: #fff;
  --p-panelmenu-item-icon-focus-color: #fff;
  --p-panelmenu-submenu-icon-color: rgba(240, 240, 240, 1);
  --p-panelmenu-submenu-icon-focus-color: rgba(240, 240, 240, 1);

  // Active menu item styling
  :deep(.active-menu-item > .p-panelmenu-item-content),
  :deep(.active-menu-item .p-panelmenu-header-content) {
    background-color: var(--p-panelmenu-item-active-background) !important;
  }

  // Submenu icon color styling
  :deep(.p-panelmenu-submenu-icon) {
    color: rgba(240, 240, 240, 1) !important;
  }

  :deep(.p-panelmenu-header-content:hover .p-panelmenu-submenu-icon),
  :deep(.p-panelmenu-item-content:hover .p-panelmenu-submenu-icon) {
    color: rgba(240, 240, 240, 1) !important;
  }

  // Menu item text handling for narrow sidebars
  :deep(.p-panelmenu-item-content),
  :deep(.p-panelmenu-header-content) {
    white-space: nowrap;
    overflow: hidden;

    .p-panelmenu-item-label,
    .p-panelmenu-header-label {
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }

  // Ensure icons don't shrink
  :deep(.p-menuitem-icon),
  :deep(.p-panelmenu-icon) {
    flex-shrink: 0;
  }
}

.sidebar-header {
  padding: vars.$spacing-lg;
  border-bottom: 1px solid rgba(vars.$color-border, 0.3);
  display: flex;
  justify-content: center;
  align-items: center;

  .logo {
    width: 150px;
    margin: vars.$spacing-md 0;
  }
}

.sidebar-nav {
  flex: 1;
  padding: vars.$spacing-md 0;
}

.sidebar-footer {
  padding: vars.$spacing-lg;
  border-top: 1px solid rgba(vars.$color-border, 0.3);
  display: flex;
  flex-direction: column;
  gap: vars.$spacing-sm;

  .action-btn {
    width: calc(100% - 0.5rem);
    min-width: 40px;
    height: 30px;
    line-height: 30px;
    border-radius: 3px;
    border: none;
    margin: 0;
    padding: 0 8px;
    cursor: pointer;
    color: #fff;
    font-size: 0.9rem;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: vars.$spacing-sm;
    transition: background-color 0.2s ease;
    white-space: nowrap;
    overflow: hidden;

    i {
      font-size: 0.9rem;
      flex-shrink: 0; // Prevent icon from shrinking
    }

    // Hide text when sidebar is too narrow, show only icon
    span {
      overflow: hidden;
      text-overflow: ellipsis;
    }

    &.logout-btn {
      background-color: rgb(205, 81, 36); // Primary orange

      &:hover {
        background-color: rgb(246, 114, 66); // Lighter orange on hover
      }
    }

    &.disabled {
      background-color: #333333;
      cursor: not-allowed;
    }
  }
}
</style>
