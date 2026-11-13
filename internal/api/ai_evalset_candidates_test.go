package api

// ai_evalset_candidates_test.go — v0.10.2111 (kalite döngüsü D6): aday
// vaka üretimi (K=5, gün penceresi, yeniden yazmama), 14 gün okuma süzgeci,
// tek "Kabul" eylemi (admin + audit), günlük koşu planına ve indirme
// dosyasına katılım, kanıt kapısı (requireEvidence).

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type memCandidateStore struct {
	mu     sync.Mutex
	worst  []chstore.CosreWorstAnswer
	inter  map[string]chstore.CosreInteraction
	ground map[string]chstore.CosreGrounding
	rows   map[string]chstore.SavedView
	writes int
}

func newMemCandidateStore() *memCandidateStore {
	return &memCandidateStore{inter: map[string]chstore.CosreInteraction{}, ground: map[string]chstore.CosreGrounding{}, rows: map[string]chstore.SavedView{}}
}

func (m *memCandidateStore) WorstCosreAnswers(_ context.Context, from time.Time, limit int) ([]chstore.CosreWorstAnswer, error) {
	var out []chstore.CosreWorstAnswer
	for _, w := range m.worst {
		if !w.CreatedAt.Before(from) && len(out) < limit {
			out = append(out, w)
		}
	}
	return out, nil
}
func (m *memCandidateStore) CosreInteractionsForEval(context.Context, []string, time.Time) (map[string]chstore.CosreInteraction, error) {
	return m.inter, nil
}
func (m *memCandidateStore) CosreGroundingByExchange(context.Context, []string, time.Time) (map[string]chstore.CosreGrounding, error) {
	return m.ground, nil
}
func (m *memCandidateStore) UpsertSavedView(_ context.Context, v chstore.SavedView) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[v.ID] = v
	m.writes++
	return nil
}
func (m *memCandidateStore) GetSavedView(_ context.Context, id string) (*chstore.SavedView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.rows[id]; ok {
		return &v, nil
	}
	return nil, nil
}
func (m *memCandidateStore) ListSavedViews(_ context.Context, _, page string) ([]chstore.SavedView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []chstore.SavedView
	for _, v := range m.rows {
		if v.Page == page && v.Name != "" {
			out = append(out, v)
		}
	}
	return out, nil
}

func useCandidateStore(t *testing.T, st *memCandidateStore) {
	t.Helper()
	prev := evalCandidateStoreFor
	evalCandidateStoreFor = func(*Server) evalCandidateStore { return st }
	t.Cleanup(func() { evalCandidateStoreFor = prev })
}

// v2Tool — v2 kataloğundan gerçek bir araç adı (sabit ad yazılmaz).
func v2Tool(t *testing.T) string {
	t.Helper()
	for name := range evalV2ToolNames() {
		return name
	}
	t.Fatal("v2 kataloğu boş")
	return ""
}

