package api

// v0.10.1124 — tanı uçları: POST /api/wiki/test-search (yalnız oturum açmış
// yönetici, şekil, içerik yok) ve GET /api/wiki/pages (oturum kullanıcısı,
// önizleme yalnız yöneticiye, sınırlı sayfalama). Adlar sentetik.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func doWikiReq(mux http.Handler, method, path, body string, c *auth.Claims) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if c != nil {
		req = req.WithContext(auth.ContextWithClaims(context.Background(), c))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestWikiTestSearchAuthAndShape(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	srv := &Server{auditQ: make(chan chstore.AuditEntry, 16)}
	mux := srv.buildMux()
	body := `{"query":"svc-orders nasıl yeniden başlatılır"}`
	for _, c := range []*auth.Claims{
		{UserID: "u-1", Role: auth.RoleViewer},
		{UserID: "u-2", Role: auth.RoleEditor},
		{UserID: "token:t1", Role: auth.RoleAdmin},
	} {
		if rec := doWikiReq(mux, "POST", "/api/wiki/test-search", body, c); rec.Code != http.StatusForbidden {
			t.Errorf("%+v: %d (403 bekleniyordu)", c, rec.Code)
		}
	}
	rec := doWikiReq(mux, "POST", "/api/wiki/test-search", body, &auth.Claims{UserID: "u-9", Role: auth.RoleAdmin, Email: "admin@example.test"})
	if rec.Code != 200 {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Mode        string   `json:"mode"`
		LiveQueries []string `json:"liveQueries"`
		Live        struct {
			Attempted bool `json:"attempted"`
			Info      struct {
				Class      string `json:"class"`
				HTTPStatus int    `json:"httpStatus"`
				APIVersion string `json:"apiVersion"`
			} `json:"info"`
		} `json:"live"`
		Final []struct {
			Title          string  `json:"title"`
			Score          float64 `json:"score"`
			Snippet        string  `json:"snippet"`
			PassesRAG      bool    `json:"passesRag"`
			PassesWikiTier bool    `json:"passesWikiTier"`
		} `json:"final"`
		Floors  map[string]float64 `json:"floors"`
		Verdict string             `json:"verdict"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Mode != wiki.ModeHybrid || !out.Live.Attempted || out.Live.Info.Class != "ok" || out.Live.Info.APIVersion != "7.0" {
		t.Fatalf("canlı tanı: %s", rec.Body.String())
	}
	if len(out.Final) == 0 || !out.Final[0].PassesRAG || !out.Final[0].PassesWikiTier || out.Floors["rag"] != ragWikiFloor || out.Verdict == "" {
		t.Fatalf("son liste + taban kararları: %s", rec.Body.String())
	}
	for _, h := range out.Final {
		if len([]rune(h.Snippet)) > wiki.DiagSnippetRunes+2 {
			t.Errorf("kesit tavanı: %d", len([]rune(h.Snippet)))
		}
	}
	if len(out.LiveQueries) == 0 || strings.Contains(out.LiveQueries[0], "nasıl") {
		t.Errorf("canlı sorgular soru sözcüksüz: %v", out.LiveQueries)
	}
	select {
	case e := <-srv.auditQ:
		if e.Action != "wiki.test_search" || strings.Contains(e.Details, "svc-orders") {
			t.Errorf("audit satırı sorgu METNİ taşımamalı: %+v", e)
		}
	default:
		t.Error("test-search audit satırı yazmalı")
	}
	if rec := doWikiReq(mux, "POST", "/api/wiki/test-search", `{"query":"nasıl ne"}`, &auth.Claims{UserID: "u-9", Role: auth.RoleAdmin}); rec.Code != http.StatusBadRequest {
		t.Errorf("terimsiz sorgu 400: %d", rec.Code)
	}
}

type fakePageLister struct {
	gotPreview bool
	gotQ       string
	gotOff     int
	gotLim     int
}

func (f *fakePageLister) WikiListPages(_ context.Context, q string, off, lim int, preview bool) ([]chstore.WikiPageRow, uint64, error) {
	f.gotQ, f.gotOff, f.gotLim, f.gotPreview = q, off, lim, preview
	row := chstore.WikiPageRow{Project: "Platform", Wiki: "Platform.wiki", Path: "/Runbooks/Restart svc-orders",
		Title: "Restart svc-orders", URL: "https://devops.example.test/x", Chunks: 2, UpdatedAt: time.UnixMilli(1_700_000_000_000)}
	if preview {
		row.Preview = runbookText
	}
	return []chstore.WikiPageRow{row}, 1, nil
}

func TestWikiPagesEndpoint(t *testing.T) {
	withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{}})
	fl := &fakePageLister{}
	prev := wikiPagesSource
	wikiPagesSource = func(*Server) wikiPageLister { return fl }
	t.Cleanup(func() { wikiPagesSource = prev })
	mux := (&Server{}).buildMux()

	if rec := doWikiReq(mux, "GET", "/api/wiki/pages", "", &auth.Claims{UserID: "token:t1", Role: auth.RoleAdmin}); rec.Code != http.StatusForbidden {
		t.Errorf("API token'ı: %d", rec.Code)
	}
	rec := doWikiReq(mux, "GET", "/api/wiki/pages?q=restart&offset=-5&limit=5000", "", &auth.Claims{UserID: "u-1", Role: auth.RoleViewer})
	if rec.Code != 200 || fl.gotPreview || fl.gotOff != 0 || fl.gotLim != chstore.WikiListMax || fl.gotQ != "restart" {
		t.Fatalf("viewer: %d preview=%v off=%d lim=%d q=%q", rec.Code, fl.gotPreview, fl.gotOff, fl.gotLim, fl.gotQ)
	}
	if strings.Contains(rec.Body.String(), "kubectl") {
		t.Error("viewer önizleme (içerik) görmemeli")
	}
	rec = doWikiReq(mux, "GET", "/api/wiki/pages?offset=100&limit=50", "", &auth.Claims{UserID: "u-9", Role: auth.RoleAdmin})
	var out struct {
		Rows    []chstore.WikiPageRow `json:"rows"`
		Total   int                   `json:"total"`
		Preview bool                  `json:"preview"`
		Mode    string                `json:"mode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !fl.gotPreview || fl.gotOff != 100 || fl.gotLim != 50 || !out.Preview || out.Total != 1 || len(out.Rows) != 1 ||
		!strings.Contains(out.Rows[0].Preview, "kubectl") || out.Mode != wiki.ModeHybrid {
		t.Fatalf("admin önizleme + sayfalama: %s", rec.Body.String())
	}
}

func TestWikiModeWarningAndLiveSyncRefused(t *testing.T) {
	if wikiModeWarning(wiki.ModeHybrid, wiki.SearchUnavailable) != "" {
		t.Error("karma modda uyarı yok")
	}
	if w := wikiModeWarning(wiki.ModeLive, wiki.SearchUnavailable); !strings.Contains(w, wiki.NoteSearchUnavailableLive) {
		t.Errorf("canlı mod + Search yok: %q", w)
	}
	if w := wikiModeWarning(wiki.ModeLive, wiki.SearchUnknown); !strings.Contains(w, "Aramayı test et") {
		t.Errorf("canlı mod + doğrulanmamış: %q", w)
	}
	if wikiModeWarning(wiki.ModeLive, wiki.SearchAvailable) != "" {
		t.Error("doğrulanmış canlı modda uyarı yok")
	}
}
