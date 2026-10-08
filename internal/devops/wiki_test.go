package devops

// v0.10.1122 — wiki REST istemcisi: sahte Azure DevOps'a karşı (httptest).
// Adlar sentetik (example.test, svc-orders).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func wikiTestService(t *testing.T, h http.HandlerFunc) *Service {
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	s := New()
	s.Configure(Settings{BaseURL: ts.URL, Collection: "DefaultCollection", PAT: "pat-xyz", Flavor: FlavorServer})
	return s
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestWikiPagePathFromGitPath(t *testing.T) {
	cases := []struct{ git, mapped, want string }{
		{"/Runbooks/Restart-svc%2Dorders.md", "/", "/Runbooks/Restart svc-orders"},
		{"/docs/Mimari/Genel-Bakış.md", "/docs", "/Mimari/Genel Bakış"},
		{"/Kurulum.md", "", "/Kurulum"},
		{"/image.png", "/", ""},
		{"", "/", ""},
	}
	for _, c := range cases {
		if got := WikiPagePathFromGitPath(c.git, c.mapped); got != c.want {
			t.Errorf("WikiPagePathFromGitPath(%q,%q)=%q want %q", c.git, c.mapped, got, c.want)
		}
	}
}

func TestFlattenWikiTreeAndLimit(t *testing.T) {
	body := []byte(`{"path":"/","subPages":[{"path":"/A","gitItemPath":"/A.md","subPages":[{"path":"/A/B","gitItemPath":"/A/B.md"}]},{"path":"/C","gitItemPath":"/C.md"}]}`)
	refs, trunc, err := flattenWikiTree(body, 0)
	if err != nil || trunc || len(refs) != 3 || refs[0].Path != "/A" || refs[1].Path != "/A/B" {
		t.Fatalf("düz liste: %+v %v %v", refs, trunc, err)
	}
	refs, trunc, _ = flattenWikiTree(body, 2)
	if !trunc || len(refs) != 2 {
		t.Fatalf("tavan: %+v %v", refs, trunc)
	}
	if _, _, err := flattenWikiTree([]byte(`<html>`), 0); err == nil {
		t.Error("JSON olmayan gövde hata")
	}
}

func TestWikiClientEndToEnd(t *testing.T) {
	var gotAuth bool
	s := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		_, pw, ok := r.BasicAuth()
		gotAuth = ok && pw == "pat-xyz"
		switch {
		case r.URL.Path == "/DefaultCollection/_apis/projects":
			jsonOK(w, map[string]any{"count": 1, "value": []map[string]string{{"name": "Platform"}}})
		case r.URL.Path == "/DefaultCollection/Platform/_apis/wiki/wikis":
			jsonOK(w, map[string]any{"value": []map[string]any{{"id": "w1", "name": "Platform.wiki", "type": "projectWiki",
				"repositoryId": "r1", "mappedPath": "/", "versions": []map[string]string{{"version": "wikiMaster"}}}}})
		case strings.HasSuffix(r.URL.Path, "/wikis/w1/pages") && r.URL.Query().Get("includeContent") == "true":
			if r.Header.Get("If-None-Match") == `"etag-1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"etag-1"`)
			jsonOK(w, map[string]any{"path": r.URL.Query().Get("path"), "gitItemPath": "/A.md", "content": "# A\nsvc-orders"})
		case strings.HasSuffix(r.URL.Path, "/repositories/r1/items"):
			if r.URL.Query().Get("versionDescriptor.version") != "wikiMaster" {
				http.Error(w, "branch missing", 400)
				return
			}
			jsonOK(w, map[string]any{"value": []map[string]any{
				{"objectId": "abc", "gitObjectType": "blob", "path": "/A.md"},
				{"objectId": "tree", "gitObjectType": "tree", "path": "/A", "isFolder": true},
			}})
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	ps, err := s.ListWikiProjects(ctx)
	if err != nil || len(ps) != 1 || !gotAuth {
		t.Fatalf("projeler: %v %v auth=%v", ps, err, gotAuth)
	}
	ws, err := s.ListWikis(ctx, "Platform")
	if err != nil || len(ws) != 1 || ws[0].Version != "wikiMaster" || ws[0].Project != "Platform" {
		t.Fatalf("wiki'ler: %+v %v", ws, err)
	}
	pg, err := s.GetWikiPage(ctx, "Platform", "w1", "/A", "")
	if err != nil || pg.ETag != "etag-1" || !strings.Contains(pg.Content, "svc-orders") {
		t.Fatalf("sayfa: %+v %v", pg, err)
	}
	if _, err := s.GetWikiPage(ctx, "Platform", "w1", "/A", `"etag-1"`); !errors.Is(err, ErrWikiNotModified) {
		t.Fatalf("koşullu GET 304 → ErrWikiNotModified: %v", err)
	}
	vers, err := s.WikiItemVersions(ctx, ws[0])
	if err != nil || vers["/A.md"] != "abc" || len(vers) != 1 {
		t.Fatalf("öğe sürümleri: %v %v", vers, err)
	}
	if u := s.WikiPageWebURL("Platform", "Platform.wiki", "/A B", ""); !strings.Contains(u, "/DefaultCollection/Platform/_wiki/wikis/Platform.wiki?pagePath=%2FA+B") {
		t.Errorf("web url: %s", u)
	}
	if u := s.WikiPageWebURL("Platform", "Platform.wiki", "/A", "https://devops.example.test/x"); u != "https://devops.example.test/x" {
		t.Errorf("remoteUrl öncelikli: %s", u)
	}
}

