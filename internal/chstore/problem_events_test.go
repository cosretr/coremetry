package chstore

// problem_events_test.go — v0.10.2163 (Davis tarzı tek Problem D4): olay
// sayımı ve çizelge okuyucularının SAF sözleşmesi.
//   - sayım yalnız açık, SAHİP olabilen satırlar için okunur (aday yoksa okuma yok);
//   - olay sayısı = bağlı ml_events satırları + eski aile sahibinin kendi
//     sinyali; üyesiz Problem'de sayı yok (0);
//   - aynı kimlikle yeniden açılan satırın eski bölümü sayılmaz;
//   - SQL ifadesi Go okuyucusuyla (MLEventProblemID) aynı üç bağı okur ve
//     zaman sınırı + LIMIT + max_execution_time taşır.
// Servis adları SENTETİK.

import (
	"strings"
	"testing"
	"time"
)

func TestCountProblemEvents(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC).UnixNano()
	min := int64(time.Minute)
	slow := Problem{ID: "svc-slowdown:orders-api", RuleID: "svc-slowdown:orders-api", Service: "orders-api", Status: "open", StartedAt: t0}
	ml := Problem{ID: "anomaly-auto:svc:payments-api", RuleID: "anomaly-auto:svc:payments-api", Service: "payments-api", Status: "open", StartedAt: t0}
	lone := Problem{ID: "svc-slowdown:search-api", RuleID: "svc-slowdown:search-api", Service: "search-api", Status: "open", StartedAt: t0}
	ref := func(svc, det, pid string, start, last int64) problemEventRef {
		return problemEventRef{Service: svc, Detector: det, ProblemID: pid, StartedAt: t0 + start*min, LastSeen: t0 + last*min}
	}
	refs := []problemEventRef{
		ref("orders-api", "svc_error_rate", slow.ID, 5, 20),
		ref("orders-api", "svc_breadth", slow.ID, 6, 20),
		ref("orders-api", MLDetMemberMetricAnomaly, slow.ID, 7, 20),
		ref("orders-api", "svc_error_rate", slow.ID, -600, -500), // aynı kimliğin ÖNCEKİ bölümü
		ref("payments-api", "svc_error_rate", ml.ID, 0, 20),
		ref("payments-api", MLDetMemberSvcSlowdown, ml.ID, 3, 20),
		ref("payments-api", "log_volume", "OTHER", 3, 20),
		ref("orders-api", "svc_error_rate", ml.ID, 3, 20), // kimlik uyuyor, servis uymuyor
	}
	got := countProblemEvents([]Problem{slow, ml, lone}, refs)
	if s := got[slow.ID]; s.Events != 4 || s.Detectors != 4 {
		t.Errorf("eski aile sahibi: 3 üye + kendi sinyali = 4 olay / 4 dedektör, %+v", s)
	}
	if s := got[ml.ID]; s.Events != 2 || s.Detectors != 2 {
		t.Errorf("ML sahip: kendi ML olayı + 1 üye = 2 olay (ayrıca +1 yok), %+v", s)
	}
	if _, ok := got[lone.ID]; ok {
		t.Error("üyesi olmayan Problem sayım almaz (rozet yok)")
	}
}

func TestEventCountCandidates(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	started := now.Add(-time.Hour).UnixNano()
	resolvedAt := now.UnixNano()
	probs := []Problem{
		{ID: "a", RuleID: "svc-slowdown:orders-api", Service: "orders-api", Status: "open", StartedAt: started},
		{ID: "b", RuleID: "anomaly-auto:svc:payments-api", Service: "payments-api", Status: "acknowledged", StartedAt: now.Add(-3 * time.Hour).UnixNano()},
		{ID: "c", RuleID: "anomaly:orders-api:p99_ms", Service: "orders-api", Status: "open", StartedAt: started},                 // yalnız-üye: üyesi olmaz
		{ID: "d", RuleID: "rule-7f3a", Service: "orders-api", Status: "open", StartedAt: started},                                 // alarm
		{ID: "e", RuleID: "svc-slowdown:search-api", Service: "search-api", Status: "resolved", ResolvedAt: &resolvedAt},          // kapalı
		{ID: "f", RuleID: "anomaly:ext:orasrc:ext:error_count", Service: "ext:orasrc", Kind: ProblemKindExternal, Status: "open"}, // dış kaynak
	}
	cands, services, since, ok := eventCountCandidates(probs, now)
	if !ok || len(cands) != 2 || cands[0].ID != "a" || cands[1].ID != "b" {
		t.Fatalf("adaylar yalnız açık sahip satırlar: %v %+v", ok, cands)
	}
	if strings.Join(services, ",") != "orders-api,payments-api" {
		t.Errorf("servisler: %v", services)
	}
	if want := now.Add(-3 * time.Hour).Add(-ProblemEventSlack); !since.Equal(want) {
		t.Errorf("pencere en eski adayın başlangıcı − pay: %v, istenen %v", since, want)
	}
	if _, _, _, ok := eventCountCandidates(probs[2:], now); ok {
		t.Error("aday yoksa okuma yapılmaz")
	}
	old := []Problem{{ID: "z", RuleID: "svc-slowdown:orders-api", Service: "orders-api", Status: "open", StartedAt: now.Add(-90 * 24 * time.Hour).UnixNano()}}
	if _, _, since, _ := eventCountCandidates(old, now); !since.Equal(now.Add(-eventCountLookback)) {
		t.Errorf("pencere en çok %v (sıcak uç; tam geçmiş çizelgede): %v", eventCountLookback, since)
	}
}

