package idempotency_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/chatbot/idempotency"
	"github.com/shridarpatil/whatomate/internal/chatbot/turn"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupIdemStore(t *testing.T) *idempotency.PostgresStore {
	t.Helper()
	db := testutil.SetupTestDB(t)
	require.NoError(t, db.AutoMigrate(&models.InboundIdempotency{}))
	return idempotency.NewPostgresStore(db)
}

func TestBegin_AcquiresAndComplete_Duplicate(t *testing.T) {
	store := setupIdemStore(t)
	ctx := context.Background()
	wamid := "wamid.test." + uuid.NewString()
	ids := turn.NewIDs()

	out, err := store.Begin(ctx, wamid, ids, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, idempotency.OutcomeAcquired, out)

	require.NoError(t, store.Complete(ctx, wamid))

	out2, err := store.Begin(ctx, wamid, turn.NewIDs(), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, idempotency.OutcomeDuplicate, out2)
}

func TestBegin_InProgressWhileLeaseValid(t *testing.T) {
	store := setupIdemStore(t)
	ctx := context.Background()
	wamid := "wamid.progress." + uuid.NewString()

	out, err := store.Begin(ctx, wamid, turn.NewIDs(), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, idempotency.OutcomeAcquired, out)

	out2, err := store.Begin(ctx, wamid, turn.NewIDs(), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, idempotency.OutcomeInProgress, out2)
}

func TestBegin_ReclaimExpiredLease(t *testing.T) {
	store := setupIdemStore(t)
	ctx := context.Background()
	wamid := "wamid.reclaim." + uuid.NewString()

	// Insert expired processing row directly
	past := time.Now().Add(-time.Hour)
	require.NoError(t, store.DB.Create(&models.InboundIdempotency{
		WAMID:         wamid,
		Status:        models.IdempotencyStatusProcessing,
		CorrelationID: "old",
		TurnID:        "old-turn",
		LeaseUntil:    past,
		CreatedAt:     past,
		UpdatedAt:     past,
	}).Error)

	ids := turn.NewIDs()
	out, err := store.Begin(ctx, wamid, ids, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, idempotency.OutcomeAcquired, out)

	var row models.InboundIdempotency
	require.NoError(t, store.DB.Where("wamid = ?", wamid).First(&row).Error)
	assert.Equal(t, ids.TurnID, row.TurnID)
}

func TestBegin_EmptyWAMID(t *testing.T) {
	store := setupIdemStore(t)
	_, err := store.Begin(context.Background(), "", turn.NewIDs(), time.Minute)
	assert.ErrorIs(t, err, idempotency.ErrEmptyWAMID)
}

func TestRelease_AllowsRetry(t *testing.T) {
	store := setupIdemStore(t)
	ctx := context.Background()
	wamid := "wamid.release." + uuid.NewString()

	out, err := store.Begin(ctx, wamid, turn.NewIDs(), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, idempotency.OutcomeAcquired, out)

	require.NoError(t, store.Release(ctx, wamid))

	out2, err := store.Begin(ctx, wamid, turn.NewIDs(), time.Minute)
	require.NoError(t, err)
	assert.Equal(t, idempotency.OutcomeAcquired, out2)
}
