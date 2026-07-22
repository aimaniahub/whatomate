package conversation_test

import (
	"context"
	"testing"

	"github.com/shridarpatil/whatomate/internal/chatbot/conversation"
	"github.com/shridarpatil/whatomate/internal/chatbot/session"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryRestartIfRequested(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	sessMgr := session.NewManager(db)
	conv := conversation.NewManager(sessMgr)
	ctx := context.Background()
	key := conversation.SessionKey(org.ID, contact.ID, "main")

	s, created, err := sessMgr.GetOrCreateActive(ctx, key, "1555", 30)
	require.NoError(t, err)
	assert.True(t, created)
	oldID := s.ID

	// Non-restart message
	s2, restarted, isNew, err := conv.TryRestartIfRequested(ctx, s, key, "1555", 30, "hello")
	require.NoError(t, err)
	assert.False(t, restarted)
	assert.Equal(t, oldID, s2.ID)
	assert.False(t, isNew)

	// Restart
	s3, restarted, isNew, err := conv.TryRestartIfRequested(ctx, s2, key, "1555", 30, "restart")
	require.NoError(t, err)
	assert.True(t, restarted)
	assert.True(t, isNew)
	assert.NotEqual(t, oldID, s3.ID)
	assert.NotNil(t, s3.SessionData[conversation.KeyRestartedAt])
}

func TestPersistGreetingSent(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	sessMgr := session.NewManager(db)
	conv := conversation.NewManager(sessMgr)
	ctx := context.Background()
	key := session.Key{OrganizationID: org.ID, ContactID: contact.ID, WhatsAppAccount: "main"}

	s, _, err := sessMgr.GetOrCreateActive(ctx, key, "1", 30)
	require.NoError(t, err)
	require.NoError(t, conv.PersistGreetingSent(ctx, s, 30))
	assert.True(t, conversation.GreetingAlreadySent(s))

	// Reload
	s2, created, err := sessMgr.GetOrCreateActive(ctx, key, "1", 30)
	require.NoError(t, err)
	assert.False(t, created)
	assert.True(t, conversation.GreetingAlreadySent(s2))
}
