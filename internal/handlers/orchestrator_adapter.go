package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shridarpatil/whatomate/internal/chatbot/orchestrator"
	"github.com/shridarpatil/whatomate/internal/chatbot/turn"
)

// logShadowPlan logs the Phase 5 planned route without executing the orchestrator.
func (a *App) logShadowPlan(ids turn.IDs) {
	plan := orchestrator.PlanRoute()
	a.Log.Info("Orchestrator shadow plan (legacy_direct path)",
		ids.With(
			"winner", plan.Winner,
			"deferred_count", len(plan.Deferred),
			"path", "legacy_direct",
		)...,
	)
}

// Ensure App implements orchestrator.LegacyHandler.
var _ orchestrator.LegacyHandler = (*App)(nil)

// HandleLegacyTurn unmarshals the turn payload and runs the existing full processor.
// Phase 5: this is the sole work unit behind Orchestrator.HandleTurn so behavior
// matches direct processIncomingMessageFull.
func (a *App) HandleLegacyTurn(ctx context.Context, tc *turn.Context) error {
	if a == nil {
		return fmt.Errorf("legacy handler: nil app")
	}
	if tc == nil {
		return fmt.Errorf("legacy handler: nil turn context")
	}
	var msg IncomingTextMessage
	if len(tc.RawMessage) > 0 {
		if err := json.Unmarshal(tc.RawMessage, &msg); err != nil {
			return fmt.Errorf("legacy handler: unmarshal message: %w", err)
		}
	}
	// Prefer IDs from turn context; processor still validates.
	ids := tc.IDs
	if !ids.Valid() {
		ids = turn.NewIDs()
	}
	a.processIncomingMessageFull(tc.PhoneNumberID, msg, tc.ProfileName, ids)
	return nil
}

// buildTurnContext packs webhook fields into a turn.Context for the orchestrator.
func buildTurnContext(phoneNumberID, profileName string, msg IncomingTextMessage, ids turn.IDs) (*turn.Context, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	text := ""
	buttonID := ""
	if msg.Type == "text" && msg.Text != nil {
		text = msg.Text.Body
	} else if msg.Type == "interactive" && msg.Interactive != nil {
		if msg.Interactive.ButtonReply != nil {
			text = msg.Interactive.ButtonReply.Title
			buttonID = msg.Interactive.ButtonReply.ID
		} else if msg.Interactive.ListReply != nil {
			text = msg.Interactive.ListReply.Title
			buttonID = msg.Interactive.ListReply.ID
		}
	} else if msg.Type == "button" && msg.Button != nil {
		text = msg.Button.Text
		buttonID = msg.Button.Payload
	}
	return &turn.Context{
		IDs:           ids,
		PhoneNumberID: phoneNumberID,
		ProfileName:   profileName,
		FromPhone:     msg.From,
		WAMID:         msg.ID,
		MsgType:       msg.Type,
		Text:          text,
		ButtonID:      buttonID,
		RawMessage:    raw,
	}, nil
}
