package wiki

// v0.10.1129 — operatör bildirimi: "12044 sayfa … — 3 hata". Kod wiki'sinde
// (git klasöründen yayımlanan) .md'siz klasörler ağaçta görünür ama GET 404
// döner; okuma tavanını aşan sayfa kesik JSON'la "beklenmeyen yanıt (sayfa
// değil)" oluyordu. İkisi de artık HATA değil "atlandı": ayrı sayaç + tavanlı
// örnek liste; çok büyük sayfanın eski içeriği korunur, 404 olan budanır.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/devops"
)

// hugeContent — okuma tavanını aşan Markdown (JSON kodlamasıyla tavan + pay).
func hugeContent() string {
	return "# Test verisi\n" + strings.Repeat("svc-orders satırı 0123456789\n", devops.WikiPageBodyCap/28+1024)
}

func codeWikiFake() *fakeDevOps {
	return &fakeDevOps{
		search: "absent",
		wikis: []*fakeWiki{{
			id: "w-code", name: "MobileSearch", project: "ChatBot", repo: "r-code",
			pages: map[string]*fakePage{
				// "/docker" ve "/docker/redis and commander" ara düğümler:
				// gitItemPath ".md" ile biter ama sayfa yok → GET 404.
				"/docker/redis and commander/compose": {content: "# compose\nsvc-orders redis bağlantısı.", obj: "c1", etag: "ce1"},
				"/Genel":                              {content: "# Genel\nsvc-orders mobil arama servisi.", obj: "c2", etag: "ce2"},
			},
			// Kod wiki'si klasörü: gitItemPath ".md"'siz; GET 404 → atlandı.
			folders: map[string]bool{"/scripts": true, "/scripts/add innerChannelType": true},
		}},
	}
}

func TestSyncSkipsContentlessFoldersWithoutError(t *testing.T) {
	f := codeWikiFake()
	s, st := newTestService(t, f)
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || !res.LastOK {
		t.Fatalf("içeriksiz klasör hata sayılmamalı: %+v", res.Errors)
	}
	// 2 ara düğüm + 2 kod klasörü, hepsi GET 404 = 4 içeriksiz (6 düğümde
	// 4 404 ama ağaç korkuluk tabanının (10) altında).
	if res.SkippedEmpty != 4 || res.SkippedLarge != 0 || len(res.Skipped) != 4 {
		t.Fatalf("atlanan sayaçları: empty=%d large=%d list=%+v", res.SkippedEmpty, res.SkippedLarge, res.Skipped)
	}
	if res.Fetched != 2 || len(st.pages) != 2 {
		t.Errorf("gerçek sayfalar indekslenmeli: fetched=%d pages=%d", res.Fetched, len(st.pages))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pageGets["404:w-code/scripts/add innerChannelType"] != 1 {
		t.Errorf("kod klasörü istenmeli ve 404 ile sınıflandırılmalı: %v", f.pageGets)
	}
	if f.pageGets["404:w-code/docker"] != 1 {
		t.Errorf("ara düğüm GET'i 404 ile sınıflandırılmalı: %v", f.pageGets)
	}
	for _, sk := range res.Skipped {
		if sk.Reason != SkipEmpty || !strings.HasPrefix(sk.Page, "ChatBot/MobileSearch/") {
			t.Errorf("atlanan künye: %+v", sk)
		}
	}
}

func TestSyncOversizedPageSkippedAndPreviousContentKept(t *testing.T) {
	f := codeWikiFake()
	f.wikis[0].folders = nil
	s, st := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := st.pages["w-code\x00/Genel"]
	if old.Content == "" {
		t.Fatal("ilk senkron sayfayı indekslemeli")
	}
	f.mu.Lock()
	f.wikis[0].pages["/Genel"] = &fakePage{content: hugeContent(), obj: "c2b", etag: "ce2b"}
	f.wikis[0].pages["/Yeni dev sayfa"] = &fakePage{content: hugeContent(), obj: "c3", etag: "ce3"}
	f.mu.Unlock()

	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("çok büyük sayfa hata sayılmamalı: %v", res.Errors)
	}
	if res.SkippedLarge != 2 {
		t.Fatalf("çok büyük sayaç: %+v", res)
	}
	large := 0
	for _, sk := range res.Skipped {
		if sk.Reason == SkipLarge {
			large++
		}
	}
	if large != 2 {
		t.Errorf("örnek liste: %+v", res.Skipped)
	}
	got, ok := st.pages["w-code\x00/Genel"]
	if !ok || got.Content != old.Content || got.Version != old.Version {
		t.Errorf("önceden indekslenen dev sayfa korunmalı (mezar taşı yok): ok=%v ver=%q", ok, got.Version)
	}
	if _, ok := st.pages["w-code\x00/Yeni dev sayfa"]; ok {
		t.Error("hiç indekslenmemiş dev sayfa yazılmamalı")
	}
}