func TestWikiClientPreviewVersionFallback(t *testing.T) {
	var versions []string
	s := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		v := r.URL.Query().Get("api-version")
		versions = append(versions, v)
		if !strings.HasSuffix(v, "-preview.1") {
			http.Error(w, `{"message":"The requested version \"6.0\" of the resource is under preview. The -preview flag must be supplied"}`, http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]any{"value": []map[string]any{}})
	})
	if _, err := s.ListWikis(context.Background(), "Platform"); err != nil {
		t.Fatalf("önizleme ekiyle yeniden denenmeli: %v (%v)", err, versions)
	}
	if len(versions) != 2 || versions[1] != "6.0-preview.1" {
		t.Errorf("sürüm sırası: %v", versions)
	}
	// Çalışan sürüm hatırlanır: ikinci çağrı ilk denemede tutar.
	versions = nil
	_, _ = s.ListWikis(context.Background(), "Platform")
	if len(versions) != 1 || versions[0] != "6.0-preview.1" {
		t.Errorf("hatırlanan sürüm önce denenmeli: %v", versions)
	}
}

func TestWikiClientAuthAndSignInErrors(t *testing.T) {
	s := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("echo pat-xyz"))
	})
	_, err := s.ListWikiProjects(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Wiki: Read") || strings.Contains(err.Error(), "pat-xyz") {
		t.Fatalf("401 kapsamı adlandırmalı ve PAT sızdırmamalı: %v", err)
	}
	s2 := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>Sign in</body></html>"))
	})
	if _, err := s2.ListWikiProjects(context.Background()); err == nil || !strings.Contains(err.Error(), "oturum açma") {
		t.Fatalf("HTML oturum açma sayfası hata olmalı: %v", err)
	}
	// Yapılandırılmamış servis.
	if _, err := New().ListWikiProjects(context.Background()); err == nil {
		t.Error("bağlantısız servis hata dönmeli")
	}
}

func TestSearchWikiPresentAbsentAndVersions(t *testing.T) {
	var body map[string]any
	present := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/DefaultCollection/_apis/search/wikisearchresults" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		jsonOK(w, map[string]any{"count": 1, "results": []map[string]any{{
			"path": "/Runbooks/Restart-svc%2Dorders.md", "wiki": map[string]string{"id": "w1", "name": "Platform.wiki", "mappedPath": "/"},
			"project": map[string]string{"name": "Platform"},
			"hits":    []map[string]any{{"highlights": []string{"restart <highlighthit>svc-orders</highlighthit>"}}},
		}}})
	})
	hits, err := present.SearchWiki(context.Background(), "svc-orders", "Platform", 5)
	if err != nil || len(hits) != 1 || hits[0].Path != "/Runbooks/Restart svc-orders" || hits[0].Snippet != "restart svc-orders" {
		t.Fatalf("arama: %+v %v", hits, err)
	}
	if f, _ := body["filters"].(map[string]any); f == nil || body["$top"].(float64) != 5 {
		t.Errorf("gövde: proje süzgeci ve $top: %v", body)
	}

	absent := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	if _, err := absent.SearchWiki(context.Background(), "x", "", 5); !errors.Is(err, ErrWikiSearchUnavailable) {
		t.Fatalf("404 → unavailable: %v", err)
	}
	var tried []string
	outOfRange := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		tried = append(tried, r.URL.Query().Get("api-version"))
		http.Error(w, "out of range", http.StatusBadRequest)
	})
	if _, err := outOfRange.SearchWiki(context.Background(), "x", "", 5); !errors.Is(err, ErrWikiSearchUnavailable) {
		t.Fatalf("tüm sürümler 400 → unavailable: %v", err)
	}
	if len(tried) != len(wikiSearchVersions) {
		t.Errorf("her sürüm bir kez denenmeli: %v", tried)
	}
	transient := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	if _, err := transient.SearchWiki(context.Background(), "x", "", 5); err == nil || errors.Is(err, ErrWikiSearchUnavailable) {
		t.Fatalf("502 geçici hatadır, unavailable değil: %v", err)
	}
}
