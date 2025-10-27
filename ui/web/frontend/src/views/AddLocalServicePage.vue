<template>
  <div class="add-service-page">
    <!-- Page Header -->
    <div class="page-header">
      <div class="header-content">
        <Button
          icon="pi pi-arrow-left"
          text
          @click="goBack"
          class="back-button"
          :label="$t('message.common.back')"
        />
        <h1>{{ $t('message.services.addLocalService') }}</h1>
      </div>
    </div>

    <!-- Wizard Content -->
    <div class="wizard-container">
      <Stepper v-model:value="activeStep" linear>
        <!-- Step 1: Basic Info -->
        <StepList>
          <Step value="1">{{ $t('message.services.wizard.basicInfo') }}</Step>
          <Step value="2">{{ $t('message.services.wizard.pricing') }}</Step>
          <Step value="3">{{ $t('message.services.wizard.serviceType') }}</Step>
          <Step value="4">{{ $t('message.services.wizard.configuration') }}</Step>
        </StepList>

        <StepPanels>
          <!-- Step 1: Basic Information -->
          <StepPanel value="1">
            <div class="wizard-step">
              <div class="field">
                <label for="service-name">{{ $t('message.services.serviceName') }} *</label>
                <InputText
                  id="service-name"
                  v-model="serviceData.name"
                  class="w-full"
                  :class="{ 'p-invalid': errors.name }"
                />
                <small v-if="errors.name" class="p-error">{{ errors.name }}</small>
              </div>

              <div class="field">
                <label for="service-description">{{ $t('message.services.serviceDescription') }}</label>
                <Textarea
                  id="service-description"
                  v-model="serviceData.description"
                  class="w-full"
                  rows="4"
                />
              </div>
            </div>

            <div class="wizard-navigation">
              <Button :label="$t('message.common.cancel')" severity="secondary" @click="goBack" />
              <Button :label="$t('message.services.wizard.next')" @click="validateAndNext(1)" />
            </div>
          </StepPanel>

          <!-- Step 2: Pricing -->
          <StepPanel value="2">
            <div class="wizard-step">
              <div class="field">
                <label>{{ $t('message.services.pricing') }} *</label>
                <div class="pricing-config">
                  <div class="pricing-row">
                    <InputNumber
                      v-model="serviceData.pricingAmount"
                      :min="0"
                      :maxFractionDigits="2"
                      :placeholder="$t('message.services.amount')"
                      class="pricing-amount"
                    />
                    <span class="tokens-label">tokens</span>
                  </div>

                  <div class="pricing-row">
                    <Select
                      v-model="serviceData.pricingType"
                      :options="pricingTypes"
                      optionLabel="label"
                      optionValue="value"
                      class="pricing-type"
                    />

                    <template v-if="serviceData.pricingType === 'RECURRING'">
                      <span class="per-label">per</span>
                      <InputNumber
                        v-model="serviceData.pricingInterval"
                        :min="1"
                        class="pricing-interval"
                      />
                      <Select
                        v-model="serviceData.pricingUnit"
                        :options="pricingUnits"
                        optionLabel="label"
                        optionValue="value"
                        class="pricing-unit"
                      />
                    </template>
                  </div>
                </div>
              </div>
            </div>

            <div class="wizard-navigation">
              <Button :label="$t('message.services.wizard.previous')" severity="secondary" @click="activeStep = '1'" />
              <Button :label="$t('message.services.wizard.next')" @click="validateAndNext(2)" />
            </div>
          </StepPanel>

          <!-- Step 3: Service Type Selection -->
          <StepPanel value="3">
            <div class="wizard-step">
              <div class="field">
                <label>{{ $t('message.services.wizard.selectType') }} *</label>
                <div class="service-type-selection">
                  <div
                    v-for="type in serviceTypes"
                    :key="type.value"
                    class="service-type-card"
                    :class="{ 'selected': serviceData.serviceType === type.value }"
                    @click="serviceData.serviceType = type.value"
                  >
                    <i :class="type.icon" class="type-icon"></i>
                    <div class="type-label">{{ type.label }}</div>
                    <div class="type-description">{{ type.description }}</div>
                  </div>
                </div>
                <small v-if="errors.serviceType" class="p-error">{{ errors.serviceType }}</small>
              </div>
            </div>

            <div class="wizard-navigation">
              <Button :label="$t('message.services.wizard.previous')" severity="secondary" @click="activeStep = '2'" />
              <Button :label="$t('message.services.wizard.next')" @click="validateAndNext(3)" />
            </div>
          </StepPanel>

          <!-- Step 4: Type-Specific Configuration -->
          <StepPanel value="4">
            <div class="wizard-step">
              <!-- DATA Service Configuration -->
              <div v-if="serviceData.serviceType === 'DATA'" class="config-section">
                <h4>{{ $t('message.services.wizard.dataConfig') }}</h4>

                <div class="field">
                  <label>{{ $t('message.services.wizard.selectFiles') }} *</label>
                  <div class="file-selection">
                    <SplitButton
                      :label="selectedFiles.length > 0
                        ? `${$t('message.services.wizard.chooseFiles')} (${selectedFiles.length})`
                        : $t('message.services.wizard.chooseFiles')"
                      icon="pi pi-file"
                      @click="openFileDialog('files')"
                      :model="filePickerMenuItems"
                      class="file-button"
                    />
                    <Button
                      v-if="selectedFiles.length > 0"
                      label="Clear All"
                      icon="pi pi-times"
                      text
                      severity="danger"
                      @click="clearFiles"
                      class="clear-button"
                    />
                  </div>
                  <input
                    ref="fileInput"
                    type="file"
                    multiple
                    @change="handleFileSelect"
                    style="display: none"
                  />
                  <input
                    ref="folderInput"
                    type="file"
                    webkitdirectory
                    @change="handleFileSelect"
                    style="display: none"
                  />
                  <div v-if="selectedFiles.length > 0" class="selected-files">
                    <div class="file-count">{{ selectedFiles.length }} {{ $t('message.services.wizard.filesSelected') }}</div>
                    <div class="file-list">
                      <div v-for="(file, index) in selectedFiles.slice(0, 5)" :key="index" class="file-item">
                        <i class="pi pi-file"></i>
                        <span>{{ file.name }}</span>
                        <span class="file-size">({{ formatBytes(file.size) }})</span>
                      </div>
                      <div v-if="selectedFiles.length > 5" class="more-files">
                        {{ $t('message.services.wizard.andMore', { count: selectedFiles.length - 5 }) }}
                      </div>
                    </div>
                  </div>
                  <small v-if="errors.files" class="p-error">{{ errors.files }}</small>
                </div>
              </div>

              <!-- DOCKER Service Configuration -->
              <div v-else-if="serviceData.serviceType === 'DOCKER'" class="config-section">
                <h4>{{ $t('message.services.wizard.dockerConfig') }}</h4>

                <div class="field">
                  <label for="docker-image">{{ $t('message.services.wizard.dockerImage') }} *</label>
                  <InputText
                    id="docker-image"
                    v-model="serviceData.dockerImage"
                    :placeholder="$t('message.services.wizard.dockerImagePlaceholder')"
                    class="w-full"
                  />
                </div>

                <div class="field">
                  <label for="docker-ports">{{ $t('message.services.wizard.ports') }}</label>
                  <InputText
                    id="docker-ports"
                    v-model="serviceData.dockerPorts"
                    :placeholder="$t('message.services.wizard.portsPlaceholder')"
                    class="w-full"
                  />
                  <small class="field-help">{{ $t('message.services.wizard.portsHelp') }}</small>
                </div>

                <div class="field">
                  <label for="docker-volumes">{{ $t('message.services.wizard.volumes') }}</label>
                  <Textarea
                    id="docker-volumes"
                    v-model="serviceData.dockerVolumes"
                    :placeholder="$t('message.services.wizard.volumesPlaceholder')"
                    class="w-full"
                    rows="3"
                  />
                </div>

                <div class="field">
                  <label for="docker-env">{{ $t('message.services.wizard.environment') }}</label>
                  <Textarea
                    id="docker-env"
                    v-model="serviceData.dockerEnv"
                    :placeholder="$t('message.services.wizard.envPlaceholder')"
                    class="w-full"
                    rows="3"
                  />
                </div>
              </div>

              <!-- STANDALONE Service Configuration -->
              <div v-else-if="serviceData.serviceType === 'STANDALONE'" class="config-section">
                <h4>{{ $t('message.services.wizard.standaloneConfig') }}</h4>

                <div class="field">
                  <label for="standalone-command">{{ $t('message.services.wizard.command') }} *</label>
                  <InputText
                    id="standalone-command"
                    v-model="serviceData.standaloneCommand"
                    :placeholder="$t('message.services.wizard.commandPlaceholder')"
                    class="w-full"
                  />
                </div>

                <div class="field">
                  <label for="standalone-args">{{ $t('message.services.wizard.arguments') }}</label>
                  <InputText
                    id="standalone-args"
                    v-model="serviceData.standaloneArgs"
                    :placeholder="$t('message.services.wizard.argsPlaceholder')"
                    class="w-full"
                  />
                </div>

                <div class="field">
                  <label for="standalone-workdir">{{ $t('message.services.wizard.workingDirectory') }}</label>
                  <InputText
                    id="standalone-workdir"
                    v-model="serviceData.standaloneWorkdir"
                    :placeholder="$t('message.services.wizard.workdirPlaceholder')"
                    class="w-full"
                  />
                </div>

                <div class="field">
                  <label for="standalone-env">{{ $t('message.services.wizard.environment') }}</label>
                  <Textarea
                    id="standalone-env"
                    v-model="serviceData.standaloneEnv"
                    :placeholder="$t('message.services.wizard.envPlaceholder')"
                    class="w-full"
                    rows="3"
                  />
                </div>
              </div>
            </div>

            <div class="wizard-navigation">
              <Button :label="$t('message.services.wizard.previous')" severity="secondary" @click="activeStep = '3'" />
              <Button
                :label="isCreating ? (currentFileIndex > 0 ? `Uploading file ${currentFileIndex}/${totalFilesCount}...` : 'Creating...') : $t('message.services.wizard.finish')"
                @click="finishWizard"
                :loading="isCreating"
              />
            </div>
          </StepPanel>
        </StepPanels>
      </Stepper>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'