func TestEvalCandidateFromDataIndependentExpect(t *testing.T) {
	tool := v2Tool(t)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	w := chstore.CosreWorstAnswer{CosreAnswerQuality: chstore.CosreAnswerQuality{ExchangeID: "ex1", CreatedAt: day.Add(time.Hour), Score: 0.12, Reasons: []string{"requery", "no_evidence"}}, Question: "yedek"}
	inter := chstore.CosreInteraction{Question: "checkout neden yavaş?", Answer: "cevap", Tools: []chstore.CosreTool{
		{Name: tool, OK: true}, {Name: "search_traces", OK: true}, {Name: "failed_tool", OK: false}, {Name: tool, OK: true},
	}}
	g := &chstore.CosreGrounding{Details: `[{"Label":"grounded","Fact":"p99 1.2s"},{"Label":"ungrounded","Fact":"payments çöktü"}]`}
	c, ok := evalCandidateFrom(w, inter, g, map[string]bool{tool: true}, day, day.Add(26*time.Hour))
	if !ok {
		t.Fatal("aday üretilmedi")
	}
	if !reflect.DeepEqual(c.AnyOfTools, []string{tool}) {
		t.Fatalf("anyOfTools %v — yalnız v2'de olan, başarılı, tekil", c.AnyOfTools)
	}
	if !reflect.DeepEqual(c.MustNotContain, []string{"payments çöktü"}) {
		t.Fatalf("mustNotContain %v", c.MustNotContain)
	}
	ec := evalCandidateCase(c)
	if ec.Mode != evalModeAgent || ec.Surface != evalAgentLoopSurface || ec.User != "checkout neden yavaş?" || ec.Prompt != "" ||
		!ec.Expect.RequireEvidence || len(ec.Expect.KeyFacts) != 0 || len(ec.Expect.MustContain) != 0 || len(ec.Expect.ExpectedTools) != 0 {
		t.Fatalf("aday vakası: %+v", ec)
	}
	if _, ok := evalCandidateFrom(chstore.CosreWorstAnswer{CosreAnswerQuality: chstore.CosreAnswerQuality{ExchangeID: "e"}}, chstore.CosreInteraction{}, nil, nil, day, day); ok {
		t.Fatal("sorusuz tur aday oldu")
	}
}

// TestEvalCandidateCasePassesFixtureCheck — kabul edilen aday repoya gömülünce
// fikstür doğrulamasından geçer (araç adı olsa da olmasa da).
func TestEvalCandidateCasePassesFixtureCheck(t *testing.T) {
	known := evalFixtureToolNames(t)
	for _, tools := range [][]string{{v2Tool(t)}, {}} {
		c := evalCandidateCase(evalsetCandidate{ExchangeID: "ex", Question: "neden yavaş?", AnyOfTools: tools, Day: "2026-11-12"})
		c.file = "aday.json"
		checkAgentFixture(t, c, known)
	}
}

func TestRequireEvidenceGate(t *testing.T) {
	c := evalCase{Surface: evalAgentLoopSurface, Mode: evalModeAgent, User: "q", Expect: evalExpect{RequireEvidence: true}}
	has := func(fails []string) bool {
		for _, f := range fails {
			if strings.HasPrefix(f, "no evidence") {
				return true
			}
		}
		return false
	}
	if f, _ := scoreEvalAgentCase(c, evalAgentOutcome{Answer: "cevap"}, evalAgentStats{}, ""); !has(f) {
		t.Fatalf("araçsız cevap kanıt kapısından geçti: %v", f)
	}
	failed := evalAgentOutcome{Answer: "cevap", Tools: []chstore.CosreTool{{Name: "x", OK: false}}}
	if f, _ := scoreEvalAgentCase(c, failed, evalAgentStats{}, ""); !has(f) {
		t.Fatalf("yalnız başarısız araç kanıt sayıldı: %v", f)
	}
	okOut := evalAgentOutcome{Answer: "cevap", Tools: []chstore.CosreTool{{Name: "x", OK: true}}}
	if f, _ := scoreEvalAgentCase(c, okOut, evalAgentStats{}, ""); has(f) {
		t.Fatalf("başarılı araç varken kanıt ihlali: %v", f)
	}
}

