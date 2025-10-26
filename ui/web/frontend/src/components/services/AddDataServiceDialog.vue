<template>
  <Dialog
    :visible="visible"
    @update:visible="(val) => emit('update:visible', val)"
    :header="$t('message.services.addDataService')"
    :modal="true"
    :style="{ width: '600px' }"
    :breakpoints="{ '960px': '75vw', '640px': '100vw' }"
    @hide="resetForm"
  >
    <div class="data-service-form">
      <!-- Service Name -->
      <div class="field">
        <label for="service-name">{{ $t('message.services.serviceName') }} *</label>
        <InputText
          id="service-name"
          v-model="serviceForm.name"
          class="w-full"
          :disabled="isUploading"
          :class="{ 'p-invalid': errors.name }"
        />
        <small v-if="errors.name" class="p-error">{{ errors.name }}</small>
      </div>

      <!-- Service Description -->
      <div class="field">
        <label for="service-description">{{ $t('message.services.serviceDescription') }}</label>
        <Textarea
          id="service-description"
          v-model="serviceForm.description"
          class="w-full"
          :disabled="isUploading"
          rows="3"
        />
      </div>

      <!-- Pricing Configuration -->
      <div class="field">
        <label>{{ $t('message.services.pricing') }} *</label>
        <div class="pricing-config">
          <div class="pricing-row">
            <InputNumber
              v-model="serviceForm.pricingAmount"
              :min="0"
              :max-fraction-digits="2"
              :disabled="isUploading"
              :placeholder="$t('message.services.amount')"
              class="pricing-amount"
            />
            <span class="tokens-label">tokens</span>
          </div>

          <div class="pricing-row">
            <Select
              v-model="serviceForm.pricingType"
              :options="pricingTypes"
              optionLabel="label"
              optionValue="value"
              :disabled="isUploading"
              class="pricing-type"
            />

            <template v-if="serviceForm.pricingType === 'RECURRING'">
              <span class="per-label">per</span>
              <InputNumber
                v-model="serviceForm.pricingInterval"
                :min="1"
                :disabled="isUploading"
                class="pricing-interval"
              />
              <Select
                v-model="serviceForm.pricingUnit"
                :options="pricingUnits"
                optionLabel="label"
                optionValue="value"
                :disabled="isUploading"
                class="pricing-unit"
              />
            </template>
          </div>
        </div>
      </div>

      <!-- File Selection -->
      <div class="field">
        <label for="file-input">{{ $t('message.services.selectFile') }} *</label>
        <div class="file-input-wrapper">
          <input
            id="file-input"
            ref="fileInput"
            type="file"
            :disabled="isUploading"
            @change="handleFileSelect"
            style="display: none"
          />
          <Button
            :label="selectedFile ? selectedFile.name : $t('message.services.chooseFile')"
            icon="pi pi-file"
            :disabled="isUploading"
            @click="() => fileInput?.click()"
            class="p-button-outlined w-full"
          />
        </div>
        <small v-if="selectedFile" class="file-info">
          {{ formatBytes(selectedFile.size) }}
        </small>
        <small v-if="errors.file" class="p-error">{{ errors.file }}</small>
      </div>

      <!-- Upload Progress -->
      <div v-if="uploadProgress" class="upload-progress">
        <div class="progress-header">
          <span class="progress-label">{{ $t('message.services.uploadProgress') }}</span>
          <span class="progress-percentage">{{ uploadProgress.percentage.toFixed(1) }}%</span>
        </div>

        <ProgressBar :value="uploadProgress.percentage" :show-value="false" />

        <div class="progress-stats">
          <div class="stat">
            <i class="pi pi-upload"></i>
            <span>{{ formatBytes(uploadProgress.bytesUploaded) }} / {{ formatBytes(uploadProgress.totalBytes) }}</span>
          </div>
          <div class="stat" v-if="uploadProgress.speed > 0">
            <i class="pi pi-gauge"></i>
            <span>{{ formatBytes(uploadProgress.speed) }}/s</span>
          </div>
          <div class="stat" v-if="uploadProgress.eta > 0">
            <i class="pi pi-clock"></i>
            <span>{{ formatTime(uploadProgress.eta) }} remaining</span>
          </div>
        </div>

        <div class="progress-actions" v-if="!isCompleted && !hasError">
          <Button
            v-if="!isPaused"
            :label="$t('message.services.pause')"
            icon="pi pi-pause"
            class="p-button-sm p-button-warning"
            @click="pauseUpload"
          />
          <Button
            v-else
            :label="$t('message.services.resume')"
            icon="pi pi-play"
            class="p-button-sm p-button-success"
            @click="resumeUpload"
          />
          <Button
            :label="$t('message.common.cancel')"
            icon="pi pi-times"
            class="p-button-sm p-button-danger p-button-text"
            @click="cancelUpload"
          />
        </div>

        <div v-if="isCompleted" class="upload-complete">
          <i class="pi pi-check-circle"></i>
          <span>{{ $t('message.services.uploadComplete') }}</span>
        </div>

        <div v-if="hasError" class="upload-error">
          <i class="pi pi-exclamation-circle"></i>
          <span>{{ errorMessage || $t('message.services.uploadFailed') }}</span>
        </div>
      </div>
    </div>

    <template #footer>
      <Button
        :label="$t('message.common.cancel')"
        icon="pi pi-times"
        text
        @click="closeDialog"
        :disabled="isUploading && !isPaused && !hasError"
      />
      <Button
        :label="uploadProgress ? $t('message.services.startUpload') : $t('message.common.save')"
        icon="pi pi-check"
        @click="saveAndUpload"
        :disabled="!isFormValid || isUploading || isCompleted"
        :loading="isCreatingService"
      />
    </template>
  </Dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import InputNumber from 'primevue/inputnumber'
