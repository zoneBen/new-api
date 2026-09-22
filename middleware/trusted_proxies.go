package middleware

import (
	"log"
	"os"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func ConfigureTrustedProxies(engine *gin.Engine) error {
	trustedProxies, usedDefaults, err := common.ResolveTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return err
	}
	if usedDefaults {
		log.Print("WARNING: TRUSTED_PROXIES is unset or blank; trusting loopback, RFC 1918, and IPv6 ULA proxy addresses for compatibility. Set TRUSTED_PROXIES=none to trust no proxies, or configure explicit proxy IPs/CIDRs to replace these defaults.")
		log.Print("WARNING: with these defaults, any client that reaches the server from a private address can choose its own rate-limit bucket and token IP allowlist entry through a forged X-Forwarded-For header. This includes a published container port, where every external client appears to come from the Docker gateway, and any reverse proxy that forwards the client's own X-Forwarded-For unchanged. Set TRUSTED_PROXIES to the addresses of your proxies only.")
	}
	return common.ConfigureTrustedProxies(engine, trustedProxies)
}
