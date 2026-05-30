/**
 * flowValidator.ts
 * ─────────────────────────────────────────────────────────────────────────
 * Static graph linter for the Chatbot Flow Builder.
 *
 * Call validateFlowGraph(nodes, edges) after loading or editing a graph.
 * It returns an array of ValidationIssue objects — empty means all-clear.
 *
 * Severity levels
 * ───────────────
 *  • fatal   – the flow WILL break at runtime; must be fixed before deploying
 *  • error   – high-probability runtime failure or misconfiguration
 *  • warning – flow works but has unusual / suspicious structure
 *  • info    – advisory note (e.g. empty optional fields)
 */

import type { Node, Edge } from '@vue-flow/core'

// ─── Public types ───────────────────────────────────────────────────────────

export type IssueSeverity = 'fatal' | 'error' | 'warning' | 'info'
export type IssueCode =
  | 'GHOST_EDGE'
  | 'UNREACHABLE_NODE'
  | 'DEAD_END'
  | 'MISSING_BUTTONS'
  | 'MISSING_CONDITION_BRANCH'
  | 'EMPTY_MESSAGE'
  | 'MISSING_GOTO_TARGET'
  | 'MISSING_API_URL'
  | 'DUPLICATE_BUTTON_ID'
  | 'NO_ENTRY_EDGE'

export interface ValidationIssue {
  /** Stable code so the UI can sort/group issues */
  code: IssueCode
  severity: IssueSeverity
  /** Human-readable title (shown bold in the UI) */
  title: string
  /** Longer explanation of what's wrong and how to fix it */
  description: string
  /** The Vue Flow node ID that the issue belongs to (if any) */
  nodeId?: string
  /** A human-readable name for that node */
  nodeLabel?: string
  /** The Vue Flow edge ID that caused the issue (if any) */
  edgeId?: string
}

// ─── Node-type helpers ───────────────────────────────────────────────────────

/** Node types that naturally have no outgoing edges and are NOT dead-ends */
const TERMINAL_TYPES = new Set(['end', 'transfer', 'goto_flow'])

/** Node types that must have ≥ 1 outgoing edge to be valid */
const MUST_HAVE_OUTGOING = new Set([
  'start',
  'message',
  'prompt',
  'buttons',
  'api_call',
  'webhook',
  'condition',
  'timing',
  'whatsapp_flow',
])

/** Node types we skip for the "empty body" check */
const SKIP_EMPTY_BODY_CHECK = new Set(['start', 'end', 'condition', 'timing', 'goto_flow', 'transfer'])

// ─── Main validator ──────────────────────────────────────────────────────────

