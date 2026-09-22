package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyCodeWithKeyAcceptsOnlyTheRegisteredCode(t *testing.T) {
	key := t.Name() + "-user@example.com"
	RegisterVerificationCodeWithKey(key, "123456", PasswordResetPurpose)
	t.Cleanup(func() { DeleteKey(key, PasswordResetPurpose) })

	assert.True(t, VerifyCodeWithKey(key, "123456", PasswordResetPurpose))
	assert.False(t, VerifyCodeWithKey(key, "1234567", PasswordResetPurpose), "a longer code must not match")
	assert.False(t, VerifyCodeWithKey(key, "12345", PasswordResetPurpose), "a truncated code must not match")
	assert.False(t, VerifyCodeWithKey(key, "", PasswordResetPurpose))
	assert.False(t, VerifyCodeWithKey(key, "123456", EmailVerificationPurpose), "purposes must not share codes")
	assert.False(t, VerifyCodeWithKey("unknown-"+key, "123456", PasswordResetPurpose))
}

func TestVerifyCodeWithKeyInvalidatesTheCodeAfterTooManyFailures(t *testing.T) {
	key := t.Name() + "-user@example.com"
	RegisterVerificationCodeWithKey(key, "123456", PasswordResetPurpose)
	t.Cleanup(func() { DeleteKey(key, PasswordResetPurpose) })

	for attempt := 1; attempt < VerificationMaxAttempts; attempt++ {
		require.False(t, VerifyCodeWithKey(key, "000000", PasswordResetPurpose), "attempt %d must fail", attempt)
	}
	assert.True(t, VerifyCodeWithKey(key, "123456", PasswordResetPurpose),
		"the code must still work below the attempt limit")

	// A fresh registration resets the counter for the same key.
	for attempt := 0; attempt < VerificationMaxAttempts; attempt++ {
		require.False(t, VerifyCodeWithKey(key, "000000", PasswordResetPurpose), "attempt %d must fail", attempt)
	}
	assert.False(t, VerifyCodeWithKey(key, "123456", PasswordResetPurpose),
		"the correct code must not survive reaching the attempt limit")
}

func TestVerifyCodeWithKeyRejectsAndDropsExpiredCodes(t *testing.T) {
	previousValidMinutes := VerificationValidMinutes
	VerificationValidMinutes = 0
	t.Cleanup(func() { VerificationValidMinutes = previousValidMinutes })

	key := t.Name() + "-user@example.com"
	RegisterVerificationCodeWithKey(key, "123456", PasswordResetPurpose)
	t.Cleanup(func() { DeleteKey(key, PasswordResetPurpose) })

	assert.False(t, VerifyCodeWithKey(key, "123456", PasswordResetPurpose), "an expired code must be rejected")
	assert.False(t, VerifyCodeWithKey(key, "123456", PasswordResetPurpose), "an expired code must not be retried")
}

func TestRegisterVerificationCodeWithKeyReplacesThePreviousCode(t *testing.T) {
	key := t.Name() + "-user@example.com"
	RegisterVerificationCodeWithKey(key, "111111", EmailVerificationPurpose)
	RegisterVerificationCodeWithKey(key, "222222", EmailVerificationPurpose)
	t.Cleanup(func() { DeleteKey(key, EmailVerificationPurpose) })

	assert.False(t, VerifyCodeWithKey(key, "111111", EmailVerificationPurpose))
	assert.True(t, VerifyCodeWithKey(key, "222222", EmailVerificationPurpose))
}

func TestDeleteKeyRemovesTheCode(t *testing.T) {
	key := t.Name() + "-user@example.com"
	RegisterVerificationCodeWithKey(key, "123456", EmailVerificationPurpose)
	DeleteKey(key, EmailVerificationPurpose)
	assert.False(t, VerifyCodeWithKey(key, "123456", EmailVerificationPurpose))
}
