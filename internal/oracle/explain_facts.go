package oracle

// explain_facts.go — v0.10.1100 (operatör onaylı: Oracle hata grubu için AI
// açıklaması span yerine Oracle bağlamıyla). anomaly paketi oracle'ı import
// edemez (oracle → anomaly); explain kurucusunun ihtiyaç duyduğu blob
// gerçekleri bu köprüden, AYNI GroupStatsCache'ten (v0.10.1092) gelir —
// Exceptions satırı, öncelik ve AI açıklaması aynı saatlik sayıyı görür.

import (
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// ExplainFacts — anomaly.OracleFactsFn imzası. ok=false: Oracle grubu değil
// ya da kaynağı yok.
func (c *GroupStatsCache) ExplainFacts(g chstore.ExceptionGroup) (anomaly.OracleExplainFacts, bool) {
	st, ok := c.Stats(g)
	if !ok {
		return anomaly.OracleExplainFacts{}, false
	}
	return ExplainFactsFrom(st), true
}

// ExplainFactsFrom — SAF: GroupStats → anomaly gerçekleri (kırılımlar sıralı).
func ExplainFactsFrom(st GroupStats) anomaly.OracleExplainFacts {
	f := anomaly.OracleExplainFacts{
		SourceID: st.Source.ID, SourceName: st.Source.Name, Lag: st.Lag,
		LastHour: st.LastHour, PrevHour: st.PrevHour, BlobOK: st.BlobOK,
	}
	if b := st.Breakdown; b != nil {
		f.Channels = namedCounts(b.Channels)
		f.Services = namedCounts(b.Services)
	}
	return f
}

func namedCounts(m map[string]uint64) []anomaly.OracleNamedCount {
	es := sortedBreakdown(m)
	if len(es) == 0 {
		return nil
	}
	out := make([]anomaly.OracleNamedCount, 0, len(es))
	for _, e := range es {
		out = append(out, anomaly.OracleNamedCount{Name: e.Name, Count: e.Count})
	}
	return out
}
