// Package ai owns free-text pipeline planning: Knowledge → RAG → LLM → fallback (Phase 13).
// Handlers still execute HTTP/LLM calls; this package owns order and knowledge short-circuit.
package ai
