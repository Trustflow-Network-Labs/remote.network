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
                <label for="pricing-amount">{{ $t('message.services.servicePrice') }} *</label>
                <div class="pricing-row">
                  <InputNumber
                    id="pricing-amount"
                    v-model="serviceData.pricingAmount"
                    :min="0"
                    :maxFractionDigits="6"
                    placeholder="0.00"
                    class="pricing-amount"
                  />
                  <span class="pricing-currency-label">USDC</span>
                </div>
                <small class="field-help">Price in USDC for this service</small>
                <Message v-if="serviceData.pricingAmount > 0 && !hasWallets" severity="warn" :closable="false" style="margin-top: 0.5rem;">
                  <div style="display: flex; align-items: flex-start; gap: 0.75rem;">
                    <i class="pi pi-exclamation-triangle" style="font-size: 1.25rem;"></i>
                    <div>
                      <strong>No wallet configured</strong>
                      <p style="margin: 0.25rem 0 0 0; font-size: 0.9rem;">
                        You need at least one wallet to receive payments for this paid service.
                        <router-link to="/wallets" style="color: inherit; text-decoration: underline;">Create a wallet</router-link> first.
                      </p>
                    </div>
                  </div>
                </Message>
              </div>

              <div class="field" style="margin-top: 1rem;">
                <label>{{ $t('message.services.pricingType') }} *</label>
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
                    @click="serviceData.serviceType = type.value as 'DATA' | 'DOCKER' | 'STANDALONE'"
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

                <!-- Docker Source Selection -->
                <div class="field">
                  <label>{{ $t('message.services.wizard.dockerSource') }} *</label>
                  <div class="docker-source-selection">
                    <div
                      v-for="source in dockerSources"
                      :key="source.value"
                      class="docker-source-card"
                      :class="{ 'selected': serviceData.dockerSource === source.value }"
                      @click="serviceData.dockerSource = source.value as 'registry' | 'git' | 'local'"
                    >
                      <i :class="source.icon" class="source-icon"></i>
                      <div class="source-label">{{ source.label }}</div>
                    </div>
                  </div>
                </div>

                <!-- Registry Configuration -->
                <div v-if="serviceData.dockerSource === 'registry'" class="source-config">
                  <div class="field">
                    <label for="docker-image">{{ $t('message.services.wizard.imageName') }} *</label>
                    <InputText
                      id="docker-image"
                      v-model="serviceData.dockerImageName"
                      :placeholder="$t('message.services.wizard.imageNamePlaceholder')"
                      class="w-full"
                      :class="{ 'p-invalid': errors.dockerImageName }"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.imageNameHelp') }}</small>
                    <small v-if="errors.dockerImageName" class="p-error">{{ errors.dockerImageName }}</small>
                  </div>

                  <div class="field">
                    <label for="docker-tag">{{ $t('message.services.wizard.imageTag') }}</label>
                    <InputText
                      id="docker-tag"
                      v-model="serviceData.dockerImageTag"
                      :placeholder="$t('message.services.wizard.imageTagPlaceholder')"
                      class="w-full"
                    />
                  </div>

                  <div class="field">
                    <label for="docker-username">{{ $t('message.services.wizard.registryUsername') }}</label>
                    <InputText
                      id="docker-username"
                      v-model="serviceData.dockerUsername"
                      :placeholder="$t('message.services.wizard.usernameOptional')"
                      class="w-full"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.registryAuthHelp') }}</small>
                  </div>

                  <div class="field">
                    <label for="docker-password">{{ $t('message.services.wizard.registryPassword') }}</label>
                    <InputText
                      id="docker-password"
                      v-model="serviceData.dockerPassword"
                      type="password"
                      :placeholder="$t('message.services.wizard.passwordOptional')"
                      class="w-full"
                    />
                  </div>
                </div>

                <!-- Git Repository Configuration -->
                <div v-else-if="serviceData.dockerSource === 'git'" class="source-config">
                  <div class="field">
                    <label for="git-repo">{{ $t('message.services.wizard.gitRepoUrl') }} *</label>
                    <InputText
                      id="git-repo"
                      v-model="serviceData.dockerGitRepo"
                      :placeholder="$t('message.services.wizard.gitRepoPlaceholder')"
                      class="w-full"
                      :class="{ 'p-invalid': errors.dockerGitRepo }"
                    />
                    <small v-if="errors.dockerGitRepo" class="p-error">{{ errors.dockerGitRepo }}</small>
                  </div>

                  <div class="field">
                    <label for="git-branch">{{ $t('message.services.wizard.gitBranch') }}</label>
                    <InputText
                      id="git-branch"
                      v-model="serviceData.dockerGitBranch"
                      :placeholder="$t('message.services.wizard.gitBranchPlaceholder')"
                      class="w-full"
                    />
                  </div>

                  <div class="field">
                    <label for="git-username">{{ $t('message.services.wizard.gitUsername') }}</label>
                    <InputText
                      id="git-username"
                      v-model="serviceData.dockerGitUsername"
                      :placeholder="$t('message.services.wizard.usernameOptional')"
                      class="w-full"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.gitAuthHelp') }}</small>
                  </div>

                  <div class="field">
                    <label for="git-password">{{ $t('message.services.wizard.gitPassword') }}</label>
                    <InputText
                      id="git-password"
                      v-model="serviceData.dockerGitPassword"
                      type="password"
                      :placeholder="$t('message.services.wizard.passwordTokenOptional')"
                      class="w-full"
                    />
                  </div>
                </div>

                <!-- Local Directory Configuration -->
                <div v-else-if="serviceData.dockerSource === 'local'" class="source-config">
                  <div class="field">
                    <label for="local-path">{{ $t('message.services.wizard.localPath') }} *</label>
                    <InputText
                      id="local-path"
                      v-model="serviceData.dockerLocalPath"
                      :placeholder="$t('message.services.wizard.localPathPlaceholder')"
                      class="w-full"
                      :class="{ 'p-invalid': errors.dockerLocalPath }"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.localPathHelp') }}</small>
                    <small v-if="errors.dockerLocalPath" class="p-error">{{ errors.dockerLocalPath }}</small>
                  </div>
                </div>
              </div>

              <!-- STANDALONE Service Configuration -->
              <div v-else-if="serviceData.serviceType === 'STANDALONE'" class="config-section">
                <h4>{{ $t('message.services.wizard.standaloneConfig') }}</h4>

                <!-- Standalone Source Selection -->
                <div class="field">
                  <label>{{ $t('message.services.wizard.standaloneSource') }} *</label>
                  <div class="standalone-source-selection">
                    <div
                      v-for="source in standaloneSources"
                      :key="source.value"
                      class="standalone-source-card"
                      :class="{ 'selected': serviceData.standaloneSource === source.value }"
                      @click="serviceData.standaloneSource = source.value as 'local' | 'upload' | 'git'"
                    >
                      <i :class="source.icon" class="source-icon"></i>
                      <div class="source-label">{{ source.label }}</div>
                    </div>
                  </div>
                </div>

                <!-- Local Executable Configuration -->
                <div v-if="serviceData.standaloneSource === 'local'" class="source-config">
                  <div class="field">
                    <label for="standalone-exec-path">{{ $t('message.services.wizard.executablePath') }} *</label>
                    <InputText
                      id="standalone-exec-path"
                      v-model="serviceData.standaloneExecutablePath"
                      :placeholder="$t('message.services.wizard.executablePathPlaceholder')"
                      class="w-full"
                      :class="{ 'p-invalid': errors.standaloneExecutablePath }"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.executablePathHelp') }}</small>
                    <small v-if="errors.standaloneExecutablePath" class="p-error">{{ errors.standaloneExecutablePath }}</small>
                  </div>
                </div>

                <!-- Upload Executable Configuration -->
                <div v-else-if="serviceData.standaloneSource === 'upload'" class="source-config">
                  <div class="field">
                    <label>{{ $t('message.services.wizard.uploadExecutable') }} *</label>
                    <div class="file-selection">
                      <Button
                        :label="standaloneExecutableFile
                          ? standaloneExecutableFile.name
                          : $t('message.services.wizard.chooseExecutable')"
                        icon="pi pi-file"
                        @click="openExecutableFileDialog"
                        class="file-button"
                      />
                      <Button
                        v-if="standaloneExecutableFile"
                        label="Clear"
                        icon="pi pi-times"
                        text
                        severity="danger"
                        @click="clearExecutableFile"
                        class="clear-button"
                      />
                    </div>
                    <input
                      ref="executableFileInput"
                      type="file"
                      @change="handleExecutableFileSelect"
                      style="display: none"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.uploadExecutableHelp') }}</small>
                    <small v-if="errors.standaloneExecutableFile" class="p-error">{{ errors.standaloneExecutableFile }}</small>
                  </div>
                </div>

                <!-- Git Repository Configuration -->
                <div v-else-if="serviceData.standaloneSource === 'git'" class="source-config">
                  <div class="field">
                    <label for="standalone-git-repo">{{ $t('message.services.wizard.gitRepoUrl') }} *</label>
                    <InputText
                      id="standalone-git-repo"
                      v-model="serviceData.standaloneGitRepo"
                      :placeholder="$t('message.services.wizard.gitRepoPlaceholder')"
                      class="w-full"
                      :class="{ 'p-invalid': errors.standaloneGitRepo }"
                    />
                    <small v-if="errors.standaloneGitRepo" class="p-error">{{ errors.standaloneGitRepo }}</small>
                  </div>

                  <div class="field">
                    <label for="standalone-git-branch">{{ $t('message.services.wizard.gitBranch') }}</label>
                    <InputText
                      id="standalone-git-branch"
                      v-model="serviceData.standaloneGitBranch"
                      :placeholder="$t('message.services.wizard.gitBranchPlaceholder')"
                      class="w-full"
                    />
                  </div>

                  <div class="field">
                    <label for="standalone-exec-path-git">{{ $t('message.services.wizard.executablePathInRepo') }} *</label>
                    <InputText
                      id="standalone-exec-path-git"
                      v-model="serviceData.standaloneExecutablePathGit"
                      :placeholder="$t('message.services.wizard.executablePathGitPlaceholder')"
                      class="w-full"
                      :class="{ 'p-invalid': errors.standaloneExecutablePathGit }"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.executablePathGitHelp') }}</small>
                    <small v-if="errors.standaloneExecutablePathGit" class="p-error">{{ errors.standaloneExecutablePathGit }}</small>
                  </div>

                  <div class="field">
                    <label for="standalone-build-cmd">{{ $t('message.services.wizard.buildCommand') }}</label>
                    <InputText
                      id="standalone-build-cmd"
                      v-model="serviceData.standaloneBuildCommand"
                      :placeholder="$t('message.services.wizard.buildCommandPlaceholder')"
                      class="w-full"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.buildCommandHelp') }}</small>
                  </div>

                  <div class="field">
                    <label for="standalone-git-username">{{ $t('message.services.wizard.gitUsername') }}</label>
                    <InputText
                      id="standalone-git-username"
                      v-model="serviceData.standaloneGitUsername"
                      :placeholder="$t('message.services.wizard.usernameOptional')"
                      class="w-full"
                    />
                  </div>

                  <div class="field">
                    <label for="standalone-git-password">{{ $t('message.services.wizard.gitPassword') }}</label>
                    <InputText
                      id="standalone-git-password"
                      v-model="serviceData.standaloneGitPassword"
                      type="password"
                      :placeholder="$t('message.services.wizard.passwordTokenOptional')"
                      class="w-full"
                    />
                  </div>
                </div>

                <!-- Common Configuration for All Sources -->
                <div class="common-config">
                  <h5>{{ $t('message.services.wizard.executionConfig') }}</h5>

                  <div class="field">
                    <label for="standalone-args">{{ $t('message.services.wizard.arguments') }}</label>
                    <InputText
                      id="standalone-args"
                      v-model="serviceData.standaloneArgs"
                      :placeholder="$t('message.services.wizard.argsPlaceholder')"
                      class="w-full"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.argsHelp') }}</small>
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
                    <small class="field-help">{{ $t('message.services.wizard.envHelp') }}</small>
                  </div>

                  <div class="field">
                    <label for="standalone-timeout">{{ $t('message.services.wizard.timeout') }}</label>
                    <InputNumber
                      id="standalone-timeout"
                      v-model="serviceData.standaloneTimeout"
                      :min="1"
                      :max="3600"
                      :placeholder="$t('message.services.wizard.timeoutPlaceholder')"
                      class="w-full"
                    />
                    <small class="field-help">{{ $t('message.services.wizard.timeoutHelp') }}</small>
                  </div>
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

    <!-- Docker Progress Modal -->
    <DockerProgressModal
      ref="dockerProgressModal"
      v-model:visible="showDockerProgress"
      :service-name="serviceData.name"
      :image-name="serviceData.dockerSource === 'registry' ? `${serviceData.dockerImageName}:${serviceData.dockerImageTag}` : serviceData.dockerSource === 'git' ? serviceData.dockerGitRepo : serviceData.dockerLocalPath"
      @close="showDockerProgress = false"
    />

    <!-- Interface Review Dialog -->
    <InterfaceReviewDialog
      v-model:visible="showInterfaceDialog"
      :service-name="serviceData.name"
      :image-name="dockerServiceResponse?.service?.name || ''"
      :suggested-interfaces="dockerServiceResponse?.suggested_interfaces || []"
      :entrypoint="dockerServiceResponse?.service?.entrypoint ? JSON.parse(dockerServiceResponse.service.entrypoint) : null"
      :cmd="dockerServiceResponse?.service?.cmd ? JSON.parse(dockerServiceResponse.service.cmd) : null"
      @confirm="handleInterfaceConfirm"
      @cancel="handleInterfaceCancel"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
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
import Message from 'primevue/message'

