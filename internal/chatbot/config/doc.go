// Package config provides the chatbot feature-flag harness and hierarchical
// resolution (global → organization → WhatsApp account).
//
// All flags default to false so production traffic remains on the legacy
// processor until a later phase enables them.
package config