import Stepper from 'primevue/stepper'
import StepList from 'primevue/steplist'
import Step from 'primevue/step'
import StepPanels from 'primevue/steppanels'
import StepPanel from 'primevue/steppanel'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import InputNumber from 'primevue/inputnumber'
import Select from 'primevue/select'
import Button from 'primevue/button'
import SplitButton from 'primevue/splitbutton'

import { useServicesStore } from '../stores/services'
import { useChunkedFileUpload } from '../composables/useChunkedFileUpload'

const router = useRouter()
const { t } = useI18n()
const toast = useToast()
const servicesStore = useServicesStore()
const { uploadMultipleFiles, uploadProgress, currentFileIndex, totalFilesCount } = useChunkedFileUpload()

// Wizard state
const activeStep = ref('1')
const isCreating = ref(false)

// Service data
const serviceData = ref({
  name: '',
  description: '',
  pricingAmount: 0,
  pricingType: 'ONE_TIME' as 'ONE_TIME' | 'RECURRING',
  pricingInterval: 1,
  pricingUnit: 'MONTHS' as 'SECONDS' | 'MINUTES' | 'HOURS' | 'DAYS' | 'WEEKS' | 'MONTHS' | 'YEARS',
  serviceType: '' as 'DATA' | 'DOCKER' | 'STANDALONE' | '',
  // DATA specific
  files: [] as File[],
  // DOCKER specific
  dockerImage: '',
  dockerPorts: '',
  dockerVolumes: '',
  dockerEnv: '',
  // STANDALONE specific
  standaloneCommand: '',
  standaloneArgs: '',
  standaloneWorkdir: '',
  standaloneEnv: ''
})

