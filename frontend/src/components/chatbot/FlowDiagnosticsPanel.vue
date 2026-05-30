<script setup lang="ts">
import type { ValidationIssue, IssueSeverity } from '@/utils/flowValidator'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  AlertTriangle,
  AlertCircle,
  Info,
  Zap,
  CheckCircle2,
  Navigation,
  RefreshCw,
} from 'lucide-vue-next'
import { computed } from 'vue'

// ─── Props & emits ──────────────────────────────────────────────────────────
const props = defineProps<{
  open: boolean
  issues: ValidationIssue[]
  isRunning: boolean
  /** Has the validator been run at least once? */
  hasRun: boolean
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  /** User clicked on an issue — parent should focus that node */
  'focus-node': [nodeId: string]
  /** User wants to re-run the validator */
  'rerun': []
}>()

// ─── Severity config ─────────────────────────────────────────────────────────
const SEV_CONFIG: Record<IssueSeverity, { icon: any; label: string; badgeClass: string; rowClass: string }> = {
  fatal: {
    icon: Zap,
    label: 'Fatal',
    badgeClass: 'bg-red-600 text-white border-0',
    rowClass: 'border-l-4 border-red-500 bg-red-50 dark:bg-red-950/20',
  },
  error: {
    icon: AlertCircle,
    label: 'Error',
    badgeClass: 'bg-orange-500 text-white border-0',
    rowClass: 'border-l-4 border-orange-400 bg-orange-50 dark:bg-orange-950/20',
  },
  warning: {
    icon: AlertTriangle,
    label: 'Warning',
    badgeClass: 'bg-yellow-500 text-white border-0',
    rowClass: 'border-l-4 border-yellow-400 bg-yellow-50 dark:bg-yellow-950/20',
  },
  info: {
    icon: Info,
    label: 'Info',
    badgeClass: 'bg-blue-500 text-white border-0',
    rowClass: 'border-l-4 border-blue-400 bg-blue-50 dark:bg-blue-950/20',
  },
}

// ─── Summary counts ──────────────────────────────────────────────────────────
const counts = computed(() => ({
  fatal:   props.issues.filter((i) => i.severity === 'fatal').length,
  error:   props.issues.filter((i) => i.severity === 'error').length,
  warning: props.issues.filter((i) => i.severity === 'warning').length,
  info:    props.issues.filter((i) => i.severity === 'info').length,
}))

const isClean = computed(() => props.hasRun && props.issues.length === 0)

