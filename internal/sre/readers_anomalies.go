package sre

// readers_anomalies.go — v0.10.1494 (CoSRE 2d, plan §2.4): öznenin skor
// sıralı en çok N anomalisi + topoloji komşularının eşzamanlı anomalileri.
// Yalnız Context Pack açıkken (dalga 2: komşular bağımlılık adımının
// kenarlarından — topology_edges_5m, ≤5 servis düğümü).
//
// Okuma: anomaly_events (ListAnomalyEvents; Services STRICT IN, LIMIT'li, en
// çok iki sorgu). Pencere: last_seen ≥ çıpa başı − 60 dk VE started_at < çıpa
// sonu. Tabloda birleşik skor yok (behaviorScore kalıcı değil), sıra SAF
// anomalyRank = tepe oranı × tazelik ağırlığı (çıpa sonuna göre). Not: satır
// olayın BUGÜNKÜ durumudur (tepe sonraki gözlemleri de içerebilir) — tekrarda
// as-of değil; pack bunu söylemez çünkü sıra yalnız gösterimdir, skora girmez.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	anomalyNeighborsMax  = 5
	anomalyReadLimit     = 50
	anomalyLookbackSlack = 60 * time.Minute
	anomalyRecencyHalf   = 30.0 // dk — ağırlık 1/(1 + yaş/30)
)

// AnomalyRow — bir anomali olayı (anomaly_events satırının kısa hâli).
type AnomalyRow struct {
	Kind         string  `json:"kind"`
	Pattern      string  `json:"pattern"`
	PeakRatio    float64 `json:"peakRatio"`
	CurrentRatio float64 `json:"currentRatio"`
	StartedAt    int64   `json:"startedAt"`
	LastSeen     int64   `json:"lastSeen"`
	Rank         float64 `json:"rank"`
	// Direction / BaselineKind — v0.10.1792 (Davis F1.4): olayın yönü
	// (up/down) ve taban türü (seasonal/consecutive/none). Boş = eski satır
	// (bilinmiyor). Sıralama (Rank) bunları okumaz; anlatım "0×" yerine
	// "taban yok" diyebilsin diye taşınır.
	Direction    string `json:"direction,omitempty"`
	BaselineKind string `json:"baselineKind,omitempty"`
}

// NeighborAnomaly — komşu servisin eşzamanlı anomali özeti.
type NeighborAnomaly struct {
	Target     string  `json:"target"`
	Count      int     `json:"count"`
	TopKind    string  `json:"topKind,omitempty"`
	TopPattern string  `json:"topPattern,omitempty"`
	TopPeak    float64 `json:"topPeak,omitempty"`
}

// AnomalyFacts — anomaliler adımının parçası.
type AnomalyFacts struct {
	SubjectOK   bool              `json:"subjectOk"`
	Subject     []AnomalyRow      `json:"subject"`
	SubjectAll  int               `json:"subjectAll"` // penceredeki toplam (kırpmadan önce)
	NeighborsOK bool              `json:"neighborsOk"`
	Neighbors   []NeighborAnomaly `json:"neighbors,omitempty"`
}

// anomaliesRequest — adımın girdisi; Neighbors dalga 2 başında dolar.
type anomaliesRequest struct {
	Service   string
	From, To  time.Time
	TopN      int
	Neighbors []string
}

// anomalyRank — SAF: tepe oranı × tazelik (çıpa sonuna göre yaş; çıpadan sonra
// görülen = yaş 0).
func anomalyRank(e chstore.AnomalyEvent, at time.Time) float64 {
	ageMin := at.Sub(time.Unix(0, e.LastSeen)).Minutes()
	if ageMin < 0 {
		ageMin = 0
	}
	return round2(e.PeakRatio / (1 + ageMin/anomalyRecencyHalf))
}