const errors = ref({
  name: '',
  serviceType: '',
  files: ''
})

const selectedFiles = ref<File[]>([])
const fileInput = ref<HTMLInputElement | null>(null)
const folderInput = ref<HTMLInputElement | null>(null)

// Pricing options
const pricingTypes = computed(() => [
  { label: t('message.services.oneTime'), value: 'ONE_TIME' },
  { label: t('message.services.recurring'), value: 'RECURRING' }
])

const pricingUnits = computed(() => [
  { label: t('message.services.seconds'), value: 'SECONDS' },
  { label: t('message.services.minutes'), value: 'MINUTES' },
  { label: t('message.services.hours'), value: 'HOURS' },
  { label: t('message.services.days'), value: 'DAYS' },
  { label: t('message.services.weeks'), value: 'WEEKS' },
  { label: t('message.services.months'), value: 'MONTHS' },
  { label: t('message.services.years'), value: 'YEARS' }
])

// Service types with descriptions
const serviceTypes = computed(() => [
  {
    value: 'DATA',
    label: t('message.services.types.data'),
    description: t('message.services.wizard.dataDescription'),
    icon: 'pi pi-database'
  },
  {
    value: 'DOCKER',
    label: t('message.services.types.docker'),
    description: t('message.services.wizard.dockerDescription'),
    icon: 'pi pi-box'
  },
  {
    value: 'STANDALONE',
    label: t('message.services.types.standalone'),
    description: t('message.services.wizard.standaloneDescription'),
    icon: 'pi pi-cog'
  }
])

