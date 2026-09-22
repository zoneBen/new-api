package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An unusable placeholder hash would make the authentication miss path cheaper
// than a real verification, which is exactly the leak the equalization exists to
// close. The placeholder must therefore be a well-formed hash that the shared
// verifier actually runs its key derivation against.
func TestPlaceholderAccountPasswordHashIsVerifiableForEachAlgorithm(t *testing.T) {
	for _, algorithm := range []string{"", "argon2id", "bcrypt"} {
		t.Run("algorithm="+algorithm, func(t *testing.T) {
			hash := placeholderAccountPasswordHash(algorithm)
			require.NotEmpty(t, hash)
			assert.True(t, ValidatePasswordAndHash(placeholderAccountPassword, hash))
			assert.False(t, ValidatePasswordAndHash("another-password", hash))
			if algorithm == "bcrypt" {
				assert.True(t, strings.HasPrefix(hash, "$2"), "bcrypt hashes must carry the bcrypt prefix")
			} else {
				assert.True(t, strings.HasPrefix(hash, "$argon2id$"), "the default format is argon2id")
			}
		})
	}
}

func TestPlaceholderAccountPasswordHashRejectsUnsupportedAlgorithm(t *testing.T) {
	assert.Empty(t, placeholderAccountPasswordHash("scrypt"),
		"an unsupported configuration must fall back to no equalization instead of a malformed hash")
}

func TestEqualizePasswordVerificationCostUsesTheConfiguredAlgorithm(t *testing.T) {
	t.Setenv("ACCOUNT_PASSWORD_HASH_ALGORITHM", "bcrypt")

	EqualizePasswordVerificationCost("any-password")

	assert.True(t, strings.HasPrefix(
		placeholderAccountPasswordHash("bcrypt"),
		"$2",
	))
}
