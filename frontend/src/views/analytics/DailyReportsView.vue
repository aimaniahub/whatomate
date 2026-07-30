<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { PageHeader, ErrorState } from '@/components/shared'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { useAppToast } from '@/composables/useAppToast'
import { useAuthStore } from '@/stores/auth'
import {
  accountsService,
  dailyReportsService,
  type DailyReportRecipient,
  type DailyReportRun,
  type DailyReportSettings
} from '@/services/api'
import { Download, Play, Plus, RefreshCw, Save, Trash2, Send } from 'lucide-vue-next'

const { t } = useI18n()
const toast = useAppToast()
const authStore = useAuthStore()

const canWrite = computed(() => authStore.hasPermission('analytics', 'write'))

const loading = ref(true)
const saving = ref(false)
const running = ref(false)
const error = ref<string | null>(null)

const settings = ref<DailyReportSettings | null>(null)
const runs = ref<DailyReportRun[]>([])
const accounts = ref<{ name: string }[]>([])
const runDate = ref('')

const form = ref({
  enabled: false,
  timezone: 'Asia/Kolkata',
  send_time: '20:00',
  whatsapp_account: '',
  recipients: [] as DailyReportRecipient[]
})

const maxRecipients = computed(() => settings.value?.max_recipients ?? 2)

async function load() {
  loading.value = true
  error.value = null
  try {
    const [settingsRes, runsRes, accountsRes] = await Promise.all([
      dailyReportsService.getSettings(),
      dailyReportsService.listRuns(),
      accountsService.list().catch(() => ({ data: { data: [] } }))
    ])
    const s = (settingsRes.data?.data ?? settingsRes.data) as DailyReportSettings
    settings.value = s
    form.value = {
      enabled: !!s.enabled,
      timezone: s.timezone || 'Asia/Kolkata',
      send_time: s.send_time || '20:00',
      whatsapp_account: s.whatsapp_account || '',
      recipients: (s.recipients || []).map((r) => ({
        id: r.id,
        name: r.name,
        phone_number: r.phone_number,
        is_active: r.is_active !== false,
        sort_order: r.sort_order
      }))
    }
    const runsData = runsRes.data?.data ?? runsRes.data
    runs.value = runsData?.runs || []

    const accData = accountsRes.data?.data ?? accountsRes.data
    const list = accData?.accounts || accData || []
    accounts.value = Array.isArray(list)
      ? list.map((a: any) => ({ name: a.name || a.Name || '' })).filter((a: any) => a.name)
      : []
  } catch (e: any) {
    error.value = e?.response?.data?.message || e?.message || 'Failed to load daily reports'
  } finally {
    loading.value = false
  }
}

function addRecipient() {
  if (form.value.recipients.length >= maxRecipients.value) {
    toast.error('Limit reached', `Maximum ${maxRecipients.value} recipients`)
    return
  }
  form.value.recipients.push({
    name: '',
    phone_number: '',
    is_active: true
  })
}

function removeRecipient(idx: number) {
  form.value.recipients.splice(idx, 1)
}

async function saveSettings() {
  if (!canWrite.value) return
  for (const r of form.value.recipients) {
    if (!r.name.trim() || !r.phone_number.trim()) {
      toast.error('Validation', 'Each recipient needs a name and WhatsApp number')
      return
    }
  }
  saving.value = true
  try {
    const res = await dailyReportsService.updateSettings({
      enabled: form.value.enabled,
      timezone: form.value.timezone || 'Asia/Kolkata',
      send_time: form.value.send_time,
      whatsapp_account: form.value.whatsapp_account,
      recipients: form.value.recipients.map((r, i) => ({
        name: r.name.trim(),
        phone_number: r.phone_number.trim(),
        is_active: r.is_active,
        sort_order: i
      }))
    })
    const s = (res.data?.data ?? res.data) as DailyReportSettings
    settings.value = s
    toast.success('Saved', 'Daily report setup updated')
  } catch (e: any) {
    toast.error('Save failed', e?.response?.data?.message || e?.message || 'Could not save')
  } finally {
    saving.value = false
  }
}

async function runNow() {
  if (!canWrite.value) return
  running.value = true
  try {
    await dailyReportsService.runNow(runDate.value || undefined)
    toast.success('Report generated', 'PDF created and send attempted to recipients')
    await load()
  } catch (e: any) {
    toast.error('Run failed', e?.response?.data?.message || e?.message || 'Could not run report')
  } finally {
    running.value = false
  }
}

