package sre

// replay notlaması: puan ölçeği, hakem çıktısı ayrıştırma,
// deterministik erişim, özet (ortalama / dağılım / erişim oranı / koşudan
// koşuya yayılım), başlatmanın canlı kaydı ASLA yazmaması, notlayıcının
// bütçe + yerel profil kapısı, paketin bildirim katmanını import etmemesi.

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestGradeScoreScale(t *testing.T) {
	want := map[string]int{GradeBullseye: 100, GradeOnTarget: 65, GradeMiss: 35, GradeNowhere: 0}
	for g, sc := range want {
		if got, ok := GradeScore(g); !ok || got != sc {
			t.Errorf("%s → %d %v", g, got, ok)
		}
	}
	if _, ok := GradeScore("great"); ok {
		t.Error("bilinmeyen not kabul edildi")
	}
}

func TestParseReplayGrade(t *testing.T) {
	g, why, reach, err := ParseReplayGrade("İşte not:\n```json\n{\"grade\":\"On Target\",\"why\":\"doğru servis, mekanizma eksik\",\"reach\":\"yes\"}\n```")
	if err != nil || g != GradeOnTarget || why == "" || reach == nil || !*reach {
		t.Fatalf("ayrıştırma: %q %q %v %v", g, why, reach, err)
	}
	if _, _, reach, err := ParseReplayGrade(`{"grade":"nowhere","why":"x","reach":"unknown"}`); err != nil || reach != nil {
		t.Errorf("unknown erişim nil olmalı: %v %v", reach, err)
	}
	for _, bad := range []string{"", "bullseye", `{"grade":"perfect"}`, `{"grade":`} {
		if _, _, _, err := ParseReplayGrade(bad); err == nil {
			t.Errorf("%q kabul edildi", bad)
		}
	}
}

func TestCulpritReached(t *testing.T) {
	ents := []string{"checkout", "db:orders", "ext:bank-gw", "payments-api"}
	for _, c := range []string{"orders", "db:orders", "Payments-API", "bank-gw", "payments"} {
		if !culpritReached(c, ents) {
			t.Errorf("%s bulunmalıydı", c)
		}
	}
	for _, c := range []string{"", "inventory", "pay"} {
		if culpritReached(c, ents) {
			t.Errorf("%s bulunmamalıydı", c)
		}
	}
}

func TestEvidenceEntitiesAndConclusion(t *testing.T) {
	inv := Investigation{Service: "checkout", Findings: []Finding{{Name: "pod-a", Level: "warn", Reasons: []string{"cpu %95"}}},
		Facts: &Facts{Deps: &DepsFacts{Edges: []EdgeFact{{Target: "db:orders", ErrorRate: 20}}},
			Deploys: &DeployFacts{Rows: []DeployFact{{Name: "checkout", Version: "2.3"}}}},
		Hypotheses: []Hypothesis{{Kind: HypDBSlow, Category: "dependency", Label: "Veritabanı yavaş", Score: 0.8, Band: "high"}}}
	ents := evidenceEntities(inv)
	if strings.Join(ents, ",") != "checkout,db:orders,pod-a" {
		t.Errorf("varlıklar: %v", ents)
	}
	c := replayConclusion(inv)
	if !strings.Contains(c, "1. Veritabanı yavaş [dependency]") || !strings.Contains(c, "özne: db:orders") || !strings.Contains(c, "Bulgu: pod-a") {
		t.Errorf("sonuç: %s", c)
	}
	if replayConclusion(Investigation{}) == "" {
		t.Error("boş sonuç dürüst cümle taşımalı")
	}
}

func intp(v int) *int    { return &v }
func boolp(v bool) *bool { return &v }

func gradedRun(rep int, g string, reach *bool) ReplayRun {
	sc, _ := GradeScore(g)
	return ReplayRun{Rep: rep, State: StateDone, Grade: g, Score: intp(sc), Reach: reach}
}

