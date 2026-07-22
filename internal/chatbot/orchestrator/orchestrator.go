package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/shridarpatil/whatomate/internal/chatbot/turn"
)

// LegacyHandler runs the pre-orchestrator full inbound processor.
// Implemented by handlers.App to avoid import cycles.
type LegacyHandler interface {
	HandleLegacyTurn(ctx context.Context, tc *turn.Context) error
}

// Logger is the minimal log surface.
type Logger interface {
	Info(msg string, args ...any)
	Debug(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Orchestrator is the single routing brain (shell in Phase 5).
type Orchestrator struct {
	Legacy LegacyHandler
	Log    Logger
	// ShadowCompare when true logs PlanRoute() without changing execution
	// (also controlled by flag at the call site; this forces log detail).
	ShadowCompare bool
}

// New creates an Orchestrator. legacy may be nil only for tests of PlanRoute.
func New(legacy LegacyHandler, log Logger) *Orchestrator {
	return &Orchestrator{Legacy: legacy, Log: log}
}

// HandleTurn runs the priority ladder skeleton then the legacy adapter.
// Phase 5 guarantee: external behavior equals calling the legacy processor alone.
func (o *Orchestrator) HandleTurn(ctx context.Context, tc *turn.Context) (*turn.Result, error) {
	if tc == nil {
		return nil, fmt.Errorf("orchestrator: nil turn context")
	}
	if tc.StartedAt.IsZero() {
		tc.StartedAt = time.Now()
	}

	result := &turn.Result{
		Path:    "orchestrator",
		Claimed: false,
	}

	plan := PlanRoute()
	for _, step := range plan.Deferred {
		result.Append(step, "defer_to_legacy", "", "phase5_stub")
	}
	result.Append(StepLegacyFull, "claim", "legacy_full", "phase5_legacy_adapter")

	if o != nil && o.ShadowCompare && o.Log != nil {
		o.Log.Info("Orchestrator shadow plan",
			append(tc.IDs.LogArgs(),
				"winner", plan.Winner,
				"deferred_count", len(plan.Deferred),
				"path", "orchestrator",
			)...,
		)
	}

	if o == nil || o.Legacy == nil {
		return result, fmt.Errorf("orchestrator: legacy handler not configured")
	}

	start := time.Now()
	err := o.Legacy.HandleLegacyTurn(ctx, tc)
	elapsed := time.Since(start).Milliseconds()
	if len(result.Timeline) > 0 {
		result.Timeline[len(result.Timeline)-1].DurationMs = elapsed
	}

	result.Claimed = true
	result.Handler = "legacy_full"
	if err != nil {
		if o.Log != nil {
			o.Log.Error("Orchestrator legacy handler failed",
				append(tc.IDs.LogArgs(), "error", err, "duration_ms", elapsed)...)
		}
		return result, err
	}
	if o.Log != nil {
		o.Log.Info("Orchestrator turn completed",
			append(tc.IDs.LogArgs(),
				"handler", result.Handler,
				"path", result.Path,
				"duration_ms", elapsed,
				"ladder_steps", len(result.Timeline),
			)...,
		)
	}
	return result, nil
}
