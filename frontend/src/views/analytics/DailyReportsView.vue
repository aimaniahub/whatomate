<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue'
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
  templatesService,
  type DailyReportRecipient,
  type DailyReportRun,
  type DailyReportSettings
} from '@/services/api'
import { Download, Play, Plus, RefreshCw, Save, Trash2, Send } from 'lucide-vue-next'

interface ReportTemplateOption {
  id: string
  name: string
  display_name?: string
  language: string
  category?: string
  status?: string
  header_type?: string
  body_content?: string
  whatsapp_account?: string
}

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
const templates = ref<ReportTemplateOption[]>([])
const templatesLoading = ref(false)
const runDate = ref('')

const form = ref({
  enabled: false,
  timezone: 'Asia/Kolkata',
  send_time: '20:00',
  whatsapp_account: '',
  recipients: [] as DailyReportRecipient[],
  report_template_name: '',
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

/** Unique key for Select: name + language (same template can exist in multiple langs). */
function templateKey(t: { name: string; language: string }) {
  return `${t.name}||${t.language || 'en'}`
}

const selectedTemplateKey = computed({
  get() {
    if (!form.value.report_template_name) return '__none__'
    return templateKey({
      name: form.value.report_template_name,
      language: form.value.report_template_language || 'en'
    })
  },
  set(key: string) {
    if (!key || key === '__none__') {
      form.value.report_template_name = ''
      form.value.report_template_language = 'en'
      return
    }
    const [name, lang] = key.split('||')
    form.value.report_template_name = name || ''
    form.value.report_template_language = lang || 'en'
  }
})

const selectedTemplate = computed(() =>
  templates.value.find(
    (t) =>
      t.name === form.value.report_template_name &&
      (t.language || 'en') === (form.value.report_template_language || 'en')
  )
)

async function loadTemplates(accountName: string) {
  templatesLoading.value = true
  try {
    const params: { status: string; account?: string; limit: number; page: number } = {
      status: 'APPROVED',
      limit: 100,
      page: 1
    }
    if (accountName) {
      params.account = accountName
    }
    const res = await templatesService.list(params)
    const data = res.data?.data ?? res.data
    const list = data?.templates || []
    const mapped: ReportTemplateOption[] = (Array.isArray(list) ? list : []).map((t: any) => ({
      id: t.id,
      name: t.name,
      display_name: t.display_name,
      language: t.language || 'en',
      category: t.category,
      status: t.status,
      header_type: t.header_type,
      body_content: t.body_content,
      whatsapp_account: t.whatsapp_account
    }))
    // Keep currently saved selection visible even if API list is filtered.
    const savedName = form.value.report_template_name
    const savedLang = form.value.report_template_language || 'en'
    if (
      savedName &&
      !mapped.some((t) => t.name === savedName && (t.language || 'en') === savedLang)
    ) {
      mapped.unshift({
        id: 'saved',
        name: savedName,
        display_name: savedName,
        language: savedLang,
        status: 'SAVED'
      })
    }
    templates.value = mapped
  } catch {
    templates.value = []
  } finally {
    templatesLoading.value = false
  }
}

watch(
  () => form.value.whatsapp_account,
  (acct) => {
    loadTemplates(acct || '')
  }
)

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
      report_template_name: s.report_template_name || '',
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

    await loadTemplates(form.value.whatsapp_account || '')
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
    // Blocks until: collect → AI complete → DOCX compose → WhatsApp send (server-side sync).
    const res = await dailyReportsService.runNow({
      date: runDate.value || undefined
    })
    const run = (res.data?.data ?? res.data) as DailyReportRun
    const chats = typeof run?.chat_count === 'number' ? `${run.chat_count} chats` : 'done'
    toast.success(
      'Report ready',
      `${chats} · AI done · sent ${run?.sent_count ?? 0} on WhatsApp. Download DOCX from history.`
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
    toast.success('Resent', 'Template / document send attempted again')
    await load()
  } catch (e: any) {
    toast.error('Resend failed', e?.response?.data?.message || e?.message || 'Could not resend')
  }
}

async function download(id: string, filename?: string) {
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
    a.download = filename || `daily-report-${id}.docx`
    a.click()
    URL.revokeObjectURL(url)
  } catch (e: any) {
    toast.error('Download failed', e?.message || 'Could not download DOCX')
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
      description="AI end-of-day chat summaries as Word (.docx), notified via template + download in History"
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

            <!-- Pick existing approved template (no need to type name) -->
            <div class="space-y-3 rounded-lg border p-4">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h3 class="text-sm font-semibold">WhatsApp template</h3>
                  <p class="text-xs text-muted-foreground">
                    Choose an existing <strong>APPROVED</strong> template for this WhatsApp account.
                    Used to notify report recipients outside the 24h window. Full DOCX stays in History.
                  </p>
                </div>
                <Badge :variant="form.report_template_name ? 'default' : 'secondary'">
                  {{ form.report_template_name ? 'Selected' : 'Not selected' }}
                </Badge>
              </div>
              <div class="space-y-2">
                <Label>Template</Label>
                <Select
                  :model-value="selectedTemplateKey"
                  :disabled="!canWrite || templatesLoading"
                  @update:model-value="(v: string) => (selectedTemplateKey = v || '__none__')"
                >
                  <SelectTrigger>
                    <SelectValue
                      :placeholder="
                        templatesLoading
                          ? 'Loading templates…'
                          : templates.length
                            ? 'Select approved template'
                            : 'No approved templates — sync Templates first'
                      "
                    />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__none__">None — don’t send template</SelectItem>
                    <SelectItem
                      v-for="t in templates"
                      :key="templateKey(t)"
                      :value="templateKey(t)"
                    >
                      {{
                        `${t.display_name || t.name} (${t.language}${t.category ? ' · ' + t.category : ''}${t.header_type ? ' · ' + t.header_type : ''})`
                      }}
                    </SelectItem>
                  </SelectContent>
                </Select>
                <p v-if="form.report_template_name" class="text-xs text-muted-foreground">
                  Saved as <code>{{ form.report_template_name }}</code>
                  · lang <code>{{ form.report_template_language }}</code>
                  <span v-if="selectedTemplate?.header_type">
                    · header <code>{{ selectedTemplate.header_type || 'TEXT' }}</code>
                  </span>
                </p>
                <p v-if="!templatesLoading && !templates.length" class="text-xs text-amber-600 dark:text-amber-400">
                  No APPROVED templates for this account. Go to Templates, sync from Meta, then refresh.
                </p>
              </div>
              <div
                v-if="selectedTemplate?.body_content"
                class="rounded-md bg-muted/50 p-3 text-xs text-muted-foreground"
              >
                <p class="font-medium text-foreground mb-1">Template body preview</p>
                <pre class="whitespace-pre-wrap font-sans">{{ selectedTemplate.body_content }}</pre>
                <p class="mt-2">
                  When sending, the app fills body variables:
                  <strong>1</strong> = report date,
                  <strong>2</strong> = chat count,
                  <strong>3</strong> = report type label.
                  Prefer a template with 1–3 body variables matching that order.
                </p>
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
                  Keep it simple: short customer-intent bullets. JSON uses id only for mapping (not shown in
                  the Word file). Name/phone come from the database. Raw chat lines are not shown when AI
                  succeeds — only if AI fails twice.
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
            Full pipeline (waits until finished): collect chats → AI summary → Word (.docx) → WhatsApp notify.
            Do not leave until it completes.
          </CardDescription>
        </CardHeader>
        <CardContent class="flex flex-wrap items-end gap-3">
          <div class="space-y-1">
            <Label>Report date</Label>
            <Input v-model="runDate" type="date" :disabled="!canWrite || running" class="w-48" />
          </div>
          <Button v-if="canWrite" :disabled="running" @click="runNow">
            <Play class="mr-2 h-4 w-4" />
            {{ running ? 'Waiting for AI + send…' : 'Run now' }}
          </Button>
          <p v-if="running" class="text-xs text-muted-foreground w-full">
            Collecting chats → AI summary (waits for all batches) → Word file → WhatsApp. Do not close this page.
          </p>
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
          <CardDescription>Past generated reports (download Word DOCX or resend WhatsApp notify)</CardDescription>
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
                    @click="download(run.id, run.pdf_filename)"
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