import Select from 'primevue/select'
import Button from 'primevue/button'
import ProgressBar from 'primevue/progressbar'
import { useServicesStore } from '../../stores/services'
import { useChunkedFileUpload } from '../../composables/useChunkedFileUpload'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'service-added'): void
}>()

const { t } = useI18n()
const toast = useToast()
const servicesStore = useServicesStore()
const {
  uploadProgress,
  isUploading,
  isPaused,
  isCompleted,
  hasError,
  upload,
  pause,
  resume,
  cancel,
  formatBytes,
  formatTime
} = useChunkedFileUpload()

// Form state
const serviceForm = ref({
  name: '',
  description: '',
  pricingAmount: 0,
  pricingType: 'ONE_TIME' as 'ONE_TIME' | 'RECURRING',
  pricingInterval: 1,
  pricingUnit: 'MONTHS' as 'SECONDS' | 'MINUTES' | 'HOURS' | 'DAYS' | 'WEEKS' | 'MONTHS' | 'YEARS'
})

const selectedFile = ref<File | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const errors = ref({
  name: '',
  file: ''
})
const isCreatingService = ref(false)
const errorMessage = ref('')
const createdServiceId = ref<number | null>(null)

// Pricing options
const pricingTypes = [
  { label: t('message.services.oneTime'), value: 'ONE_TIME' },
  { label: t('message.services.recurring'), value: 'RECURRING' }
]

const pricingUnits = [
  { label: t('message.services.seconds'), value: 'SECONDS' },
  { label: t('message.services.minutes'), value: 'MINUTES' },
  { label: t('message.services.hours'), value: 'HOURS' },
  { label: t('message.services.days'), value: 'DAYS' },
  { label: t('message.services.weeks'), value: 'WEEKS' },
  { label: t('message.services.months'), value: 'MONTHS' },
  { label: t('message.services.years'), value: 'YEARS' }
]

// Computed
const isFormValid = computed(() => {
  return serviceForm.value.name.trim() !== '' &&
    serviceForm.value.pricingAmount >= 0 &&
    selectedFile.value !== null
})

// Methods
const handleFileSelect = (event: Event) => {
  const target = event.target as HTMLInputElement
  if (target.files && target.files.length > 0) {
    selectedFile.value = target.files[0]
    errors.value.file = ''
  }
}

const validateForm = (): boolean => {
  errors.value = { name: '', file: '' }

  if (!serviceForm.value.name.trim()) {
    errors.value.name = t('message.services.nameRequired')
    return false
  }

  if (!selectedFile.value) {
    errors.value.file = t('message.services.fileRequired')
    return false
  }

  return true
}

