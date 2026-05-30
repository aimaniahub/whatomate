<script setup lang="ts">
import { computed } from 'vue'
import type { Node, Edge } from '@vue-flow/core'
import { MarkerType } from '@vue-flow/core'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  SelectSeparator,
} from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  ArrowRight,
  MousePointerClick,
  GitBranch,
  Clock,
  Globe,
  Users,
  MessageSquare,
  ExternalLink,
  StopCircle,
  Play,
  MessageCircle,
  Shuffle,
  Unlink,
} from 'lucide-vue-next'

// ---------------------------------------------------------------------------
// Props & emits
// ---------------------------------------------------------------------------
const props = defineProps<{
  open: boolean
  nodes: Node[]
  edges: Edge[]
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
  /**
   * Fired when the user rewires an edge via the Destination dropdown.
   * The parent calls removeEdges(toRemove) + addEdges(toAdd) +
   * sets hasUnsavedChanges = true.
   */
  'rewire': [payload: { removeEdgeId: string; newEdge: Edge | null }]
}>()

// ---------------------------------------------------------------------------
// Node type meta — colour dot + icon, mirrors the flow-builder palette
// ---------------------------------------------------------------------------
const NODE_META: Record<string, { color: string; icon: any; label: string }> = {
  start:         { color: 'bg-slate-500',   icon: Play,             label: 'Start' },
  message:       { color: 'bg-blue-600',    icon: MessageSquare,    label: 'Text' },
  prompt:        { color: 'bg-blue-500',    icon: MessageSquare,    label: 'Prompt' },
  buttons:       { color: 'bg-purple-600',  icon: MousePointerClick, label: 'Buttons' },
  api_call:      { color: 'bg-orange-600',  icon: Globe,            label: 'API' },
  webhook:       { color: 'bg-orange-500',  icon: Globe,            label: 'Webhook' },
  whatsapp_flow: { color: 'bg-green-600',   icon: MessageCircle,    label: 'WA Flow' },
  transfer:      { color: 'bg-amber-600',   icon: Users,            label: 'Transfer' },
  condition:     { color: 'bg-indigo-600',  icon: GitBranch,        label: 'Condition' },
  timing:        { color: 'bg-cyan-600',    icon: Clock,            label: 'Timing' },
  goto_flow:     { color: 'bg-teal-600',    icon: ExternalLink,     label: 'Go to Flow' },
  end:           { color: 'bg-slate-600',   icon: StopCircle,       label: 'End' },
}
const DEFAULT_META = { color: 'bg-gray-500', icon: Shuffle, label: 'Node' }

// ---------------------------------------------------------------------------
// Derived routing rows
// ---------------------------------------------------------------------------
interface RoutingRow {
  edgeId: string
  sourceId: string
  sourceLabel: string
  sourceType: string
  /** The sourceHandle value stored on the edge (undefined = default) */
  sourceHandleId: string | null
  trigger: string
  triggerKind: 'button' | 'condition' | 'default'
  targetId: string
  targetLabel: string
  targetType: string
}

const nodeMap = computed<Map<string, Node>>(() => {
  const m = new Map<string, Node>()
  for (const n of props.nodes) m.set(n.id, n)
  return m
})

/** All selectable nodes for the Destination dropdown (excludes the start node) */
const destinationOptions = computed(() =>
  props.nodes.map((n) => ({
    id: n.id,
    label: (n.data?.label as string) || n.id,
    type: (n.type as string) || 'unknown',
  })),
)

function resolveTrigger(
  handle: string | null | undefined,
  sourceNode: Node | undefined,
): { trigger: string; triggerKind: RoutingRow['triggerKind'] } {
  const h = handle || ''
  if (h.startsWith('button:')) {
    const buttonId = h.slice('button:'.length)
    const btnList: any[] = sourceNode?.data?.config?.buttons || []
    const match = btnList.find((b: any) => b.id === buttonId || `button:${b.id}` === h)
    return { trigger: match?.title || buttonId, triggerKind: 'button' }
  }
  if (h === 'true')              return { trigger: 'True / Yes',           triggerKind: 'condition' }
  if (h === 'false')             return { trigger: 'False / No',           triggerKind: 'condition' }
  if (h === 'in_hours')          return { trigger: 'In Hours',             triggerKind: 'condition' }
  if (h === 'out_of_hours')      return { trigger: 'Out of Hours',         triggerKind: 'condition' }
  if (h === 'validation_failed') return { trigger: 'Validation Failed',    triggerKind: 'condition' }
  if (h === 'max_retries')       return { trigger: 'Max Retries Exceeded', triggerKind: 'condition' }
  if (h && h !== 'default')      return { trigger: h,                      triggerKind: 'condition' }
  return { trigger: 'Default Route', triggerKind: 'default' }
}

