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
import { ScrollArea } from '@/components/ui/scroll-area'
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
  recipients: [] as DailyReportRecipient[],
  report_template_name: 'daily_chat_report',
  report_template_language: 'en',
  ai_enabled: false,
  ai_provider: 'openrouter',
  ai_api_key: '',
  ai_model: 'openai/gpt-4o-mini',
  ai_max_tokens: 3000,
  ai_temperature: 0.3,
  ai_system_prompt: ''
})

const defaultAIPrompt = ref('')

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
    defaultAIPrompt.value = s.ai_default_prompt || ''
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
      })),
      report_template_name: s.report_template_name || 'daily_chat_report',
      report_template_language: s.report_template_language || 'en',
      ai_enabled: !!s.ai_enabled,
      ai_provider: s.ai_provider || 'openrouter',
      ai_api_key: '', // never preload secret; leave blank to keep existing
      ai_model: s.ai_model || 'openai/gpt-4o-mini',
      ai_max_tokens: s.ai_max_tokens || 3000,
      ai_temperature: s.ai_temperature ?? 0.3,
      ai_system_prompt: s.ai_system_prompt || s.ai_default_prompt || ''
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
    const payload: Parameters<typeof dailyReportsService.updateSettings>[0] = {
      enabled: form.value.enabled,
      timezone: form.value.timezone || 'Asia/Kolkata',
      send_time: form.value.send_time,
      whatsapp_account: form.value.whatsapp_account,
      recipients: form.value.recipients.map((r, i) => ({
        name: r.name.trim(),
        phone_number: r.phone_number.trim(),
        is_active: r.is_active,
        sort_order: i
      })),
      report_template_name: form.value.report_template_name.trim(),
      report_template_language: form.value.report_template_language.trim() || 'en',
      ai_enabled: form.value.ai_enabled,
      ai_provider: form.value.ai_provider,
      ai_model: form.value.ai_model,
      ai_max_tokens: Number(form.value.ai_max_tokens) || 3000,
      ai_temperature: Number(form.value.ai_temperature) || 0.3,
      ai_system_prompt: form.value.ai_system_prompt
    }
    // Only send API key when user typed a new one
    if (form.value.ai_api_key.trim()) {
      payload.ai_api_key = form.value.ai_api_key.trim()
    }
    const res = await dailyReportsService.updateSettings(payload)
    const s = (res.data?.data ?? res.data) as DailyReportSettings
    settings.value = s
    form.value.enabled = !!s.enabled
    form.value.send_time = s.send_time || form.value.send_time
    form.value.timezone = s.timezone || form.value.timezone
    form.value.ai_api_key = ''
    form.value.ai_enabled = !!s.ai_enabled
    form.value.ai_provider = s.ai_provider || form.value.ai_provider
    form.value.ai_model = s.ai_model || form.value.ai_model
    form.value.ai_system_prompt = s.ai_system_prompt || form.value.ai_system_prompt
    const scheduleMsg = s.schedule_active
      ? `Schedule ON · ${s.schedule_cadence || 'daily'}`
      : 'Schedule OFF'
    toast.success(
      'Saved',
      `${scheduleMsg}. ${s.ai_ready ? 'AI ready.' : 'Enable AI + API key for summaries.'}`
    )
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
    // Wait for full AI → PDF → send pipeline before notifying the user.
    const res = await dailyReportsService.runNow({
      date: runDate.value || undefined
    })
    const run = (res.data?.data ?? res.data) as DailyReportRun
    const chats = typeof run?.chat_count === 'number' ? `${run.chat_count} chats` : 'done'
    toast.success(
      'Report created',
      `${chats} · sent ${run?.sent_count ?? 0}. Download DOCX from history.`
    )
    if (run?.send_errors) {
      toast.warning('Send issues', run.send_errors)
    }
    if (run?.error_message) {
      toast.warning('Run note', run.error_message)
    }
    await load()
  } catch (e: any) {
    const msg =
      e?.response?.data?.message ||
      e?.response?.data?.data?.message ||
      e?.message ||
      'Could not run report'
    toast.error('Run failed', msg)
    await load()
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
    const blob = new Blob([res.data], {
      type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
    })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `daily-report-${id}.docx`
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
  <div class="flex flex-col h-full min-h-0">
    <PageHeader
      :title="t('nav.dailyReports')"
      description="AI end-of-day chat summaries as PDF, sent to admin/employee WhatsApp numbers"
    />

    <ScrollArea class="flex-1 min-h-0">
      <div class="flex flex-col gap-6 p-6 pb-16">
    <ErrorState v-if="error" :description="error" @retry="load" />

    <template v-else>
      <!-- Schedule status (visible after save / always from DB) -->
      <Card>
        <CardHeader class="pb-3">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div>
              <CardTitle class="text-base">Schedule</CardTitle>
              <CardDescription>
                Runs <strong>every day</strong> for that calendar day’s chats (timezone-aware).
              </CardDescription>
            </div>
            <Badge :variant="settings?.schedule_active ? 'default' : 'secondary'">
              {{ settings?.schedule_active ? 'Active' : 'Off' }}
            </Badge>
          </div>
        </CardHeader>
        <CardContent class="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
          <div>
            <p class="text-xs text-muted-foreground">Cadence</p>
            <p class="font-medium">{{ settings?.schedule_cadence || '—' }}</p>
          </div>
          <div>
            <p class="text-xs text-muted-foreground">Next generate (AI starts)</p>
            <p class="font-medium">
              {{
                settings?.next_fire_at
                  ? new Date(settings.next_fire_at).toLocaleString()
                  : settings?.schedule_active
                    ? '—'
                    : 'Enable schedule to activate'
              }}
            </p>
          </div>
          <div>
            <p class="text-xs text-muted-foreground">Next send time</p>
            <p class="font-medium">
              {{
                settings?.next_send_at
                  ? new Date(settings.next_send_at).toLocaleString()
                  : '—'
              }}
            </p>
          </div>
          <div>
            <p class="text-xs text-muted-foreground">Today’s report ({{ settings?.today_report_date || '—' }})</p>
            <p class="font-medium">
              <Badge
                v-if="settings?.today_run_status"
                :variant="statusVariant(settings.today_run_status)"
                class="mr-1"
              >
                {{ settings.today_run_status }}
              </Badge>
              <span v-if="settings?.today_run_triggered_by" class="text-muted-foreground">
                via {{ settings.today_run_triggered_by }}
              </span>
              <span v-if="!settings?.today_run_status">Not run yet today</span>
            </p>
          </div>
          <div>
            <p class="text-xs text-muted-foreground">Last scheduled fire</p>
            <p class="font-medium">
              <template v-if="settings?.last_scheduled_date">
                {{ settings.last_scheduled_date }}
                <Badge
                  v-if="settings.last_scheduled_status"
                  :variant="statusVariant(settings.last_scheduled_status)"
                  class="ml-1"
                >
                  {{ settings.last_scheduled_status }}
                </Badge>
              </template>
              <span v-else>—</span>
            </p>
            <p v-if="settings?.last_scheduled_at" class="text-xs text-muted-foreground">
              {{ new Date(settings.last_scheduled_at).toLocaleString() }}
            </p>
          </div>
          <div>
            <p class="text-xs text-muted-foreground">Lead time</p>
            <p class="font-medium">
              Starts {{ settings?.schedule_lead_minutes ?? 10 }} min before send time
            </p>
          </div>
        </CardContent>
      </Card>

      <!-- Setup -->
      <Card>
        <CardHeader class="flex flex-row items-start justify-between gap-4 space-y-0">
          <div>
            <CardTitle>Setup</CardTitle>
            <CardDescription>
              Max {{ maxRecipients }} recipients (admin + employee). Timezone defaults to Asia/Kolkata.
              Turn on schedule and Save — it runs every day at the send time.
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
                <Label for="dr-enabled">Enable daily schedule</Label>
              </div>
              <Badge v-if="form.enabled" variant="default">Will run every day</Badge>
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
                <Label>WhatsApp account (sender + chat source)</Label>
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
                <p class="text-xs text-muted-foreground">
                  Chats for this account are included. Report is sent from this account.
                </p>
              </div>
            </div>

            <!-- Utility TEXT template (outside 24h window) -->
            <div class="space-y-3 rounded-lg border p-4">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h3 class="text-sm font-semibold">WhatsApp utility template (TEXT header)</h3>
                  <p class="text-xs text-muted-foreground">
                    Simple utility message so recipients get a WhatsApp ping outside the 24h window.
                    Only the recipients below receive it. Full DOCX stays in History for download
                    (free-form file send only works if that number already messaged you within 24h).
                  </p>
                </div>
                <Badge :variant="settings?.template_configured ? 'default' : 'secondary'">
                  {{ settings?.template_configured ? 'Template set' : 'Not configured' }}
                </Badge>
              </div>
              <div class="grid gap-4 sm:grid-cols-2">
                <div class="space-y-2">
                  <Label>Template name</Label>
                  <Input
                    v-model="form.report_template_name"
                    :disabled="!canWrite"
                    placeholder="daily_chat_report"
                  />
                </div>
                <div class="space-y-2">
                  <Label>Language code</Label>
                  <Input
                    v-model="form.report_template_language"
                    :disabled="!canWrite"
                    placeholder="en"
                  />
                </div>
              </div>
              <div class="rounded-md bg-muted/50 p-3 text-xs text-muted-foreground space-y-1">
                <p class="font-medium text-foreground">Meta Business Manager → create template with these exact fields:</p>
                <ul class="list-disc pl-4 space-y-1">
                  <li><strong>Category:</strong> Utility</li>
                  <li><strong>Name:</strong> <code>daily_chat_report</code> (must match field above)</li>
                  <li><strong>Language:</strong> English (code <code>en</code> — match Language field)</li>
                  <li><strong>Header type:</strong> Text</li>
                  <li><strong>Header text:</strong> <code>Daily Chat Report</code> (static text, no &#123;&#123;vars&#125;&#125;)</li>
                  <li>
                    <strong>Body text (copy exactly):</strong><br />
                    <code class="block mt-1 whitespace-pre-wrap">Here is your report for {{1}}.
Total chats: {{2}}.
{{3}}
Open Whatomate → Analytics → Daily Reports to download the full Word file.</code>
                  </li>
                  <li>Sample values for review: <code>2026-07-31</code> / <code>5</code> / <code>Daily chat report</code></li>
                  <li>Submit → wait <strong>APPROVED</strong> → Whatomate Templates → Sync</li>
                </ul>
                <p class="font-medium text-foreground mt-2">Whatomate fills body variables as:</p>
                <ul class="list-disc pl-4 space-y-1">
                  <li><code>{{1}}</code> → report date (YYYY-MM-DD)</li>
                  <li><code>{{2}}</code> → chat count (0 if none)</li>
                  <li><code>{{3}}</code> → type label: Daily chat report / Daily chat report (no chats)</li>
                </ul>
              </div>
            </div>

            <!-- AI settings (same page) -->
            <div class="space-y-4 rounded-lg border p-4">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 class="text-sm font-semibold">AI settings</h3>
                  <p class="text-xs text-muted-foreground">
                    Used only for daily report summaries. Configure here — no chatbot settings needed.
                  </p>
                </div>
                <div class="flex items-center gap-2">
                  <Badge v-if="settings?.ai_ready" variant="default">AI ready</Badge>
                  <Badge v-else variant="secondary">AI not ready</Badge>
                  <Switch id="dr-ai-enabled" v-model:checked="form.ai_enabled" :disabled="!canWrite" />
                  <Label for="dr-ai-enabled">Enable AI</Label>
                </div>
              </div>

              <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                <div class="space-y-2">
                  <Label>Provider</Label>
                  <Select
                    :model-value="form.ai_provider"
                    :disabled="!canWrite"
                    @update:model-value="(v: string) => (form.ai_provider = v)"
                  >
                    <SelectTrigger>
                      <SelectValue placeholder="Provider" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="openrouter">OpenRouter</SelectItem>
                      <SelectItem value="openai">OpenAI</SelectItem>
                      <SelectItem value="anthropic">Anthropic</SelectItem>
                      <SelectItem value="google">Google</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div class="space-y-2">
                  <Label>Model</Label>
                  <Input
                    v-model="form.ai_model"
                    :disabled="!canWrite"
                    placeholder="openai/gpt-4o-mini"
                  />
                </div>
                <div class="space-y-2">
                  <Label>API key</Label>
                  <Input
                    v-model="form.ai_api_key"
                    type="password"
                    :disabled="!canWrite"
                    :placeholder="
                      settings?.ai_has_api_key
                        ? '•••• saved (type new key to replace)'
                        : 'Paste API key'
                    "
                    autocomplete="off"
                  />
                  <p v-if="settings?.ai_has_api_key" class="text-xs text-muted-foreground">
                    Key is saved. Leave blank to keep current key.
                  </p>
                </div>
                <div class="space-y-2">
                  <Label>Max tokens</Label>
                  <Input
                    v-model.number="form.ai_max_tokens"
                    type="number"
                    min="256"
                    max="16000"
                    :disabled="!canWrite"
                  />
                </div>
                <div class="space-y-2">
                  <Label>Temperature</Label>
                  <Input
                    v-model.number="form.ai_temperature"
                    type="number"
                    min="0"
                    max="2"
                    step="0.1"
                    :disabled="!canWrite"
                  />
                </div>
              </div>

              <div class="space-y-2">
                <div class="flex items-center justify-between gap-2">
                  <Label>System prompt</Label>
                  <Button
                    v-if="canWrite && defaultAIPrompt"
                    type="button"
                    variant="ghost"
                    size="sm"
                    @click="form.ai_system_prompt = defaultAIPrompt"
                  >
                    Reset to default
                  </Button>
                </div>
                <textarea
                  v-model="form.ai_system_prompt"
                  :disabled="!canWrite"
                  rows="8"
                  class="flex w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 font-mono"
                  placeholder="Prompt that structures bullet summaries as JSON by chat id…"
                />
                <p class="text-xs text-muted-foreground">
                  Must ask for JSON with items[].id and items[].bullets (max 3). Name/phone are filled from
                  the database after AI runs.
                </p>
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
            Uses AI settings on this page. Builds a Word (.docx) report from real chats for the selected day,
            then sends it to recipients.
          </CardDescription>
        </CardHeader>
        <CardContent class="flex flex-wrap items-end gap-3">
          <div class="space-y-1">
            <Label>Report date</Label>
            <Input v-model="runDate" type="date" :disabled="!canWrite || running" class="w-48" />
          </div>
          <Button v-if="canWrite" :disabled="running" @click="runNow">
            <Play class="mr-2 h-4 w-4" />
            {{ running ? 'Building report…' : 'Run now' }}
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
    </ScrollArea>
  </div>
</template>
