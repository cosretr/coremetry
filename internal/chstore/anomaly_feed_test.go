package chstore

// anomaly_feed_test.go — v0.10.2080: eski anomaly_events okuyucularının
// ML olaylarını da görmesi (ListAnomalyFeed). Pinler: eski log_template_new
// satırı ML new_template olayıyla taşındıysa listede BİR kez (legacyId
// kuralı); emekli "ml:" kopya satırı ML satırıyla çakışmaz; süzgecin Go
// kısmı (aktif, başlangıç penceresi, susturma) ML satırına da uygulanır.

import (
	"testing"
	"time"
)

const fT = int64(1_800_000_000) * int64(time.Second)

func fLegacy(id string, last int64, status string) AnomalyEvent {
	return AnomalyEvent{ID: id, Kind: "log_template_new", Service: "orders-api", StartedAt: last - int64(time.Minute), LastSeen: last, Status: status}
}

func fML(id, det string, last int64, ev string) MLEvent {
	return MLEvent{ID: id, Detector: det, Signal: MLSignalLog, Stage: MLStageShadow, Service: "orders-api",
		StartedAt: last - int64(time.Minute), LastSeen: last, WindowSec: 300, Score: 4, Observed: 25, Evidence: ev}
}

func TestMergeAnomalyFeed(t *testing.T) {
	now := fT
	cases := []struct {
		name   string
		legacy []AnomalyEvent
		ml     []MLEvent
		f      ListAnomalyEventsFilter
		want   []string
	}{
		{"legacyId eşleşen eski satır düşer, ML kalır",
			[]AnomalyEvent{fLegacy("old1", now, "active"), fLegacy("old2", now-int64(time.Hour), "cleared")},
			[]MLEvent{fML("m1", "new_template", now, `{"legacyId":"old1"}`)},
			ListAnomalyEventsFilter{}, []string{"m1", "old2"}},
		{"legacyId başka dedektörde yok sayılır",
			[]AnomalyEvent{fLegacy("old1", now, "active")},
			[]MLEvent{fML("m1", "template_spike", now-int64(time.Second), `{"legacyId":"old1"}`)},
			ListAnomalyEventsFilter{}, []string{"old1", "m1"}},
		{"emekli ml: kopya satırı aynı kimlikle bir kez",
			[]AnomalyEvent{{ID: "m1", Kind: "ml:trace_error_count", LastSeen: now, Status: "active"}},
			[]MLEvent{fML("m1", "trace_error_count", now, "")},
			ListAnomalyEventsFilter{}, []string{"m1"}},
		{"ActiveOnly: kapalı ML olayı düşer",
			nil,
			[]MLEvent{fML("open", "new_template", now, ""), func() MLEvent { e := fML("closed", "new_template", now, ""); e.Stage = MLStageClosed; return e }()},
			ListAnomalyEventsFilter{ActiveOnly: true}, []string{"open"}},
		{"ActiveOnly: 4 kovadan bayat ML olayı düşer",
			nil,
			[]MLEvent{fML("stale", "new_template", now-21*int64(time.Minute), "")},
			ListAnomalyEventsFilter{ActiveOnly: true}, nil},
		{"başlangıç penceresi ML satırına da",
			nil,
			[]MLEvent{fML("in", "new_template", now, ""), fML("before", "new_template", now-2*int64(time.Hour), "")},
			ListAnomalyEventsFilter{FromNs: now - int64(time.Hour), ToNs: now + 1}, []string{"in"}},
		{"susturulan kimlik ML satırında da elenir",
			nil,
			[]MLEvent{fML("m1", "new_template", now, ""), fML("m2", "new_template", now-1, "")},
			ListAnomalyEventsFilter{ExcludeIDs: []string{"m1"}}, []string{"m2"}},
		{"aktif önce, sonra yeni önce, tavan",
			[]AnomalyEvent{fLegacy("oldc", now, "cleared"), fLegacy("olda", now-int64(time.Minute), "active")},
			[]MLEvent{fML("m1", "op_latency", now-int64(30*time.Second), "")},
			ListAnomalyEventsFilter{Limit: 2}, []string{"m1", "olda"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MergeAnomalyFeed(c.legacy, c.ml, c.f, now)
			var ids []string
			for _, e := range got {
				ids = append(ids, e.ID)
			}
			if len(ids) != len(c.want) {
				t.Fatalf("got %v want %v", ids, c.want)
			}
			for i := range ids {
				if ids[i] != c.want[i] {
					t.Fatalf("got %v want %v", ids, c.want)
				}
			}
		})
	}
}

func TestMLEventAsAnomaly(t *testing.T) {
	e := fML("m1", "new_template", fT, `{"template":"Batch job <*> failed","sample":"Batch job 7 failed","logQuery":"\"Batch job\" AND failed","legacyId":"x"}`)
	e.Verdict = VerdictNotAnomaly
	a := MLEventAsAnomaly(e, fT)
	if a.Kind != "ml:new_template" || a.Pattern != "Batch job <*> failed" || a.Sample != "Batch job 7 failed" ||
		a.LogQuery == "" || a.Status != "active" || a.CurrentCount != 25 || a.Verdict != VerdictNotAnomaly {
		t.Fatalf("şekil yanlış: %+v", a)
	}
	e.Code = "ORA-00060"
	if got := MLEventAsAnomaly(e, fT).Pattern; got != "ORA-00060" {
		t.Fatalf("kod önce: %q", got)
	}
	if a.PeakRatio != CountOnlyRatio || a.BaselineKind != AnomalyBaselineNone {
		t.Fatalf("tabansız olay sayım olayı olmalı: %+v", a)
	}
	bare := fML("m2", "op_latency", fT-time.Hour.Nanoseconds(), "")
	bare.Baseline = 5
	if a := MLEventAsAnomaly(bare, fT); a.Pattern != "op_latency" || a.Status != "cleared" || a.PeakRatio != 5 || a.BaselineKind != "" {
		t.Fatalf("yedek desen / durum: %+v", a)
	}
}

func TestMLFeedFilter(t *testing.T) {
	now := time.Unix(0, fT)
	if _, ok := mlFeedFilter(ListAnomalyEventsFilter{Kind: SubjectAnomalyKind}, now); ok {
		t.Fatal("eski tür süzgeci ML okumamalı")
	}
	if _, ok := mlFeedFilter(ListAnomalyEventsFilter{Services: []string{}}, now); ok {
		t.Fatal("boş servis kümesi = hiçbir şey; ML okunmamalı")
	}
	mf, ok := mlFeedFilter(ListAnomalyEventsFilter{Kind: "ml:new_template", Services: []string{"orders-api"}, SinceNs: fT - int64(time.Hour), FromNs: fT - int64(time.Minute), Limit: 5000}, now)
	if !ok || mf.Detector != "new_template" || len(mf.Services) != 1 || mf.Since.UnixNano() != fT-int64(time.Minute) || mf.Limit != MLUnifiedCap {
		t.Fatalf("süzgeç: %+v", mf)
	}
}
