// Package knowledge re-exports AI knowledge short-circuit helpers (Phase 13).
// Prefer chatbot/ai.KnowledgeHit for static FAQ-style answers before RAG/LLM.
package knowledge

import "github.com/shridarpatil/whatomate/internal/chatbot/ai"

// Hit is an alias for ai.KnowledgeHit.
var Hit = ai.KnowledgeHit
