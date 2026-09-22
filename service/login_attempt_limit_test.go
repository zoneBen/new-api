package service

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useInMemoryLoginAttempts pins the login throttle to the in-process store and
// returns an identifier unique to this test, so one test's spent budget cannot
// lock another test's account out.
func useInMemoryLoginAttempts(t *testing.T) string {
	t.Helper()
	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	common.RedisEnabled, common.RDB = false, nil
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousEnabled, previousClient })
	return t.Name() + "@example.com"
}

func TestLoginAttemptsExhaustAndClear(t *testing.T) {
	identifier := useInMemoryLoginAttempts(t)

	require.False(t, LoginAttemptsExhausted(identifier))
	for range LoginAttemptFailureLimit - 1 {
		RecordFailedLoginAttempt(identifier)
	}
	assert.False(t, LoginAttemptsExhausted(identifier), "the last allowed attempt is not spent yet")

	RecordFailedLoginAttempt(identifier)
	assert.True(t, LoginAttemptsExhausted(identifier))
	assert.False(t, LoginAttemptsExhausted("other-"+identifier), "budgets are per identifier")

	ClearLoginAttempts(identifier)
	assert.False(t, LoginAttemptsExhausted(identifier), "a successful login restores the budget")
}

func TestLoginAttemptBudgetSharesNormalizedIdentifiers(t *testing.T) {
	identifier := useInMemoryLoginAttempts(t)
	require.Equal(t, loginAttemptIdentifier(" Someone@Example.COM "), loginAttemptIdentifier("someone@example.com"))

	shouted := " " + strings.ToUpper(identifier) + " "
	for range LoginAttemptFailureLimit {
		RecordFailedLoginAttempt(shouted)
	}
	assert.True(t, LoginAttemptsExhausted(identifier), "case and padding must not open a second budget")

	ClearLoginAttempts(shouted)
	assert.False(t, LoginAttemptsExhausted(identifier))
}

func TestLoginAttemptsFailOpenWhenRedisIsUnreachable(t *testing.T) {
	useInMemoryLoginAttempts(t)
	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousEnabled, previousClient })
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("forced redis failure")
		},
		MaxRetries: -1,
	})
	t.Cleanup(func() { _ = common.RDB.Close() })

	assert.False(t, LoginAttemptsExhausted("unreachable@example.com"), "an unusable counter must not block logins")
	RecordFailedLoginAttempt("unreachable@example.com")
	ClearLoginAttempts("unreachable@example.com")
}

func TestRedisLoginAttemptsExhaustAndExpire(t *testing.T) {
	server := miniredis.RunT(t)
	previousEnabled, previousClient := common.RedisEnabled, common.RDB
	t.Cleanup(func() { common.RedisEnabled, common.RDB = previousEnabled, previousClient })
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = common.RDB.Close() })

	identifier := "redis-" + t.Name() + "@example.com"
	key := loginAttemptFailureKey(identifier)
	for range LoginAttemptFailureLimit - 1 {
		RecordFailedLoginAttempt(identifier)
	}
	assert.False(t, LoginAttemptsExhausted(identifier))
	ttl := server.TTL(key)
	assert.Positive(t, ttl, "the counter must expire on its own")
	assert.LessOrEqual(t, ttl, time.Duration(LoginAttemptWindowSeconds)*time.Second)

	RecordFailedLoginAttempt(identifier)
	assert.True(t, LoginAttemptsExhausted(identifier))
	count, err := server.Get(key)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(LoginAttemptFailureLimit), count, "the window counts every failure, it does not restart")

	ClearLoginAttempts(identifier)
	assert.False(t, server.Exists(key))
	assert.False(t, LoginAttemptsExhausted(identifier))

	server.FastForward(time.Duration(LoginAttemptWindowSeconds) * time.Second)
	RecordFailedLoginAttempt(identifier)
	assert.False(t, LoginAttemptsExhausted(identifier), "a new window starts after the old one expires")
}

// resetGlobalLoginFailures clears the node-wide burst counter before and after a
// test so the assertions do not depend on failures recorded elsewhere.
func resetGlobalLoginFailures(t *testing.T) {
	t.Helper()
	clearGlobalLoginFailures := func() {
		globalLoginFailures.mutex.Lock()
		defer globalLoginFailures.mutex.Unlock()
		globalLoginFailures.windowStart = time.Time{}
		globalLoginFailures.count = 0
		globalLoginFailures.warned = false
	}
	clearGlobalLoginFailures()
	t.Cleanup(clearGlobalLoginFailures)
}

func TestLoginFailureBurstWarnsOncePerWindow(t *testing.T) {
	resetGlobalLoginFailures(t)

	for range LoginAttemptAlertThreshold - 1 {
		globalLoginFailures.record()
	}
	assert.False(t, globalLoginFailures.warned, "the warning waits for the threshold")

	globalLoginFailures.record()
	assert.True(t, globalLoginFailures.warned)

	// The counter keeps rising inside the window, but record returns before
	// logging again, so a sustained attack cannot flood the log.
	for range 10 {
		globalLoginFailures.record()
	}
	assert.True(t, globalLoginFailures.warned)
	assert.Equal(t, LoginAttemptAlertThreshold+10, globalLoginFailures.count)

	globalLoginFailures.mutex.Lock()
	globalLoginFailures.windowStart = time.Now().Add(-time.Duration(LoginAttemptWindowSeconds) * time.Second)
	globalLoginFailures.mutex.Unlock()
	globalLoginFailures.record()
	assert.False(t, globalLoginFailures.warned, "the next window warns again")
	assert.Equal(t, 1, globalLoginFailures.count)
}