func TestSync404PrunesPreviouslyIndexedPage(t *testing.T) {
	f := codeWikiFake()
	f.wikis[0].folders = nil
	s, st := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Ağaç hâlâ listeliyor (ara düğüm olarak) ama içerik gitti → 404.
	f.mu.Lock()
	f.wikis[0].pages["/docker/redis and commander/compose/alt"] = &fakePage{content: "# alt\nsvc-orders", obj: "c9", etag: "ce9"}
	delete(f.wikis[0].pages, "/docker/redis and commander/compose")
	f.mu.Unlock()
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || res.SkippedEmpty != 3 {
		t.Fatalf("404 atlandı sayılmalı: errors=%v empty=%d", res.Errors, res.SkippedEmpty)
	}
	if _, ok := st.pages["w-code\x00/docker/redis and commander/compose"]; ok {
		t.Error("404 dönen sayfa budanmalı")
	}
	if res.Deleted != 1 {
		t.Errorf("silinen: %d", res.Deleted)
	}
}

func TestSyncMany404SkipsPruneForThatWiki(t *testing.T) {
	f := codeWikiFake()
	f.wikis[0].folders = nil
	s, st := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// /Genel kaynaktan kalktı; ağaçta 10 içeriksiz klasör + 4 düğüm (3 ara
	// düğüm 404) → 13/14 404: budama bu geçişte ATLANIR.
	f.mu.Lock()
	delete(f.wikis[0].pages, "/Genel")
	f.wikis[0].folders = map[string]bool{}
	for i := 0; i < 10; i++ {
		f.wikis[0].folders["/scripts/k"+string(rune('a'+i))] = true
	}
	f.mu.Unlock()
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "çok sayıda 404 — budama atlandı") {
		t.Fatalf("tek korkuluk hatası beklenir: %v", res.Errors)
	}
	if _, ok := st.pages["w-code\x00/Genel"]; !ok || res.Deleted != 0 {
		t.Errorf("korkulukta budama yapılmamalı: deleted=%d", res.Deleted)
	}
}

func TestTooMany404(t *testing.T) {
	cases := []struct {
		nf, n int
		want  bool
	}{{0, 0, false}, {9, 9, false}, {5, 10, false}, {6, 10, true}, {4, 6, false}, {51, 100, true}}
	for _, c := range cases {
		if got := tooMany404(c.nf, c.n); got != c.want {
			t.Errorf("tooMany404(%d,%d)=%v", c.nf, c.n, got)
		}
	}
}

func TestStatusAddSkipCapped(t *testing.T) {
	var st Status
	for i := 0; i < statusSkippedMax+5; i++ {
		st.addSkip("P/W/x", SkipLarge)
	}
	st.addSkip("P/W/y", "")
	if st.SkippedLarge != statusSkippedMax+5 || st.SkippedEmpty != 1 || len(st.Skipped) != statusSkippedMax {
		t.Fatalf("tavan: %+v", st)
	}
}

func TestReadPageFriendlyErrors(t *testing.T) {
	f := codeWikiFake()
	f.wikis[0].pages["/Dev"] = &fakePage{content: hugeContent(), obj: "c5", etag: "ce5"}
	s, _ := newTestService(t, f)
	for _, mode := range []string{ModeHybrid, ModeLive} {
		s.Configure(Config{Enabled: true, Mode: mode})
		rec, err := s.ReadPage(context.Background(), "ChatBot", "MobileSearch", "/docker")
		if err != nil || rec != nil {
			t.Errorf("%s: 404 → bulunamadı (nil, nil) beklenir: %v", mode, err)
		}
		_, err = s.ReadPage(context.Background(), "ChatBot", "MobileSearch", "/Dev")
		if !errors.Is(err, ErrPageTooLarge) || strings.Contains(err.Error(), "{") {
			t.Errorf("%s: çok büyük sayfa anlaşılır hata vermeli: %v", mode, err)
		}
	}
}
