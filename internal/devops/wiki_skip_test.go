package devops

// v0.10.1129 — kod wiki'si .md'siz klasör düğümü ağaçta KALIR (ön-atlama
// yok: gitItemPath biçimi sunucu sürümüne göre değişebilir), 404 →
// ErrWikiPageNotFound (ham ADO gövdesi yok), tavanı aşan sayfa →
// ErrWikiPageTooLarge (kesik JSON "beklenmeyen yanıt" değil). Adlar sentetik.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestFlattenWikiTreeKeepsCodeWikiFolders(t *testing.T) {
	body := []byte(`{"path":"/","subPages":[
		{"path":"/docker","gitItemPath":"/docker","isParentPage":true,"subPages":[
			{"path":"/docker/redis and commander","gitItemPath":"/docker/redis and commander","isParentPage":true},
			{"path":"/docker/compose","gitItemPath":"/docker/compose.md"}]},
		{"path":"/Genel","gitItemPath":"/Genel.MD"},
		{"path":"/Bilinmez"}]}`)
	refs, _, err := flattenWikiTree(body, 0)
	if err != nil || len(refs) != 5 {
		t.Fatalf("ağaç: %+v %v", refs, err)
	}
	if refs[0].Path != "/docker" || refs[0].GitItemPath != "/docker" {
		t.Errorf(".md'siz klasör düğümü listede kalmalı (404 yedeği sınıflandırır): %+v", refs[0])
	}
}

func TestGetWikiPageNotFoundAndTooLarge(t *testing.T) {
	big := strings.Repeat("x", WikiPageBodyCap)
	exact := strings.Repeat("y", WikiPageBodyCap-64)
	s := wikiTestService(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("path") {
		case "/docker/redis and commander":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Wiki page '/docker/redis and commander' could not be found."}`))
		case "/Dev":
			jsonOK(w, map[string]any{"path": "/Dev", "content": big})
		case "/Sinirda":
			// Gövde tavanın altında kalır (içerik + JSON zarfı ≤ tavan).
			jsonOK(w, map[string]any{"path": "/Sinirda", "content": exact})
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	_, err := s.GetWikiPage(ctx, "ChatBot", "w1", "/docker/redis and commander", "")
	if !errors.Is(err, ErrWikiPageNotFound) || strings.Contains(err.Error(), "message") {
		t.Fatalf("404 → ErrWikiPageNotFound (ham gövdesiz): %v", err)
	}
	_, err = s.GetWikiPage(ctx, "ChatBot", "w1", "/Dev", "")
	if !errors.Is(err, ErrWikiPageTooLarge) {
		t.Fatalf("tavanı aşan → ErrWikiPageTooLarge: %v", err)
	}
	pg, err := s.GetWikiPage(ctx, "ChatBot", "w1", "/Sinirda", "")
	if err != nil || len(pg.Content) != len(exact) {
		t.Fatalf("tavan altı sayfa okunmalı: %v len=%d", err, len(pg.Content))
	}
}
