package vmetrics

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/promapi"
)

// sdk_language.go — 2026-11-21 (runtime tespiti, prod gözlemi): her metrik
// serisi OTel resource etiketi `telemetry_sdk_language` taşır ("java" /
// "dotnet" / "python" …). Span'i olmayan (yalnız metrik gönderen) serviste
// runtime'ı söyleyen en ucuz okuma: tek label-values çağrısı, servis
// süzgeçli, son 1 saat.

const sdkLanguageLabel = "telemetry_sdk_language"

// ServiceSDKLanguage — servisin metrik serilerindeki telemetry_sdk_language
// değeri. Birden çok değer varsa en sık değil, kararlı olsun diye alfabetik
// İLK boş olmayan (sidecar'lı servis nadir; tespit zaten dil > katalog sırası).
// Değer yoksa "" (hata değil).
func (s *Service) ServiceSDKLanguage(ctx context.Context, service string) (string, error) {
	cfg, err := s.ready()
	if err != nil {
		return "", err
	}
	svc := strings.TrimSpace(service)
	if svc == "" {
		return "", nil
	}
	now := time.Now()
	params := url.Values{
		"start":   {promTime(now.Add(-time.Hour))},
		"end":     {promTime(now)},
		"match[]": {"{" + serviceLabel() + "=" + quotePromString(svc) + "}"},
	}
	vals, err := promapi.QueryStrings(ctx, s.requestCtx(ctx, "/api/v1/label/"+sdkLanguageLabel+"/values", params, cfg))
	if err != nil {
		return "", err
	}
	sort.Strings(vals)
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v, nil
		}
	}
	return "", nil
}