import { useServicesStore } from '../stores/services'
import { useWalletsStore } from '../stores/wallets'
import { useChunkedFileUpload } from '../composables/useChunkedFileUpload'
import { useWebSocket } from '../composables/useWebSocket'
import { api } from '../services/api'
import InterfaceReviewDialog from '../components/services/InterfaceReviewDialog.vue'
import DockerProgressModal from '../components/services/DockerProgressModal.vue'

const router = useRouter()
const { t } = useI18n()
const toast = useToast()
const servicesStore = useServicesStore()
const walletsStore = useWalletsStore()
const { uploadMultipleFiles, currentFileIndex, totalFilesCount } = useChunkedFileUpload()
const { subscribe, MessageType } = useWebSocket()

// Wizard state
const activeStep = ref('1')
const isCreating = ref(false)

// Check if user has any wallets (for payment warnings)
const hasWallets = computed(() => walletsStore.totalWallets > 0)

// Interface review dialog state
const showInterfaceDialog = ref(false)
const dockerServiceResponse = ref<any>(null)

// Docker progress modal state
const showDockerProgress = ref(false)
const dockerProgressModal = ref<InstanceType<typeof DockerProgressModal> | null>(null)
const dockerOperationId = ref<string | null>(null)

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
  dockerSource: 'registry' as 'registry' | 'git' | 'local',
  dockerImageName: '',
  dockerImageTag: 'latest',
  dockerUsername: '',
  dockerPassword: '',
  dockerGitRepo: '',
  dockerGitBranch: 'main',
  dockerGitUsername: '',
  dockerGitPassword: '',
  dockerLocalPath: '',
  // STANDALONE specific
  standaloneSource: 'local' as 'local' | 'upload' | 'git',
  standaloneExecutablePath: '',
  standaloneExecutablePathGit: '',
  standaloneGitRepo: '',
  standaloneGitBranch: 'main',
  standaloneBuildCommand: '',
  standaloneGitUsername: '',
  standaloneGitPassword: '',
  standaloneCommand: '',
  standaloneArgs: '',
  standaloneWorkdir: '',
  standaloneEnv: '',
  standaloneTimeout: 600
})