// rankAnomalies — SAF: skor azalan; eşitlikte erken başlayan, sonra tür+desen.
func rankAnomalies(evs []chstore.AnomalyEvent, at time.Time) []AnomalyRow {
	out := make([]AnomalyRow, 0, len(evs))
	for _, e := range evs {
		out = append(out, AnomalyRow{Kind: e.Kind, Pattern: truncRunes(e.Pattern, 120), PeakRatio: round2(e.PeakRatio), CurrentRatio: round2(e.CurrentRatio),
			StartedAt: e.StartedAt, LastSeen: e.LastSeen, Rank: anomalyRank(e, at),
			Direction: e.Direction, BaselineKind: anomalyRowBaselineKind(e)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rank != b.Rank {
			return a.Rank > b.Rank
		}
		if a.StartedAt != b.StartedAt {
			return a.StartedAt < b.StartedAt
		}
		return a.Kind+"\x00"+a.Pattern < b.Kind+"\x00"+b.Pattern
	})
	return out
}

// neighborTargets — SAF: bağımlılık kenarlarının servis düğümleri (çağrı
// sırası, ≤5; db:/ext:/q: düğümleri anomaly_events servisi değildir).
func neighborTargets(df *DepsFacts, self string) []string {
	if df == nil {
		return nil
	}
	var out []string
	for _, e := range df.Edges {
		if isServiceNode(e.Target) && e.Target != self && len(out) < anomalyNeighborsMax {
			out = append(out, e.Target)
		}
	}
	return out
}

// neighborAnomaliesOf — SAF: komşu satırları → hedef başına özet (hedef sırası).
func neighborAnomaliesOf(targets []string, evs []chstore.AnomalyEvent, at time.Time) []NeighborAnomaly {
	by := map[string][]chstore.AnomalyEvent{}
	for _, e := range evs {
		by[e.Service] = append(by[e.Service], e)
	}
	var out []NeighborAnomaly
	for _, t := range targets {
		rows := rankAnomalies(by[t], at)
		if len(rows) == 0 {
			continue
		}
		out = append(out, NeighborAnomaly{Target: t, Count: len(rows), TopKind: rows[0].Kind, TopPattern: rows[0].Pattern, TopPeak: rows[0].PeakRatio})
	}
	return out
}

// runAnomalies — anomaliler adımı.
func runAnomalies(ctx context.Context, rd readers, req anomaliesRequest) stepOutcome {
	st := Step{Name: stepAnomalies}
	if rd.anomalies == nil {
		return skippedOutcome(stepAnomalies, "anomali okuyucusu bağlı değil")
	}
	filter := func(svcs []string) chstore.ListAnomalyEventsFilter {
		return chstore.ListAnomalyEventsFilter{Services: svcs, SinceNs: req.From.Add(-anomalyLookbackSlack).UnixNano(), ToNs: req.To.UnixNano(), Limit: anomalyReadLimit}
	}
	af := &AnomalyFacts{Subject: []AnomalyRow{}}
	evs, err := rd.anomalies(ctx, filter([]string{req.Service}))
	if err == nil {
		af.SubjectOK = true
		rows := rankAnomalies(evs, req.To)
		af.SubjectAll = len(rows)
		n := req.TopN
		if n <= 0 {
			n = DefaultContextPackSettings().TopAnomalies
		}
		if len(rows) > n {
			rows = rows[:n]
		}
		af.Subject = rows
	}
	var nerr error
	if len(req.Neighbors) > 0 {
		var nevs []chstore.AnomalyEvent
		if nevs, nerr = rd.anomalies(ctx, filter(req.Neighbors)); nerr == nil {
			af.NeighborsOK = true
			af.Neighbors = neighborAnomaliesOf(req.Neighbors, nevs, req.To)
		}
	}
	switch {
	case err != nil && (nerr != nil || len(req.Neighbors) == 0):
		st.State, st.Summary = StepError, truncRunes("anomaliler okunamadı: "+err.Error(), 400)
		return stepOutcome{step: st}
	case err != nil || nerr != nil:
		st.State = StepPartial
	default:
		st.State = StepOK
	}
	st.Summary = fmt.Sprintf("öznede %d anomali (ilk %d)", af.SubjectAll, len(af.Subject))
	if len(req.Neighbors) > 0 {
		st.Summary += fmt.Sprintf(" · %d komşunun %d'inde eşzamanlı anomali", len(req.Neighbors), len(af.Neighbors))
	}
	if af.SubjectAll == 0 && len(af.Neighbors) == 0 && st.State == StepOK {
		st.NoData = true
		st.Summary = "veri yok: pencerede öznede ve komşularda anomali olayı yok"
	}
	return stepOutcome{step: st, facts: &Facts{Anomalies: af}}
}

// anomalyRowBaselineKind — SAF (v0.10.1792): satırın taban türü; eski satırda
// (baseline_kind ”) tabansızlık eski yorumla (ratio 0) "none" yazılır,
// aksi boş (bilinmiyor).
func anomalyRowBaselineKind(e chstore.AnomalyEvent) string {
	if e.BaselineKind != "" {
		return e.BaselineKind
	}
	if chstore.AnomalyBaselineless(e) {
		return chstore.AnomalyBaselineNone
	}
	return ""
}
