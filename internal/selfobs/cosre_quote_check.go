package selfobs

// cosre_quote_check.go — v0.10.2461: CoSRE cevap doğrulayıcısının alıntı
// denetimi öz-gözlemi: cosre.answer.quote_check{result=verified|mismatch}.
// Tur başına, kayıt anında (taslak denetimleri sayılmaz).

import (
	"context"
	"log"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	quoteCheckOnce sync.Once
	quoteCheckCtr  metric.Int64Counter
	quoteCheckSets setCache[string]
)

// CountCoSREQuoteCheck — result SABİT bir değer olmalı (kardinalite 2); n ≤ 0 yazılmaz.
func CountCoSREQuoteCheck(result string, n int) {
	if n <= 0 {
		return
	}
	quoteCheckOnce.Do(func() {
		var e error
		if quoteCheckCtr, e = Meter().Int64Counter("cosre.answer.quote_check",
			metric.WithDescription("CoSRE cevabındaki tırnak içi alıntıların kanıt denetimi: result=verified|mismatch")); e != nil {
			log.Printf("[selfobs] cosre.answer.quote_check: %v", e)
		}
	})
	if quoteCheckCtr == nil {
		return
	}
	set := quoteCheckSets.get(result, func() attribute.Set { return attribute.NewSet(attribute.String("result", result)) })
	quoteCheckCtr.Add(context.Background(), int64(n), metric.WithAttributeSet(set))
}