async function resend(id: string) {
  if (!canWrite.value) return
  try {
    await dailyReportsService.resend(id)
    toast.success('Resent', 'PDF send attempted again')
    await load()
  } catch (e: any) {
    toast.error('Resend failed', e?.response?.data?.message || e?.message || 'Could not resend')
  }
}

async function download(id: string) {
  try {
    const { api } = await import('@/services/api')
    const res = await api.get(`/analytics/daily-reports/runs/${id}/download`, {
      responseType: 'blob'
    })
    const blob = new Blob([res.data], { type: 'application/pdf' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `daily-report-${id}.pdf`
    a.click()
    URL.revokeObjectURL(url)
  } catch (e: any) {
    toast.error('Download failed', e?.message || 'Could not download PDF')
  }
}

function statusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (status) {
    case 'completed':
      return 'default'
    case 'empty':
      return 'secondary'
    case 'failed':
      return 'destructive'
    case 'running':
    case 'pending':
      return 'outline'
    default:
      return 'secondary'
  }
}

onMounted(load)
</script>

<template>
  <div class="flex flex-col gap-6 p-6">
    <PageHeader
      :title="t('nav.dailyReports')"
      description="AI end-of-day chat summaries as PDF, sent to admin/employee WhatsApp numbers"
    />

    <ErrorState v-if="error" :description="error" @retry="load" />

    <template v-else>
      <!-- Setup -->
      <Card>
        <CardHeader class="flex flex-row items-start justify-between gap-4 space-y-0">
          <div>
            <CardTitle>Setup</CardTitle>
            <CardDescription>
              Max {{ maxRecipients }} recipients (admin + employee). Timezone defaults to Asia/Kolkata.
              <span v-if="settings?.next_run_preview" class="block mt-1 text-xs">
                Next schedule window: {{ settings.next_run_preview }}
              </span>
            </CardDescription>
          </div>
          <Button v-if="canWrite" :disabled="saving" @click="saveSettings">
            <Save class="mr-2 h-4 w-4" />
            {{ saving ? 'Saving…' : 'Save setup' }}
          </Button>
        </CardHeader>
        <CardContent class="space-y-6">
          <div v-if="loading" class="space-y-3">
            <Skeleton class="h-8 w-full" />
            <Skeleton class="h-8 w-2/3" />
          </div>
          <template v-else>
            <div class="flex flex-wrap items-center gap-6">
              <div class="flex items-center gap-3">
                <Switch id="dr-enabled" v-model:checked="form.enabled" :disabled="!canWrite" />
                <Label for="dr-enabled">Enable scheduled send</Label>
              </div>
            </div>

            <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <div class="space-y-2">
                <Label>Timezone</Label>
                <Input v-model="form.timezone" :disabled="!canWrite" placeholder="Asia/Kolkata" />
              </div>
              <div class="space-y-2">
                <Label>Send time (local)</Label>
                <Input v-model="form.send_time" type="time" :disabled="!canWrite" />
              </div>
              <div class="space-y-2">
                <Label>WhatsApp account (sender)</Label>
                <Select
                  v-if="accounts.length"
                  :model-value="form.whatsapp_account"
                  :disabled="!canWrite"
                  @update:model-value="(v: string) => (form.whatsapp_account = v)"
                >
                  <SelectTrigger>
                    <SelectValue placeholder="Select account" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="a in accounts" :key="a.name" :value="a.name">
                      {{ a.name }}
                    </SelectItem>
                  </SelectContent>
                </Select>
                <Input
                  v-else
                  v-model="form.whatsapp_account"
                  :disabled="!canWrite"
                  placeholder="Account name"
                />
              </div>
            </div>

            <div class="space-y-3">
              <div class="flex items-center justify-between">
                <Label>Recipients (max {{ maxRecipients }})</Label>
                <Button
                  v-if="canWrite"
                  variant="outline"
                  size="sm"
                  :disabled="form.recipients.length >= maxRecipients"
                  @click="addRecipient"
                >
                  <Plus class="mr-1 h-4 w-4" />
                  Add
                </Button>
              </div>
              <div
                v-if="!form.recipients.length"
                class="rounded-md border border-dashed p-4 text-sm text-muted-foreground"
              >
                No recipients yet. Add up to {{ maxRecipients }} people (e.g. Admin, Employee).
              </div>
              <div
                v-for="(rec, idx) in form.recipients"
                :key="idx"
                class="grid gap-3 rounded-lg border p-3 sm:grid-cols-[1fr_1fr_auto_auto] sm:items-end"
              >
                <div class="space-y-1">
                  <Label class="text-xs">Name</Label>
                  <Input v-model="rec.name" :disabled="!canWrite" placeholder="Admin" />
                </div>
                <div class="space-y-1">
                  <Label class="text-xs">WhatsApp number</Label>
                  <Input
                    v-model="rec.phone_number"
                    :disabled="!canWrite"
                    placeholder="9198XXXXXXXX"
                  />
                </div>
                <div class="flex items-center gap-2 pb-2">
                  <Switch v-model:checked="rec.is_active" :disabled="!canWrite" />
                  <span class="text-xs text-muted-foreground">Active</span>
                </div>
                <Button
                  v-if="canWrite"
                  variant="ghost"
                  size="icon"
                  class="text-destructive"
                  @click="removeRecipient(idx)"
                >
                  <Trash2 class="h-4 w-4" />
                </Button>
              </div>
            </div>
          </template>
        </CardContent>
      </Card>

      <!-- Run now -->
      <Card>
        <CardHeader>
          <CardTitle>Run now</CardTitle>
          <CardDescription>
            Generate PDF for a day (all contacts with messages) and send to recipients. Leave date
            empty for today.
          </CardDescription>
        </CardHeader>
        <CardContent class="flex flex-wrap items-end gap-3">
          <div class="space-y-1">
            <Label>Report date</Label>
            <Input v-model="runDate" type="date" :disabled="!canWrite || running" class="w-48" />
          </div>
          <Button v-if="canWrite" :disabled="running" @click="runNow">
            <Play class="mr-2 h-4 w-4" />
            {{ running ? 'Running…' : 'Run now' }}
          </Button>
          <Button variant="outline" :disabled="loading" @click="load">
            <RefreshCw class="mr-2 h-4 w-4" />
            Refresh
          </Button>
        </CardContent>
      </Card>

      <!-- History -->
      <Card>
        <CardHeader>
          <CardTitle>History</CardTitle>
          <CardDescription>Past generated reports (download PDF or resend)</CardDescription>
        </CardHeader>
        <CardContent>
          <div v-if="loading" class="space-y-2">
            <Skeleton class="h-10 w-full" />
            <Skeleton class="h-10 w-full" />
          </div>
          <div
            v-else-if="!runs.length"
            class="rounded-md border border-dashed p-8 text-center text-sm text-muted-foreground"
          >
            No runs yet. Use Run now or wait for the scheduled send.
          </div>
          <Table v-else>
            <TableHeader>
              <TableRow>
                <TableHead>Date</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Chats</TableHead>
                <TableHead>Trigger</TableHead>
                <TableHead>Sent</TableHead>
                <TableHead>Finished</TableHead>
                <TableHead class="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="run in runs" :key="run.id">
                <TableCell class="font-medium">{{ run.report_date }}</TableCell>
                <TableCell>
                  <Badge :variant="statusVariant(run.status)">{{ run.status }}</Badge>
                  <p
                    v-if="run.error_message || run.send_errors"
                    class="mt-1 max-w-xs truncate text-xs text-muted-foreground"
                    :title="run.error_message || run.send_errors"
                  >
                    {{ run.error_message || run.send_errors }}
                  </p>
                </TableCell>
                <TableCell>{{ run.chat_count }} ({{ run.message_count }} msgs)</TableCell>
                <TableCell>{{ run.triggered_by }}</TableCell>
                <TableCell>{{ run.sent_count }}</TableCell>
                <TableCell class="text-xs text-muted-foreground">
                  {{ run.finished_at ? new Date(run.finished_at).toLocaleString() : '—' }}
                </TableCell>
                <TableCell class="text-right space-x-1">
                  <Button
                    v-if="run.has_pdf"
                    variant="outline"
                    size="sm"
                    @click="download(run.id)"
                  >
                    <Download class="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    v-if="canWrite && run.has_pdf"
                    variant="outline"
                    size="sm"
                    @click="resend(run.id)"
                  >
                    <Send class="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </template>
  </div>
</template>
