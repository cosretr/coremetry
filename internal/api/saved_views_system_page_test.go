package api

// v0.10.1024 — kod incelemesi (prod'da gözlenmedi): genel kayıtlı-görünüm ucu
// (POST /api/views, rol kapısız) her page değerini kabul ediyordu; viewer
// page="problem-verdict" satırı yazıp ekip kararı ("problem değil") taklit
// edebiliyordu — PUT /api/problem-verdicts'in editor kapısı ve denetimi
// atlanıyor, susturma politikası açıksa bildirim de susuyordu. Sistem sayfaları
// artık genel uçtan yazılamaz (her rol 400, depoya / denetime dokunmadan) ve
// silinemez (admin dahil 403). Okuma tarafı: chstore/problem_verdict_test.go.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSavedViewCreateRejection(t *testing.T) {
	cases := []struct {
		page   string
		reject bool
	}{
		{chstore.ProblemVerdictPage, true},
		{"traces", false},
		{"logs", false},
		{"problems", false},
		{"inbox", false},
		{"alert-template", false},
		{"dashboard-star", false},
		{"ai-chat", false},        // kişisel; kendi ucu sahibin owner_id'siyle yazar
		{"promql-history", false}, // kişisel
		{"table:traces", false},   // kişisel tercih
		{"Problem-Verdict", false},
	}
	for _, c := range cases {
		msg := savedViewCreateRejection(c.page)
		if (msg != "") != c.reject {
			t.Errorf("savedViewCreateRejection(%q) = %q, ret=%v beklenir", c.page, msg, c.reject)
		}
		if c.reject && !strings.Contains(msg, "reserved page") {
			t.Errorf("ret metni 'reserved page' demeli: %q", msg)
		}
	}
}

// Uçtan uca (store'suz Server): sistem sayfası her rol için depoya ve denetime
// DOKUNMADAN 400. Handler depoya ulaşsaydı nil store panikler — panik burada
// "ret depodan önce değil" hatası olarak raporlanır.
func TestCreateSavedViewRejectsSystemPageForEveryRole(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 8)}
	// Handler doğrudan (rota kaydı api.go'da, aşağıdaki kaynak pininde); test
	// burada mux'a kayıt yazmaz — make audit CHECK 7 test dosyalarını da tarar.
	h := http.HandlerFunc(s.createSavedView)

	forged := `"{\"signature\":\"e:9f8a7c6d5e4b3a21\",\"kind\":\"exception\",\"service\":\"orders-api\"}"`
	bodies := map[string]string{
		"sahte karar":                `{"name":"noise","page":"problem-verdict","queryString":` + forged + `}`,
		"sahte karar, paylaşımlı":    `{"name":"noise","page":"problem-verdict","queryString":` + forged + `,"shared":true}`,
		"boşluklu page":              `{"name":"real","page":"  problem-verdict ","queryString":` + forged + `}`,
		"geçersiz karar adıyla bile": `{"name":"whatever","page":"problem-verdict","queryString":"{}"}`,
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin} {
		for name, body := range bodies {
			code, resp := func() (code int, resp string) {
				defer func() {
					if rec := recover(); rec != nil {
						t.Fatalf("%s / %s: handler depoya ulaştı (ret depodan önce olmalı): %v", role, name, rec)
					}
				}()
				w := httptest.NewRecorder()
				h.ServeHTTP(w, asRole(httptest.NewRequest("POST", "/api/views", strings.NewReader(body)), role))
				return w.Code, w.Body.String()
			}()
			if code != http.StatusBadRequest {
				t.Errorf("%s / %s → %d, 400 beklenir", role, name, code)
			}
			if !strings.Contains(resp, "reserved page") {
				t.Errorf("%s / %s: hata metni 'reserved page' demeli: %q", role, name, resp)
			}
		}
	}
	if n := len(s.auditQ); n != 0 {
		t.Errorf("reddedilen yazım denetim kaydı düşmemeli, %d kayıt var", n)
	}
}

