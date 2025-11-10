<template>
  <div :class="['self-peer-card', 'data']">
    <div class="card-top">
      <div class="peer-icon">
        <i class="pi pi-download"></i>
      </div>
      <div class="peer-label">Local Peer</div>
      <div class="peer-id" v-if="peerId">
        <span>{{ shorten(peerId, 6, 6) }}</span>
        <i class="pi pi-copy copy-icon" @click.stop="copyToClipboard(peerId)" title="Copy peer ID"></i>
      </div>
    </div>

    <div class="card-rip">
      <div
        :class="['card-rip-connector', 'left', 'allowed', 'data']"
        :data-card-id="'self-peer'"
        :data-connector="'input'"
      ></div>
      <div
        :class="['card-rip-connector', 'right', 'not-allowed', 'data']"
        :data-card-id="'self-peer'"
        :data-connector="'output'"
      ></div>
    </div>

    <div class="card-bottom">
      <i class="pi pi-check-circle"></i>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useAuthStore } from '../../stores/auth'
import { useTextUtils } from '../../composables/useTextUtils'
import { useClipboard } from '../../composables/useClipboard'

const authStore = useAuthStore()
const { shorten } = useTextUtils()
const { copyToClipboard } = useClipboard()

const peerId = computed(() => authStore.peerId)
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.self-peer-card {
  position: absolute;
  width: 160px !important;
  height: 200px !important;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  color: #000;
  filter: drop-shadow(1px 1px 3px rgba(0, 0, 0, 0.3));
  cursor: move;
  user-select: none;

  .card-top {
    flex: 1 1 auto;
    max-height: 130px;
    background-color: #fff;
    border-top-right-radius: 5px;
    border-top-left-radius: 5px;

    display: flex;
    flex-direction: column;
    flex-wrap: nowrap;
    justify-content: center;
    align-content: center;
    align-items: center;
    gap: 8px;
    padding: 16px 8px;

    .peer-icon {
      font-size: 2rem;
      color: rgb(205, 81, 36);
    }

    .peer-label {
      font-weight: 700;
      font-size: 14px;
      color: rgb(205, 81, 36);
    }

    .peer-id {
      font-size: 10px;
      color: #666;
      font-family: monospace;
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 4px;

      .copy-icon {
        cursor: pointer;
        font-size: 9px;
        opacity: 0.5;
        transition: opacity 0.2s ease;

        &:hover {
          opacity: 1;
          color: rgb(205, 81, 36);
        }
      }
    }
  }

  .card-bottom {
    background-color: #fff;
    border-bottom-right-radius: 5px;
    border-bottom-left-radius: 5px;
    padding: 5px;
    height: 50px;
    width: 100%;

    display: flex;
    flex-direction: row;
    flex-wrap: nowrap;
    justify-content: center;
    align-content: center;
    align-items: center;

    i {
      font-size: 1.2rem;
      color: #16a34a;
    }
  }

  .card-rip {
    background-color: #fff;
    height: 20px;
    margin: 0 10px;
    background-image: url(data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAYAAAACCAYAAAB7Xa1eAAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAAYdEVYdFNvZnR3YXJlAHBhaW50Lm5ldCA0LjAuOWwzfk4AAAAaSURBVBhXY5g7f97/2XPn/AcCBmSMQ+I/AwB2eyNBlrqzUQAAAABJRU5ErkJggg==);
    background-size: 4px 2px;
    background-repeat: repeat-x;
    background-position: center;
    position: relative;
    box-shadow: 0 1px 0 0 #fff, 0 -1px 0 0 #fff;

    &:before,
    &:after {
      content: '';
      position: absolute;
      width: 30px;
      height: 30px;
      top: 50%;
      transform: translate(-50%, -50%) rotate(45deg);
      border: 5px solid transparent;
      border-top-color: #fff;
      border-right-color: #fff;
      border-radius: 100%;
      pointer-events: none;
    }

    &:before {
      left: -10px;
    }

    &:after {
      transform: translate(-50%, -50%) rotate(225deg);
      right: -40px;
    }

    .card-rip-connector {
      position: absolute;
      width: 20px;
      height: 20px;
      border: 5px solid;
      border-color: transparent;
      border-radius: 50%;

      &.left {
        left: -20px;
      }

      &.right {
        right: -20px;
      }

      &.allowed.data {
        border-color: rgb(205, 81, 36);
      }

      &.not-allowed {
        border-color: #fff !important;
        background: #fff;
      }
    }
  }

  &:hover {
    .card-rip {
      .card-rip-connector.allowed.data {
        background-color: rgba(205, 81, 36, 0.1);
      }
    }
  }
}
</style>
