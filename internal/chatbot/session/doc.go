// Package session owns chatbot session lifecycle: get-or-create, save with
// optimistic concurrency, complete, expire, and duplicate open-session cleanup.
//
// Phase 3 of the chatbot refactor. Handlers should prefer Manager over ad-hoc
// SQL against chatbot_sessions.
package session