// File picker menu items for SplitButton dropdown
const filePickerMenuItems = computed(() => [
  {
    label: t('message.services.wizard.chooseFolder'),
    icon: 'pi pi-folder',
    command: () => openFileDialog('folder')
  }
])

// Helper functions
function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 Bytes'
  const k = 1024
  const sizes = ['Bytes', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i]
}

function openFileDialog(type: 'files' | 'folder') {
  if (type === 'files' && fileInput.value) {
    fileInput.value.click()
  } else if (type === 'folder' && folderInput.value) {
    folderInput.value.click()
  }
}

function handleFileSelect(event: Event) {
  const target = event.target as HTMLInputElement
  if (target.files) {
    // Append new files to existing selection
    const newFiles = Array.from(target.files)
    selectedFiles.value = [...selectedFiles.value, ...newFiles]
    errors.value.files = ''

    // Reset input value to allow re-selecting same files
    target.value = ''
  }
}

function clearFiles() {
  selectedFiles.value = []
  if (fileInput.value) fileInput.value.value = ''
  if (folderInput.value) folderInput.value.value = ''
}

function validateStep(step: number): boolean {
  errors.value = { name: '', serviceType: '', files: '' }

  if (step === 1) {
    if (!serviceData.value.name.trim()) {
      errors.value.name = t('message.services.nameRequired')
      return false
    }
  }

  if (step === 3) {
    if (!serviceData.value.serviceType) {
      errors.value.serviceType = t('message.services.wizard.typeRequired')
      return false
    }
  }

  return true
}

function validateAndNext(currentStep: number) {
  if (validateStep(currentStep)) {
    activeStep.value = String(currentStep + 1)
  }
}

async function finishWizard() {
  // Validate based on service type
  if (serviceData.value.serviceType === 'DATA' && selectedFiles.value.length === 0) {
    errors.value.files = t('message.services.fileRequired')
    return
  }

  isCreating.value = true

  try {
    if (serviceData.value.serviceType === 'DATA') {
      // Create DATA service entry in database
      const newService = await servicesStore.addService({
        service_type: 'DATA',
        type: 'storage',
        name: serviceData.value.name,
        description: serviceData.value.description,
        endpoint: '', // Will be set after file upload
        capabilities: {},
        status: 'INACTIVE', // Inactive until upload completes
        pricing_amount: serviceData.value.pricingAmount,
        pricing_type: serviceData.value.pricingType,
        pricing_interval: serviceData.value.pricingInterval,
        pricing_unit: serviceData.value.pricingUnit
      })

      toast.add({
        severity: 'success',
        summary: t('message.common.success'),
        detail: t('message.services.serviceCreated'),
        life: 3000
      })

      // Upload files sequentially with path preservation
      await uploadMultipleFiles({
        files: selectedFiles.value,
        serviceId: newService.id!,
        chunkSize: 1024 * 1024, // 1MB chunks
        onComplete: () => {
          toast.add({
            severity: 'success',
            summary: t('message.common.success'),
            detail: t('message.services.uploadCompleteMessage'),
            life: 5000
          })
          // Navigate back to services page
          setTimeout(() => {
            router.push('/services')
          }, 2000)
        },
        onError: (error) => {
          toast.add({
            severity: 'error',
            summary: t('message.common.error'),
            detail: `Upload failed: ${error.message}`,
            life: 5000
          })
        }
      })
    } else {
      // DOCKER and STANDALONE service types
      toast.add({
        severity: 'info',
        summary: t('message.common.info'),
        detail: `${serviceData.value.serviceType} service creation coming soon`,
        life: 3000
      })
      router.push('/services')
    }
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || t('message.services.createFailed'),
      life: 5000
    })
  } finally {
    isCreating.value = false
  }
}

