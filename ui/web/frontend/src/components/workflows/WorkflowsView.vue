<template>
  <AppLayout>
    <div class="workflows">
      <div class="workflows-header">
        <h1>{{ $t('message.workflows.title') }}</h1>
        <Button
          :label="$t('message.workflows.create')"
          icon="pi pi-plus"
          @click="createWorkflow"
          severity="success"
        />
      </div>

      <div v-if="workflowsStore.loading" class="loading">
        <ProgressSpinner style="width:50px;height:50px" strokeWidth="4" />
      </div>

      <DataTable
        v-else
        :value="workflowsStore.workflows"
        :paginator="true"
        :rows="10"
        class="workflows-table"
        :rowsPerPageOptions="[5, 10, 25]"
        responsiveLayout="scroll"
      >
        <template #empty>
          <div class="empty-state">
            <i class="pi pi-inbox"></i>
            <p>{{ $t('message.common.noData') }}</p>
          </div>
        </template>

        <Column field="name" :header="$t('message.workflows.name')" :sortable="true"></Column>
        <Column field="description" :header="$t('message.workflows.description')"></Column>
        <Column :exportable="false" style="min-width:8rem">
          <template #body="slotProps">
            <Button
              icon="pi pi-pencil"
              text
              rounded
              severity="info"
              @click="editWorkflow(slotProps.data)"
            />
            <Button
              icon="pi pi-trash"
              text
              rounded
              severity="danger"
              @click="confirmDeleteWorkflow(slotProps.data)"
            />
          </template>
        </Column>
      </DataTable>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useConfirm } from 'primevue/useconfirm'
import { useToast } from 'primevue/usetoast'

import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import ProgressSpinner from 'primevue/progressspinner'

import AppLayout from '../layout/AppLayout.vue'
import { useWorkflowsStore } from '../../stores/workflows'

const router = useRouter()
const { t } = useI18n()
const confirm = useConfirm()
const toast = useToast()

const workflowsStore = useWorkflowsStore()

function createWorkflow() {
  // TODO: Navigate to workflow creation or show dialog
  toast.add({
    severity: 'info',
    summary: t('message.common.info'),
    detail: 'Workflow creation coming soon',
    life: 3000
  })
}

function editWorkflow(workflow: any) {
  // TODO: Navigate to workflow editor
  toast.add({
    severity: 'info',
    summary: t('message.common.info'),
    detail: 'Workflow editing coming soon',
    life: 3000
  })
}

function confirmDeleteWorkflow(workflow: any) {
  confirm.require({
    message: t('message.workflows.deleteConfirm'),
    header: t('message.common.confirm'),
    icon: 'pi pi-exclamation-triangle',
    acceptClass: 'p-button-danger',
    accept: async () => {
      try {
        await workflowsStore.deleteWorkflow(workflow.id)
        toast.add({
          severity: 'success',
          summary: t('message.common.success'),
          detail: t('message.workflows.deleteSuccess'),
          life: 3000
        })
      } catch (error) {
        toast.add({
          severity: 'error',
          summary: t('message.common.error'),
          detail: t('message.workflows.deleteError'),
          life: 3000
        })
      }
    }
  })
}

onMounted(async () => {
  await workflowsStore.fetchWorkflows()
})
</script>

<style scoped lang="scss">
@use '../../scss/variables' as vars;

.workflows {
  min-height: 100vh;
  padding: vars.$spacing-lg;
}

.workflows-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: vars.$spacing-xl;

  h1 {
    color: vars.$color-primary;
    font-size: vars.$font-size-xxl;
    margin: 0;
  }
}

.loading {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: vars.$spacing-xl;
}

.empty-state {
  text-align: center;
  padding: vars.$spacing-xl;
  color: vars.$color-text-secondary;

  i {
    font-size: 3rem;
    margin-bottom: vars.$spacing-md;
  }

  p {
    font-size: vars.$font-size-lg;
    margin: 0;
  }
}
</style>
