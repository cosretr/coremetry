package cosrelearn

// CoSRE öğrenme Faz 3 — vaka kurucu (saf çekirdek + sahte depoyla bir tik).

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var c0 = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

func TestProblemCaseEligible(t *testing.T) {
	short := chstore.ClosedProblemForCase{Severity: "warning", StartedAt: c0, ResolvedAt: c0.Add(2 * time.Minute)}
	long := chstore.ClosedProblemForCase{Severity: "warning", StartedAt: c0, ResolvedAt: c0.Add(6 * time.Minute)}
	crit := chstore.ClosedProblemForCase{Severity: "critical", StartedAt: c0, ResolvedAt: c0.Add(time.Minute)}
	if ProblemCaseEligible(short) || !ProblemCaseEligible(long) || !ProblemCaseEligible(crit) {
		t.Fatal("uygunluk: kısa uyarı hayır, ≥5 dk evet, kritik evet")
	}
}

func TestComputeREDDelta(t *testing.T) {
	ns := func(d time.Duration) int64 { return c0.Add(d).UnixNano() }
	series := []chstore.SpanMetricSeries{
		{GroupKey: []string{"rate"}, Points: []chstore.SpanMetricPoint{{Time: ns(-10 * time.Minute), Value: 10}, {Time: ns(0), Value: 4}, {Time: ns(5 * time.Minute), Value: 6}}},
		{GroupKey: []string{"error_rate"}, Points: []chstore.SpanMetricPoint{{Time: ns(-5 * time.Minute), Value: 0.01}, {Time: ns(time.Minute), Value: 0.2}}},
		{GroupKey: []string{"p99"}, Points: []chstore.SpanMetricPoint{{Time: ns(time.Minute), Value: 900}}},
	}
	d := ComputeREDDelta(series, c0)
	if d.RateBefore != 10 || d.RateDuring != 5 || d.ErrBefore != 0.01 || d.ErrDuring != 0.2 || d.P99Before != 0 || d.P99During != 900 {
		t.Fatalf("%+v", d)
	}
}

func TestPickDeploy(t *testing.T) {
	es := []chstore.RecentDeployEntry{
		{Service: "a", Version: "1.0", FirstSeenNs: c0.Add(-2 * time.Hour).UnixNano()}, // pencere dışı
		{Service: "a", Version: "1.1", FirstSeenNs: c0.Add(-30 * time.Minute).UnixNano()},
		{Service: "a", Version: "1.2", FirstSeenNs: c0.Add(-10 * time.Minute).UnixNano()},
		{Service: "b", Version: "9", FirstSeenNs: c0.Add(-5 * time.Minute).UnixNano()},
	}
	ok, v := PickDeploy(es, "a", c0.Add(-time.Hour), c0)
	if !ok || v != "1.2" {
		t.Fatalf("%v %q", ok, v)
	}
	if ok, _ := PickDeploy(es, "c", c0.Add(-time.Hour), c0); ok {
		t.Fatal("başka servisin deploy'u sayıldı")
	}
}

func TestSuggestedCausePriority(t *testing.T) {
	v := &chstore.RCAVerdictRecord{Verdict: "root_cause_identified", RCEntity: "db-orders", RCFailMode: "connection pool exhausted"}
	h := &chstore.RootCauseHypothesis{TopSuspect: "payments", Confidence: 0.62}
	if txt, src := SuggestedCause(v, h, "özet"); src != "rca_verdict" || txt != "db-orders: connection pool exhausted" {
		t.Errorf("hüküm önce: %q %q", txt, src)
	}
	nv := &chstore.RCAVerdictRecord{Verdict: "insufficient_evidence", RCEntity: "x"}
	if txt, src := SuggestedCause(nv, h, "özet"); src != "hypothesis" || txt != "Olası kaynak: payments (güven %62)" {
		t.Errorf("kök neden bulunamadıysa hipotez: %q %q", txt, src)
	}
	if txt, src := SuggestedCause(nil, nil, " özet "); src != "ai_summary" || txt != "özet" {
		t.Errorf("son çare AI özeti: %q %q", txt, src)
	}
	if txt, src := SuggestedCause(nil, nil, ""); txt != "" || src != "" {
		t.Errorf("öneri yoksa boş: %q %q", txt, src)
	}
}

func TestDraftFromException(t *testing.T) {
	e := chstore.ResolvedExceptionForCase{Fingerprint: "ora:abc", Type: "MCA-00029", Message: "KSK_MAIN_PIN_CHANGE", Service: "acm-kiosk-core-prod",
		FirstSeen: c0.Add(-72 * time.Hour), LastSeen: c0.Add(-time.Hour), ResolvedAt: c0, AISummary: "PIN değişimi reddi"}
	d := DraftFromException(e, c0)
	if d.CaseID != "ex:ora:abc" || d.Status != chstore.CaseDraft || d.CosreCorrect != -1 {
		t.Fatalf("kimlik/durum: %+v", d)
	}
	if len(d.ErrorCodes) != 1 || d.ErrorCodes[0] != "MCA-00029" || d.Title != "MCA-00029: KSK_MAIN_PIN_CHANGE" {
		t.Errorf("kod/başlık: %+v", d)
	}
	if !d.OpenedAt.Equal(c0.Add(-2 * time.Hour)) {
		t.Errorf("uzun ömürlü grupta pencere son görülmeye daralmalı: %v", d.OpenedAt)
	}
	if d.SuggestedSource != "ai_summary" || d.FinalCause != "" {
		t.Errorf("nihai neden insandan gelir: %+v", d)
	}
}