func TestSavedViewDeleteRejection(t *testing.T) {
	claims := func(role, uid string) *auth.Claims {
		return &auth.Claims{UserID: uid, Email: uid + "@example.test", Role: role}
	}
	verdictRow := chstore.SavedView{ID: "pv:e:9f8a7c6d5e4b3a21", OwnerID: "", Name: "noise", Page: chstore.ProblemVerdictPage}
	forgedOwn := chstore.SavedView{ID: "a1b2c3d4e5f6", OwnerID: "u1", Name: "noise", Page: chstore.ProblemVerdictPage}
	own := chstore.SavedView{ID: "aa", OwnerID: "u1", Name: "my traces", Page: "traces"}
	others := chstore.SavedView{ID: "bb", OwnerID: "u2", Name: "their traces", Page: "traces"}
	shared := chstore.SavedView{ID: "cc", OwnerID: "", Name: "team logs", Page: "logs"}

	cases := []struct {
		name   string
		cur    chstore.SavedView
		claims *auth.Claims
		code   int
	}{
		// Sistem sayfası: ADMİN DAHİL 403 — tek denetimli yol PUT /api/problem-verdicts.
		{"karar satırı / viewer", verdictRow, claims(auth.RoleViewer, "u1"), http.StatusForbidden},
		{"karar satırı / editor", verdictRow, claims(auth.RoleEditor, "u1"), http.StatusForbidden},
		{"karar satırı / admin", verdictRow, claims(auth.RoleAdmin, "u1"), http.StatusForbidden},
		{"karar satırı / oturumsuz", verdictRow, nil, http.StatusForbidden},
		{"sistem sayfasında kendi satırı", forgedOwn, claims(auth.RoleViewer, "u1"), http.StatusForbidden},
		// Mevcut sahiplik kuralı aynen.
		{"kendi görünümü / viewer", own, claims(auth.RoleViewer, "u1"), 0},
		{"başkasının görünümü / editor", others, claims(auth.RoleEditor, "u1"), http.StatusForbidden},
		{"paylaşımlı görünüm / editor", shared, claims(auth.RoleEditor, "u1"), http.StatusForbidden},
		{"başkasının görünümü / admin", others, claims(auth.RoleAdmin, "u1"), 0},
		{"paylaşımlı görünüm / admin", shared, claims(auth.RoleAdmin, "u1"), 0},
	}
	for _, c := range cases {
		code, msg := savedViewDeleteRejection(c.cur, c.claims)
		if code != c.code {
			t.Errorf("%s: code=%d (%q), %d beklenir", c.name, code, msg, c.code)
		}
		if c.cur.Page == chstore.ProblemVerdictPage && !strings.Contains(msg, "reserved page") {
			t.Errorf("%s: hata metni 'reserved page' demeli: %q", c.name, msg)
		}
	}
}

// Kaynak pini: ret işlevleri handler'larda depodan (ve denetimden) ÖNCE
// çağrılır; silme kararı sistem sayfası defterini sahiplik switch'inden önce
// sorar (admin dalı onu atlayamaz); rota genel handler'a bağlı kalır.
func TestSavedViewHandlersCheckSystemPagesBeforeStore(t *testing.T) {
	b, err := os.ReadFile("anomaly_extra.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	before := func(fn, body, first, then string) {
		t.Helper()
		i, j := strings.Index(body, first), strings.Index(body, then)
		if i < 0 || j < 0 || i > j {
			t.Errorf("%s: %q, %q'dan önce gelmeli (i=%d j=%d)", fn, first, then, i, j)
		}
	}
	create := funcBody(src, "createSavedView")
	before("createSavedView", create, "savedViewCreateRejection(body.Page)", "s.store.UpsertSavedView")
	before("createSavedView", create, "savedViewCreateRejection(body.Page)", `s.audit(r, "saved_view.create"`)
	before("createSavedView", create, "body.Page = strings.TrimSpace(body.Page)", "savedViewCreateRejection(body.Page)")

	del := funcBody(src, "deleteSavedView")
	before("deleteSavedView", del, "savedViewDeleteRejection(*cur, auth.FromContext(r.Context()))", "s.store.DeleteSavedView")

	rej := funcBody(src, "savedViewDeleteRejection")
	before("savedViewDeleteRejection", rej, "chstore.IsSystemSavedViewPage(cur.Page)", "claims.Role == auth.RoleAdmin")

	api, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	// Pin `mux.HandleFunc(` önekini taşımaz: make audit CHECK 7 (çift rota)
	// internal/api/*.go'yu test dosyaları dahil tarar.
	for _, w := range []string{
		`"POST   /api/views", s.createSavedView)`,
		`"DELETE /api/views/{id}", s.deleteSavedView)`,
	} {
		if !strings.Contains(string(api), w) {
			t.Errorf("api.go %q taşımalı (ret işlevleri bu handler'larda)", w)
		}
	}
}