func TestProblemEventRefsSQL(t *testing.T) {
	for _, frag := range []string{
		"FROM ml_events FINAL",
		"last_seen >= ?", "service IN ?", "problem_ref != ''", "position(evidence, 'problem_id') > 0", "position(evidence, 'problemId') > 0",
		"JSONExtractString(evidence, 'problem_id')",
		"JSONExtractBool(evidence, 'svcShadow', 'live')", "JSONExtractString(evidence, 'svcShadow', 'problemId')",
		"JSONExtractBool(evidence, 'groupShadow', 'live')", "JSONExtractString(evidence, 'groupShadow', 'problemId')",
		"LIMIT ?", "SETTINGS max_execution_time = 5",
	} {
		if !strings.Contains(problemEventRefsSQL, frag) {
			t.Errorf("sayım SQL'inde yok: %q", frag)
		}
	}
	if strings.Contains(problemEventRefsSQL, "coremetry.") || strings.Contains(strings.ToUpper(problemEventRefsSQL), "JOIN") || strings.Count(strings.ToUpper(problemEventRefsSQL), "SELECT") != 1 {
		t.Error("sayım SQL'i niteliksiz tablo, alt sorgusuz ve JOIN'siz olmalı (dağıtık kurulum)")
	}
	// Go okuyucusu aynı üç bağı aynı öncelikle okur (üye → servis düzeyi → grup).
	both := MLEvent{Evidence: `{"problem_id":"M","svcShadow":{"live":true,"problemId":"S"},"groupShadow":{"live":true,"problemId":"G"}}`}
	svcGrp := MLEvent{Evidence: `{"svcShadow":{"live":true,"problemId":"S"},"groupShadow":{"live":true,"problemId":"G"}}`}
	if MLEventProblemID(both) != "M" || MLEventProblemID(svcGrp) != "S" {
		t.Error("bağ önceliği: üye olayı, sonra servis düzeyi damga, sonra grup damgası")
	}
	i, j, k := strings.Index(mlEventProblemIDExpr, "'problem_id'"), strings.Index(mlEventProblemIDExpr, "'svcShadow'"), strings.Index(mlEventProblemIDExpr, "'groupShadow'")
	if i < 0 || j < i || k < j {
		t.Error("SQL ifadesi aynı önceliği izlemeli")
	}
}

func TestProblemOwnMeasure(t *testing.T) {
	cases := []struct {
		name string
		p    Problem
		det  string
		own  bool
	}{
		{"yaygın yavaşlama, kendi ölçüsü", Problem{RuleID: "svc-slowdown:orders-api", Metric: "p99_ms", Comparator: ">="}, MLDetMemberSvcSlowdown, true},
		{"hata oranı anomalisi, kendi ölçüsü", Problem{RuleID: "anomaly:orders-api:error_rate", Metric: "error_rate", Comparator: ">"}, MLDetMemberMetricAnomaly, true},
		{"hata oranı sahibi, ML üyesi yükseltmiş (>=)", Problem{RuleID: "anomaly:orders-api:error_rate", Metric: "error_rate", Comparator: ">="}, MLDetMemberMetricAnomaly, false},
		{"hata oranı sahibi, yaygın yavaşlama yükseltmiş", Problem{RuleID: "anomaly:orders-api:error_rate", Metric: "p99_ms", Comparator: ">="}, MLDetMemberMetricAnomaly, false},
		{"ML sahip: ayrı kendi satırı yok", Problem{RuleID: "anomaly-auto:svc:orders-api", Metric: "error_rate", Comparator: ">="}, "", true},
	}
	for _, c := range cases {
		if got := ProblemOwnDetector(c.p); got != c.det {
			t.Errorf("%s: kendi dedektörü %q, istenen %q", c.name, got, c.det)
		}
		if got := ProblemMeasureIsOwn(c.p); got != c.own {
			t.Errorf("%s: ölçü kendi mi %v, istenen %v", c.name, got, c.own)
		}
	}
}

// Çizelge okuması yalnız üyesi OLABİLECEK satırda yapılır (kapanmış sahip
// dahil — olay sonrası da okunur); alarm kuralı / yalnız-üye / dış kaynak /
// servissiz satırda ml_events okunmaz.
func TestProblemCanHaveEvents(t *testing.T) {
	cases := []struct {
		p    Problem
		want bool
	}{
		{Problem{ID: "a", RuleID: "svc-slowdown:orders-api", Service: "orders-api", Status: "open"}, true},
		{Problem{ID: "b", RuleID: "anomaly-auto:group:ev1", Service: "orders-api", Status: "resolved"}, true},
		{Problem{ID: "c", RuleID: "anomaly:orders-api:error_rate", Service: "orders-api", Kind: ProblemKindService}, true},
		{Problem{ID: "d", RuleID: "anomaly:orders-api:p99_ms", Service: "orders-api"}, false},
		{Problem{ID: "e", RuleID: "rule-7f3a", Service: "orders-api"}, false},
		{Problem{ID: "f", RuleID: "exception-storm"}, false},
		{Problem{ID: "g", RuleID: "anomaly:ext:orasrc:ext:error_count", Service: "ext:orasrc", Kind: ProblemKindExternal}, false},
		{Problem{RuleID: "svc-slowdown:orders-api", Service: "orders-api"}, false},
	}
	for _, c := range cases {
		if got := problemCanHaveEvents(c.p); got != c.want {
			t.Errorf("%s (%s): %v, istenen %v", c.p.ID, c.p.RuleID, got, c.want)
		}
	}
}