type fakeCaseStore struct {
	probs    []chstore.ClosedProblemForCase
	exs      []chstore.ResolvedExceptionForCase
	have     map[string]bool
	upserted []chstore.IncidentCase
}

func (f *fakeCaseStore) ListClosedProblemsForCases(context.Context, time.Time, int) ([]chstore.ClosedProblemForCase, error) {
	return f.probs, nil
}
func (f *fakeCaseStore) ListResolvedExceptionsForCases(context.Context, time.Time, int) ([]chstore.ResolvedExceptionForCase, error) {
	return f.exs, nil
}
func (f *fakeCaseStore) ExistingCaseIDs(context.Context, []string) (map[string]bool, error) {
	return f.have, nil
}
func (f *fakeCaseStore) UpsertIncidentCases(_ context.Context, c []chstore.IncidentCase) error {
	f.upserted = c
	return nil
}
func (f *fakeCaseStore) TopExceptionCodesForService(context.Context, string, time.Time, time.Time, int) ([]string, error) {
	return []string{"MCA-00029", "ORA-01017"}, nil
}
func (f *fakeCaseStore) TopRollupErrorCodes(_ context.Context, _, dim string, _, _ time.Time, _ int) ([]string, error) {
	if dim == "function_code" {
		return []string{"FN01"}, nil
	}
	return []string{"CH-ATM"}, nil
}
func (f *fakeCaseStore) ServiceREDSeries(context.Context, string, time.Time, time.Time) []chstore.SpanMetricSeries {
	return nil
}
func (f *fakeCaseStore) GetDeploysInWindow(context.Context, time.Time, time.Time, int) ([]chstore.RecentDeployEntry, error) {
	return []chstore.RecentDeployEntry{{Service: "svc", Version: "2.3", FirstSeenNs: c0.Add(-20 * time.Minute).UnixNano()}}, nil
}
func (f *fakeCaseStore) GetHypothesis(context.Context, string, string) (*chstore.RootCauseHypothesis, error) {
	return &chstore.RootCauseHypothesis{TopSuspect: "db", Confidence: 0.5}, nil
}
func (f *fakeCaseStore) LatestRCAVerdictForAnchor(context.Context, string, string) (*chstore.RCAVerdictRecord, error) {
	return nil, nil
}

func TestBuildCasesSkipsExistingAndShortAndFillsSignals(t *testing.T) {
	st := &fakeCaseStore{
		probs: []chstore.ClosedProblemForCase{
			{ID: "p1", Service: "svc", Severity: "critical", StartedAt: c0, ResolvedAt: c0.Add(10 * time.Minute)},
			{ID: "p2", Service: "svc", Severity: "warning", StartedAt: c0, ResolvedAt: c0.Add(time.Minute)}, // kısa
			{ID: "p3", Service: "svc", Severity: "critical", StartedAt: c0, ResolvedAt: c0.Add(10 * time.Minute)},
		},
		exs:  []chstore.ResolvedExceptionForCase{{Fingerprint: "f1", Type: "MCA-00029", Service: "svc", FirstSeen: c0, ResolvedAt: c0.Add(time.Hour)}},
		have: map[string]bool{"p:p3": true},
	}
	n, err := BuildCases(context.Background(), st, c0.Add(2*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v %+v", n, err, st.upserted)
	}
	p := st.upserted[0]
	if p.CaseID != "p:p1" || p.SuggestedSource != "hypothesis" || !p.DeployInWindow || p.DeployVersion != "2.3" {
		t.Errorf("problem vakası: %+v", p)
	}
	if len(p.ErrorCodes) != 2 || p.FunctionCodes[0] != "FN01" || p.ChannelCodes[0] != "CH-ATM" {
		t.Errorf("sinyaller: %+v", p)
	}
	x := st.upserted[1]
	if x.CaseID != "ex:f1" || len(x.ErrorCodes) != 2 || x.ErrorCodes[0] != "MCA-00029" {
		t.Errorf("exception vakası kodları tekil birleşmeli: %+v", x.ErrorCodes)
	}
}

// v0.10.1531 (Problem standardı R3, D4) — "Gerçek problem" işaretli imzanın
// kısa ömürlü problemi de taslak alır; işaretsiz kısa problem almaz.
func TestProblemCaseEligibleMarked(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	short := chstore.ClosedProblemForCase{ID: "p1", RuleID: "anomaly:svc:error_rate", Service: "svc", Severity: "warning",
		StartedAt: t0, ResolvedAt: t0.Add(2 * time.Minute)}
	if ProblemCaseEligibleMarked(short, nil) {
		t.Fatal("işaretsiz 2 dk'lık problem taslak almamalı")
	}
	real := map[string]bool{"p:anomaly:svc:error_rate|svc": true}
	if !ProblemCaseEligibleMarked(short, real) {
		t.Fatal("Gerçek problem işaretli imza taslak almalı")
	}
	other := short
	other.Service = "other"
	if ProblemCaseEligibleMarked(other, real) {
		t.Fatal("işaret imzaya bağlı: başka servis taslak almaz")
	}
}