const saveAndUpload = async () => {
  if (!validateForm()) return

  isCreatingService.value = true

  try {
    // Create service entry in database
    const newService = await servicesStore.addService({
      service_type: 'DATA',
      type: 'storage',
      name: serviceForm.value.name,
      description: serviceForm.value.description,
      endpoint: '', // Will be set after file upload
      capabilities: {},
      status: 'INACTIVE', // Inactive until upload completes
      pricing_amount: serviceForm.value.pricingAmount,
      pricing_type: serviceForm.value.pricingType,
      pricing_interval: serviceForm.value.pricingInterval,
      pricing_unit: serviceForm.value.pricingUnit
    })

    createdServiceId.value = newService.id!

    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.services.serviceCreated'),
      life: 3000
    })

    // Start file upload
    await upload({
      file: selectedFile.value!,
      serviceId: newService.id!,
      chunkSize: 1024 * 1024, // 1MB chunks
      onComplete: () => {
        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.services.uploadCompleteMessage'),
          life: 5000
        })
        emit('service-added')
        // Don't close dialog immediately - let user see completion
        setTimeout(() => {
          closeDialog()
        }, 2000)
      },
      onError: (error) => {
        errorMessage.value = error.message
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: t('message.services.uploadFailed'),
          life: 5000
        })
      }
    })
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || t('message.services.createFailed'),
      life: 5000
    })
  } finally {
    isCreatingService.value = false
  }
}

const pauseUpload = () => {
  pause()
}

const resumeUpload = () => {
  resume()
}

const cancelUpload = () => {
  cancel()
  toast.add({
    severity: 'warn',
    summary: t('message.common.warning'),
    detail: t('message.services.uploadCancelled'),
    life: 3000
  })
  closeDialog()
}

const resetForm = () => {
  serviceForm.value = {
    name: '',
    description: '',
    pricingAmount: 0,
    pricingType: 'ONE_TIME',
    pricingInterval: 1,
    pricingUnit: 'MONTHS'
  }
  selectedFile.value = null
  errors.value = { name: '', file: '' }
  errorMessage.value = ''
  createdServiceId.value = null
}

const closeDialog = () => {
  emit('update:visible', false)
}

// Watch for dialog visibility changes
watch(() => props.visible, (newVal) => {
  if (!newVal) {
    // Reset form when dialog is closed
    setTimeout(resetForm, 300)
  }
})
</script>

<style scoped lang="scss">
.data-service-form {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
  padding: 0.5rem 0;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;

  label {
    font-weight: 500;
    font-size: 0.9rem;
  }
}

.w-full {
  width: 100%;
}

.pricing-config {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.pricing-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;

  .pricing-amount {
    flex: 1;
  }

  .tokens-label {
    font-size: 0.9rem;
    color: var(--text-color-secondary);
  }

  .pricing-type {
    flex: 1;
  }

  .per-label {
    font-size: 0.9rem;
    color: var(--text-color-secondary);
  }

  .pricing-interval {
    width: 80px;
  }

  .pricing-unit {
    flex: 1;
  }
}

.file-input-wrapper {
  width: 100%;
}

.file-info {
  display: block;
  margin-top: 0.25rem;
  color: var(--text-color-secondary);
  font-size: 0.85rem;
}

.upload-progress {
  padding: 1rem;
  background: var(--surface-50);
  border-radius: 6px;
  border: 1px solid var(--surface-200);
}

.progress-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.75rem;

  .progress-label {
    font-weight: 500;
  }

  .progress-percentage {
    font-weight: 600;
    color: var(--primary-color);
  }
}

.progress-stats {
  display: flex;
  gap: 1.5rem;
  margin-top: 0.75rem;
  font-size: 0.85rem;

  .stat {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--text-color-secondary);

    i {
      color: var(--primary-color);
    }
  }
}

.progress-actions {
  display: flex;
  gap: 0.5rem;
  margin-top: 1rem;
}

.upload-complete {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-top: 1rem;
  padding: 0.75rem;
  background: var(--green-50);
  border-radius: 4px;
  color: var(--green-700);
  font-weight: 500;

  i {
    font-size: 1.25rem;
  }
}

.upload-error {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-top: 1rem;
  padding: 0.75rem;
  background: var(--red-50);
  border-radius: 4px;
  color: var(--red-700);
  font-weight: 500;

  i {
    font-size: 1.25rem;
  }
}

.p-error {
  color: var(--red-500);
  font-size: 0.85rem;
  margin-top: 0.25rem;
}

.p-invalid {
  border-color: var(--red-500);
}
</style>
