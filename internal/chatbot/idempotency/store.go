package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shridarpatil/whatomate/internal/chatbot/turn"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefaultLease is how long a processing claim is exclusive before reclaim.
const DefaultLease = 2 * time.Minute

// Outcome of Begin.
type Outcome int

const (
	// OutcomeAcquired means this turn owns processing for the WAMID.
	OutcomeAcquired Outcome = iota
	// OutcomeDuplicate means the WAMID was already completed — skip processing.
	OutcomeDuplicate
	// OutcomeInProgress means another worker holds a valid lease — skip.
	OutcomeInProgress
)

// ErrEmptyWAMID is returned when Begin is called without a message id.
var ErrEmptyWAMID = errors.New("idempotency: empty wamid")

// Store reserves and completes WAMID processing leases.
type Store interface {
	// Begin claims the WAMID for processing. Caller must Complete or the lease expires.
	Begin(ctx context.Context, wamid string, ids turn.IDs, lease time.Duration) (Outcome, error)
	// Complete marks the WAMID as successfully processed.
	Complete(ctx context.Context, wamid string) error
	// Release abandons a processing claim (e.g. fatal error before side effects).
	// Prefer Complete after partial work to avoid double-sends; Release allows retry.
	Release(ctx context.Context, wamid string) error
}

// PostgresStore implements Store using inbound_idempotency.
type PostgresStore struct {
	DB *gorm.DB
}

// NewPostgresStore creates a Postgres-backed idempotency store.
func NewPostgresStore(db *gorm.DB) *PostgresStore {
	return &PostgresStore{DB: db}
}

// Begin implements Store.
func (s *PostgresStore) Begin(ctx context.Context, wamid string, ids turn.IDs, lease time.Duration) (Outcome, error) {
	if s == nil || s.DB == nil {
		return OutcomeAcquired, nil // no-op when unconfigured
	}
	if wamid == "" {
		return OutcomeAcquired, ErrEmptyWAMID
	}
	if lease <= 0 {
		lease = DefaultLease
	}
	now := time.Now()
	leaseUntil := now.Add(lease)

	row := models.InboundIdempotency{
		WAMID:         wamid,
		Status:        models.IdempotencyStatusProcessing,
		CorrelationID: ids.CorrelationID,
		TurnID:        ids.TurnID,
		LeaseUntil:    leaseUntil,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Insert if not exists. On conflict, inspect existing row.
	err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	if err != nil {
		return 0, fmt.Errorf("idempotency begin insert: %w", err)
	}

	// If we inserted, RowsAffected may be 1; GORM OnConflict DoNothing still returns nil.
	// Always re-read to see if we own the row.
	var existing models.InboundIdempotency
	if err := s.DB.WithContext(ctx).Where("wamid = ?", wamid).First(&existing).Error; err != nil {
		return 0, fmt.Errorf("idempotency begin load: %w", err)
	}

	if existing.Status == models.IdempotencyStatusCompleted {
		return OutcomeDuplicate, nil
	}

	// We own it if turn_id matches our claim from a successful insert path,
	// or we reclaim an expired lease.
	if existing.TurnID == ids.TurnID && existing.Status == models.IdempotencyStatusProcessing {
		return OutcomeAcquired, nil
	}

	if existing.Status == models.IdempotencyStatusProcessing && existing.LeaseUntil.After(now) {
		return OutcomeInProgress, nil
	}

	// Reclaim expired processing lease.
	res := s.DB.WithContext(ctx).Model(&models.InboundIdempotency{}).
		Where("wamid = ? AND status = ? AND lease_until <= ?", wamid, models.IdempotencyStatusProcessing, now).
		Updates(map[string]any{
			"correlation_id": ids.CorrelationID,
			"turn_id":        ids.TurnID,
			"lease_until":    leaseUntil,
			"updated_at":     now,
		})
	if res.Error != nil {
		return 0, fmt.Errorf("idempotency reclaim: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// Lost race to another reclaimer or completed mid-flight.
		if err := s.DB.WithContext(ctx).Where("wamid = ?", wamid).First(&existing).Error; err != nil {
			return 0, err
		}
		if existing.Status == models.IdempotencyStatusCompleted {
			return OutcomeDuplicate, nil
		}
		return OutcomeInProgress, nil
	}
	return OutcomeAcquired, nil
}

// Complete implements Store.
func (s *PostgresStore) Complete(ctx context.Context, wamid string) error {
	if s == nil || s.DB == nil || wamid == "" {
		return nil
	}
	now := time.Now()
	return s.DB.WithContext(ctx).Model(&models.InboundIdempotency{}).
		Where("wamid = ?", wamid).
		Updates(map[string]any{
			"status":       models.IdempotencyStatusCompleted,
			"completed_at": now,
			"updated_at":   now,
		}).Error
}

// Release implements Store — deletes processing row so a later retry can Begin.
func (s *PostgresStore) Release(ctx context.Context, wamid string) error {
	if s == nil || s.DB == nil || wamid == "" {
		return nil
	}
	return s.DB.WithContext(ctx).
		Where("wamid = ? AND status = ?", wamid, models.IdempotencyStatusProcessing).
		Delete(&models.InboundIdempotency{}).Error
}

// Ensure interface compliance.
var _ Store = (*PostgresStore)(nil)
