package api

// problem_events_test.go — v0.10.2163 (Davis tarzı tek Problem D4): olay
// çizelgesinin SAF kurucusu + cache anahtarı. Sözleşme: üyesiz Problem'de
// çizelge BOŞ (sahibin kendi satırı tek başına gösterilmez); eski aile
// sahibinin kendi sinyali "own" satırıdır, ML sahibinde ayrı satır yoktur;
// sıra zaman, eşitlikte sahip önce; üye olayı kuralını, ML olayı kimliğini
// taşır; sahibin ölçüsü üyeden geldiyse kendi satırı değer taşımaz.

import (
	"fmt"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBuildProblemEventRows(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)
	owner := chstore.Problem{ID: "svc-slowdown:orders-api", RuleID: "svc-slowdown:orders-api", RuleName: "Yaygın yavaşlama", Severity: "critical", Service: "orders-api",
		Metric: "p99_ms", Comparator: ">=", Value: 9000, Threshold: 300, Status: "open", StartedAt: now.Add(-20 * time.Minute).UnixNano()}
	mlEv := chstore.MLEvent{ID: "ml1", Detector: "svc_error_rate", Service: "orders-api", Stage: chstore.MLStageShadow, WindowSec: 300,
		StartedAt: now.Add(-15 * time.Minute).UnixNano(), LastSeen: now.Add(-time.Minute).UnixNano(), Observed: 0.034, Baseline: 0.01, Direction: "up",
		Evidence: `{"svcShadow":{"live":true,"problemId":"svc-slowdown:orders-api"}}`}
	p99row := chstore.Problem{ID: "x9", RuleID: "anomaly:orders-api:p99_ms", Service: "orders-api", Metric: "p99_ms", Comparator: ">", Value: 900, Threshold: 300,
		Status: "open", StartedAt: now.Add(-20 * time.Minute).UnixNano()}
	member := chstore.NewMemberEvent(chstore.MLDetMemberMetricAnomaly, p99row, owner, p99row.StartedAt, now.Add(-2*time.Minute).UnixNano(), true)

	if rows := buildProblemEventRows(owner, nil, now.UnixNano()); rows == nil || len(rows) != 0 {
		t.Fatalf("üyesiz Problem: boş (nil değil) çizelge, %+v", rows)
	}
	rows := buildProblemEventRows(owner, []chstore.MLEvent{mlEv, member}, now.UnixNano())
	if len(rows) != 3 {
		t.Fatalf("sahibin kendi satırı + 2 üye: %+v", rows)
	}
	own, mem, ml := rows[0], rows[1], rows[2]
	if !own.Own || own.Detector != chstore.MLDetMemberSvcSlowdown || own.RuleID != owner.RuleID || own.StartedAt != owner.StartedAt ||
		own.Observed != 9000 || own.Baseline != 300 || !own.Live || own.LastSeen != now.UnixNano() {
		t.Errorf("sahip satırı (eşit başlangıçta önce): %+v", own)
	}
	if mem.Own || mem.Detector != chstore.MLDetMemberMetricAnomaly || mem.RuleID != "anomaly:orders-api:p99_ms" || mem.EventID != "" || mem.Live || mem.Observed != 900 {
		t.Errorf("üye olayı satırı (ayrılmış üye live=false): %+v", mem)
	}
	if ml.Detector != "svc_error_rate" || ml.EventID != "ml1" || ml.RuleID != "" || !ml.Live || ml.Observed != 0.034 || ml.Baseline != 0.01 {
		t.Errorf("ML olayı satırı: %+v", ml)
	}
	if problemEventDetectors(rows) != 3 {
		t.Errorf("farklı dedektör sayısı: %d", problemEventDetectors(rows))
	}

	t.Run("ML sahip: ayrı sahip satırı yok", func(t *testing.T) {
		mlOwner := chstore.Problem{ID: "anomaly-auto:svc:orders-api", RuleID: "anomaly-auto:svc:orders-api", Service: "orders-api", Status: "open", StartedAt: owner.StartedAt}
		rows := buildProblemEventRows(mlOwner, []chstore.MLEvent{mlEv}, now.UnixNano())
		if len(rows) != 1 || rows[0].Own {
			t.Fatalf("ML sahip: %+v", rows)
		}
	})
	t.Run("sahibin ölçüsü üyeden geldiyse kendi satırı değer taşımaz; kapalı sahip bitişte biter", func(t *testing.T) {
		raised := chstore.Problem{ID: "a1", RuleID: "anomaly:orders-api:error_rate", Service: "orders-api", Metric: "p99_ms", Comparator: ">=", Value: 9000, Threshold: 300,
			Status: "resolved", StartedAt: owner.StartedAt}
		end := now.Add(-5 * time.Minute).UnixNano()
		raised.ResolvedAt = &end
		rows := buildProblemEventRows(raised, []chstore.MLEvent{mlEv}, now.UnixNano())
		if len(rows) != 2 || !rows[0].Own || rows[0].Observed != 0 || rows[0].Baseline != 0 || rows[0].Live || rows[0].LastSeen != end {
			t.Fatalf("yükseltilmiş sahip: %+v", rows)
		}
	})
}

