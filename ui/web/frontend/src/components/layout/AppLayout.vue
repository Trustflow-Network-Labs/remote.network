<template>
  <div class="app-layout window-container">
    <NavigationSidebar class="sidebar-pane" />
    <div class="resizer"></div>
    <div class="main-content content-pane">
      <ConnectionStatus class="connection-status-bar" />
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import NavigationSidebar from './NavigationSidebar.vue'
import ConnectionStatus from '../common/ConnectionStatus.vue'
import { useResizer } from '../../composables/useResizer'

const { initResizer } = useResizer()

onMounted(() => {
  initResizer(
    '.window-container',
    '.sidebar-pane',
    '.content-pane',
    '.resizer'
  )
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.app-layout {
  display: flex;
  flex-direction: row;
  flex-wrap: nowrap;
  justify-content: flex-start;
  align-content: flex-start;
  align-items: flex-start;
  width: 100%;
  min-height: 100vh;
  background: vars.$color-background;
}

.sidebar-pane {
  width: 250px;
  overflow: auto;
  height: 100vh;
}

.resizer {
  position: relative;
  width: 8px;
  height: 100vh;
  background-color: vars.$color-border;
  cursor: col-resize;
  flex-shrink: 0;

  // Visual handle in the center
  &::before {
    content: "";
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);

    width: 4px;
    height: 40px;

    background-image: repeating-linear-gradient(
      to bottom,
      #fff,
      #fff 2px,
      transparent 2px,
      transparent 4px
    );

    border-radius: 2px;
  }

  &:hover {
    background-color: vars.$color-accent;
  }
}

.main-content {
  flex: 1;
  overflow-y: auto;
  overflow-x: hidden;
  height: 100vh;
  position: relative;
}

.connection-status-bar {
  position: sticky;
  top: 0;
  left: 0;
  right: 0;
  z-index: 1000;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
}
</style>