const rows = computed<RoutingRow[]>(() =>
  props.edges.map((edge) => {
    const sourceNode = nodeMap.value.get(edge.source)
    const targetNode = nodeMap.value.get(edge.target)
    const { trigger, triggerKind } = resolveTrigger(edge.sourceHandle, sourceNode)
    return {
      edgeId: edge.id,
      sourceId: edge.source,
      sourceLabel: (sourceNode?.data?.label as string) || edge.source,
      sourceType:  (sourceNode?.type  as string) || 'unknown',
      sourceHandleId: edge.sourceHandle ?? null,
      trigger,
      triggerKind,
      targetId:    edge.target,
      targetLabel: (targetNode?.data?.label as string) || edge.target,
      targetType:  (targetNode?.type  as string) || 'unknown',
    }
  }),
)

// Group by source node
const groupedRows = computed(() => {
  const groups: Array<{ sourceId: string; sourceLabel: string; sourceType: string; rows: RoutingRow[] }> = []
  const seen = new Map<string, number>()
  for (const row of rows.value) {
    if (!seen.has(row.sourceId)) {
      seen.set(row.sourceId, groups.length)
      groups.push({ sourceId: row.sourceId, sourceLabel: row.sourceLabel, sourceType: row.sourceType, rows: [] })
    }
    groups[seen.get(row.sourceId)!].rows.push(row)
  }
  return groups
})

// ---------------------------------------------------------------------------
// Rewire logic — called when the user picks a new destination in the dropdown
// ---------------------------------------------------------------------------
let _edgeCounter = Date.now()
function genEdgeId() { return `rt_edge_${++_edgeCounter}` }

function onDestinationChange(row: RoutingRow, newTargetId: string) {
  if (newTargetId === row.targetId) return   // no real change

  const newEdge: Edge | null =
    newTargetId === '__none__'
      ? null
      : {
          id: genEdgeId(),
          source: row.sourceId,
          target: newTargetId,
          sourceHandle: row.sourceHandleId ?? undefined,
          type: 'default',
          animated: true,
          markerEnd: MarkerType.ArrowClosed,
          label:
            row.sourceHandleId && row.sourceHandleId !== 'default'
              ? row.sourceHandleId
              : '',
        }

  emit('rewire', { removeEdgeId: row.edgeId, newEdge })
}