func TestProblemEventsKey(t *testing.T) {
	if problemEventsKey("a") == problemEventsKey("b") || problemEventsKey("P-1a2b") != "problem:events:v3:P-1a2b" {
		t.Fatal("anahtar kimliği taşımalı")
	}
}

// v0.10.2380 — servis hata oranı olayının kaydı satıra iliştirilir (başarılı /
// başarısız istek grafiği); yalnız EN YENİ uygun olayda, yalnız çizilebilen
// kayıtta. Kaydı olmayan (eski) olayda satır bugünkü gibi.
func TestBuildProblemEventRowsRequestsRecord(t *testing.T) {
	now := time.Date(2026, 11, 19, 21, 16, 0, 0, time.UTC)
	owner := chstore.Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", Service: "orders", Status: "open", StartedAt: now.Add(-40 * time.Minute).UnixNano()}
	record := func(t0 int64, stepSec int) string {
		return fmt.Sprintf(`{"v":1,"subject":{"service":"orders","resolved":true},"series":{"source":"vm","query":"q","aggregation":"err_ratio","unit":"percent","stepSec":%d},`+
			`"baseline":{"expected":0.4,"lo":0,"hi":1.4,"method":"m","stepSec":%d},"direction":"up","onsetAt":%d,"detectedAt":%d,"peakValue":6,"valueAtDetection":6,"expectedValue":0.4,`+
			`"volume":300,"evidence":[{"kind":"series","source":"vm","summary":"s","independent":false}],"class":"service_anomaly",`+
			`"requests":{"minute":%d,"failedPerMin":6,"okPerMin":94},`+
			`"snapshot":{"t0":%d,"step":60,"aggregation":"err_ratio","v":[0.4,6,6],"lo":[0,0,0],"hi":[1.4,1.4,1.4],"n":[100,100,100]},"title":"t"}`,
			stepSec, stepSec, (t0+60)*1e9, (t0+120)*1e9, t0+120, t0)
	}
	ev := func(id string, startMin int, rec string) chstore.MLEvent {
		return chstore.MLEvent{ID: id, Detector: "svc_error_rate", Service: "orders", Stage: chstore.MLStageShadow, WindowSec: 300,
			StartedAt: now.Add(-time.Duration(startMin) * time.Minute).UnixNano(), LastSeen: now.Add(-time.Duration(startMin-5) * time.Minute).UnixNano(),
			Observed: 0.06, Baseline: 0.004, Direction: "up", Record: rec, PublishState: "published"}
	}
	t0 := now.Add(-30 * time.Minute).Unix()
	rows := buildProblemEventRows(owner, []chstore.MLEvent{ev("old", 30, record(t0, 60)), ev("new", 10, record(t0+1200, 60)), ev("norec", 20, "")}, now.UnixNano())
	if len(rows) != 3 {
		t.Fatalf("üç olay satırı: %+v", rows)
	}
	for _, r := range rows {
		switch r.EventID {
		case "new":
			if r.Record == nil || r.Record.Detector != "svc_error_rate" || r.Record.Requests == nil || r.Record.Requests.FailedPerMin != 6 ||
				len(r.Record.Snapshot.N) != 3 || len(r.Record.Evidence) != 0 {
				t.Errorf("en yeni olay kaydı taşımalı (metin alanları atılmış): %+v", r.Record)
			}
		default:
			if r.Record != nil {
				t.Errorf("%s satırı kayıt taşımamalı (gövde tek kayıtla sınırlı)", r.EventID)
			}
		}
	}
	// 5 dakikalık (eski kural) seri: oran × hacim bir dakikanın hata sayısı değil → kayıt yok
	rows = buildProblemEventRows(owner, []chstore.MLEvent{ev("old5m", 10, record(t0, 300))}, now.UnixNano())
	if len(rows) != 1 || rows[0].Record != nil {
		t.Fatalf("eski 5 dk'lık kayıt iliştirilmemeli: %+v", rows)
	}
}
