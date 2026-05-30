/**
 * flowImportExport.ts
 * ─────────────────────────────────────────────────────────────────────────
 * Pure utility functions for Export and Import of chatbot flow canvases.
 *
 * Design decisions:
 *  • Export bundles the raw Vue Flow node/edge shapes PLUS top-level flow
 *    metadata so the recipient file is self-contained.
 *  • Import regenerates every ID (node IDs, edge IDs) to prevent database
 *    primary-key collisions when the same file is imported more than once
 *    or imported into a different organisation.
 *  • The exported format version is stamped so future migrations can detect
 *    old files and handle them gracefully.
 */

import type { Node, Edge } from '@vue-flow/core'
import { MarkerType } from '@vue-flow/core'

// ─── Export ──────────────────────────────────────────────────────────────────

export interface FlowExportMeta {
  name: string
  description: string
  enabled: boolean
  triggerKeywords: string
  initialMessage: string
  completionMessage: string
}

export interface FlowExportBundle {
  /** Semver-style version so future readers can handle old files. */
  exportVersion: 1
  exportedAt: string
  meta: FlowExportMeta
  nodes: Node[]
  edges: Edge[]
}

/**
 * Download the current canvas state as a .json file.
 *
 * @param nodes   Reactive Vue Flow nodes array
 * @param edges   Reactive Vue Flow edges array
 * @param meta    Top-level flow metadata (name, description, …)
 * @param filename Desired download filename (default: chatbot_flow_export.json)
 */
export function exportFlowToFile(
  nodes: Node[],
  edges: Edge[],
  meta: FlowExportMeta,
  filename = 'chatbot_flow_export.json',
): void {
  const bundle: FlowExportBundle = {
    exportVersion: 1,
    exportedAt: new Date().toISOString(),
    meta,
    // Deep-clone so the snapshot is immutable w.r.t. subsequent canvas edits
    nodes: JSON.parse(JSON.stringify(nodes)),
    edges: JSON.parse(JSON.stringify(edges)),
  }

  const json = JSON.stringify(bundle, null, 2)
  const blob = new Blob([json], { type: 'application/json' })
  const url  = URL.createObjectURL(blob)

  const a    = document.createElement('a')
  a.href     = url
  a.download = filename
  a.click()

  // Revoke after a tick so browsers with slower download UIs aren't cut off
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

// ─── Import ───────────────────────────────────────────────────────────────────

export interface ImportResult {
  ok: true
  nodes: Node[]
  edges: Edge[]
  meta: FlowExportMeta
}

export interface ImportError {
  ok: false
  message: string
}

let _importCounter = Date.now()
function freshId(prefix = 'node') {
  return `${prefix}_imp_${++_importCounter}`
}

/**
 * Parse and validate an imported JSON file, regenerating all IDs.
 *
 * The function is **synchronous** once the file is read; the Promise just
 * wraps the FileReader API.
 *
 * @param file  The File object from the hidden <input type="file"> element
 * @returns     A Promise resolving to ImportResult | ImportError
 */
export function importFlowFromFile(file: File): Promise<ImportResult | ImportError> {
  return new Promise((resolve) => {
    const reader = new FileReader()

    reader.onload = (evt) => {
      try {
        const raw = evt.target?.result as string
        const parsed = JSON.parse(raw)

        // ── Basic structural validation ──────────────────────────────────
        if (!parsed || typeof parsed !== 'object') {
          return resolve({ ok: false, message: 'The file is not a valid JSON object.' })
        }

        // Support both the structured export bundle AND a bare {nodes,edges} shape
        const bundle = (parsed.exportVersion === 1 ? parsed : { ...parsed, meta: null }) as FlowExportBundle

        if (!Array.isArray(bundle.nodes) || !Array.isArray(bundle.edges)) {
          return resolve({
            ok: false,
            message: 'Invalid file: missing "nodes" or "edges" arrays. Make sure you are uploading a Whatomate flow export file.',
          })
        }

        // ── ID regeneration ───────────────────────────────────────────────
        // Pass 1: build old-ID → new-ID map for every node
        const idMap = new Map<string, string>()
        for (const n of bundle.nodes) {
          // Keep the special __start__ sentinel identical; it is never
          // stored in the DB and its uniqueness scope is per-canvas session.
          const newId = n.id === '__start__' ? '__start__' : freshId('node')
          idMap.set(n.id, newId)
        }

        // Pass 2: rebuild nodes with new IDs
        const newNodes: Node[] = bundle.nodes.map((n) => ({
          ...n,
          id: idMap.get(n.id)!,
          // Preserve position, data, type verbatim — only the ID changes
          data: JSON.parse(JSON.stringify(n.data ?? {})),
        }))

        // Pass 3: rebuild edges with new IDs, remapped source/target
        const newEdges: Edge[] = bundle.edges.map((e) => {
          const newSource = idMap.get(e.source) ?? e.source
          const newTarget = idMap.get(e.target) ?? e.target
          return {
            ...e,
            id:     freshId('edge'),
            source: newSource,
            target: newTarget,
            // Restore display properties in case they were missing
            type:      e.type ?? 'default',
            animated:  e.animated ?? true,
            markerEnd: e.markerEnd ?? MarkerType.ArrowClosed,
          }
        })

        // ── Meta extraction ────────────────────────────────────────────────
        const meta: FlowExportMeta = bundle.meta ?? {
          name:              '',
          description:       '',
          enabled:           true,
          triggerKeywords:   '',
          initialMessage:    '',
          completionMessage: '',
        }

        resolve({ ok: true, nodes: newNodes, edges: newEdges, meta })
      } catch (err) {
        resolve({
          ok: false,
          message: `Failed to parse file: ${err instanceof Error ? err.message : String(err)}`,
        })
      }
    }

    reader.onerror = () =>
      resolve({ ok: false, message: 'Could not read the selected file. Please try again.' })

    reader.readAsText(file)
  })
}
