package service

import (
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMidjourneyImageAccessBindsOnlyTheTaskID(t *testing.T) {
	previousSecret := common.CryptoSecret
	common.CryptoSecret = "midjourney-image-access-test-secret"
	t.Cleanup(func() { common.CryptoSecret = previousSecret })

	access, err := IssueMidjourneyImageAccess("mj-1234")
	require.NoError(t, err)
	assert.Len(t, access, 43)
	assert.True(t, VerifyMidjourneyImageAccess(access, "mj-1234"))
	assert.False(t, VerifyMidjourneyImageAccess(access, "mj-1235"))
	assert.False(t, VerifyMidjourneyImageAccess(access, "mj-123"))
	assert.False(t, VerifyMidjourneyImageAccess(access, ""))

	common.CryptoSecret = "another-node-secret"
	assert.False(t, VerifyMidjourneyImageAccess(access, "mj-1234"), "a capability must not survive a secret rotation")
}

func TestMidjourneyImageAccessRejectsMalformedCapability(t *testing.T) {
	previousSecret := common.CryptoSecret
	common.CryptoSecret = "midjourney-image-access-test-secret"
	t.Cleanup(func() { common.CryptoSecret = previousSecret })

	access, err := IssueMidjourneyImageAccess("mj-1234")
	require.NoError(t, err)

	testCases := map[string]string{
		"empty":           "",
		"padded":          access + "=",
		"truncated":       access[:len(access)-1],
		"extended":        access + "a",
		"standard base64": access[:42] + "+",
		"non signature":   "not-a-capability",
	}
	for name, candidate := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.False(t, VerifyMidjourneyImageAccess(candidate, "mj-1234"))
		})
	}
}

func TestIssueMidjourneyImageAccessRejectsUnusableInputs(t *testing.T) {
	previousSecret := common.CryptoSecret
	t.Cleanup(func() { common.CryptoSecret = previousSecret })

	common.CryptoSecret = "midjourney-image-access-test-secret"
	_, err := IssueMidjourneyImageAccess("   ")
	assert.ErrorIs(t, err, ErrMidjourneyImageAccessInvalid)
	_, err = IssueMidjourneyImageAccess(strings.Repeat("a", maxMidjourneyImageIDLength+1))
	assert.ErrorIs(t, err, ErrMidjourneyImageAccessInvalid)

	common.CryptoSecret = ""
	_, err = IssueMidjourneyImageAccess("mj-1234")
	assert.ErrorIs(t, err, ErrMidjourneyImageAccessInvalid)
	assert.False(t, VerifyMidjourneyImageAccess("any", "mj-1234"), "without a secret nothing can be verified")
}

func TestBuildMidjourneyImageURLCarriesAVerifiableCapability(t *testing.T) {
	previousSecret := common.CryptoSecret
	previousServerAddress := system_setting.ServerAddress
	common.CryptoSecret = "midjourney-image-url-test-secret"
	system_setting.ServerAddress = "https://gateway.example/"
	t.Cleanup(func() {
		common.CryptoSecret = previousSecret
		system_setting.ServerAddress = previousServerAddress
	})

	imageURL, err := BuildMidjourneyImageURL("mj 1234/x")
	require.NoError(t, err)
	parsed, err := url.Parse(imageURL)
	require.NoError(t, err)
	assert.Equal(t, "gateway.example", parsed.Host)
	assert.Equal(t, "/mj/image/mj 1234/x", parsed.Path, "the task ID round-trips through gin's path decoding")
	assert.True(t, VerifyMidjourneyImageAccess(
		parsed.Query().Get(MidjourneyImageAccessQueryParameter),
		"mj 1234/x",
	))
	assert.NotContains(t, imageURL, "//mj/image/", "a trailing slash on the configured address must not be duplicated")
}

func TestBuildMidjourneyImageURLFailsWithoutAddressOrCapability(t *testing.T) {
	previousSecret := common.CryptoSecret
	previousServerAddress := system_setting.ServerAddress
	t.Cleanup(func() {
		common.CryptoSecret = previousSecret
		system_setting.ServerAddress = previousServerAddress
	})

	common.CryptoSecret = "midjourney-image-url-test-secret"
	system_setting.ServerAddress = "   "
	_, err := BuildMidjourneyImageURL("mj-1234")
	require.Error(t, err)

	system_setting.ServerAddress = "https://gateway.example"
	_, err = BuildMidjourneyImageURL("")
	assert.ErrorIs(t, err, ErrMidjourneyImageAccessInvalid)

	common.CryptoSecret = ""
	_, err = BuildMidjourneyImageURL("mj-1234")
	assert.ErrorIs(t, err, ErrMidjourneyImageAccessInvalid)
}
