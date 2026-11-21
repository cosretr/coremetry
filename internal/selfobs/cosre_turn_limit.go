package selfobs

// cosre_turn_limit.go — CoSRE kişi başı tur sınırının öz-gözlemi:
// cosre.chat.turn_limit{result=limited_hour|limited_day|degraded}.
// degraded = Redis sayacı yok (R0: engelleme yok, tur geçti).

import (
	"context"
	"log"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	turnLimitOnce sync.Once
	turnLimitCtr  metric.Int64Counter
	turnLimitSets setCache[string]
)

// CountCoSRETurnLimit — result SABİT bir değer olmalı (kardinalite 3).
func CountCoSRETurnLimit(result string) {
	turnLimitOnce.Do(func() {
		var e error
		if turnLimitCtr, e = Meter().Int64Counter("cosre.chat.turn_limit",
			metric.WithDescription("CoSRE kişi başı tur sınırı: result=limited_hour|limited_day|degraded")); e != nil {
			log.Printf("[selfobs] cosre.chat.turn_limit: %v", e)
		}
	})
	if turnLimitCtr == nil {
		return
	}
	set := turnLimitSets.get(result, func() attribute.Set { return attribute.NewSet(attribute.String("result", result)) })
	turnLimitCtr.Add(context.Background(), 1, metric.WithAttributeSet(set))
}
