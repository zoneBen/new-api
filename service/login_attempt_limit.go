package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/go-redis/redis/v8"
)

const (
	// LoginAttemptFailureLimit is the number of failed password logins allowed
	// per identifier within LoginAttemptWindowSeconds before further attempts
	// are rejected outright, regardless of the client IP.
	LoginAttemptFailureLimit = 20
	// LoginAttemptWindowSeconds is the sliding window. It stays short on
	// purpose: it bounds both the brute-force budget and how long a locked-out
	// account stays unusable.
	LoginAttemptWindowSeconds int64 = 15 * 60
	// LoginAttemptAlertThreshold is the number of failures across all
	// identifiers within LoginAttemptWindowSeconds that raises a warning.
	LoginAttemptAlertThreshold = 100
	loginAttemptFailurePrefix  = "login:failure:v1:"
)

var loginAttemptFailures common.InMemoryRateLimiter

// loginAttemptLimiter returns the process-wide failure budget store. Init keeps
// the first configuration, so calling it on the access path is safe and avoids
// starting a background ticker at package load.
func loginAttemptLimiter() *common.InMemoryRateLimiter {
	loginAttemptFailures.Init(time.Duration(LoginAttemptWindowSeconds) * time.Second)
	return &loginAttemptFailures
}

// loginAttemptIdentifier is the key a failure budget is charged to: the submitted
// username or email, case-folded and trimmed. It deliberately does not depend on
// the account existing, so an unknown name spends the same budget as a known one.
func loginAttemptIdentifier(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// loginAttemptFailureKey hashes the identifier so raw usernames and email
// addresses never become part of an in-process or Redis key. Normalizing here
// keeps every caller on one budget for a given account, whatever it passed in.
func loginAttemptFailureKey(username string) string {
	identifier := loginAttemptIdentifier(username)
	if identifier == "" {
		return ""
	}
	return loginAttemptFailurePrefix + common.GenerateHMAC(identifier)
}

// LoginAttemptsExhausted reports whether username spent its failed-login budget.
// It runs before the credential lookup and applies to unknown names too, so a
// rejection does not reveal whether the account exists.
func LoginAttemptsExhausted(username string) bool {
	key := loginAttemptFailureKey(username)
	if key == "" {
		return false
	}
	if common.RedisEnabled && common.RDB != nil {
		return redisLoginAttemptsExhausted(key)
	}
	return loginAttemptLimiter().Saturated(key, LoginAttemptFailureLimit, LoginAttemptWindowSeconds)
}

// RecordFailedLoginAttempt charges one failed login to username.
func RecordFailedLoginAttempt(username string) {
	key := loginAttemptFailureKey(username)
	if key == "" {
		return
	}
	if common.RedisEnabled && common.RDB != nil {
		recordRedisLoginAttemptFailure(key)
	} else {
		loginAttemptLimiter().Request(key, LoginAttemptFailureLimit, LoginAttemptWindowSeconds)
	}
	globalLoginFailures.record()
}

// ClearLoginAttempts forgets the failed logins of username, so a successful
// login restores the account's budget.
func ClearLoginAttempts(username string) {
	key := loginAttemptFailureKey(username)
	if key == "" {
		return
	}
	if common.RedisEnabled && common.RDB != nil {
		if err := common.RDB.Del(context.Background(), key).Err(); err != nil {
			logger.LogWarn(context.Background(), "failed to clear login attempt counter: "+err.Error())
		}
		return
	}
	loginAttemptLimiter().Reset(key)
}

// redisLoginAttemptsExhausted fails open: a counter backend that cannot answer
// must not block every login on the instance.
func redisLoginAttemptsExhausted(key string) bool {
	value, err := common.RDB.Get(context.Background(), key).Result()
	if errors.Is(err, redis.Nil) {
		return false
	}
	if err != nil {
		logger.LogWarn(context.Background(), "failed to read login attempt counter: "+err.Error())
		return false
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		logger.LogWarn(context.Background(), "unreadable login attempt counter: "+err.Error())
		return false
	}
	return count >= LoginAttemptFailureLimit
}

// recordRedisLoginAttemptFailure starts the window with SetNX so the counter
// keeps a single TTL across nodes, then increments what already exists.
func recordRedisLoginAttemptFailure(key string) {
	ctx := context.Background()
	created, err := common.RDB.SetNX(ctx, key, 1, time.Duration(LoginAttemptWindowSeconds)*time.Second).Result()
	if err != nil {
		logger.LogWarn(ctx, "failed to start login attempt counter: "+err.Error())
		return
	}
	if created {
		return
	}
	if err := common.RDB.Incr(ctx, key).Err(); err != nil {
		logger.LogWarn(ctx, "failed to increment login attempt counter: "+err.Error())
	}
}

// globalLoginFailures counts failures across every identifier on this node and
// warns once per window when the rate looks like credential stuffing rather than
// one account under attack.
//
// It only warns. Rejecting logins globally would let any caller stop every user
// of the instance from signing in.
type loginFailureBurst struct {
	mutex       sync.Mutex
	windowStart time.Time
	count       int
	warned      bool
}

var globalLoginFailures loginFailureBurst

func (b *loginFailureBurst) record() {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	now := time.Now()
	if b.windowStart.IsZero() || now.Sub(b.windowStart) >= time.Duration(LoginAttemptWindowSeconds)*time.Second {
		b.windowStart = now
		b.count = 0
		b.warned = false
	}
	b.count++
	if b.warned || b.count < LoginAttemptAlertThreshold {
		return
	}
	b.warned = true
	logger.LogWarn(context.Background(), "login failures are abnormally frequent across all accounts; check for credential stuffing")
}
