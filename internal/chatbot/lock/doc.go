// Package lock owns per-session serialization for concurrent inbound turns.
//
// RedisLocker uses SET NX with token-based release. Enabled when
// chatbot.session_lock_v1 is on.
package lock

