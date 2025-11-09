<template>
  <div :class="['service-card', { 'data': isData, 'docker': isDocker, 'standalone': isStandalone }]">
    <div class="card-top">
      <div class="card-controls">
        <i class="pi pi-times-circle" @click="closeCard"></i>
      </div>
      <div class="service-name">{{ job.service_name || 'Unknown Service' }}</div>
      <div class="service-price" v-if="job.pricing_amount">{{ formattedPrice }}</div>
      <div class="service-peer" v-if="job.peer_id">
        <span>{{ shorten(job.peer_id, 6, 6) }}</span>
        <i class="pi pi-copy copy-icon" @click.stop="copyToClipboard(job.peer_id)" title="Copy peer ID"></i>
      </div>
      <div :class="['service-type', { 'data': isData, 'docker': isDocker, 'standalone': isStandalone }]">
        <i :class="serviceIcon" v-if="isData"></i>
        <i :class="serviceIcon" v-if="isDocker || isStandalone"></i>
        {{ serviceTypeLabel }}
      </div>
    </div>

    <div class="card-rip">
      <div :class="['card-rip-connector', 'left', 'not-allowed', { 'data': isData, 'docker': isDocker, 'standalone': isStandalone }]"></div>
      <div :class="['card-rip-connector', 'right', 'allowed', { 'data': isData, 'docker': isDocker, 'standalone': isStandalone }]"></div>
    </div>

    <div class="card-bottom">
      <i :class="['pi', { 'pi-exclamation-triangle red': !hasInputs, 'pi-check-square': hasInputs }]"></i>
      <i :class="['pi', 'pi-wallet']"></i>
      <i class="pi pi-cog" @click="configureJob"></i>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useTextUtils } from '../../composables/useTextUtils'
import { useClipboard } from '../../composables/useClipboard'
import type { WorkflowJob } from '../../stores/workflows'

interface Props {
  job: WorkflowJob
  serviceCardId: number
}

const props = defineProps<Props>()
const emit = defineEmits<{
  closeServiceCard: [id: number]
  configureServiceCard: [id: number]
}>()

const { shorten } = useTextUtils()
const { copyToClipboard } = useClipboard()

const isData = computed(() => props.job.service_type === 'DATA')
const isDocker = computed(() => props.job.service_type === 'DOCKER')
const isStandalone = computed(() => props.job.service_type === 'STANDALONE')

const serviceIcon = computed(() => {
  switch (props.job.service_type) {
    case 'DATA':
      return 'pi pi-file'
    case 'DOCKER':
      return 'pi pi-server'
    case 'STANDALONE':
      return 'pi pi-server'
    default:
      return 'pi pi-question-circle'
  }
})

const serviceTypeLabel = computed(() => {
  return props.job.service_type || 'Unknown'
})

const formattedPrice = computed(() => {
  const amount = props.job.pricing_amount || 0
  const type = props.job.pricing_type || 'ONE_TIME'
  const interval = props.job.pricing_interval || 1
  const unit = props.job.pricing_unit || 'MONTHS'

  const tokenLabel = amount === 1 ? 'token' : 'tokens'
  let priceStr = `${amount} ${tokenLabel}`

  if (type === 'RECURRING') {
    const unitStr = interval > 1 ? `${interval} ${unit.toLowerCase()}` : unit.toLowerCase().slice(0, -1)
    priceStr += `/${unitStr}`
  }

  return priceStr
})

const hasInputs = computed(() => {
  return props.job.input_mapping && Object.keys(props.job.input_mapping).length > 0
})

function closeCard() {
  emit('closeServiceCard', props.serviceCardId)
}

function configureJob() {
  emit('configureServiceCard', props.serviceCardId)
}
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.service-card {
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
    justify-content: space-between;
    align-content: center;
    align-items: center;

    .card-controls {
      position: absolute;
      width: 100%;
      padding: 5px;
      display: flex;
      flex-direction: row;
      flex-wrap: nowrap;
      justify-content: flex-end;
      align-content: center;
      align-items: center;

      i {
        font-size: .75rem;
        cursor: pointer;
      }
    }

    .service-name {
      padding: 16px 8px 2px 8px;
      display: -webkit-box;
      -webkit-box-orient: vertical;
      overflow: hidden;
      text-overflow: ellipsis;
      line-clamp: 2;
      -webkit-line-clamp: 2;
      font-weight: 600;
      font-size: 13px;
    }

    .service-price {
      padding: 0 8px 2px 8px;
      font-size: 11px;
      color: #16a34a;
      font-weight: 700;
      text-align: center;
    }

    .service-peer {
      padding: 0 8px 4px 8px;
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

    .service-type {
      width: 100%;
      background-color: rgba(205, 81, 36, 1);
      color: #fff;
      padding: 8px;
      margin-bottom: 5px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;

      &.data {
        background-color: rgb(205, 81, 36);
      }

      &.docker {
        background-color: #4060c3;
      }

      &.standalone {
        background-color: #4060c3;
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
    justify-content: space-evenly;
    align-content: center;
    align-items: center;

    i {
      font-size: 1rem;
      cursor: pointer;

      &.red {
        color: rgb(211, 31, 31);
      }
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
      pointer-events:none;
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

      &.not-allowed {
        border-color: #fff !important;
        background: #fff;
      }
    }
  }

  &:hover {
    .card-rip {
      .card-rip-connector {
        &.allowed.data {
          border-color: rgb(205, 81, 36);
        }
        &.allowed.docker {
          border-color: #4060c3;
        }
        &.allowed.standalone {
          border-color: #4060c3;
        }
      }
    }
  }
}
</style>
