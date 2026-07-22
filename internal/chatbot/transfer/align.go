package transfer

import (
	"context"

	"github.com/shridarpatil/whatomate/internal/chatbot/session"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Reason for completing a bot session on human handoff.
const ReasonHumanTransfer = "human_transfer"

// AlignSessionOnTransfer marks the chatbot session completed so automation
// does not resume under an active agent transfer (Phase 14).
// Safe no-op if mgr or sess is nil.
func AlignSessionOnTransfer(ctx context.Context, mgr *session.Manager, sess *models.ChatbotSession) error {
	if mgr == nil || sess == nil {
		return nil
	}
	if sess.Status != models.SessionStatusActive {
		return nil
	}
	return mgr.Complete(ctx, sess, models.SessionStatusCompleted, ReasonHumanTransfer)
}
