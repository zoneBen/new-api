package zhipu

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSysLog redirects common.SysLog (which writes to gin.DefaultWriter)
// into a buffer for the duration of the test.
func captureSysLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := gin.DefaultWriter
	var output bytes.Buffer
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previous })
	return &output
}

// TestGetZhipuTokenNeverLogsTheKey covers the misconfiguration branch: a key
// that does not split into `<id>.<secret>`. The rejected value used to be
// written to the log verbatim, so a channel configured with the wrong key —
// or with every real key after an upstream format change — put the credential
// into stdout and every rotated log file next to it.
func TestGetZhipuTokenNeverLogsTheKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name  string
		key   string
		space bool
	}{
		{name: "no separator", key: "not-a-zhipu-key-4f8c2b1e"},
		{name: "too many segments", key: "id.secret.extra"},
		{name: "empty key", key: ""},
		{name: "pasted label with whitespace", key: "zhipu key with spaces"},
		{name: "trailing newline", key: "not-a-zhipu-key\n", space: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := captureSysLog(t)

			require.Empty(t, getZhipuToken(tc.key))

			logged := output.String()
			require.Contains(t, logged, "invalid zhipu key")
			if trimmed := strings.TrimSpace(tc.key); trimmed != "" {
				// No part of the rejected value may appear, whether raw or trimmed.
				assert.NotContains(t, logged, tc.key)
				assert.NotContains(t, logged, trimmed)
			}
			assert.Contains(t, logged, fmt.Sprintf("length=%d", len(tc.key)))
			assert.Contains(t, logged, fmt.Sprintf("hasWhitespace=%t", tc.space))
			assert.Contains(t, logged, logSafeKeyFingerprint(tc.key))
		})
	}
}

// TestGetZhipuTokenLogsNothingForAWellFormedKey pins the other side of the
// diagnostic: a key in the documented shape is signed locally and produces no
// log entry at all.
func TestGetZhipuTokenLogsNothingForAWellFormedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	output := captureSysLog(t)

	require.NotEmpty(t, getZhipuToken("key-id-4f8c2b1e.key-secret-9a1d"))

	assert.Empty(t, output.String())
}

// TestLogSafeKeyFingerprintDoesNotCarryTheKey guards the derivation itself: the
// fingerprint must be short, stable and unrelated to the key text.
func TestLogSafeKeyFingerprintDoesNotCarryTheKey(t *testing.T) {
	const key = "key-id-4f8c2b1e.key-secret-9a1d"

	fingerprint := logSafeKeyFingerprint(key)

	require.Equal(t, fingerprint, logSafeKeyFingerprint(key))
	require.Len(t, fingerprint, 8)
	assert.NotContains(t, key, fingerprint)
	assert.NotEqual(t, fingerprint, logSafeKeyFingerprint(key+"x"))
}
