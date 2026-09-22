package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSetupRouter() *gin.Engine {
	router := gin.New()
	router.POST("/setup", SetupAccessGuard(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	return router
}

func callSetup(router http.Handler, remoteAddr string, headers map[string]string) int {
	request := httptest.NewRequest(http.MethodPost, "/setup", nil)
	request.RemoteAddr = remoteAddr
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestSetupAccessGuardWithTokenConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SETUP_TOKEN", "one-time-secret")

	testCases := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		wantStatus int
	}{
		{
			name:       "correct header token",
			headers:    map[string]string{SetupTokenHeader: "one-time-secret"},
			remoteAddr: "203.0.113.10:12345",
			wantStatus: http.StatusOK,
		},
		{
			name:       "correct bearer token",
			headers:    map[string]string{"Authorization": "Bearer one-time-secret"},
			remoteAddr: "203.0.113.10:12345",
			wantStatus: http.StatusOK,
		},
		{
			name:       "token wins over proxy headers",
			headers:    map[string]string{SetupTokenHeader: "one-time-secret", "X-Forwarded-For": "198.51.100.7"},
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusOK,
		},
		{
			name:       "wrong token",
			headers:    map[string]string{SetupTokenHeader: "guessed"},
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "missing token",
			headers:    nil,
			remoteAddr: "127.0.0.1:12345",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.wantStatus, callSetup(newSetupRouter(), testCase.remoteAddr, testCase.headers))
		})
	}
}

func TestSetupAccessGuardWithoutTokenRequiresDirectPrivatePeer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SETUP_TOKEN", "")

	testCases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		wantStatus int
	}{
		{name: "IPv4 loopback", remoteAddr: "127.0.0.1:12345", wantStatus: http.StatusOK},
		{name: "IPv6 loopback", remoteAddr: "[::1]:12345", wantStatus: http.StatusOK},
		{name: "container gateway", remoteAddr: "172.20.0.2:12345", wantStatus: http.StatusOK},
		{name: "LAN client", remoteAddr: "192.168.10.2:12345", wantStatus: http.StatusOK},
		{name: "IPv6 unique local", remoteAddr: "[fd12:3456::2]:12345", wantStatus: http.StatusOK},
		{
			name:       "public peer",
			remoteAddr: "198.51.100.10:12345",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "public peer spoofing a private client address",
			remoteAddr: "198.51.100.10:12345",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "private peer behind a reverse proxy",
			remoteAddr: "127.0.0.1:12345",
			headers:    map[string]string{"X-Forwarded-For": "198.51.100.10"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "private peer behind a proxy that only sets X-Real-Ip",
			remoteAddr: "172.20.0.2:12345",
			headers:    map[string]string{"X-Real-Ip": "198.51.100.10"},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.wantStatus, callSetup(newSetupRouter(), testCase.remoteAddr, testCase.headers))
		})
	}
}

func TestSetupAccessGuardTrimsConfiguredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SETUP_TOKEN", "  padded-secret  ")

	require.Equal(t, http.StatusOK, callSetup(newSetupRouter(), "127.0.0.1:12345", map[string]string{
		SetupTokenHeader: "padded-secret",
	}))
	assert.Equal(t, http.StatusForbidden, callSetup(newSetupRouter(), "127.0.0.1:12345", map[string]string{
		SetupTokenHeader: "padded-secret-extra",
	}))
}
