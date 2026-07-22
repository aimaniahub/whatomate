package session

import (
	"context"
	"time"
)

// Logger is the minimal log surface the sweeper needs.
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Sweeper periodically expires stale sessions and closes duplicate opens.
type Sweeper struct {
	Manager     *Manager
	Log         Logger
	Interval    time.Duration
	TimeoutMins int // default inactivity window when expires_at is null
	BatchLimit  int
}

// NewSweeper constructs a sweeper. interval defaults to 1 minute.
func NewSweeper(m *Manager, log Logger) *Sweeper {
	return &Sweeper{
		Manager:     m,
		Log:         log,
		Interval:    time.Minute,
		TimeoutMins: DefaultTimeoutMins,
		BatchLimit:  500,
	}
}

// Run blocks until ctx is cancelled, ticking on Interval.
func (s *Sweeper) Run(ctx context.Context) {
	if s == nil || s.Manager == nil {
		return
	}
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	// One pass at start so deploys clean quickly.
	s.tick(ctx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Sweeper) tick(ctx context.Context) {
	timeout := s.TimeoutMins
	if timeout <= 0 {
		timeout = DefaultTimeoutMins
	}
	limit := s.BatchLimit
	if limit <= 0 {
		limit = 500
	}

	n, err := s.Manager.ExpireStale(ctx, timeout, limit)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("session sweeper expire failed", "error", err)
		}
	} else if n > 0 && s.Log != nil {
		s.Log.Info("session sweeper expired sessions", "count", n)
	}

	d, err := s.Manager.CloseDuplicateOpens(ctx, limit)
	if err != nil {
		if s.Log != nil {
			s.Log.Error("session sweeper dedupe failed", "error", err)
		}
	} else if d > 0 && s.Log != nil {
		s.Log.Info("session sweeper superseded duplicates", "count", d)
	}
}