func TestWriteEvalsetCandidatesKAndDayWindow(t *testing.T) {
	st := newMemCandidateStore()
	useCandidateStore(t, st)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		id := "in" + string(rune('a'+i))
		st.worst = append(st.worst, chstore.CosreWorstAnswer{CosreAnswerQuality: chstore.CosreAnswerQuality{ExchangeID: id, CreatedAt: day.Add(time.Duration(i) * time.Hour)}, Question: "soru " + id})
	}
	// Ertesi günün satırı (WorstCosreAnswers üst sınırsız) ve boş soru.
	st.worst = append([]chstore.CosreWorstAnswer{
		{CosreAnswerQuality: chstore.CosreAnswerQuality{ExchangeID: "next", CreatedAt: day.Add(25 * time.Hour)}, Question: "yarın"},
		{CosreAnswerQuality: chstore.CosreAnswerQuality{ExchangeID: "blank", CreatedAt: day.Add(time.Minute)}},
	}, st.worst...)
	s := &Server{}
	n, err := s.WriteEvalsetCandidates(context.Background(), day)
	if err != nil || n != evalCandidateK {
		t.Fatalf("yazılan %d (err %v), istenen %d", n, err, evalCandidateK)
	}
	if len(st.rows) != evalCandidateK {
		t.Fatalf("%d satır", len(st.rows))
	}
	for _, id := range []string{"evc-next", "evc-blank", "evc-inf", "evc-ing"} {
		if _, ok := st.rows[id]; ok {
			t.Fatalf("%s yazılmamalıydı", id)
		}
	}
	row := st.rows["evc-ina"]
	if row.Page != chstore.EvalsetCandidatePage || row.OwnerID != "" || row.Name != "soru ina" || row.Pinned {
		t.Fatalf("satır: %+v", row)
	}
	// Kabul edilmiş aday yeniden koşuda EZİLMEZ.
	c, _ := decodeEvalCandidate(row)
	c.Accepted = true
	acc, _ := evalCandidateRow(c, row.CreatedAt)
	st.rows[acc.ID] = acc
	writes := st.writes
	if _, err := s.WriteEvalsetCandidates(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	if st.writes != writes {
		t.Fatalf("yeniden koşu %d satır yazdı", st.writes-writes)
	}
	if got, _ := decodeEvalCandidate(st.rows["evc-ina"]); !got.Accepted {
		t.Fatal("kabul durumu ezildi")
	}
}

func TestEvalCandidateVisibility(t *testing.T) {
	now := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		accepted bool
		age      time.Duration
		want     bool
	}{
		{"yeni aday", false, time.Hour, true},
		{"13 gün 23 sa", false, 14*24*time.Hour - time.Hour, true},
		{"tam 14 gün", false, 14 * 24 * time.Hour, false},
		{"30 gün, kabul edilmiş", true, 30 * 24 * time.Hour, true},
	} {
		if got := evalCandidateVisible(evalsetCandidate{Accepted: tc.accepted}, now.Add(-tc.age), now); got != tc.want {
			t.Errorf("%s: %v, istenen %v", tc.name, got, tc.want)
		}
	}
}

