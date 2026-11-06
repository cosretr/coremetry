package api

// logs_fieldstats_sample.go — v0.10.1852 (Log Search Faz 2 dilim 2.1; audit
// §3.2, §5 risk #1). /api/logs/fieldstats `est=<pencere toplamı>` → ES
// random_sampler olasılığı (logstore.FieldStatsSampleProbability). Rota yok;
// getLogsFieldStats'in (api_logs.go) iki yardımcısı.
//
// Kurallar:
//   - Yalnız örneklemli backend (logstore.FieldStatsSampler — ES). CH log yolu
//     yeteneği uygulamaz: `est` yok sayılır, anahtar ve yanıt bayt-bayt aynı.
//   - errorLift açıkken örnekleme yok: `est` sayfa penceresinin toplamı, hata
//     seçimininki değil; lift'in iki sayımı aynı (tam) tabanda kalır.
//   - Önbellek anahtarı `est`i değil BASAMAĞI taşır (kardinalite sınırlı);
//     örneklemesizde ek yok (eski anahtar aynen).

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// logsFieldStatsSampleProbability — SAF: ham `est` + bayraklar → olasılık (0 = yok).
func logsFieldStatsSampleProbability(raw string, lift, capable bool) float64 {
	if lift || !capable {
		return 0
	}
	est, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || est <= 0 {
		return 0
	}
	return logstore.FieldStatsSampleProbability(est)
}

func (s *Server) logsFieldStatsSampleP(raw string, lift bool) float64 {
	_, capable := s.logs.(logstore.FieldStatsSampler)
	return logsFieldStatsSampleProbability(raw, lift, capable)
}

// logsFieldStatsSampleKey — SAF: anahtar eki; 0 → "" (örneklemesiz anahtar aynı).
func logsFieldStatsSampleKey(p float64) string {
	if p <= 0 {
		return ""
	}
	return fmt.Sprintf(":smpP=%g", p)
}
