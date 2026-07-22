package lock_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/chatbot/lock"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisLocker_Serializes(t *testing.T) {
	rdb := testutil.SetupTestRedis(t)
	if rdb == nil {
		t.Skip("redis not available")
	}
	l := lock.NewRedisLocker(rdb)
	l.Wait = 2 * time.Second
	l.RetryEvery = 20 * time.Millisecond

	ctx := context.Background()
	key := "test-serial-" + time.Now().Format("150405.000")

	var concurrent int32
	var maxConcurrent int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := l.Acquire(ctx, key, time.Second)
			require.NoError(t, err)
			defer unlock()

			cur := atomic.AddInt32(&concurrent, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), maxConcurrent, "lock must serialize holders")
}

func TestRedisLocker_NotAcquired(t *testing.T) {
	rdb := testutil.SetupTestRedis(t)
	if rdb == nil {
		t.Skip("redis not available")
	}
	l := lock.NewRedisLocker(rdb)
	l.Wait = 80 * time.Millisecond
	l.RetryEvery = 20 * time.Millisecond

	ctx := context.Background()
	key := "test-busy-" + time.Now().Format("150405.000")

	unlock, err := l.Acquire(ctx, key, 2*time.Second)
	require.NoError(t, err)
	defer unlock()

	_, err = l.Acquire(ctx, key, 2*time.Second)
	assert.ErrorIs(t, err, lock.ErrNotAcquired)
}

func TestSessionKey(t *testing.T) {
	assert.Equal(t, "org:acct:phone", lock.SessionKey("org", "acct", "phone"))
}

func TestNilLockerNoop(t *testing.T) {
	var l *lock.RedisLocker
	unlock, err := l.Acquire(context.Background(), "k", time.Second)
	require.NoError(t, err)
	unlock()
}
