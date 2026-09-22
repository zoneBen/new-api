package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	// MidjourneyImageAccessQueryParameter carries the capability that authorizes
	// a single forwarded Midjourney image.
	MidjourneyImageAccessQueryParameter = "access"
	midjourneyImageAccessVersion        = "mj-image-v1"
	midjourneyImageAccessLength         = 43
	maxMidjourneyImageIDLength          = 191
)

var ErrMidjourneyImageAccessInvalid = errors.New("midjourney image access is invalid")

func midjourneyImageAccessMessage(mjID string) []byte {
	return []byte(midjourneyImageAccessVersion + "\x00" + mjID)
}

// IssueMidjourneyImageAccess creates a stable capability bound to exactly one
// Midjourney task ID. The payload carries no user or upstream data, so the
// signature is safe to hand to a plain <img> tag, which cannot send the panel
// bearer token.
func IssueMidjourneyImageAccess(mjID string) (string, error) {
	mjID = strings.TrimSpace(mjID)
	if mjID == "" || len(mjID) > maxMidjourneyImageIDLength || common.CryptoSecret == "" {
		return "", ErrMidjourneyImageAccessInvalid
	}

	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write(midjourneyImageAccessMessage(mjID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyMidjourneyImageAccess verifies the capability against the requested
// task ID without reading task, channel, or user state. The comparison is
// constant-time, so a rejected caller learns nothing about the expected value.
func VerifyMidjourneyImageAccess(access, mjID string) bool {
	mjID = strings.TrimSpace(mjID)
	if len(access) != midjourneyImageAccessLength ||
		mjID == "" || len(mjID) > maxMidjourneyImageIDLength ||
		common.CryptoSecret == "" {
		return false
	}

	actualSignature, err := base64.RawURLEncoding.Strict().DecodeString(access)
	if err != nil || len(actualSignature) != sha256.Size {
		return false
	}

	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write(midjourneyImageAccessMessage(mjID))
	return hmac.Equal(actualSignature, mac.Sum(nil))
}

// BuildMidjourneyImageURL returns the absolute capability URL that serves one
// forwarded Midjourney image. It returns an error instead of a URL when the
// capability cannot be issued, so callers fall back to the upstream image URL
// rather than publishing a proxy URL that the proxy itself would reject.
func BuildMidjourneyImageURL(mjID string) (string, error) {
	mjID = strings.TrimSpace(mjID)
	if mjID == "" || len(mjID) > maxMidjourneyImageIDLength {
		return "", ErrMidjourneyImageAccessInvalid
	}

	baseAddress := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if baseAddress == "" {
		return "", errors.New("midjourney image base address is empty")
	}

	access, err := IssueMidjourneyImageAccess(mjID)
	if err != nil {
		return "", err
	}

	return baseAddress + "/mj/image/" + url.PathEscape(mjID) +
		"?" + MidjourneyImageAccessQueryParameter + "=" + access, nil
}