const errors = ref({
  name: '',
  serviceType: '',
  files: '',
  dockerImageName: '',
  dockerGitRepo: '',
  dockerLocalPath: '',
  standaloneExecutablePath: '',
  standaloneExecutableFile: '',
  standaloneExecutablePathGit: '',
  standaloneGitRepo: ''
})

const selectedFiles = ref<File[]>([])
const fileInput = ref<HTMLInputElement | null>(null)
const folderInput = ref<HTMLInputElement | null>(null)
const standaloneExecutableFile = ref<File | null>(null)
const executableFileInput = ref<HTMLInputElement | null>(null)

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

// Payment networks are now auto-detected from provider's wallets at payment time
// No need to fetch or display during service creation

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

// Docker sources
const dockerSources = computed(() => [
  {
    value: 'registry',
    label: t('message.services.wizard.fromRegistry'),
    icon: 'pi pi-cloud-download'
  },
  {
    value: 'git',
    label: t('message.services.wizard.fromGit'),
    icon: 'pi pi-github'
  },
  {
    value: 'local',
    label: t('message.services.wizard.fromLocal'),
    icon: 'pi pi-folder-open'
  }
])

// Standalone sources
const standaloneSources = computed(() => [
  {
    value: 'local',
    label: t('message.services.wizard.fromLocal'),
    icon: 'pi pi-folder-open'
  },
  {
    value: 'upload',
    label: t('message.services.wizard.fromUpload'),
    icon: 'pi pi-cloud-upload'
  },
  {
    value: 'git',
    label: t('message.services.wizard.fromGit'),
    icon: 'pi pi-github'
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

// Standalone executable file handling
function openExecutableFileDialog() {
  if (executableFileInput.value) {
    executableFileInput.value.click()
  }
}

function handleExecutableFileSelect(event: Event) {
  const target = event.target as HTMLInputElement
  if (target.files && target.files.length > 0) {
    standaloneExecutableFile.value = target.files[0]
    errors.value.standaloneExecutableFile = ''
    target.value = ''
  }
}

function clearExecutableFile() {
  standaloneExecutableFile.value = null
  if (executableFileInput.value) executableFileInput.value.value = ''
}

function validateStep(step: number): boolean {
  errors.value = {
    name: '',
    serviceType: '',
    files: '',
    dockerImageName: '',
    dockerGitRepo: '',
    dockerLocalPath: '',
    standaloneExecutablePath: '',
    standaloneExecutableFile: '',
    standaloneExecutablePathGit: '',
    standaloneGitRepo: ''
  }

  if (step === 1) {
    if (!serviceData.value.name.trim()) {
      errors.value.name = t('message.services.nameRequired')
      return false
    }
  }

  if (step === 2) {
    // Payment networks are optional - auto-detected from provider's wallets
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

// WebSocket event handlers for Docker operations
function setupDockerWebSocketHandlers() {
  // Subscribe to Docker operation start
  subscribe(MessageType.DOCKER_OPERATION_START, (payload: any) => {
    if (payload.operation_id === dockerOperationId.value) {
      if (dockerProgressModal.value) {
        dockerProgressModal.value.setCurrentOperation(payload.message)
        dockerProgressModal.value.addOutputLine(payload.message, false)
      }
    }
  })

  // Subscribe to Docker operation progress
  subscribe(MessageType.DOCKER_OPERATION_PROGRESS, (payload: any) => {
    if (payload.operation_id === dockerOperationId.value) {
      if (dockerProgressModal.value) {
        dockerProgressModal.value.setCurrentOperation(payload.message)
        if (payload.stream) {
          dockerProgressModal.value.addOutputLine(payload.stream, false)
        } else if (payload.message) {
          dockerProgressModal.value.addOutputLine(payload.message, false)
        }
      }
    }
  })

  // Subscribe to Docker operation complete
  subscribe(MessageType.DOCKER_OPERATION_COMPLETE, (payload: any) => {
    if (payload.operation_id === dockerOperationId.value) {
      if (dockerProgressModal.value) {
        dockerProgressModal.value.setStatus('completed')
        dockerProgressModal.value.setCurrentOperation('Docker service created successfully')
      }

      // Store response data
      dockerServiceResponse.value = payload

      // Hide progress modal and show interface review dialog
      setTimeout(() => {
        showDockerProgress.value = false
        showInterfaceDialog.value = true
      }, 1000)
    }
  })

  // Subscribe to Docker operation error
  subscribe(MessageType.DOCKER_OPERATION_ERROR, (payload: any) => {
    if (payload.operation_id === dockerOperationId.value) {
      if (dockerProgressModal.value) {
        dockerProgressModal.value.setStatus('error')
        dockerProgressModal.value.addOutputLine(payload.error, true)
      }

      // Show error toast after a delay
      setTimeout(() => {
        showDockerProgress.value = false
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: payload.error,
          life: 5000
        })
        isCreating.value = false
      }, 2000)
    }
  })

  // Subscribe to Docker pull progress (legacy support)
  subscribe(MessageType.DOCKER_PULL_PROGRESS, (payload: any) => {
    if (dockerProgressModal.value && payload.service_name === serviceData.value.name) {
      const line = payload.status + (payload.progress ? ` ${payload.progress}` : '')
      dockerProgressModal.value.addOutputLine(line, false)
      dockerProgressModal.value.setCurrentOperation(payload.status)
    }
  })

  // Subscribe to Docker build output (legacy support)
  subscribe(MessageType.DOCKER_BUILD_OUTPUT, (payload: any) => {
    if (dockerProgressModal.value && payload.service_name === serviceData.value.name) {
      if (payload.stream) {
        dockerProgressModal.value.addOutputLine(payload.stream, false)
      }
      if (payload.error) {
        dockerProgressModal.value.addOutputLine(payload.error, true)
        dockerProgressModal.value.setStatus('error')
      }
    }
  })
}

async function finishWizard() {
  // Validate based on service type
  if (serviceData.value.serviceType === 'DATA' && selectedFiles.value.length === 0) {
    errors.value.files = t('message.services.fileRequired')
    return
  }

  // Validate DOCKER service configuration
  if (serviceData.value.serviceType === 'DOCKER') {
    if (serviceData.value.dockerSource === 'registry' && !serviceData.value.dockerImageName.trim()) {
      errors.value.dockerImageName = t('message.services.imageRequired')
      return
    }
    if (serviceData.value.dockerSource === 'git' && !serviceData.value.dockerGitRepo.trim()) {
      errors.value.dockerGitRepo = t('message.services.repoRequired')
      return
    }
    if (serviceData.value.dockerSource === 'local' && !serviceData.value.dockerLocalPath.trim()) {
      errors.value.dockerLocalPath = t('message.services.pathRequired')
      return
    }
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
    } else if (serviceData.value.serviceType === 'DOCKER') {
      // Setup WebSocket handlers
      setupDockerWebSocketHandlers()

      // Show Docker progress modal
      showDockerProgress.value = true

      // Wait for modal to mount
      await new Promise(resolve => setTimeout(resolve, 100))

      // Create DOCKER service (async with WebSocket progress)
      let response
      try {
        switch (serviceData.value.dockerSource) {
          case 'registry':
            response = await api.createDockerFromRegistry({
              service_name: serviceData.value.name,
              image_name: serviceData.value.dockerImageName,
              image_tag: serviceData.value.dockerImageTag || 'latest',
              description: serviceData.value.description,
              username: serviceData.value.dockerUsername,
              password: serviceData.value.dockerPassword
            })
            break

          case 'git':
            response = await api.createDockerFromGit({
              service_name: serviceData.value.name,
              repo_url: serviceData.value.dockerGitRepo,
              branch: serviceData.value.dockerGitBranch || 'main',
              username: serviceData.value.dockerGitUsername,
              password: serviceData.value.dockerGitPassword,
              description: serviceData.value.description
            })
            break

          case 'local':
            response = await api.createDockerFromLocal({
              service_name: serviceData.value.name,
              local_path: serviceData.value.dockerLocalPath,
              description: serviceData.value.description
            })
            break
        }

        // Check if response is async (has operation_id)
        if (response.operation_id) {
          // Store operation ID for WebSocket event tracking
          dockerOperationId.value = response.operation_id

          if (dockerProgressModal.value) {
            dockerProgressModal.value.setCurrentOperation('Docker build started...')
            dockerProgressModal.value.addOutputLine(response.message || 'Operation started', false)
          }

          // Don't close modal or navigate - wait for WebSocket completion
          // isCreating will be reset by WebSocket complete/error handlers
        } else {
          // Legacy synchronous response (for registry pulls)
          // Mark progress modal as completed
          if (dockerProgressModal.value) {
            dockerProgressModal.value.setStatus('completed')
            dockerProgressModal.value.setCurrentOperation('Docker service created successfully')
          }

          // Wait a moment before showing interface dialog
          await new Promise(resolve => setTimeout(resolve, 1000))

          // Hide progress modal and show interface review dialog
          showDockerProgress.value = false
          dockerServiceResponse.value = response
          showInterfaceDialog.value = true
          isCreating.value = false
        }
      } catch (error: any) {
        // Mark progress modal as error
        if (dockerProgressModal.value) {
          dockerProgressModal.value.setStatus('error')
          dockerProgressModal.value.addOutputLine(error.message || 'Docker service creation failed', true)
        }
        isCreating.value = false
        throw error
      }
    } else if (serviceData.value.serviceType === 'STANDALONE') {
      // STANDALONE service creation
      // Parse arguments and environment variables
      const args = serviceData.value.standaloneArgs
        ? serviceData.value.standaloneArgs.split(' ').filter(arg => arg.trim() !== '')
        : []

      const envVars: Record<string, string> = {}
      if (serviceData.value.standaloneEnv.trim()) {
        const envLines = serviceData.value.standaloneEnv.split('\n')
        for (const line of envLines) {
          const trimmed = line.trim()
          if (trimmed && trimmed.includes('=')) {
            const [key, ...valueParts] = trimmed.split('=')
            envVars[key.trim()] = valueParts.join('=').trim()
          }
        }
      }

      try {
        if (serviceData.value.standaloneSource === 'local') {
          // Create standalone service from local executable
          if (!serviceData.value.standaloneExecutablePath.trim()) {
            errors.value.standaloneExecutablePath = t('message.services.executablePathRequired')
            return
          }

          await api.createStandaloneFromLocal({
            service_name: serviceData.value.name,
            executable_path: serviceData.value.standaloneExecutablePath,
            arguments: args,
            working_directory: serviceData.value.standaloneWorkdir || undefined,
            environment_variables: Object.keys(envVars).length > 0 ? envVars : undefined,
            timeout_seconds: serviceData.value.standaloneTimeout || 600,
            description: serviceData.value.description
          })

          toast.add({
            severity: 'success',
            summary: t('message.common.success'),
            detail: t('message.services.standaloneCreated'),
            life: 3000
          })

          router.push('/services')
        } else if (serviceData.value.standaloneSource === 'upload') {
          // Create standalone service from uploaded file
          if (!standaloneExecutableFile.value) {
            errors.value.standaloneExecutableFile = t('message.services.fileRequired')
            return
          }

          // Create service record first
          const newService = await servicesStore.addService({
            service_type: 'STANDALONE',
            type: 'standalone',
            name: serviceData.value.name,
            description: serviceData.value.description,
            endpoint: '',
            capabilities: {},
            status: 'INACTIVE',
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

          // Upload executable file via WebSocket
          await uploadMultipleFiles({
            files: [standaloneExecutableFile.value],
            serviceId: newService.id!,
            chunkSize: 1024 * 1024, // 1MB chunks
            onComplete: async () => {
              toast.add({
                severity: 'success',
                summary: t('message.common.success'),
                detail: t('message.services.uploadCompleteMessage'),
                life: 3000
              })

              // Finalize service with configuration
              try {
                if (!standaloneExecutableFile.value) {
                  throw new Error('Executable file is missing')
                }

                const executablePath = `${newService.id}/${standaloneExecutableFile.value.name}`

                await api.finalizeStandaloneUpload(newService.id!, {
                  executable_path: executablePath,
                  arguments: args,
                  working_directory: serviceData.value.standaloneWorkdir || undefined,
                  environment_variables: Object.keys(envVars).length > 0 ? envVars : undefined,
                  timeout_seconds: serviceData.value.standaloneTimeout || 600
                })

                toast.add({
                  severity: 'success',
                  summary: t('message.common.success'),
                  detail: t('message.services.standaloneCreated'),
                  life: 3000
                })

                setTimeout(() => {
                  router.push('/services')
                }, 1000)
              } catch (error: any) {
                toast.add({
                  severity: 'error',
                  summary: t('message.common.error'),
                  detail: `Finalization failed: ${error.message}`,
                  life: 5000
                })
              }
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
        } else if (serviceData.value.standaloneSource === 'git') {
          // Create standalone service from Git repository
          if (!serviceData.value.standaloneGitRepo.trim()) {
            errors.value.standaloneGitRepo = t('message.services.repoRequired')
            return
          }
          if (!serviceData.value.standaloneExecutablePathGit.trim()) {
            errors.value.standaloneExecutablePathGit = t('message.services.executablePathRequired')
            return
          }

          await api.createStandaloneFromGit({
            service_name: serviceData.value.name,
            repo_url: serviceData.value.standaloneGitRepo,
            branch: serviceData.value.standaloneGitBranch || 'main',
            executable_path: serviceData.value.standaloneExecutablePathGit,
            build_command: serviceData.value.standaloneBuildCommand || undefined,
            arguments: args,
            working_directory: serviceData.value.standaloneWorkdir || undefined,
            environment_variables: Object.keys(envVars).length > 0 ? envVars : undefined,
            timeout_seconds: serviceData.value.standaloneTimeout || 600,
            username: serviceData.value.standaloneGitUsername || undefined,
            password: serviceData.value.standaloneGitPassword || undefined,
            description: serviceData.value.description
          })

          toast.add({
            severity: 'success',
            summary: t('message.common.success'),
            detail: t('message.services.standaloneCreated'),
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
        isCreating.value = false
      }
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

// Handle interface review dialog confirm
async function handleInterfaceConfirm(data: { interfaces: any[], entrypoint: string[], cmd: string[] }) {
  try {
    const serviceId = dockerServiceResponse.value?.service?.id
    if (!serviceId) {
      throw new Error('Service ID not found')
    }

    // Update service interfaces
    await api.updateDockerServiceInterfaces(serviceId, data.interfaces)

    // Update entrypoint and cmd if service is Docker
    if (dockerServiceResponse.value) {
      await api.updateDockerServiceConfig(serviceId, {
        entrypoint: JSON.stringify(data.entrypoint),
        cmd: JSON.stringify(data.cmd)
      })
    }

    // Update pricing information (which was entered in Step 2 but not saved during creation)
    await api.updateServicePricing(serviceId, {
      pricing_amount: serviceData.value.pricingAmount,
      pricing_type: serviceData.value.pricingType,
      pricing_interval: serviceData.value.pricingInterval,
      pricing_unit: serviceData.value.pricingUnit
    })

    toast.add({
      severity: 'success',
      summary: t('message.common.success'),
      detail: t('message.services.dockerCreated'),
      life: 3000
    })

    router.push('/services')
  } catch (error: any) {
    toast.add({
      severity: 'error',
      summary: t('message.common.error'),
      detail: error.message || 'Failed to update interfaces',
      life: 5000
    })
  }
}

// Handle interface review dialog cancel
function handleInterfaceCancel() {
  // User canceled, but service was already created
  // Just redirect to services page
  router.push('/services')
}

function goBack() {
  router.push('/services')
}

// Fetch wallets for payment warnings
onMounted(async () => {
  await walletsStore.fetchWallets()
})
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

  .pricing-currency-label {
    font-weight: 600;
    color: var(--text-color);
    font-size: 1rem;
    padding: 0 0.5rem;
    white-space: nowrap;
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

.docker-source-selection {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1rem;
  margin-top: 1rem;
  margin-bottom: 1.5rem;
}

.docker-source-card {
  padding: 1.5rem 1rem;
  border: 2px solid rgb(49, 64, 92);
  border-radius: 8px;
  background-color: rgb(38, 49, 65);
  cursor: pointer;
  transition: all 0.2s ease;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.75rem;

  &:hover {
    background-color: rgb(49, 64, 92);
    border-color: rgba(vars.$color-primary, 0.5);
    transform: translateY(-2px);
  }

  &.selected {
    border-color: vars.$color-primary;
    background-color: rgb(49, 64, 92);
    box-shadow: 0 0 0 1px vars.$color-primary;
  }

  .source-icon {
    font-size: 2rem;
    color: vars.$color-primary;
  }

  .source-label {
    font-size: 1rem;
    font-weight: 600;
    color: vars.$color-text;
  }
}

.source-config {
  margin-top: 1.5rem;
  padding: 1.5rem;
  background-color: rgba(vars.$color-primary, 0.05);
  border-radius: 8px;
  border-left: 3px solid vars.$color-primary;
}

.standalone-source-selection {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1rem;
  margin-top: 1rem;
  margin-bottom: 1.5rem;
}

.standalone-source-card {
  padding: 1.5rem 1rem;
  border: 2px solid rgb(49, 64, 92);
  border-radius: 8px;
  background-color: rgb(38, 49, 65);
  cursor: pointer;
  transition: all 0.2s ease;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.75rem;

  &:hover {
    background-color: rgb(49, 64, 92);
    border-color: rgba(vars.$color-primary, 0.5);
    transform: translateY(-2px);
  }

  &.selected {
    border-color: vars.$color-primary;
    background-color: rgb(49, 64, 92);
    box-shadow: 0 0 0 1px vars.$color-primary;
  }

  .source-icon {
    font-size: 2rem;
    color: vars.$color-primary;
  }

  .source-label {
    font-size: 1rem;
    font-weight: 600;
    color: vars.$color-text;
  }
}

.common-config {
  margin-top: 2rem;
  padding-top: 1.5rem;
  border-top: 2px solid rgba(vars.$color-primary, 0.2);

  h5 {
    color: vars.$color-primary;
    margin-bottom: 1rem;
    font-size: 1.1rem;
  }
}
</style>
