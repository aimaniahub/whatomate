package lock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// DefaultTTL is the session lock hold time (must cover a typical turn).
const DefaultTTL = 30 * time.Second

// DefaultWait is how long Acquire retries before failing.
const DefaultWait = 5 * time.Second

// ErrNotAcquired means the lock could not be obtained within the wait budget.
var ErrNotAcquired = errors.New("chatbot lock: not acquired")

// Locker serializes concurrent turns for the same session key.
type Locker interface {
	// Acquire holds an exclusive lock until Unlock is called or TTL expires.
	// unlock is always non-nil when err is nil; callers must defer unlock().
	Acquire(ctx context.Context, key string, ttl time.Duration) (unlock func(), err error)
}

// RedisLocker implements Locker with SET NX + token compare-and-delete.
type RedisLocker struct {
	RDB *redis.Client
	// KeyPrefix defaults to "chatbot:lock:".
	KeyPrefix string
	// Wait is total time to retry acquire (default DefaultWait).
	Wait time.Duration
	// RetryEvery is sleep between attempts (default 50ms).
	RetryEvery time.Duration
}

// NewRedisLocker creates a Redis-backed locker. rdb must be non-nil.
func NewRedisLocker(rdb *redis.Client) *RedisLocker {
	return &RedisLocker{
		RDB:        rdb,
		KeyPrefix:  "chatbot:lock:",
		Wait:       DefaultWait,
		RetryEvery: 50 * time.Millisecond,
	}
}

// SessionKey builds the lock key for org + WhatsApp account + phone (or contact id).
func SessionKey(orgID, account, phoneOrContact string) string {
	return fmt.Sprintf("%s:%s:%s", orgID, account, phoneOrContact)
}

// Acquire implements Locker.
func (l *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (func(), error) {
	if l == nil || l.RDB == nil {
		// No Redis → no-op lock (legacy concurrency).
		return func() {}, nil
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	wait := l.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	retry := l.RetryEvery
	if retry <= 0 {
		retry = 50 * time.Millisecond
	}
	prefix := l.KeyPrefix
	if prefix == "" {
		prefix = "chatbot:lock:"
	}
	fullKey := prefix + key
	token := uuid.NewString()
	deadline := time.Now().Add(wait)

	for {
		ok, err := l.RDB.SetNX(ctx, fullKey, token, ttl).Result()
		if err != nil {
			return nil, fmt.Errorf("chatbot lock setnx: %w", err)
		}
		if ok {
			return func() { l.release(context.Background(), fullKey, token) }, nil
		}
		if time.Now().After(deadline) {
			return nil, ErrNotAcquired
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retry):
		}
	}
}

// release deletes the key only if the token matches (Lua).
func (l *RedisLocker) release(ctx context.Context, fullKey, token string) {
	if l == nil || l.RDB == nil {
		return
	}
	// Compare-and-del to avoid releasing another holder's lock after TTL expiry.
	script := redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
else
  return 0
end`)
	_ = script.Run(ctx, l.RDB, []string{fullKey}, token).Err()
}

var _ Locker = (*RedisLocker)(nil)
