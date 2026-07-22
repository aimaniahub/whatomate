package handlers

import (
	"context"
	"time"

	"github.com/shridarpatil/whatomate/internal/chatbot/session"
)

// NewSessionSweeper builds a session expire/dedupe sweeper bound to the app engine.
func NewSessionSweeper(a *App) *session.Sweeper {
	if a == nil || a.Chatbot == nil || a.Chatbot.Sessions == nil {
		return nil
	}
	s := session.NewSweeper(a.Chatbot.Sessions, a.Log)
	s.Interval = time.Minute
	s.TimeoutMins = session.DefaultTimeoutMins
	s.BatchLimit = 500
	return s
}

// RunSessionSweeper is a thin alias for tests / manual starts.
func (a *App) RunSessionSweeper(ctx context.Context) {
	s := NewSessionSweeper(a)
	if s == nil {
		return
	}
	s.Run(ctx)
}