func TestSummarizeReplay(t *testing.T) {
	rep := ReplayReport{Items: []ReplayItem{
		{ProblemID: "a", Runs: []ReplayRun{gradedRun(1, GradeBullseye, boolp(true)), gradedRun(2, GradeBullseye, boolp(true))}},
		{ProblemID: "b", Runs: []ReplayRun{gradedRun(1, GradeOnTarget, boolp(true)), gradedRun(2, GradeNowhere, boolp(false))}},
		{ProblemID: "c", Runs: []ReplayRun{gradedRun(1, GradeMiss, nil), {Rep: 2, State: StateError}}},
		{ProblemID: "d", Runs: []ReplayRun{{Rep: 1, State: StateQueued}}},
		{ProblemID: "e", State: CaseSkipped},
	}}
	sm := SummarizeReplay(rep)
	if sm.Problems != 4 || sm.Skipped != 1 || sm.Runs != 7 || sm.Graded != 5 || sm.Errored != 1 || sm.Pending != 1 {
		t.Fatalf("sayaçlar: %+v", sm)
	}
	// (100+100+65+0+35)/5 = 60
	if sm.Score == nil || *sm.Score != 60 {
		t.Errorf("puan: %v", sm.Score)
	}
	if sm.Dist[GradeBullseye] != 2 || sm.Dist[GradeOnTarget] != 1 || sm.Dist[GradeMiss] != 1 || sm.Dist[GradeNowhere] != 1 {
		t.Errorf("dağılım: %v", sm.Dist)
	}
	if sm.ReachKnown != 4 || sm.Reached != 3 || sm.ReachRate == nil || *sm.ReachRate != 0.75 {
		t.Errorf("erişim: %+v", sm)
	}
	// Çok koşulu: a (yayılım 0), b (65) → ortalama 32.5, en çok 65, kararsız 1.
	if sm.MultiRun != 2 || sm.Unstable != 1 || sm.MaxSpread != 65 || sm.MeanSpread == nil || *sm.MeanSpread != 32.5 {
		t.Errorf("varyans: %+v", sm)
	}
	if e := SummarizeReplay(ReplayReport{}); e.Score != nil || e.ReachRate != nil || e.MeanSpread != nil {
		t.Errorf("boş rapor oranları null olmalı: %+v", e)
	}
	mean, spread, n, unstable := ItemStats(rep.Items[1])
	if mean == nil || *mean != 32.5 || spread != 65 || n != 2 || !unstable {
		t.Errorf("ItemStats: %v %d %d %v", mean, spread, n, unstable)
	}
}

func TestNormalizeReplayRequest(t *testing.T) {
	n := NormalizeReplayRequest(ReplayReportRequest{})
	if n.Days != 30 || n.Repeats != 2 || n.Limit != 20 || n.Trigger != "manual" {
		t.Fatalf("varsayılan: %+v", n)
	}
	n = NormalizeReplayRequest(ReplayReportRequest{Days: 45, Repeats: 9, Limit: 999, Trigger: "schedule"})
	if n.Days != 90 || n.Repeats != 3 || n.Limit != 30 || n.Trigger != "schedule" {
		t.Errorf("kırpma: %+v", n)
	}
	if NormalizeReplayRequest(ReplayReportRequest{Days: 1000}).Days != 180 {
		t.Error("gün tavanı")
	}
}

func TestScheduleDue(t *testing.T) {
	now := evalT0
	if scheduleDue(ReplaySchedule{}, nil, now) {
		t.Error("kapalı zamanlama koştu")
	}
	if !scheduleDue(ReplaySchedule{Weekly: true}, nil, now) {
		t.Error("hiç rapor yokken haftalık koşmalı")
	}
	recent := []ReplayReport{{CreatedAt: now.Add(-6 * 24 * time.Hour).UnixNano()}}
	if scheduleDue(ReplaySchedule{Weekly: true}, recent, now) {
		t.Error("6 gün önceki rapordan sonra koştu")
	}
	old := []ReplayReport{{CreatedAt: now.Add(-8 * 24 * time.Hour).UnixNano()}}
	if !scheduleDue(ReplaySchedule{Weekly: true}, old, now) {
		t.Error("8 gün sonra koşmalı")
	}
}

func TestReplayJudgeGate(t *testing.T) {
	cfg := NormalizeSettings(DefaultSettings()) // 30/saat → hakem payı 15
	n := &fakeNarrator{active: true, auto: true}
	if ok, why := replayJudgeGate(n, cfg, 0, true); !ok {
		t.Fatalf("açık kapı: %s", why)
	}
	if ok, _ := replayJudgeGate(n, cfg, 15, true); ok {
		t.Error("saatlik bütçenin yarısı aşıldı")
	}
	if ok, why := replayJudgeGate(n, cfg, 0, false); ok || !strings.Contains(why, "yerel") {
		t.Errorf("yerel olmayan profil: %v %s", ok, why)
	}
	if ok, _ := replayJudgeGate(&fakeNarrator{active: true}, cfg, 0, true); ok {
		t.Error("otomatik AI kapalıyken hakem çağrıldı")
	}
}

