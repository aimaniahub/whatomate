// Package idempotency owns WAMID at-most-once processing leases.
//
// Begin claims a WhatsApp message id; Complete marks success; expired
// processing leases may be reclaimed. Used when chatbot.idempotency_v1 is on.
package idempotency

