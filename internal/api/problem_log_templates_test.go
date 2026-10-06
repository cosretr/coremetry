package api

// v0.10.1113 — GET /api/problems/{id}/log-templates ("Başlangıçta doğan log
// şablonları"). Pinler: (1) servis kümesi = özne (yalnız kind=service) +
// hipotezin çağrı-grafiği adayları, ≤5; (2) önbellek anahtarı TÜM girdileri
// taşır — başlangıç, özne, SIRALI küme içeriği (uzunluk değil, v0.5.187);
// (3) küme boşsa CH okuması ve önbellek girdisi YOK; (4) eksik problem 404;
// (5) bilinen-okuması hatası 500 ve önbelleğe yazılmaz; (6) aynı girdinin
// ikinci isteği önbellekten; (7) rota kendi dosyasında, api.go'da değil.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/correlator"
)

func TestProblemLogTemplateServices(t *testing.T) {
	hyp := &chstore.RootCauseHypothesis{Candidates: []chstore.ScoredCause{
		{Service: "svc-orders", Score: 0.8},
		{Service: "node-7", Kind: chstore.NodeKindNode, Score: 0.7}, // node adı, servis değil
		{Service: "payments-api", Score: 0.6},                       // özneyle aynı → tekrar yok
		{Service: "checkout", Score: 0.5},
		{Service: "ledger", Score: 0.4},
		{Service: "inventory", Score: 0.3},
		{Service: "billing", Score: 0.2},
	}}
	cases := []struct {
		name string
		p    chstore.Problem
		hyp  *chstore.RootCauseHypothesis
		want []string
	}{
		{"servis öznesi + RCA adayları (≤5, node hariç)", chstore.Problem{Service: "payments-api", Kind: "service"}, hyp,
			[]string{"payments-api", "svc-orders", "checkout", "ledger", "inventory"}},
		{"boş kind = servis", chstore.Problem{Service: "payments-api"}, nil, []string{"payments-api"}},
		{"db öznesi: yalnız adaylar", chstore.Problem{Service: "db:oracle@crm", Kind: chstore.ProblemKindDB},
			&chstore.RootCauseHypothesis{Candidates: []chstore.ScoredCause{{Service: "svc-orders"}}}, []string{"svc-orders"}},
		{"dış özne, hipotez yok: boş", chstore.Problem{Service: "ext:influx/x", Kind: chstore.ProblemKindExternal}, nil, []string{}},
		{"servissiz filo problemi, hipotez yok: boş", chstore.Problem{Service: ""}, nil, []string{}},
		// N5 — Kind'ı boş olmayan HER aday atlanır: rollout adayının Service'i
		// bir rollout öznesi, servis adı değil (correlator.CauseKindRollout).
		{"rollout adayı atlanır", chstore.Problem{Service: "payments-api"},
			&chstore.RootCauseHypothesis{Candidates: []chstore.ScoredCause{
				{Service: "rollout:prod-a/payments/payments-api@7", Kind: correlator.CauseKindRollout, Score: 0.9},
				{Service: "svc-orders", Score: 0.5},
			}}, []string{"payments-api", "svc-orders"}},
		{"bilinmeyen servis-dışı tür de atlanır", chstore.Problem{Service: "payments-api"},
			&chstore.RootCauseHypothesis{Candidates: []chstore.ScoredCause{{Service: "queue:orders", Kind: "queue"}}},
			[]string{"payments-api"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := problemLogTemplateServices(problemLogTemplateSubject(c.p), c.hyp)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestProblemLogTemplatesKey(t *testing.T) {
	base := problemLogTemplatesKey(1_000, "payments-api", []string{"payments-api", "svc-orders"})
	if got := problemLogTemplatesKey(1_000, "payments-api", []string{"svc-orders", "payments-api"}); got != base {
		t.Errorf("aynı küme farklı sırayla farklı anahtar: %s vs %s", got, base)
	}
	for name, other := range map[string]string{
		"başlangıç":           problemLogTemplatesKey(2_000, "payments-api", []string{"payments-api", "svc-orders"}),
		"özne":                problemLogTemplatesKey(1_000, "svc-orders", []string{"payments-api", "svc-orders"}),
		"aynı uzunlukta küme": problemLogTemplatesKey(1_000, "payments-api", []string{"payments-api", "checkout"}),
		"alt küme":            problemLogTemplatesKey(1_000, "payments-api", []string{"payments-api"}),
		"birleşik ad tuzağı":  problemLogTemplatesKey(1_000, "payments-api", []string{"payments-apisvc-orders"}),
	} {
		if other == base {
			t.Errorf("%s anahtara girmiyor: %s", name, base)
		}
	}
	if strings.Contains(base, "n=") || !strings.HasPrefix(base, "problem-log-templates:v1:at=1000:") {
		t.Errorf("anahtar biçimi: %s", base)
	}
}

// fakeProblemLogTemplatesStore — üç okumanın sahte deposu.
type fakeProblemLogTemplatesStore struct {
	problem   *chstore.Problem
	hyp       *chstore.RootCauseHypothesis
	hypErr    error
	lists     [][]chstore.LogTemplate
	listErrs  []error
	listCalls []chstore.ListLogTemplatesFilter
}

func (f *fakeProblemLogTemplatesStore) GetProblem(context.Context, string) (*chstore.Problem, error) {
	return f.problem, nil
}
func (f *fakeProblemLogTemplatesStore) GetHypothesis(context.Context, string, string) (*chstore.RootCauseHypothesis, error) {
	return f.hyp, f.hypErr
}
func (f *fakeProblemLogTemplatesStore) ListLogTemplates(_ context.Context, flt chstore.ListLogTemplatesFilter) ([]chstore.LogTemplate, error) {
	i := len(f.listCalls)
	f.listCalls = append(f.listCalls, flt)
	if i < len(f.listErrs) && f.listErrs[i] != nil {
		return nil, f.listErrs[i]
	}
	if i < len(f.lists) {
		return f.lists[i], nil
	}
	return nil, nil
}

func TestGetProblemLogTemplates(t *testing.T) {
	orig := problemLogTemplatesStoreOf
	t.Cleanup(func() { problemLogTemplatesStoreOf = orig })
	start := time.Unix(1_790_000_000, 0).UnixNano()
	newServer := func(st *fakeProblemLogTemplatesStore) *Server {
		problemLogTemplatesStoreOf = func(*Server) problemLogTemplatesStore { return st }
		return &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	}
	call := func(s *Server) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/problems/p1/log-templates", nil)
		r.SetPathValue("id", "p1")
		s.getProblemLogTemplates(w, r)
		return w
	}

	t.Run("eksik problem → 404", func(t *testing.T) {
		w := call(newServer(&fakeProblemLogTemplatesStore{}))
		if w.Code != 404 {
			t.Fatalf("code=%d", w.Code)
		}
	})

	t.Run("servis kümesi boş → okuma yok, boş liste", func(t *testing.T) {
		st := &fakeProblemLogTemplatesStore{problem: &chstore.Problem{ID: "p1", Service: "db:oracle@crm", Kind: chstore.ProblemKindDB, StartedAt: start}}
		w := call(newServer(st))
		if w.Code != 200 || len(st.listCalls) != 0 {
			t.Fatalf("code=%d listCalls=%d", w.Code, len(st.listCalls))
		}
		var body problemLogTemplatesResponse
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body.Templates == nil || len(body.Templates) != 0 || len(body.Services) != 0 {
			t.Fatalf("body %s", w.Body.String())
		}
	})

	t.Run("özne + aday; ikinci istek önbellekten", func(t *testing.T) {
		st := &fakeProblemLogTemplatesStore{
			problem: &chstore.Problem{ID: "p1", Service: "payments-api", Kind: "service", StartedAt: start},
			hyp:     &chstore.RootCauseHypothesis{Candidates: []chstore.ScoredCause{{Service: "svc-orders"}}},
			lists: [][]chstore.LogTemplate{
				{{ID: "t1", Template: "ledger write rejected for account <*>", FirstSeen: start - 40*int64(time.Second),
					LastSeen: start, TotalCount: 9, Services: []string{"svc-orders"}}},
				{},
			},
		}
		s := newServer(st)
		w := call(s)
		if w.Code != 200 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		var body problemLogTemplatesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(body.Services, []string{"payments-api", "svc-orders"}) {
			t.Errorf("services %v", body.Services)
		}
		if body.StartedAt != start || body.FromNs != start-int64(10*time.Minute) || body.ToNs != start+int64(5*time.Minute) {
			t.Errorf("pencere %+v", body)
		}
		if len(body.Templates) != 1 || body.Templates[0].OffsetSec != -40 || body.Templates[0].Service != "svc-orders" ||
			body.Templates[0].Query == "" {
			t.Fatalf("templates %+v", body.Templates)
		}
		if got := st.listCalls[0].AnyServices; !reflect.DeepEqual(got, []string{"payments-api", "svc-orders"}) {
			t.Errorf("aday okuması servis kümesi %v", got)
		}
		n := len(st.listCalls)
		if w2 := call(s); w2.Code != 200 || len(st.listCalls) != n {
			t.Errorf("ikinci istek önbellekten gelmedi: code=%d okuma %d → %d", w2.Code, n, len(st.listCalls))
		}
	})

	t.Run("hipotez okunamazsa yalnız özne", func(t *testing.T) {
		st := &fakeProblemLogTemplatesStore{
			problem: &chstore.Problem{ID: "p1", Service: "payments-api", StartedAt: start + 1},
			hypErr:  errors.New("CH down"),
			lists:   [][]chstore.LogTemplate{{}},
		}
		w := call(newServer(st))
		if w.Code != 200 || len(st.listCalls) != 1 || !reflect.DeepEqual(st.listCalls[0].AnyServices, []string{"payments-api"}) {
			t.Fatalf("code=%d calls=%+v", w.Code, st.listCalls)
		}
	})

	t.Run("bilinen okuması düşer → 500, önbelleğe yazılmaz", func(t *testing.T) {
		st := &fakeProblemLogTemplatesStore{
			problem: &chstore.Problem{ID: "p1", Service: "payments-api", StartedAt: start + 2},
			lists: [][]chstore.LogTemplate{{{ID: "t1", Template: "ledger write rejected for account <*>",
				FirstSeen: start, LastSeen: start, TotalCount: 9, Services: []string{"payments-api"}}}},
			listErrs: []error{nil, errors.New("code: 159, timeout exceeded")},
		}
		s := newServer(st)
		if w := call(s); w.Code != 500 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		st.listErrs = nil
		st.lists = [][]chstore.LogTemplate{nil, nil, {}, {}}
		if w := call(s); w.Code != 200 || len(st.listCalls) <= 2 {
			t.Errorf("hata önbelleğe yazılmış: code=%d okumalar=%d", w.Code, len(st.listCalls))
		}
	})
}

// TestProblemLogTemplatesRouteOwnFile — rota kendi dosyasında, defterden;
// api.go'da yok; serveCached ve 60 s TTL.
func TestProblemLogTemplatesRouteOwnFile(t *testing.T) {
	src, err := os.ReadFile("problem_log_templates.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		`registerRoutesExtra("problem-log-templates"`,
		`"GET /api/problems/{id}/log-templates"`,
		`s.serveCached(w, r, problemLogTemplatesKey(`,
		`problemLogTemplatesTTL = 60 * time.Second`,
	} {
		if !strings.Contains(string(src), must) {
			t.Errorf("problem_log_templates.go %q içermiyor", must)
		}
	}
	apiSrc, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(apiSrc), "/log-templates") {
		t.Error("rota api.go'ya yazılmış — kendi dosyasında kalmalı")
	}
}