function goBack() {
  router.push('/services')
}
</script>

<style scoped lang="scss">
@use '../scss/variables' as vars;

.add-service-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  overflow: hidden;
  padding: vars.$spacing-lg;
  max-width: 1200px;
  margin: 0 auto;
  width: 100%;
}

.page-header {
  flex-shrink: 0;
  margin-bottom: vars.$spacing-xl;

  .header-content {
    display: flex;
    align-items: center;
    gap: vars.$spacing-md;

    h1 {
      font-size: vars.$font-size-xxl;
      font-weight: 600;
      color: vars.$color-text;
      margin: 0;
    }

    .back-button {
      color: vars.$color-primary;
    }
  }
}

.wizard-container {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: auto;
  width: 100%;

  :deep(.p-stepper) {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  :deep(.p-stepper-panels) {
    flex: 1;
    overflow: auto;
  }
}

// Override PrimeVue Stepper styling
:deep(.p-steppanel) {
  background-color: rgb(49, 64, 92);
  border-radius: 4px;
  padding: 1.5rem;
  color: #fff;
}

:deep(.p-stepper) {
  background: transparent;
}

:deep(.p-step) {
  background-color: rgb(49, 64, 92);

  &.p-step-active {
    background-color: rgb(205, 81, 36);
  }
}

:deep(.p-step-title) {
  color: rgba(240, 240, 240, 0.9);
  font-weight: 500;
}

:deep(.p-step-active .p-step-title) {
  color: white !important;
  font-weight: 600;
}

:deep(.p-step-active .p-step-number) {
  color: #4060c3 !important;
}

// Style completed step separators (steps before active step)
:deep(.p-step:has(~ .p-step-active) .p-stepper-separator) {
  background-color: #4060c3 !important;
}

// Button styling to match navigation theme
:deep(.p-button:not(.p-button-secondary):not(.p-button-text):not(.p-button-outlined)) {
  --p-panelmenu-item-active-background: #4060c3;
  background-color: var(--p-panelmenu-item-active-background) !important;
  border-color: var(--p-panelmenu-item-active-background) !important;

  &:hover:not(:disabled) {
    background-color: #5070d3 !important;
    border-color: #5070d3 !important;
  }
}

// Select dropdown styling to match dark theme
:deep(.p-select) {
  background-color: rgb(38, 49, 65) !important;
  border-color: rgb(49, 64, 92) !important;
  color: #fff !important;

  &:hover {
    border-color: #4060c3 !important;
  }

  &:focus {
    border-color: #4060c3 !important;
    box-shadow: 0 0 0 0.2rem rgba(64, 96, 195, 0.25) !important;
  }
}

:deep(.p-select-label) {
  color: rgb(240, 240, 240) !important;
}

:deep(.p-select-dropdown-icon) {
  color: rgb(240, 240, 240) !important;
}

:deep(.p-select-overlay) {
  background-color: rgb(38, 49, 65) !important;
  border-color: rgb(49, 64, 92) !important;
}

:deep(.p-select-overlay .p-select-list) {
  background-color: rgb(38, 49, 65) !important;
}

:deep(.p-select-overlay .p-select-list-container) {
  background-color: rgb(38, 49, 65) !important;
}

:deep(.p-select-option) {
  color: #fff !important;
  background-color: transparent !important;

  &:hover {
    background-color: rgb(49, 64, 92) !important;
  }

  &.p-select-option-selected {
    background-color: #4060c3 !important;
  }
}

// TieredMenu styling (used by SplitButton dropdown)
:deep(.p-tieredmenu) {
  background-color: rgb(38, 49, 65) !important;
  border: 1px solid rgb(49, 64, 92) !important;
  color: rgb(240, 240, 240) !important;
}

:deep(.p-tieredmenu-item-content) {
  color: rgb(240, 240, 240) !important;

  &:hover {
    background-color: rgb(49, 64, 92) !important;
  }
}

:deep(.p-tieredmenu-item-link) {
  color: rgb(240, 240, 240) !important;

  &:hover {
    background-color: rgb(49, 64, 92) !important;
  }
}

:deep(.p-tieredmenu-item-icon) {
  color: rgb(205, 81, 36) !important;
}

:deep(.p-tieredmenu-separator) {
  border-color: rgb(49, 64, 92) !important;
}

// Better visibility for error messages
:deep(.p-error) {
  color: #ff5252 !important;
  font-weight: 500;
}

:deep(.p-invalid) {
  border-color: #ff5252 !important;
}

// Style secondary buttons to match standard orange buttons
:deep(.p-button-secondary) {
  background-color: rgb(205, 81, 36) !important;
  color: #fff !important;
  border: none !important;

  &:hover:not(:disabled) {
    background-color: rgb(246, 114, 66) !important;
  }

  &:disabled {
    background-color: #333333 !important;
    cursor: not-allowed;
  }
}

.wizard-step {
  min-height: 300px;
  padding: 1rem 0;

  .field {
    margin-bottom: 1.5rem;

    label {
      display: block;
      font-weight: 500;
      margin-bottom: 0.5rem;
    }

    .field-help {
      display: block;
      margin-top: 0.25rem;
      font-size: 0.85rem;
      color: vars.$color-text-secondary;
    }
  }
}

.wizard-navigation {
  display: flex;
  justify-content: space-between;
  padding-top: 1.5rem;
  border-top: 1px solid rgba(vars.$color-primary, 0.2);
  margin-top: 1.5rem;
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
    color: vars.$color-text-secondary;
  }

  .pricing-type {
    flex: 1;
  }

  .per-label {
    font-size: 0.9rem;
    color: vars.$color-text-secondary;
  }

  .pricing-interval {
    width: 80px;
  }

  .pricing-unit {
    flex: 1;
  }
}