// ---------------------------------------------------------------------------
// UI helpers
// ---------------------------------------------------------------------------
function triggerVariant(kind: RoutingRow['triggerKind']): 'default' | 'secondary' | 'outline' {
  if (kind === 'button')    return 'default'
  if (kind === 'condition') return 'secondary'
  return 'outline'
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent
      id="flow-routing-table-dialog"
      class="max-w-5xl w-[95vw] h-[88vh] flex flex-col p-0 gap-0"
    >
      <!-- ── Header ── -->
      <DialogHeader class="px-6 pt-5 pb-4 border-b flex-shrink-0">
        <DialogTitle class="flex items-center gap-2 text-base font-semibold">
          <GitBranch class="w-4 h-4 text-primary" />
          Interactive Routing Table
        </DialogTitle>
        <DialogDescription class="text-xs text-muted-foreground mt-0.5">
          View and rewire every connection without touching the canvas.
          <span class="font-medium text-foreground">{{ rows.length }}</span>
          edge{{ rows.length !== 1 ? 's' : '' }} across
          <span class="font-medium text-foreground">{{ groupedRows.length }}</span>
          source node{{ groupedRows.length !== 1 ? 's' : '' }}.
          Changes are applied instantly and mark the flow as unsaved.
        </DialogDescription>
      </DialogHeader>

      <!-- ── Body ── -->
      <ScrollArea class="flex-1">
        <!-- Empty state -->
        <div
          v-if="rows.length === 0"
          class="flex flex-col items-center justify-center py-20 text-muted-foreground gap-3"
        >
          <ArrowRight class="w-10 h-10 opacity-30" />
          <p class="text-sm">No connections yet. Draw edges on the canvas to populate this table.</p>
        </div>

        <!-- Grouped rows -->
        <div v-else class="p-4 space-y-5">
          <div
            v-for="group in groupedRows"
            :key="group.sourceId"
            class="rounded-lg border overflow-hidden shadow-sm"
          >
            <!-- Group header -->
            <div class="flex items-center gap-2 px-4 py-2.5 bg-muted/60 border-b">
              <component
                :is="(NODE_META[group.sourceType] ?? DEFAULT_META).icon"
                class="w-3.5 h-3.5 flex-shrink-0 text-muted-foreground"
              />
              <span class="font-semibold text-sm truncate">{{ group.sourceLabel }}</span>
              <Badge variant="outline" class="text-[10px] px-1.5 ml-auto flex-shrink-0">
                {{ (NODE_META[group.sourceType] ?? DEFAULT_META).label }}
              </Badge>
            </div>

            <!-- Edges -->
            <Table>
              <TableHeader>
                <TableRow class="hover:bg-transparent border-b">
                  <TableHead class="w-[36%] text-xs pl-4 py-2 font-medium">Trigger / Handle</TableHead>
                  <TableHead class="w-[7%] text-center text-xs py-2"></TableHead>
                  <TableHead class="text-xs py-2 font-medium">Destination Node</TableHead>
                  <TableHead class="w-[14%] text-xs py-2 pr-4 text-right font-medium">Target Type</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow
                  v-for="row in group.rows"
                  :key="row.edgeId"
                  class="text-sm group/row hover:bg-muted/20 transition-colors"
                >
                  <!-- Trigger badge -->
                  <TableCell class="pl-4 py-2.5">
                    <Badge
                      :variant="triggerVariant(row.triggerKind)"
                      class="text-xs font-normal gap-1"
                    >
                      <component
                        :is="row.triggerKind === 'button'
                          ? MousePointerClick
                          : row.triggerKind === 'condition'
                            ? GitBranch
                            : ArrowRight"
                        class="w-3 h-3 flex-shrink-0"
                      />
                      {{ row.trigger }}
                    </Badge>
                  </TableCell>

                  <!-- Arrow -->
                  <TableCell class="text-center py-2.5 text-muted-foreground">
                    <ArrowRight class="w-3.5 h-3.5 inline-block" />
                  </TableCell>

                  <!-- ── Destination Select ── -->
                  <TableCell class="py-2">
                    <Select
                      :model-value="row.targetId"
                      @update:model-value="(val) => onDestinationChange(row, val as string)"
                    >
                      <SelectTrigger
                        class="h-8 text-xs w-full max-w-xs border-dashed hover:border-primary focus:border-primary transition-colors"
                        :id="`dest-select-${row.edgeId}`"
                      >
                        <div class="flex items-center gap-1.5 min-w-0">
                          <span
                            :class="['w-2 h-2 rounded-full flex-shrink-0',
                              (NODE_META[row.targetType] ?? DEFAULT_META).color]"
                          />
                          <SelectValue :placeholder="row.targetLabel" class="truncate text-xs" />
                        </div>
                      </SelectTrigger>

                      <SelectContent class="max-h-64">
                        <!-- All nodes grouped by type -->
                        <SelectItem
                          v-for="opt in destinationOptions"
                          :key="opt.id"
                          :value="opt.id"
                          class="text-xs"
                        >
                          <div class="flex items-center gap-2">
                            <span
                              :class="['w-2 h-2 rounded-full flex-shrink-0',
                                (NODE_META[opt.type] ?? DEFAULT_META).color]"
                            />
                            <span class="truncate">{{ opt.label }}</span>
                            <span class="text-muted-foreground ml-auto pl-3 text-[10px]">
                              {{ (NODE_META[opt.type] ?? DEFAULT_META).label }}
                            </span>
                          </div>
                        </SelectItem>

                        <SelectSeparator />

                        <!-- Disconnect option -->
                        <SelectItem value="__none__" class="text-xs text-destructive">
                          <div class="flex items-center gap-2">
                            <Unlink class="w-3 h-3 flex-shrink-0" />
                            <span>None / Disconnect</span>
                          </div>
                        </SelectItem>
                      </SelectContent>
                    </Select>
                  </TableCell>

                  <!-- Target type (reflects live selection) -->
                  <TableCell class="text-right pr-4 py-2.5 text-muted-foreground text-xs">
                    {{ (NODE_META[row.targetType] ?? DEFAULT_META).label }}
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </div>
      </ScrollArea>

      <!-- ── Footer hint ── -->
      <div class="px-6 py-3 border-t flex-shrink-0 bg-muted/30">
        <p class="text-xs text-muted-foreground">
          💡 Use the <span class="font-medium text-foreground">Destination</span> dropdown on any row to rewire it.
          The canvas updates instantly. Remember to click <span class="font-medium text-foreground">Save Flow</span> when done.
        </p>
      </div>
    </DialogContent>
  </Dialog>
</template>