export function validateFlowGraph(nodes: Node[], edges: Edge[]): ValidationIssue[] {
  const issues: ValidationIssue[] = []

  if (nodes.length === 0) return issues

  // ── Build fast lookup structures ──────────────────────────────────────────
  const nodeMap = new Map<string, Node>()
  for (const n of nodes) nodeMap.set(n.id, n)

  // Sets of node IDs that appear as a source or target of any edge
  const hasOutgoing = new Set<string>()
  const hasIncoming = new Set<string>()
  for (const e of edges) {
    hasOutgoing.add(e.source)
    hasIncoming.add(e.target)
  }

  // The entry/start node: the one with type === 'start', or the explicit __start__ ID
  const startNode = nodes.find((n) => n.type === 'start' || n.id === '__start__')

  // ── Rule 1: Ghost Edge ("button no longer exists") ──────────────────────
  for (const edge of edges) {
    const sh = edge.sourceHandle
    if (!sh || !sh.startsWith('button:')) continue

    const sourceNode = nodeMap.get(edge.source)
    if (!sourceNode || sourceNode.type !== 'buttons') continue

    const buttonId = sh.slice('button:'.length)
    const btnList: any[] = sourceNode.data?.config?.buttons ?? []
    // The button must exist (by its `id` field) in the current config
    const exists = btnList.some((b: any) => b?.id === buttonId)

    if (!exists) {
      issues.push({
        code: 'GHOST_EDGE',
        severity: 'fatal',
        title: 'Broken Connection (Ghost Edge)',
        description:
          `This connection's trigger button ("${buttonId}") no longer exists in the ` +
          `Buttons node. The chat will silently freeze when a user reaches this step. ` +
          `Delete this edge and re-draw it after correcting the button ID.`,
        nodeId: sourceNode.id,
        nodeLabel: sourceNode.data?.label || sourceNode.id,
        edgeId: edge.id,
      })
    }
  }

  // ── Rule 2: Duplicate button IDs within a single Buttons node ────────────
  for (const node of nodes) {
    if (node.type !== 'buttons') continue
    const btnList: any[] = node.data?.config?.buttons ?? []
    const seen = new Map<string, number>()
    for (const btn of btnList) {
      const id: string = btn?.id || ''
      if (!id) continue
      seen.set(id, (seen.get(id) ?? 0) + 1)
    }
    for (const [id, count] of seen.entries()) {
      if (count > 1) {
        issues.push({
          code: 'DUPLICATE_BUTTON_ID',
          severity: 'error',
          title: 'Duplicate Button ID',
          description:
            `Button ID "${id}" appears ${count} times in this node. ` +
            `Only the first matching edge will ever be followed; the rest are dead. ` +
            `Give every button a unique ID.`,
          nodeId: node.id,
          nodeLabel: node.data?.label || node.id,
        })
      }
    }
  }

  // ── Rule 3: Unreachable nodes ─────────────────────────────────────────────
  for (const node of nodes) {
    // The start node is always reachable (it's the entry point)
    if (node.type === 'start' || node.id === '__start__') continue
    // A node that is never the target of any edge can never be reached
    if (!hasIncoming.has(node.id)) {
      issues.push({
        code: 'UNREACHABLE_NODE',
        severity: 'warning',
        title: 'Unreachable Node',
        description:
          `This node has no incoming connections. No conversation path leads here, ` +
          `so it will never execute. Connect it from another node or remove it.`,
        nodeId: node.id,
        nodeLabel: node.data?.label || node.id,
      })
    }
  }

  // ── Rule 4: Dead-end nodes ────────────────────────────────────────────────
  for (const node of nodes) {
    if (TERMINAL_TYPES.has(node.type as string)) continue
    if (!MUST_HAVE_OUTGOING.has(node.type as string)) continue
    if (hasOutgoing.has(node.id)) continue

    // Special case: buttons node — dead-end only if it has ≥ 1 reply button
    if (node.type === 'buttons') {
      const replyBtns = (node.data?.config?.buttons ?? []).filter(
        (b: any) => !b?.type || b?.type === 'reply',
      )
      if (replyBtns.length === 0) continue // all URL/phone — no outgoing handle expected
    }

    issues.push({
      code: 'DEAD_END',
      severity: 'warning',
      title: 'Dead End',
      description:
        `This node has no outgoing connections. The conversation will stop ` +
        `here without informing the user. Add a connection to the next step, ` +
        `or replace this with an End node.`,
      nodeId: node.id,
      nodeLabel: node.data?.label || node.id,
    })
  }

  // ── Rule 5: Start node not connected to anything ──────────────────────────
  if (startNode && !hasOutgoing.has(startNode.id)) {
    issues.push({
      code: 'NO_ENTRY_EDGE',
      severity: 'fatal',
      title: 'No Entry Connection',
      description:
        `The Start node is not connected to any other node. ` +
        `The flow will fail immediately. Connect the Start node to your first step.`,
      nodeId: startNode.id,
      nodeLabel: 'Start',
    })
  }

  // ── Rule 6: Buttons node with zero buttons ─────────────────────────────────
  for (const node of nodes) {
    if (node.type !== 'buttons') continue
    const btnList: any[] = node.data?.config?.buttons ?? []
    if (btnList.length === 0) {
      issues.push({
        code: 'MISSING_BUTTONS',
        severity: 'error',
        title: 'Buttons Node Has No Buttons',
        description:
          `This Buttons node is configured with zero buttons. WhatsApp will ` +
          `reject the message. Add at least one button in the properties panel.`,
        nodeId: node.id,
        nodeLabel: node.data?.label || node.id,
      })
    }
  }

  // ── Rule 7: Condition node missing branch edges ──────────────────────────
  for (const node of nodes) {
    if (node.type !== 'condition') continue
    const outEdges = edges.filter((e) => e.source === node.id)
    const handles = new Set(outEdges.map((e) => e.sourceHandle ?? ''))
    const hasTrue  = handles.has('true')
    const hasFalse = handles.has('false')
    if (!hasTrue || !hasFalse) {
      const missing = [!hasTrue && '"True" branch', !hasFalse && '"False" branch'].filter(Boolean).join(' and ')
      issues.push({
        code: 'MISSING_CONDITION_BRANCH',
        severity: 'error',
        title: 'Condition Node Missing Branch',
        description:
          `The ${missing} is not connected. When the condition evaluates to that ` +
          `result, the conversation will have nowhere to go and will freeze. ` +
          `Draw a connection from both the True and False handles.`,
        nodeId: node.id,
        nodeLabel: node.data?.label || node.id,
      })
    }
  }

  // ── Rule 8: Empty message body ────────────────────────────────────────────
  for (const node of nodes) {
    if (SKIP_EMPTY_BODY_CHECK.has(node.type as string)) continue
    const cfg = node.data?.config ?? {}
    const body: string =
      cfg.message ?? cfg.body ?? cfg.message_template ?? ''
    if (!body.trim()) {
      issues.push({
        code: 'EMPTY_MESSAGE',
        severity: 'info',
        title: 'Empty Message Body',
        description:
          `This node has no message text configured. It will send a blank ` +
          `message to the user. Fill in the message content in the properties panel.`,
        nodeId: node.id,
        nodeLabel: node.data?.label || node.id,
      })
    }
  }

  // ── Rule 9: Go-to-Flow node with no target flow ──────────────────────────
  for (const node of nodes) {
    if (node.type !== 'goto_flow') continue
    if (!node.data?.config?.flow_id) {
      issues.push({
        code: 'MISSING_GOTO_TARGET',
        severity: 'error',
        title: 'Go-to-Flow Has No Target',
        description:
          `This node will transfer the user to another flow, but no target ` +
          `flow has been selected. The transfer will silently fail. ` +
          `Open the properties panel and choose a destination flow.`,
        nodeId: node.id,
        nodeLabel: node.data?.label || node.id,
      })
    }
  }

  // ── Rule 10: API / Webhook node with no URL ───────────────────────────────
  for (const node of nodes) {
    if (node.type !== 'api_call' && node.type !== 'webhook') continue
    if (!node.data?.config?.url?.trim()) {
      issues.push({
        code: 'MISSING_API_URL',
        severity: 'error',
        title: 'API Node Has No URL',
        description:
          `This API node has no endpoint URL configured. The call will fail ` +
          `at runtime and the flow will stall. Set the URL in the properties panel.`,
        nodeId: node.id,
        nodeLabel: node.data?.label || node.id,
      })
    }
  }

  // ── Sort: fatal → error → warning → info ─────────────────────────────────
  const ORDER: Record<IssueSeverity, number> = { fatal: 0, error: 1, warning: 2, info: 3 }
  issues.sort((a, b) => ORDER[a.severity] - ORDER[b.severity])

  return issues
}
