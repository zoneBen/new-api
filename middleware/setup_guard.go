package middleware

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// SetupTokenHeader carries the one-time setup token when SETUP_TOKEN is set.
const SetupTokenHeader = "X-Setup-Token"

// proxyEvidenceHeaders are set by reverse proxies. Their presence means the
// request crossed a hop the server cannot attribute, so the direct peer address
// is no longer evidence that the caller is local.
var proxyEvidenceHeaders = []string{
	"X-Forwarded-For",
	"X-Real-Ip",
	"Forwarded",
	"X-Forwarded-Proto",
	"X-Forwarded-Host",
}

// SetupAccessGuard authorizes POST /api/setup, which creates the initial root
// account. Without a gate, any instance reachable from the internet can be
// claimed by whoever calls the endpoint before the operator finishes setup.
//
// Two modes:
//
//   - SETUP_TOKEN is set: the request must present the token, either as
//     X-Setup-Token or as an "Authorization: Bearer <token>" header. This is the
//     mode internet-facing deployments should use.
//   - SETUP_TOKEN is unset: the request must arrive as a direct connection from
//     a loopback or private (RFC 1918 / RFC 4193) address and must carry no
//     proxy headers. This keeps local, LAN, and single-host container setups
//     working without configuration, while rejecting public callers and any
//     request that arrived through a reverse proxy.
func SetupAccessGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		deny := func(message string) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": message,
			})
		}

		if token := strings.TrimSpace(os.Getenv("SETUP_TOKEN")); token != "" {
			presented := c.GetHeader(SetupTokenHeader)
			if presented == "" {
				presented, _ = strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
			}
			if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presented)), []byte(token)) != 1 {
				deny("初始化令牌无效")
				return
			}
			c.Next()
			return
		}

		peerHost, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			peerHost = c.Request.RemoteAddr
		}
		peerIP := net.ParseIP(peerHost)
		if peerIP == nil || !(peerIP.IsLoopback() || peerIP.IsPrivate()) {
			deny("初始化请求必须来自本机或内网直连；如需从公网初始化，请配置 SETUP_TOKEN")
			return
		}
		for _, header := range proxyEvidenceHeaders {
			if c.GetHeader(header) != "" {
				deny("初始化请求经过代理，无法确认来源；请配置 SETUP_TOKEN")
				return
			}
		}
		c.Next()
	}
}
