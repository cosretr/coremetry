package selfobs

// ai_thinking.go — v0.10.2458: yüzey başına Qwen düşünme kipinin öz-gözlemi.
// ai.llm.thinking.duration{surface, thinking, status} — tek histogram, sayısı
// çağrı adedini verir; operatör aynı yüzeyde on / off gecikmesini kıyaslar.
// Kardinalite sabit: surface 8 (copilot.ThinkSurface), thinking 4 (on | off |
// model_default | unsupported), status 2 (ok | error).

import (
	"context"
	"log"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type aiThinkKey struct{ surface, thinking, status string }

// aiThinkBuckets — LLM çağrıları: kısa sınıflayıcı (≈0,5 sn) → uzun düşünme (dakikalar).
var aiThinkBuckets = []float64{0.25, 0.5, 1, 2, 4, 8, 15, 30, 60, 120, 300}

var (
	aiThinkOnce sync.Once
	aiThinkHist metric.Float64Histogram
	aiThinkSets setCache[aiThinkKey]
)

// RecordAIThinkingCall — surface / thinking SABİT kümelerden gelmeli.
func RecordAIThinkingCall(surface, thinking string, failed bool, d time.Duration) {
	aiThinkOnce.Do(func() {
		var e error
		if aiThinkHist, e = Meter().Float64Histogram("ai.llm.thinking.duration",
			metric.WithUnit("s"),
			metric.WithDescription("LLM çağrı süresi, düşünme yüzeyi × kip (on|off|model_default|unsupported) × status"),
			metric.WithExplicitBucketBoundaries(aiThinkBuckets...)); e != nil {
			log.Printf("[selfobs] ai.llm.thinking.duration: %v", e)
		}
	})
	if aiThinkHist == nil {
		return
	}
	status := "ok"
	if failed {
		status = "error"
	}
	k := aiThinkKey{surface, thinking, status}
	set := aiThinkSets.get(k, func() attribute.Set {
		return attribute.NewSet(attribute.String("surface", surface), attribute.String("thinking", thinking), attribute.String("status", status))
	})
	aiThinkHist.Record(context.Background(), d.Seconds(), metric.WithAttributeSet(set))
}