function onFocusNode(issue: ValidationIssue) {
  if (issue.nodeId) emit('focus-node', issue.nodeId)
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent
      id="flow-diagnostics-dialog"
      class="max-w-2xl w-[95vw] h-[80vh] flex flex-col p-0 gap-0"
    >
      <!-- ── Header ── -->
      <DialogHeader class="px-6 pt-5 pb-4 border-b flex-shrink-0">
        <div class="flex items-center justify-between">
          <DialogTitle class="flex items-center gap-2 text-base font-semibold">
            <AlertCircle class="w-4 h-4 text-primary" />
            Flow Diagnostics
          </DialogTitle>
          <Button
            variant="outline"
            size="sm"
            :disabled="isRunning"
            class="h-7 text-xs gap-1.5"
            @click="emit('rerun')"
          >
            <RefreshCw :class="['w-3 h-3', isRunning && 'animate-spin']" />
            Re-run
          </Button>
        </div>
        <DialogDescription class="text-xs text-muted-foreground mt-1">
          Static analysis of your flow graph. Click any issue to jump to the problem node.
        </DialogDescription>

        <!-- Summary bar (shown after first run) -->
        <div v-if="hasRun" class="flex items-center gap-2 mt-3 flex-wrap">
          <template v-if="isClean">
            <CheckCircle2 class="w-4 h-4 text-green-500" />
            <span class="text-sm font-medium text-green-600 dark:text-green-400">All checks passed — flow looks good!</span>
          </template>
          <template v-else>
            <div v-if="counts.fatal > 0" class="flex items-center gap-1 text-xs font-medium text-red-600">
              <Zap class="w-3 h-3" />{{ counts.fatal }} fatal
            </div>
            <div v-if="counts.error > 0" class="flex items-center gap-1 text-xs font-medium text-orange-600">
              <AlertCircle class="w-3 h-3" />{{ counts.error }} error{{ counts.error !== 1 ? 's' : '' }}
            </div>
            <div v-if="counts.warning > 0" class="flex items-center gap-1 text-xs font-medium text-yellow-600">
              <AlertTriangle class="w-3 h-3" />{{ counts.warning }} warning{{ counts.warning !== 1 ? 's' : '' }}
            </div>
            <div v-if="counts.info > 0" class="flex items-center gap-1 text-xs font-medium text-blue-600">
              <Info class="w-3 h-3" />{{ counts.info }} info
            </div>
          </template>
        </div>
      </DialogHeader>

      <!-- ── Body ── -->
      <ScrollArea class="flex-1">

        <!-- Not yet run -->
        <div
          v-if="!hasRun && !isRunning"
          class="flex flex-col items-center justify-center py-20 gap-3 text-muted-foreground"
        >
          <AlertCircle class="w-10 h-10 opacity-30" />
          <p class="text-sm">Click <span class="font-semibold text-foreground">Run Diagnostics</span> in the toolbar to analyse your flow.</p>
        </div>

        <!-- Running -->
        <div
          v-else-if="isRunning"
          class="flex flex-col items-center justify-center py-20 gap-3 text-muted-foreground"
        >
          <RefreshCw class="w-8 h-8 animate-spin opacity-40" />
          <p class="text-sm">Analysing graph…</p>
        </div>

        <!-- All-clear -->
        <div
          v-else-if="isClean"
          class="flex flex-col items-center justify-center py-20 gap-3"
        >
          <CheckCircle2 class="w-12 h-12 text-green-500 opacity-80" />
          <p class="text-sm font-medium text-green-600 dark:text-green-400">No issues found.</p>
          <p class="text-xs text-muted-foreground">Your flow passed all 10 validation checks.</p>
        </div>

        <!-- Issue list -->
        <div v-else class="p-4 space-y-2.5">
          <div
            v-for="(issue, idx) in issues"
            :key="idx"
            :class="[
              'rounded-lg p-3.5 cursor-pointer transition-all duration-150',
              SEV_CONFIG[issue.severity].rowClass,
              issue.nodeId
                ? 'hover:shadow-sm hover:scale-[1.005] active:scale-100'
                : 'cursor-default',
            ]"
            @click="onFocusNode(issue)"
          >
            <div class="flex items-start gap-3">
              <!-- Severity icon -->
              <component
                :is="SEV_CONFIG[issue.severity].icon"
                class="w-4 h-4 flex-shrink-0 mt-0.5"
                :class="{
                  'text-red-500':    issue.severity === 'fatal',
                  'text-orange-500': issue.severity === 'error',
                  'text-yellow-500': issue.severity === 'warning',
                  'text-blue-500':   issue.severity === 'info',
                }"
              />

              <div class="flex-1 min-w-0">
                <!-- Title row -->
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="text-sm font-semibold leading-tight">{{ issue.title }}</span>
                  <Badge :class="['text-[10px] px-1.5 py-0', SEV_CONFIG[issue.severity].badgeClass]">
                    {{ SEV_CONFIG[issue.severity].label }}
                  </Badge>
                </div>

                <!-- Node label -->
                <div v-if="issue.nodeLabel" class="flex items-center gap-1 mt-0.5">
                  <span class="text-xs text-muted-foreground">Node:</span>
                  <span class="text-xs font-medium">{{ issue.nodeLabel }}</span>
                  <Navigation
                    v-if="issue.nodeId"
                    class="w-2.5 h-2.5 text-primary ml-0.5 flex-shrink-0"
                  />
                </div>

                <!-- Description -->
                <p class="text-xs text-muted-foreground mt-1.5 leading-relaxed">
                  {{ issue.description }}
                </p>
              </div>
            </div>
          </div>
        </div>
      </ScrollArea>

      <!-- ── Footer ── -->
      <div class="px-6 py-3 border-t flex-shrink-0 bg-muted/30">
        <p class="text-xs text-muted-foreground">
          💡 <span class="font-medium text-foreground">Fatal</span> and
          <span class="font-medium text-foreground">Error</span> issues will cause runtime failures.
          Click any issue to jump to the node on the canvas.
        </p>
      </div>
    </DialogContent>
  </Dialog>
</template>