func seedCandidate(t *testing.T, st *memCandidateStore, ex string, created time.Time, accepted bool) string {
	t.Helper()
	c := evalsetCandidate{ID: evalCandidateIDPrefix + ex, ExchangeID: ex, Question: "soru " + ex, Day: "2026-11-12", Accepted: accepted}
	row, err := evalCandidateRow(c, created.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	st.rows[row.ID] = row
	return row.ID
}

func TestEvalCandidateRoutes(t *testing.T) {
	st := newMemCandidateStore()
	useCandidateStore(t, st)
	h := newEvalHarness(t, false, evalTestCases())
	h.s.registerEvalsetCandidateRoutes(h.mux)
	now := time.Now()
	fresh := seedCandidate(t, st, "fresh", now.Add(-time.Hour), false)
	old := seedCandidate(t, st, "old", now.Add(-15*24*time.Hour), false)
	keep := seedCandidate(t, st, "keep", now.Add(-40*24*time.Hour), true)

	w := h.do(t, http.MethodGet, "/api/ai/evalset/candidates", "", auth.RoleAdmin)
	var list struct {
		Candidates []evalsetCandidate
		TTLDays    int `json:"ttlDays"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 {
		t.Fatalf("liste %d: %s", w.Code, w.Body.String())
	}
	ids := map[string]bool{}
	for _, c := range list.Candidates {
		ids[c.ID] = true
	}
	if !ids[fresh] || ids[old] || !ids[keep] || list.TTLDays != 14 {
		t.Fatalf("görünen adaylar %v (ttl %d)", ids, list.TTLDays)
	}
	if w := h.do(t, http.MethodGet, "/api/ai/evalset/candidates", "", auth.RoleEditor); w.Code != http.StatusForbidden {
		t.Fatalf("editor liste: %d", w.Code)
	}

	if w := h.do(t, http.MethodPost, "/api/ai/evalset/candidates/"+fresh+"/accept", "", auth.RoleEditor); w.Code != http.StatusForbidden {
		t.Fatalf("editor kabul: %d", w.Code)
	}
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/candidates/"+old+"/accept", "", auth.RoleAdmin); w.Code != http.StatusNotFound {
		t.Fatalf("süresi dolmuş aday kabul: %d", w.Code)
	}
	w = h.do(t, http.MethodPost, "/api/ai/evalset/candidates/"+fresh+"/accept", "", auth.RoleAdmin)
	if w.Code != http.StatusOK {
		t.Fatalf("kabul %d: %s", w.Code, w.Body.String())
	}
	got, _ := decodeEvalCandidate(st.rows[fresh])
	if !got.Accepted || got.AcceptedBy != "admin@example.com" || !st.rows[fresh].Pinned {
		t.Fatalf("kabul satırı: %+v", got)
	}
	select {
	case e := <-h.s.auditQ:
		if e.Action != "evalset.candidate.accept" || e.TargetID != fresh {
			t.Fatalf("audit: %+v", e)
		}
	default:
		t.Fatal("kabul audit yazmadı")
	}
	// İkinci kabul eylemsiz (idempotent, yeni audit yok).
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/candidates/"+fresh+"/accept", "", auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("ikinci kabul %d", w.Code)
	}
	select {
	case e := <-h.s.auditQ:
		t.Fatalf("ikinci kabul audit yazdı: %+v", e)
	default:
	}
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/candidates/foo/accept", "", auth.RoleAdmin); w.Code != http.StatusNotFound {
		t.Fatalf("yabancı kimlik: %d", w.Code)
	}

	// Kabul edilen adaylar günlük koşu planına girer.
	cases := h.s.evalDailyCandidateCases(context.Background())
	if len(cases) != 2 {
		t.Fatalf("plan aday vakası %d, istenen 2 (fresh + keep)", len(cases))
	}
	for _, c := range cases {
		if !evalAgentCase(c) || !strings.HasPrefix(c.ID, evalCandidateCasePrefix) {
			t.Fatalf("plan vakası: %+v", c)
		}
	}
	ex := evalCandidateExportCase(evalsetCandidate{ExchangeID: "e", Question: "q"})
	b, _ := json.Marshal(ex)
	var back evalCase
	if err := json.Unmarshal(b, &back); err != nil || !evalAgentCase(back) || back.User != "q" || !back.Expect.RequireEvidence {
		t.Fatalf("indirme vakası yükleyiciden ajan vakası olarak dönmüyor: %s", b)
	}
}

// TestRunDailyEvalsetIncludesAcceptedCandidates — günlük koşunun planı =
// gömülü küme + kabul edilmiş adaylar (kabul edilmemiş girmez).
func TestRunDailyEvalsetIncludesAcceptedCandidates(t *testing.T) {
	st := newMemCandidateStore()
	useCandidateStore(t, st)
	h := newEvalHarness(t, false, evalTestCases())
	markEvalHarnessLocal(h, true)
	now := time.Now()
	seedCandidate(t, st, "acc", now.Add(-time.Hour), true)
	seedCandidate(t, st, "pending", now.Add(-time.Hour), false)
	started, note, err := h.s.RunDailyEvalset(context.Background())
	if err != nil || !started {
		t.Fatalf("koşu başlamadı: %q %v", note, err)
	}
	// Çıplak test sunucusunda (store/deps yok) ajan vakası panikler ve koşucu
	// yakalar (failed) — burada yalnız planın kapsamı ölçülür.
	runs, _ := h.store.ListEvalRuns(context.Background(), 1)
	if len(runs) != 1 || runs[0].Total != 5 {
		t.Fatalf("plan toplamı %+v, istenen 4 gömülü + 1 aday", runs)
	}
}
