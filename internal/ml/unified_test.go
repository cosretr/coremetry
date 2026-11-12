package ml

// v0.10.2051 — /anomalies birleştirmesi sunucuya taşındı. Durumlar
// frontend/src/components/coremetry/anomalies/unifiedRows.test.ts ve
// mlGroupRows testlerinden birebir taşındı (aynı girdi → aynı satır,
// aynı lider, aynı anahtar, aynı sıra). Eski sorun: istemci birleştirmesi
// iki kaynağı 200'er satırla kesiyordu, yoğun pencerede satırlar sessizce
// düşüyordu. Servis adları SENTETİK.

import (
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const uMin = int64(time.Minute)

var uT = int64(1_800_000_000) * int64(time.Second)

func uAE(id string, mod func(*chstore.AnomalyEvent)) chstore.AnomalyEvent {
	e := chstore.AnomalyEvent{ID: id, Kind: "log_template_new", Pattern: "Batch job <*> failed", Service: "orders-batch",
		StartedAt: uT, LastSeen: uT, CurrentCount: 3, Sample: "Batch job 7 failed", Status: "active"}
	if mod != nil {
		mod(&e)
	}
	return e
}

func uME(id string, mod func(*chstore.MLEvent)) chstore.MLEvent {
	e := chstore.MLEvent{ID: id, Detector: "error_rate", Signal: "log", Stage: "shadow", Service: "orders-batch",
		StartedAt: uT, LastSeen: uT + 10*uMin, WindowSec: 300, Baseline: 1, Observed: 9, Score: 6, Direction: "up"}
	if mod != nil {
		mod(&e)
	}
	return e
}

func uIn(ml []chstore.MLEvent, leg []chstore.AnomalyEvent) UnifiedInput {
	return UnifiedInput{ML: ml, Legacy: leg, Now: time.Unix(0, uT+11*uMin), Slack: 10 * time.Minute}
}

func uLegIDs(in UnifiedInput, r UnifiedRow) []string {
	var out []string
	for _, i := range r.Legacy {
		out = append(out, in.Legacy[i].ID)
	}
	return out
}

func TestBuildUnified(t *testing.T) {
	t.Run("aynı saniyede doğan iki yeni kalıp tek satır; lider tepe orandan", func(t *testing.T) {
		in := uIn(nil, []chstore.AnomalyEvent{uAE("a1", nil), uAE("a2", func(e *chstore.AnomalyEvent) { e.CurrentCount, e.PeakRatio = 5, 1 })})
		rows := BuildUnified(in)
		if len(rows) != 1 || len(rows[0].Legacy) != 2 {
			t.Fatalf("satırlar = %+v", rows)
		}
		r := rows[0]
		if in.Legacy[r.LegLead].ID != "a2" || r.Key != "ae:a2" || r.MLLead != -1 {
			t.Fatalf("lider/anahtar = %d %q", r.LegLead, r.Key)
		}
		if !reflect.DeepEqual(r.Signals, []string{"log"}) || r.Status != UnifiedWatching {
			t.Fatalf("sinyal/durum = %v %s", r.Signals, r.Status)
		}
	})
	t.Run("aynı serviste ML olayı varsa eski olay onunla birleşir; lider ML", func(t *testing.T) {
		in := uIn([]chstore.MLEvent{uME("m1", nil)}, []chstore.AnomalyEvent{uAE("a1", func(e *chstore.AnomalyEvent) { e.StartedAt, e.LastSeen = uT+5*uMin, uT+5*uMin })})
		rows := BuildUnified(in)
		if len(rows) != 1 || rows[0].Key != "ml:ev:m1" || in.ML[rows[0].MLLead].ID != "m1" || len(rows[0].Legacy) != 1 {
			t.Fatalf("satırlar = %+v", rows)
		}
	})
	t.Run("farklı servis ya da boşluk > teyit penceresi → ayrı satır", func(t *testing.T) {
		if n := len(BuildUnified(uIn(nil, []chstore.AnomalyEvent{uAE("a1", nil), uAE("a2", func(e *chstore.AnomalyEvent) { e.Service = "payments" })}))); n != 2 {
			t.Fatalf("farklı servis: %d satır", n)
		}
		if n := len(BuildUnified(uIn(nil, []chstore.AnomalyEvent{uAE("a1", nil), uAE("a2", func(e *chstore.AnomalyEvent) { e.StartedAt, e.LastSeen = uT+11*uMin, uT+11*uMin })}))); n != 2 {
			t.Fatalf("boşluk: %d satır", n)
		}
		if n := len(BuildUnified(uIn(nil, []chstore.AnomalyEvent{uAE("a1", nil), uAE("a2", func(e *chstore.AnomalyEvent) { e.StartedAt, e.LastSeen = uT+10*uMin, uT+10*uMin })}))); n != 1 {
			t.Fatalf("sınır (tam pencere) dahil olmalı: %d satır", n)
		}
	})
	t.Run("sıra: süren önce, sonra yeni başlayan; biten ayrı", func(t *testing.T) {
		in := uIn(nil, []chstore.AnomalyEvent{
			uAE("old", func(e *chstore.AnomalyEvent) {
				e.Service, e.StartedAt, e.LastSeen, e.Status = "s1", uT-60*uMin, uT-60*uMin, "cleared"
			}),
			uAE("a", func(e *chstore.AnomalyEvent) { e.Service, e.StartedAt, e.LastSeen = "s2", uT-30*uMin, uT-30*uMin }),
			uAE("b", func(e *chstore.AnomalyEvent) { e.Service = "s3" }),
		})
		var open, done []UnifiedRow
		for _, r := range BuildUnified(in) {
			if UnifiedOpen(r.Status) {
				open = append(open, r)
			} else {
				done = append(done, r)
			}
		}
		SortUnifiedOpen(open)
		SortUnifiedDone(done)
		var got []string
		for _, r := range append(open, done...) {
			got = append(got, uLegIDs(in, r)[0])
		}
		if !reflect.DeepEqual(got, []string{"b", "a", "old"}) || len(done) != 1 {
			t.Fatalf("sıra = %v", got)
		}
	})
	t.Run("ML grubu: üyeler grup kimliğine katlanır, aralık + sinyaller gruptan, gölge 'açardı' → Sürüyor", func(t *testing.T) {
		m1 := uME("m1", func(e *chstore.MLEvent) { e.Score = 9 })
		m2 := uME("m2", func(e *chstore.MLEvent) { e.Detector, e.Signal = "op_latency", "trace" })
		g := &MLGroup{ID: "mlg-x", Service: "orders-batch", Start: uT - 30*uMin, End: uT + 10*uMin, Signals: []string{"log", "trace", "metric"}}
		in := uIn([]chstore.MLEvent{m1, m2}, nil)
		in.Groups = map[string]*MLGroup{"m1": g, "m2": g}
		in.Shadows = map[string]GroupDecision{"mlg-x": {Group: "mlg-x", WouldOpen: true}}
		rows := BuildUnified(in)
		if len(rows) != 1 {
			t.Fatalf("satırlar = %+v", rows)
		}
		r := rows[0]
		if r.Key != "ml:mlg-x" || r.Start != uT-30*uMin || r.Status != UnifiedRunning || in.ML[r.MLLead].ID != "m1" {
			t.Fatalf("satır = %+v", r)
		}
		if !reflect.DeepEqual(r.Signals, []string{"trace", "log", "metric"}) {
			t.Fatalf("sinyaller = %v", r.Signals)
		}
	})
	t.Run("ML: 'değil' kararı tek başına → Değil dendi; 'gerçek' varsa değil", func(t *testing.T) {
		in := uIn([]chstore.MLEvent{uME("m1", func(e *chstore.MLEvent) { e.Verdict = chstore.VerdictNotAnomaly })}, nil)
		if s := BuildUnified(in)[0].Status; s != UnifiedRejected {
			t.Fatalf("durum = %s", s)
		}
	})
	t.Run("ML: kapalı damga ya da 4 kovadan bayat → Bitti; kronik olay da satırdır", func(t *testing.T) {
		in := uIn([]chstore.MLEvent{
			uME("closed", func(e *chstore.MLEvent) { e.Stage = chstore.MLStageClosed }),
			uME("stale", func(e *chstore.MLEvent) { e.Service, e.LastSeen = "payments", uT-20*uMin }),
			uME("chr", func(e *chstore.MLEvent) { e.Service, e.Stage = "billing", chstore.MLStageChronic }),
		}, nil)
		got := map[string]string{}
		for _, r := range BuildUnified(in) {
			got[r.Service] = r.Status
		}
		want := map[string]string{"orders-batch": UnifiedDone, "payments": UnifiedDone, "billing": UnifiedWatching}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("durumlar = %v", got)
		}
	})
	t.Run("eski olay: 'değil' → Değil dendi; birleşik durum en süreni", func(t *testing.T) {
		in := uIn(nil, []chstore.AnomalyEvent{
			uAE("r", func(e *chstore.AnomalyEvent) { e.Verdict = chstore.VerdictNotAnomaly }),
			uAE("c", func(e *chstore.AnomalyEvent) { e.Status = "cleared" }),
		})
		if s := BuildUnified(in)[0].Status; s != UnifiedDone {
			t.Fatalf("durum = %s", s)
		}
	})
	t.Run("servissiz eski olaylar kendi aralarında zincirlenir", func(t *testing.T) {
		in := uIn(nil, []chstore.AnomalyEvent{uAE("n1", func(e *chstore.AnomalyEvent) { e.Service = "" }), uAE("n2", func(e *chstore.AnomalyEvent) { e.Service = "" })})
		if rows := BuildUnified(in); len(rows) != 1 || rows[0].Service != "" {
			t.Fatalf("satırlar = %+v", rows)
		}
	})
}

func TestLegacySignal(t *testing.T) {
	for kind, want := range map[string]string{
		"log_pattern": "log", "log_template_new": "log", "trace_op_latency": "trace", "trace_op": "trace", "op_latency": "trace",
		"behavior_change": "metric", "subject_red": "metric", "elastic_ml": "log", "ml:error_rate": "log", "heap": "metric",
	} {
		if got := LegacySignal(kind); got != want {
			t.Errorf("%s → %s, istenen %s", kind, got, want)
		}
	}
}

func TestUnifiedSignalOf(t *testing.T) {
	cases := []struct{ det, sig, want string }{
		{"metric_jvm", "log", "metric"}, {"trace_error_count", "", "trace"}, {"error_rate", "trace", "log"},
		{"unknown", "trace", "trace"}, {"unknown", "", "log"}, {"unknown", "metric", "metric"},
	}
	for _, c := range cases {
		if got := UnifiedSignalOf(c.det, c.sig); got != c.want {
			t.Errorf("%s/%s → %s, istenen %s", c.det, c.sig, got, c.want)
		}
	}
}
