// Package conversation owns thin conversation-level policies: greeting-once,
// restart intent, and free-chat session ownership signals.
//
// Phase 4 of the chatbot refactor. It does not replace the inbound orchestrator;
// it extracts policy decisions that used to live as ad-hoc ifs in the processor.
package conversation
