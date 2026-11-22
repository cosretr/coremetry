package mcptools

// sampling_label.go — v0.10.2457: one wording for span-derived numbers
// under sampling weight (ingest.sampling.mode = on). A chstore reader that
// weighted its read says so on the result (Weighted); the tool then labels
// the numbers "örnekleme düzeltilmiş" instead of "sampled" / "örneklenmiş".
// Mode off / shadow: every text stays as before. Unsampled VictoriaMetrics
// sources keep their own labels.

import "github.com/cilcenk/coremetry/internal/chstore"

const spanLabelWeighted = "örnekleme düzeltilmiş"

// spanLabel — SAF: short English-context label.
func spanLabel(weighted bool) string {
	if weighted {
		return spanLabelWeighted
	}
	return "sampled"
}

// spanLabelLong — SAF: the "stored (sampled)" phrase.
func spanLabelLong(weighted bool) string {
	if weighted {
		return spanLabelWeighted + " (sampling-weighted)"
	}
	return "stored (sampled)"
}

// spanLabelTR — SAF: Turkish-context label.
func spanLabelTR(weighted bool) string {
	if weighted {
		return spanLabelWeighted
	}
	return "örneklenmiş"
}

// dpWeightedNote — dependency_path notes[1] when MV calls are weighted.
const dpWeightedNote = "mv calls are " + spanLabelWeighted + " (sampling-weighted represented calls; avg/p99 stay stored-span values); latency_kind avg_ms (mv) or p95_ms (vm, unsampled)"

// hrWeightedSpanNote — get_service_health trace_errors note under weighting.
const hrWeightedSpanNote = spanLabelWeighted + ": saklanan span'ler sample_weight ile temsil ettikleri trafiğe çevrildi — gerçek hata oranının tahmini (kesin oran: metrics)"

func dpAnyWeighted(es []dpEdge) bool {
	for _, e := range es {
		if e.Weighted {
			return true
		}
	}
	return false
}

// opTotalsWeighted — SAF: any weighted operation total.
func opTotalsWeighted(m map[string]chstore.OpTotal) bool {
	for _, t := range m {
		if t.Weighted {
			return true
		}
	}
	return false
}
