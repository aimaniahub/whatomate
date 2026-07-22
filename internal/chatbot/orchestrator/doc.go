// Package orchestrator hosts the Conversation Orchestrator and priority ladder.
//
// Phase 5: HandleTurn walks the ladder for timeline/observability, then
// delegates the entire turn to a LegacyHandler (handlers.processIncomingMessageFull).
// Later phases replace ladder stubs with real engines while keeping this shell.
package orchestrator
