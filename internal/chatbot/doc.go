// Package chatbot is the root of the refactored conversation runtime.
//
// Phase 1 establishes package boundaries, a feature-flag harness, and turn
// correlation identifiers. Business logic remains in internal/handlers until
// later phases extract engines behind the Conversation Orchestrator.
//
// See CHATBOT_TARGET_ARCHITECTURE.md and CHATBOT_IMPLEMENTATION_SPECIFICATION.md.
package chatbot
