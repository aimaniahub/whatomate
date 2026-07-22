package turn_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/chatbot/turn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewIDs_UniqueAndValid(t *testing.T) {
	a := turn.NewIDs()
	b := turn.NewIDs()
	require.True(t, a.Valid())
	require.True(t, b.Valid())
	assert.NotEqual(t, a.CorrelationID, b.CorrelationID)
	assert.NotEqual(t, a.TurnID, b.TurnID)
	assert.NotEqual(t, a.CorrelationID, a.TurnID)
}

func TestNewTurn_ReusesCorrelation(t *testing.T) {
	first := turn.NewIDs()
	second := turn.NewTurn(first.CorrelationID)
	assert.Equal(t, first.CorrelationID, second.CorrelationID)
	assert.NotEqual(t, first.TurnID, second.TurnID)
	assert.True(t, second.Valid())
}

func TestNewTurn_EmptyCorrelationAllocatesBoth(t *testing.T) {
	id := turn.NewTurn("")
	assert.True(t, id.Valid())
}

func TestLogArgs_With(t *testing.T) {
	id := turn.IDs{CorrelationID: "c1", TurnID: "t1"}
	args := id.With("from", "1555")
	require.Len(t, args, 6)
	assert.Equal(t, "correlation_id", args[0])
	assert.Equal(t, "c1", args[1])
	assert.Equal(t, "turn_id", args[2])
	assert.Equal(t, "t1", args[3])
	assert.Equal(t, "from", args[4])
	assert.Equal(t, "1555", args[5])
}
