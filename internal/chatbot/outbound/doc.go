// Package outbound is the WhatsApp send gateway boundary (Phase 7+/P1).
// Production sends still use handlers.SendOutgoingMessage; this package
// reserves the port for a future thin wrapper.
package outbound