// ── sahte rapor deposu ──────────────────────────────────────────────────────

type replayFakeStore struct {
	*evalFakeStore
	rmu     sync.Mutex
	reports map[string]chstore.SavedView
	extras  map[string]chstore.ProblemRootCauseExtra
}

func newReplayFakeStore() *replayFakeStore {
	return &replayFakeStore{evalFakeStore: newEvalFakeStore(), reports: map[string]chstore.SavedView{}, extras: map[string]chstore.ProblemRootCauseExtra{}}
}

func (f *replayFakeStore) ListProblemRootCauseExtras(_ context.Context, ids []string) (map[string]chstore.ProblemRootCauseExtra, error) {
	out := map[string]chstore.ProblemRootCauseExtra{}
	for _, id := range ids {
		if e, ok := f.extras[id]; ok {
			out[id] = e
		}
	}
	return out, nil
}
func (f *replayFakeStore) ListSREReplayReports(_ context.Context, _ int) ([]chstore.SavedView, error) {
	f.rmu.Lock()
	defer f.rmu.Unlock()
	out := []chstore.SavedView{}
	for _, v := range f.reports {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}
func (f *replayFakeStore) GetSREReplayReport(_ context.Context, id string) (*chstore.SavedView, error) {
	f.rmu.Lock()
	defer f.rmu.Unlock()
	v, ok := f.reports[chstore.SREReplayReportID(id)]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (f *replayFakeStore) PutSREReplayReport(_ context.Context, id, state, body string, createdAt int64) error {
	f.rmu.Lock()
	defer f.rmu.Unlock()
	f.reports[chstore.SREReplayReportID(id)] = chstore.SavedView{ID: chstore.SREReplayReportID(id), Page: chstore.SREReplayReportPage,
		Name: state, QueryString: body, CreatedAt: createdAt}
	return nil
}

func seedReplay(t *testing.T) *replayFakeStore {
	t.Helper()
	st := newReplayFakeStore()
	st.cases = []chstore.IncidentCase{
		confirmedCase("p1", "checkout", "dependency", "orders-db bağlantı havuzu doldu", evalT0.Add(-48*time.Hour)),
		confirmedCase("p2", "billing", "noise", "gürültü", evalT0.Add(-24*time.Hour)),
	}
	st.problems["p1"] = chstore.Problem{ID: "p1", Service: "checkout", StartedAt: evalT0.Add(-48 * time.Hour).UnixNano()}
	st.extras["p1"] = chstore.ProblemRootCauseExtra{ProblemID: "p1", Culprit: "db:orders"}
	return st
}

func TestStartReplayReportNeverWritesLiveRecord(t *testing.T) {
	st := seedReplay(t)
	cfg := NormalizeSettings(DefaultSettings())
	rep, err := StartReplayReport(context.Background(), st, ReplayReportRequest{Repeats: 2, Now: evalT0, By: "admin@x"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found != 2 || len(rep.Items) != 2 || rep.Items[1].State != CaseSkipped || len(rep.Items[0].Runs) != 2 || rep.Items[0].Culprit != "db:orders" {
		t.Fatalf("rapor: %+v", rep)
	}
	if st.caseQ.Status != chstore.CaseConfirmed || st.caseQ.Source != "problem" || !st.caseQ.From.Equal(evalT0.Add(-30*24*time.Hour)) {
		t.Errorf("etiket okuması: %+v", st.caseQ)
	}
	if _, ok := st.rows[chstore.SREInvestigationID("p1")]; ok {
		t.Fatal("canlı sre-p1 kaydı yazıldı")
	}
	for id := range st.rows {
		if !strings.HasPrefix(id, "sre-replay-"+rep.ReportID+"n") {
			t.Errorf("rapor dışı kuyruk satırı: %s", id)
		}
	}
	if len(st.rows) != 2 {
		t.Errorf("Problem × koşu = 2 tekrar işi olmalı: %d", len(st.rows))
	}
	for r := 1; r <= 2; r++ {
		inv := st.get(t, chstore.SREReplayKey(ReplayRunKey(rep.ReportID, r), "p1"))
		if inv.Kind != KindReplay || inv.AsOf != evalT0.Add(-48*time.Hour).Add(replayAsOfDelay).UnixNano() {
			t.Errorf("tekrar işi problemin penceresine çıpalı değil: %+v", inv)
		}
	}
	if _, err := StartReplayReport(context.Background(), st, ReplayReportRequest{Now: evalT0.Add(time.Minute)}, cfg); !errors.Is(err, ErrReplayBusy) {
		t.Errorf("sürüyorken ikinci rapor: %v", err)
	}
	off := false
	cfg.Enabled = &off
	if _, err := StartReplayReport(context.Background(), newReplayFakeStore(), ReplayReportRequest{Now: evalT0}, cfg); !errors.Is(err, ErrDisabled) {
		t.Errorf("kapalı ajan: %v", err)
	}
}

func TestStartReplayVariantSetsPack(t *testing.T) {
	st := seedReplay(t)
	rep, err := StartReplayReport(context.Background(), st, ReplayReportRequest{Repeats: 1, Now: evalT0, Variant: ReplayVariant{ContextPack: true}},
		NormalizeSettings(DefaultSettings()))
	if err != nil {
		t.Fatal(err)
	}
	if inv := st.get(t, chstore.SREReplayKey(ReplayRunKey(rep.ReportID, 1), "p1")); !inv.Pack {
		t.Error("varyant contextPack tekrar işine geçmedi")
	}
}

// finishReplayRow — drain'in yerine: tekrar satırını bitmiş yazar.
func finishReplayRow(t *testing.T, st *replayFakeStore, runKey, pid string, deps []EdgeFact) {
	t.Helper()
	key := chstore.SREReplayKey(runKey, pid)
	inv := st.get(t, key)
	inv.State, inv.FinishedAt = StateDone, evalT0.UnixNano()
	inv.Facts = &Facts{Deps: &DepsFacts{Edges: deps}}
	inv.Hypotheses = []Hypothesis{{Kind: HypDBSlow, Category: "dependency", Score: 0.7, Band: "high"}}
	inv.Steps = []Step{{Name: stepDeps, State: StepOK}}
	if err := save(context.Background(), st, inv); err != nil {
		t.Fatal(err)
	}
}

func TestProcessReplayReportGradesAndFinalizes(t *testing.T) {
	st := seedReplay(t)
	rep, err := StartReplayReport(context.Background(), st, ReplayReportRequest{Repeats: 2, Now: evalT0}, NormalizeSettings(DefaultSettings()))
	if err != nil {
		t.Fatal(err)
	}
	finishReplayRow(t, st, ReplayRunKey(rep.ReportID, 1), "p1", []EdgeFact{{Target: "db:orders", ErrorRate: 30}})
	finishReplayRow(t, st, ReplayRunKey(rep.ReportID, 2), "p1", []EdgeFact{{Target: "ext:bank", ErrorRate: 30}})
	answers := []string{`{"grade":"bullseye","why":"aynı neden","reach":"no"}`, `{"grade":"miss","why":"dış sisteme baktı","reach":"yes"}`}
	var users []string
	judge := ReplayJudge{
		Gate: func(context.Context) (bool, string) { return true, "" },
		Call: func(_ context.Context, sys, user string) (string, error) {
			if !strings.Contains(sys, "bullseye") {
				t.Errorf("sistem promptu ölçeği taşımıyor")
			}
			users = append(users, user)
			out := answers[0]
			answers = answers[1:]
			return out, nil
		},
		Model: func() string { return "gemma-test" },
	}
	next, changed, left, err := ProcessReplayReport(context.Background(), st, rep, judge, 3, evalT0.Add(time.Hour))
	if err != nil || !changed || left != 1 {
		t.Fatalf("tik: changed=%v left=%d err=%v", changed, left, err)
	}
	if next.State != ReplayDone || next.Judge != "gemma-test" {
		t.Fatalf("rapor bitmedi: %+v", next)
	}
	runs := next.Items[0].Runs
	// Suçlu etiketli → erişim KANITTAN (hakemin "no"su ezmez): 1. koşu db:orders gördü, 2. görmedi.
	if runs[0].Grade != GradeBullseye || *runs[0].Score != 100 || runs[0].Reach == nil || !*runs[0].Reach || runs[0].ReachBy != "evidence" {
		t.Errorf("koşu 1: %+v", runs[0])
	}
	if runs[1].Grade != GradeMiss || runs[1].Reach == nil || *runs[1].Reach || runs[1].ReachBy != "evidence" {
		t.Errorf("koşu 2: %+v", runs[1])
	}
	if !strings.Contains(users[0], "orders-db bağlantı havuzu doldu") || !strings.Contains(users[0], "suçlu: db:orders") {
		t.Errorf("hakem girdisi etiketi taşımıyor: %s", users[0])
	}
	sm := SummarizeReplay(next)
	if sm.Score == nil || *sm.Score != 67.5 || sm.Unstable != 1 || sm.MaxSpread != 65 {
		t.Errorf("özet: %+v", sm)
	}
}

func TestProcessReplayReportGateClosedWaitsThenGivesUp(t *testing.T) {
	st := seedReplay(t)
	rep, _ := StartReplayReport(context.Background(), st, ReplayReportRequest{Repeats: 1, Now: evalT0}, NormalizeSettings(DefaultSettings()))
	finishReplayRow(t, st, ReplayRunKey(rep.ReportID, 1), "p1", nil)
	called := 0
	judge := ReplayJudge{Gate: func(context.Context) (bool, string) { return false, "hakem profili yerel (self-hosted) değil" },
		Call: func(context.Context, string, string) (string, error) { called++; return "", nil }}
	next, _, _, _ := ProcessReplayReport(context.Background(), st, rep, judge, 3, evalT0.Add(time.Minute))
	if called != 0 || next.State != ReplayRunning || next.Items[0].Runs[0].State != StateDone || next.Items[0].Runs[0].Grade != "" {
		t.Fatalf("kapı kapalıyken: called=%d %+v", called, next.Items[0].Runs[0])
	}
	next, _, _, _ = ProcessReplayReport(context.Background(), st, next, judge, 3, evalT0.Add(4*time.Hour))
	if called != 0 || next.State != ReplayDone || !strings.Contains(next.Items[0].Runs[0].GradeErr, "yerel") {
		t.Fatalf("bayat rapor kapanmadı: %+v", next.Items[0].Runs[0])
	}
}

func TestProcessReplayJudgeErrorRetriesOnce(t *testing.T) {
	st := seedReplay(t)
	rep, _ := StartReplayReport(context.Background(), st, ReplayReportRequest{Repeats: 1, Now: evalT0}, NormalizeSettings(DefaultSettings()))
	finishReplayRow(t, st, ReplayRunKey(rep.ReportID, 1), "p1", nil)
	judge := ReplayJudge{Call: func(context.Context, string, string) (string, error) { return "ne diyeyim", nil }}
	next, _, _, _ := ProcessReplayReport(context.Background(), st, rep, judge, 3, evalT0.Add(time.Minute))
	if r := next.Items[0].Runs[0]; r.Attempts != 1 || r.GradeErr != "" || next.State != ReplayRunning {
		t.Fatalf("ilk hata: %+v", r)
	}
	next, _, _, _ = ProcessReplayReport(context.Background(), st, next, judge, 3, evalT0.Add(2*time.Minute))
	if r := next.Items[0].Runs[0]; r.GradeErr == "" || next.State != ReplayDone {
		t.Fatalf("ikinci hata: %+v", r)
	}
	if sm := SummarizeReplay(next); sm.Graded != 0 || sm.Errored != 1 || sm.Score != nil {
		t.Errorf("notsuz koşu puana girdi: %+v", sm)
	}
}

func TestEncodeReplayReportRoundTrip(t *testing.T) {
	rep := ReplayReport{ReportID: "rabc", State: ReplayRunning, Repeats: 1, CreatedAt: 5,
		Items: []ReplayItem{{ProblemID: "p1", Runs: []ReplayRun{gradedRun(1, GradeMiss, nil)}}}}
	body, err := encodeReplayReport(rep)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := DecodeReplayReport(chstore.SavedView{ID: chstore.SREReplayReportID("rabc"), Page: chstore.SREReplayReportPage, Name: ReplayRunning, QueryString: body})
	if !ok || got.Items[0].Runs[0].Grade != GradeMiss {
		t.Fatalf("gidiş-dönüş: %+v %v", got, ok)
	}
	if _, ok := DecodeReplayReport(chstore.SavedView{ID: "sre-replay-report-other", Page: chstore.SREReplayReportPage, Name: "done", QueryString: body}); ok {
		t.Error("kimliği uyuşmayan satır kabul edildi")
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(body), &raw)
	if _, ok := raw["items"]; !ok {
		t.Error("items alanı yok")
	}
}

// TestSREPackageNeverImportsNotify — tekrar (ve sre paketi) hiçbir bildirim /
// gönderi yoluna bağlanamaz: notify / slack / teams / mail paketleri import edilmez.
func TestSREPackageNeverImportsNotify(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			p := strings.Trim(im.Path.Value, `"`)
			if strings.Contains(p, "/internal/notify") || strings.Contains(p, "/internal/api") {
				t.Errorf("%s → %s: sre paketi bildirim / api katmanına bağlanamaz", f, p)
			}
		}
	}
}
