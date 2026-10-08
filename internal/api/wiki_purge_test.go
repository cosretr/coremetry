package api

// v0.10.1124 inceleme F4 — "İndeksi temizle": yalnız oturumlu yönetici,
// audit'li, durum sayıları sıfırlanır. Sayfa listesi yalnız http(s) URL döner.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/wiki"
)

type fakePurger struct{ calls int }

func (f *fakePurger) WikiPurge(context.Context) error { f.calls++; return nil }

func TestWikiPurgeEndpoint(t *testing.T) {
	withWikiService(t, wiki.Config{Enabled: true, Mode: wiki.ModeLive}, &fakeWikiAPI{pages: map[string]string{}})
	fp := &fakePurger{}
	prev := wikiPurgeSource
	wikiPurgeSource = func(*Server) wikiPurger { return fp }
	t.Cleanup(func() { wikiPurgeSource = prev })
	srv := &Server{auditQ: make(chan chstore.AuditEntry, 4)}
	mux := srv.buildMux()
	for _, c := range []*auth.Claims{
		{UserID: "u-1", Role: auth.RoleViewer},
		{UserID: "u-2", Role: auth.RoleEditor},
		{UserID: "token:t1", Role: auth.RoleAdmin},
	} {
		if rec := doWikiReq(mux, "POST", "/api/wiki/purge", "", c); rec.Code != http.StatusForbidden {
			t.Errorf("%+v: %d (403 bekleniyordu)", c, rec.Code)
		}
	}
	if fp.calls != 0 {
		t.Fatal("yetkisiz çağrı temizlememeli")
	}
	rec := doWikiReq(mux, "POST", "/api/wiki/purge", "", &auth.Claims{UserID: "u-9", Role: auth.RoleAdmin})
	var out struct {
		Purged bool `json:"purged"`
		Status struct {
			IndexedPages int `json:"indexedPages"`
		} `json:"status"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.Purged || fp.calls != 1 {
		t.Fatalf("admin temizlik: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case e := <-srv.auditQ:
		if e.Action != "wiki.purge" {
			t.Errorf("audit: %+v", e)
		}
	default:
		t.Error("temizlik audit satırı yazmalı")
	}
}

type schemeLister struct{}

func (schemeLister) WikiListPages(context.Context, string, int, int, bool) ([]chstore.WikiPageRow, uint64, error) {
	return []chstore.WikiPageRow{
		{Title: "ok", URL: "https://devops.example.test/a"},
		{Title: "kötü", URL: "javascript:alert(1)"},
		{Title: "veri", URL: " DATA:text/html,x"},
	}, 3, nil
}

func TestWikiPagesOnlyHTTPURLs(t *testing.T) {
	withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{}})
	prev := wikiPagesSource
	wikiPagesSource = func(*Server) wikiPageLister { return schemeLister{} }
	t.Cleanup(func() { wikiPagesSource = prev })
	rec := doWikiReq((&Server{}).buildMux(), "GET", "/api/wiki/pages", "", &auth.Claims{UserID: "u-1", Role: auth.RoleViewer})
	var out struct {
		Rows []chstore.WikiPageRow `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Rows) != 3 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if out.Rows[0].URL == "" || out.Rows[1].URL != "" || out.Rows[2].URL != "" {
		t.Errorf("yalnız http(s) URL kalmalı: %+v", out.Rows)
	}
}