.service-type-selection {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 1rem;
  margin-top: 1rem;
}

.service-type-card {
  padding: 1.5rem;
  border: 2px solid rgb(49, 64, 92);
  border-radius: 8px;
  background-color: rgb(38, 49, 65);
  cursor: pointer;
  transition: all 0.2s ease;
  text-align: center;

  &:hover {
    background-color: rgb(49, 64, 92);
    border-color: rgba(vars.$color-primary, 0.5);
  }

  &.selected {
    border-color: vars.$color-primary;
    background-color: rgb(49, 64, 92);
  }

  .type-icon {
    font-size: 2.5rem;
    color: vars.$color-primary;
    margin-bottom: 0.5rem;
  }

  .type-label {
    font-size: 1.1rem;
    font-weight: 600;
    margin-bottom: 0.5rem;
  }

  .type-description {
    font-size: 0.85rem;
    color: vars.$color-text-secondary;
  }
}

.config-section {
  h4 {
    color: vars.$color-primary;
    margin-bottom: 1rem;
  }
}

.file-selection {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  margin-bottom: 1rem;

  .file-button {
    flex: 1;
  }

  .clear-button {
    flex-shrink: 0;
  }
}

.selected-files {
  margin-top: 1rem;
  padding: 1rem;
  background-color: rgb(38, 49, 65);
  border-radius: 4px;

  .file-count {
    font-weight: 500;
    margin-bottom: 0.5rem;
    color: vars.$color-primary;
  }

  .file-list {
    max-height: 200px;
    overflow-y: auto;
  }

  .file-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0;
    font-size: 0.9rem;

    i {
      color: vars.$color-primary;
    }

    .file-size {
      color: vars.$color-text-secondary;
      font-size: 0.85rem;
    }
  }

  .more-files {
    padding: 0.5rem 0;
    font-style: italic;
    color: vars.$color-text-secondary;
  }
}

.p-error {
  color: var(--red-500);
  font-size: 0.85rem;
  display: block;
  margin-top: 0.25rem;
}

.p-invalid {
  border-color: var(--red-500);
}
</style>
