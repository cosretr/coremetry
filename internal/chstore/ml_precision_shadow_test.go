package chstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMLPrecisionShadowDefaultsAndNormalize(t *testing.T) {
	d := DefaultMLPrecisionShadow()
	if d.Mode != MLPrecisionModeShadow || d.MinLabels != 20 || d.DemoteUpper != 0.50 || d.RestoreLower != 0.60 ||
		d.WindowDays != 30 || d.MaxPerDay != 1 || d.MaxShare != 0.25 {
		t.Fatalf("belgenin Kararlar tablosu: %+v", d)
	}
	n := NormalizeMLPrecisionShadow(MLPrecisionShadow{Mode: "yes", MinLabels: 1, DemoteUpper: 2, RestoreLower: 0.3, WindowDays: 400,
		MaxPerDay: 0, MaxShare: -1, Pins: map[string]string{"op_latency": "on", "error_rate": "shadow", "nope": "on", "log_volume": "maybe"}})
	if n.Mode != MLPrecisionModeShadow || n.MinLabels != 20 || n.DemoteUpper != 0.5 || n.RestoreLower != 0.6 || n.WindowDays != 30 ||
		n.MaxPerDay != 1 || n.MaxShare != 0.25 || len(n.Pins) != 2 || n.Pins["op_latency"] != "on" || n.Pins["error_rate"] != "shadow" {
		t.Fatalf("normalize: %+v", n)
	}
	// boş blob → shadow; kayıtlı on aynen; ConfigHash kipten bağımsız
	c := NormalizeMLConfig(MLConfig{})
	if c.PrecisionShadow == nil || c.PrecisionShadowMode() != MLPrecisionModeShadow {
		t.Fatalf("boş blob: %+v", c.PrecisionShadow)
	}
	on := c
	on.PrecisionShadow = &MLPrecisionShadow{Mode: MLPrecisionModeOn}
	if NormalizeMLConfig(on).PrecisionShadowMode() != MLPrecisionModeOn {
		t.Error("kayıtlı on korunmalı")
	}
	if on.ConfigHash() != c.ConfigHash() || DefaultMLConfig().ConfigHash() != NormalizeMLConfig(DefaultMLConfig()).ConfigHash() {
		t.Error("gölge kuralı ConfigHash'e girmez")
	}
	raw, _ := json.Marshal(NormalizeMLConfig(MLConfig{}))
	if !strings.Contains(string(raw), `"precisionShadow":{"mode":"shadow","minLabels":20,"demoteUpper":0.5,"restoreLower":0.6,"windowDays":30,"maxPerDay":1,"maxShare":0.25,"pins":{}}`) {
		t.Errorf("ayar şekli: %s", raw)
	}
}

func TestMLPrecisionRuleScope(t *testing.T) {
	for det, want := range map[string]bool{"op_latency": true, "error_rate": true, "svc_breadth": true, "template_spike": true,
		"svc_error_rate": false, "svc_response_time": false, "endpoint_impact": false, "pod_oom": false, "metric_kafka": false,
		"throughput_drop": false, "new_template": false, "svc_volume_spike": false, "svc-slowdown": false, "": false} {
		if got, _ := MLPrecisionRuleScope(det); got != want {
			t.Errorf("%s: %v, want %v", det, got, want)
		}
	}
}

func TestPruneMLPrecisionRecords(t *testing.T) {
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)
	recs := []MLPrecisionRecord{{Detector: "a", At: now.Add(-31 * 24 * time.Hour).Unix()}, {Detector: "b", At: now.Add(-time.Hour).Unix()}, {Detector: "c", At: now.Unix()}}
	got := PruneMLPrecisionRecords(recs, now)
	if len(got) != 2 || got[0].Detector != "c" || got[1].Detector != "b" {
		t.Errorf("30 gün + en yeni önde: %+v", got)
	}
}

// Kuralın iki CH okuması: zaman sınırlı, LIMIT, süre tavanı; oy okumaz.
func TestMLPrecisionShadowSQLBounded(t *testing.T) {
	for name, sql := range map[string]string{"links": mlRecentProblemLinksSQL(), "fed": mlProblemsFedSQL()} {
		for _, want := range []string{"FROM ml_events FINAL", ">= ?", "LIMIT ", "max_execution_time", mlEventProblemIDExpr} {
			if !strings.Contains(sql, want) {
				t.Errorf("%s: %q içermeli", name, want)
			}
		}
		if strings.Contains(sql, "anomaly_verdicts") || strings.Contains(sql, "FROM spans") {
			t.Errorf("%s: oy / ham span okunmaz", name)
		}
	}
	if !strings.Contains(mlProblemsFedSQL(), problemCaseLabelSQL) || !strings.Contains(mlProblemsFedSQL(), "c.case_cat != 'noise'") {
		t.Error("vaka ifadesi Döküm / karne ile aynı")
	}
}

func TestMLScorecardLabelDays(t *testing.T) {
	sql := mlScorecardSQL(true, true)
	for _, want := range []string{
		"if(e.verdict != '', toDate(e.created_at), toDate(l.case_at)) AS label_day",
		"uniqExactIfState(label_day, src IN ('olay', 'vaka') AND ok IN (0, 1)) AS days_u",
		"toUInt64(uniqExactIfMerge(days_u)) AS label_days",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q içermeli", want)
		}
	}
	if strings.Count(sql, "FROM ml_events") != 1 {
		t.Error("tek sorgu kalmalı")
	}
	if r := FinishMLDetectorScore("op_latency", mlScoreCounts{N: 24, Correct: 6, LabelDays: 5}); r.LabelDays != 5 {
		t.Errorf("label days: %+v", r)
	}
}

// Elenenler: gölge damgası yalnız DetShadow (kural on) iken okunur; aksi
// halde SQL v0.10.2485 ile aynı.
func TestMLEventsWhereDetShadow(t *testing.T) {
	for _, rc := range []bool{false, true} {
		base, _ := mlEventsWhere(MLEventsFilter{View: MLViewHeld, recCols: rc})
		if strings.Contains(base, "detShadow") {
			t.Errorf("recCols=%v: kapalıyken damga okunmaz: %s", rc, base)
		}
		on, _ := mlEventsWhere(MLEventsFilter{View: MLViewHeld, recCols: rc, DetShadow: true})
		if !strings.Contains(on, `position(evidence, '"detShadow"') > 0`) {
			t.Errorf("recCols=%v: on kipinde damga okunur: %s", rc, on)
		}
		svc, _ := mlEventsWhere(MLEventsFilter{View: MLViewService, recCols: rc, DetShadow: true})
		if strings.Contains(svc, "detShadow") {
			t.Errorf("servis görünümü değişmez: %s", svc)
		}
	}
}
