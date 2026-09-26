package api

import (
	"os"
	"strings"
	"testing"
)

// v0.10.444 — pencere kıyası yalnız RED okur: buildServiceContext pencere
// başına 8 okuma (ham exception taraması, komşu örnekleme…) yapıyordu.
// v0.10.944 — ve o RED tüm-pencere yüzdeliğidir (kova ortalaması değil).
func TestWindowCompareReadsOnlyRED(t *testing.T) {
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) guidedWindowCompareBundle(")
	if i < 0 {
		t.Fatal("bundle yok")
	}
	body := src[i:]
	if j := strings.Index(body, "\n}\n"); j > 0 {
		body = body[:j]
	}
	// v0.10.944 — pin GetServiceSummary5m + aggRED idi; o çift 5 dk kova
	// yüzdeliklerini span ağırlıklı ORTALIYORDU. Artık pencere başına tek
	// satır: ServiceWindowRED (tdigest durumlarının birleşimi) + windowRED.
	if strings.Contains(body, "buildServiceContext(") || !strings.Contains(body, "ServiceWindowRED(") ||
		strings.Contains(body, "GetServiceSummary5m(") || strings.Contains(body, "aggRED(") {
		t.Fatal("window_compare buildServiceContext/kova ortalaması yerine ServiceWindowRED + windowRED kullanmalı")
	}
}
