package api

import (
	"fmt"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// problemHealth — v0.10.2488 (entity tutarlılık denetimi dilim 4, Karar A2).
// Sağlık noktasının TEK tanımı: yalnız AÇIK Problem'lerden. Açık kritik →
// "red"; açık uyarı / bilgi → "yellow"; yok → "green" (ön yüzde nötr halka,
// K5). Hata oranı eşikleri burada KULLANILMAZ — hata oranı sapması hücre
// rengidir (FE `lib/format/tone.ts` `errorTone`), sağlık değil.
//
// Noktayı besleyen her uç bunu çağırır: `/api/services` (api.go),
// `/api/services/metric-rank` (services_metric_rank.go), Logs kapsam seçicisi
// (logs_scope.go). FE tek bileşen `components/coremetry/HealthDot.tsx`.
//
// `scoreHealth` (hata oranı dallı) yalnız deploy raporu / rollout ayrıntısının
// "deploy sonrası" hükmünde kalır — o bir nokta değil, deploy öncesi/sonrası
// RED karşılaştırmasının özeti.
func problemHealth(c chstore.OpenProblemCounts) (string, string) {
	switch {
	case c.Critical > 0:
		return "red", fmt.Sprintf("%d open critical", c.Critical)
	case c.Warning > 0:
		return "yellow", fmt.Sprintf("%d open warning", c.Warning)
	case c.Info > 0:
		return "yellow", fmt.Sprintf("%d open info", c.Info)
	}
	return "green", ""
}
