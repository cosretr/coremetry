package uistream

import (
	"regexp"
	"strings"
)

// Hata sınıfları — data-error parçasının `class` alanı (istemci kopyası
// sınıfa göre eylem önerir; ham metin `raw`da aynen).
const (
	ClassDeadline     = "deadline"
	ClassOverflow     = "overflow"
	ClassCancelled    = "cancelled"
	ClassMaxTokens    = "max_tokens"
	ClassProviderDown = "provider_down"
	ClassQuota        = "quota"
	ClassClosed       = "closed"
	ClassUnknown      = "unknown"
)

// closedErrorText — akış terminal olaysız kapandığında (blocks.Emitter.Close ile aynı metin).
const closedErrorText = "stream closed before completion"

// sağlayıcı HTTPError biçimi "<etiket> 5xx: …" (internal/ai/provider/errors.go).
var reProvider5xx = regexp.MustCompile(`(?:^|\s)5\d\d:`)

// ClassifyError — SAF: ham hata metninin sınıfı. Kalıplar kaynaktaki
// metinlerden (errors_test.go her sınıf için bir gerçek metin taşır).
func ClassifyError(raw string) string {
	low := strings.ToLower(raw)
	switch {
	case raw == "":
		return ClassUnknown
	case strings.Contains(raw, closedErrorText):
		return ClassClosed
	case strings.Contains(raw, "tavanına dayandı"): // api/chat_deadline.go
		return ClassDeadline
	case strings.Contains(raw, "bağlam penceresine sığmadı"): // api/chat_overflow.go
		return ClassOverflow
	case strings.Contains(raw, "İstek iptal edildi"): // api/chat_loop_steps.go
		return ClassCancelled
	case strings.Contains(low, "model returned no answer"), strings.Contains(low, "model returned empty content"): // provider/salvage.go
		return ClassMaxTokens
	case isQuotaText(low):
		return ClassQuota
	case reProvider5xx.MatchString(raw), strings.Contains(low, "connection refused"), strings.Contains(low, "no such host"):
		return ClassProviderDown
	}
	return ClassUnknown
}

// isQuotaText — copilot.isQuotaErr'in anahtar sözcükleriyle aynı küme.
func isQuotaText(low string) bool {
	for _, kw := range []string{" 429", "429:", "quota", "rate limit", "rate_limit", "resource_exhausted", "too many requests"} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}
