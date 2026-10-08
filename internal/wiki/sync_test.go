package wiki

// v0.10.1122 — artımlı senkron, silme güvenliği, canlı arama yedeği, lider
// kapısı ve hibrit arama — gerçek devops istemcisi sahte Azure DevOps'a karşı.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func runbookWiki() *fakeDevOps {
	return &fakeDevOps{
		search: "absent",
		wikis: []*fakeWiki{
			{id: "w-plat", name: "Platform.wiki", project: "Platform", repo: "r-plat", pages: map[string]*fakePage{
				"/Runbooks":                    {content: "# Runbooks\nNöbetçi ekip için işletim kılavuzlarının dizini burada tutulur.", obj: "o1", etag: "e1"},
				"/Runbooks/Restart svc-orders": {content: "# Yeniden başlatma\n## Adımlar\nkubectl rollout restart deploy/svc-orders -n orders komutunu çalıştırın ve ERR-1042 hata oranını izleyin.", obj: "o2", etag: "e2"},
				"/Mimari":                      {content: "# Mimari\nÖdeme akışı svc-payments ve svc-ledger servisleri arasında kuyruklar üzerinden ilerler.", obj: "o3", etag: "e3"},
			}},
			{id: "w-pay", name: "Payments.wiki", project: "Payments", repo: "r-pay", pages: map[string]*fakePage{
				"/Sahiplik": {content: "# Sahiplik\nsvc-ledger servisinin sahibi defter ekibidir; nöbet kanalı ledger-oncall.", obj: "o4", etag: "e4"},
			}},
		},
	}
}

func newTestService(t *testing.T, f *fakeDevOps) (*Service, *memStore) {
	dv, _ := newFakeDevOps(t, f)
	st := newMemStore()
	s := New(st, func() API { return dv }, nil)
	s.rps = 1000
	s.Configure(Config{Enabled: true})
	return s, st
}

func TestSyncInitialIndexesAllPages(t *testing.T) {
	f := runbookWiki()
	s, st := newTestService(t, f)
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Projects != 2 || res.Wikis != 2 || res.Pages != 4 || res.Fetched != 4 || len(res.Errors) != 0 {
		t.Fatalf("ilk senkron: %+v", res)
	}
	if res.IndexedPages != 4 || res.IndexedChunks == 0 || !res.LastOK || res.LastFinishedAt == 0 {
		t.Errorf("durum özeti: %+v", res)
	}
	p := st.pages["w-plat\x00/Runbooks/Restart svc-orders"]
	if p.Version != "obj:o2" || p.Title != "Restart svc-orders" || !strings.HasPrefix(p.URL, "https://devops.example.test/") {
		t.Errorf("sayfa künyesi: %+v", p)
	}
	// Durum CH blobunda (başka pod'lar da görür).
	var persisted Status
	if err := json.Unmarshal(st.settings[StatusKey], &persisted); err != nil || persisted.Fetched != 4 {
		t.Errorf("durum blobu yazılmadı: %v %+v", err, persisted)
	}
}

func TestSyncIncrementalObjectIDSkipsUnchanged(t *testing.T) {
	f := runbookWiki()
	s, st := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	before := len(f.pageGets)
	gets := 0
	for _, n := range f.pageGets {
		gets += n
	}
	f.wikis[0].pages["/Mimari"].content += "\nYeni bölüm: svc-ledger geri alma."
	f.wikis[0].pages["/Mimari"].obj = "o3b"
	f.mu.Unlock()
	upserts := st.upserts

	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Fetched != 1 || res.Unchanged != 3 {
		t.Fatalf("yalnız değişen sayfa okunmalı: %+v", res)
	}
	f.mu.Lock()
	gets2 := 0
	for _, n := range f.pageGets {
		gets2 += n
	}
	f.mu.Unlock()
	if gets2-gets != 1 || before != 4 {
		t.Errorf("değişmeyen sayfa İSTENMEMELİ: %d yeni GET", gets2-gets)
	}
	if st.upserts-upserts != 1 || st.pages["w-plat\x00/Mimari"].Version != "obj:o3b" {
		t.Errorf("değişen sayfa yeni sürümle yazılmalı: upserts=%d", st.upserts-upserts)
	}
}

func TestSyncConditionalGETWhenItemsUnavailable(t *testing.T) {
	f := runbookWiki()
	f.itemsOff = true
	s, st := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := st.pages["w-pay\x00/Sahiplik"].Version; v != "etag:e4" {
		t.Fatalf("öğe listesi yokken ETag saklanmalı: %q", v)
	}
	f.mu.Lock()
	f.wikis[1].pages["/Sahiplik"].content = "# Sahiplik\nsvc-ledger artık mutabakat ekibinde; nöbet kanalı recon-oncall."
	f.wikis[1].pages["/Sahiplik"].etag = "e4b"
	f.mu.Unlock()
	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Fetched != 1 || res.Unchanged != 3 || f.condHits != 3 {
		t.Fatalf("koşullu GET: değişmeyen 3 sayfa 304 almalı: %+v condHits=%d", res, f.condHits)
	}
	if !strings.Contains(st.pages["w-pay\x00/Sahiplik"].Content, "recon-oncall") {
		t.Error("ETag'i değişen sayfanın yeni içeriği yazılmalı")
	}
}

func TestSyncDeletesRemovedPagesAndOutOfScopeWikis(t *testing.T) {
	f := runbookWiki()
	s, st := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.wikis[0].pages, "/Mimari")
	f.mu.Unlock()
	res, _ := s.Sync(context.Background())
	if res.Deleted != 1 {
		t.Fatalf("kaynakta silinen sayfa budanmalı: %+v", res)
	}
	if _, ok := st.pages["w-plat\x00/Mimari"]; ok {
		t.Error("silinen sayfa depoda kaldı")
	}
	// İzin listesi daralınca kapsam dışı wiki'nin sayfaları budanır.
	s.Configure(Config{Enabled: true, Projects: []string{"Platform"}})
	res, _ = s.Sync(context.Background())
	if res.Deleted != 1 || res.Projects != 1 {
		t.Fatalf("kapsamdan çıkan wiki budanmalı: %+v", res)
	}
	if _, ok := st.pages["w-pay\x00/Sahiplik"]; ok {
		t.Error("kapsam dışı wiki sayfası kaldı")
	}
}

func TestSyncTransientTreeErrorDoesNotPrune(t *testing.T) {
	f := runbookWiki()
	s, st := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.treeFailsWiki = "w-plat"
	f.mu.Unlock()
	res, _ := s.Sync(context.Background())
	if res.Deleted != 0 || len(res.Errors) == 0 || res.LastOK {
		t.Fatalf("geçici ağaç hatası budama YAPMAMALI ve raporlanmalı: %+v", res)
	}
	if len(st.pages) != 4 {
		t.Errorf("indeks boşaltıldı: %d sayfa", len(st.pages))
	}
}

func TestSyncPageCapTruncatesWithoutPruning(t *testing.T) {
	f := runbookWiki()
	s, st := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Configure(Config{Enabled: true, MaxPages: 2})
	res, _ := s.Sync(context.Background())
	if !res.Truncated || res.Deleted != 0 {
		t.Fatalf("tavan: truncated + budama yok: %+v", res)
	}
	if len(st.pages) != 4 {
		t.Errorf("tavan sonrası sayfalar silinmemeli: %d", len(st.pages))
	}
}

func TestSyncUnauthorizedAndSignInPageReported(t *testing.T) {
	f := runbookWiki()
	dv, _ := newFakeDevOps(t, f)
	cfg := dv.CurrentSettings()
	cfg.PAT = "wrong-pat"
	dv.Configure(cfg)
	st := newMemStore()
	s := New(st, func() API { return dv }, nil)
	s.Configure(Config{Enabled: true})
	res, _ := s.Sync(context.Background())
	if res.LastOK || len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "Wiki: Read") {
		t.Fatalf("401 Wiki (Read) kapsamını adlandırmalı: %+v", res.Errors)
	}
	for _, e := range res.Errors {
		if strings.Contains(e, "wrong-pat") {
			t.Fatal("PAT hata metnine sızdı")
		}
	}
	f.mu.Lock()
	f.htmlSignIn = true
	f.mu.Unlock()
	cfg.PAT = fakePAT
	dv.Configure(cfg)
	res, _ = s.Sync(context.Background())
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "oturum açma") {
		t.Fatalf("HTML oturum açma sayfası hata olmalı: %+v", res.Errors)
	}
}

func TestSyncDueAndLeaderOnlyLoop(t *testing.T) {
	now := time.UnixMilli(10_000_000)
	cfg := Config{Enabled: true}
	if !SyncDue(cfg, Status{}, now) {
		t.Error("hiç koşmamışsa due")
	}
	ran := Status{LastStartedAt: now.Add(-30 * time.Minute).UnixMilli()}
	if SyncDue(cfg, ran, now) {
		t.Error("60 dk aralıkta 30 dk sonra due olmamalı")
	}
	ran.RequestedAt = now.UnixMilli()
	if !SyncDue(cfg, ran, now) {
		t.Error("manuel istek due yapmalı")
	}
	if SyncDue(Config{}, Status{}, now) {
		t.Error("kapalıyken asla")
	}
	if (Config{IntervalMin: 5}).Interval() != 15*time.Minute {
		t.Error("aralık alt sınırı 15 dk")
	}

	// Lider değilken döngü DevOps'a hiç dokunmaz; lider olunca koşar.
	f := runbookWiki()
	s, st := newTestService(t, f)
	s.tick = 5 * time.Millisecond
	var leader atomic.Bool
	var asked atomic.Int32
	var reloads atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunLoop(ctx, func() bool { asked.Add(1); return leader.Load() }, func(context.Context) { reloads.Add(1) })
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for asked.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	f.mu.Lock()
	reqs := f.requests
	f.mu.Unlock()
	if reqs != 0 || reloads.Load() != 0 {
		t.Errorf("lider olmayan pod %d istek attı / %d ayar okudu", reqs, reloads.Load())
	}
	leader.Store(true)
	for time.Now().Before(deadline) {
		st.mu.Lock()
		n := len(st.pages)
		st.mu.Unlock()
		if n == 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if len(st.pages) != 4 || reloads.Load() == 0 {
		t.Errorf("lider pod senkronu koşmalı: %d sayfa, %d reload", len(st.pages), reloads.Load())
	}
}

func TestSyncSingleFlightInProcess(t *testing.T) {
	f := runbookWiki()
	s, _ := newTestService(t, f)
	s.runMu.Lock()
	s.running = true
	s.runMu.Unlock()
	if _, err := s.Sync(context.Background()); !errors.Is(err, ErrSyncRunning) {
		t.Fatalf("eşzamanlı ikinci geçiş reddedilmeli: %v", err)
	}
}

func TestSearchLocalExactTermAndScope(t *testing.T) {
	f := runbookWiki()
	s, _ := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(context.Background(), "svc-orders nasıl restart edilir", "", 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Hits[0].Path != "/Runbooks/Restart svc-orders" || res.Mode != "lexical" {
		t.Fatalf("tam teknik terim en üstte: %+v", res.Hits)
	}
	if res.Hits[0].URL == "" || res.Live {
		t.Errorf("url dolu, canlı yedek gereksiz: %+v", res)
	}
	if _, err := s.Search(context.Background(), "nasıl ne", "", 5, 1); !errors.Is(err, ErrNoTerms) {
		t.Error("terimsiz sorgu ErrNoTerms")
	}
	s.Configure(Config{Enabled: true, Projects: []string{"Payments"}})
	if _, err := s.Search(context.Background(), "svc-orders", "Platform", 5, 1); !errors.Is(err, ErrOutOfScope) {
		t.Error("kapsam dışı proje reddedilmeli")
	}
	res, _ = s.Search(context.Background(), "svc-orders restart", "", 5, 1)
	for _, h := range res.Hits {
		if h.Project != "Payments" {
			t.Errorf("izin listesi dışı sonuç: %+v", h)
		}
	}
}

func TestSearchLiveFallbackPresentAndAbsent(t *testing.T) {
	// İndeks BOŞ (senkron koşmadı) → canlı arama denenir.
	f := runbookWiki()
	f.search = "present"
	s, st := newTestService(t, f)
	res, err := s.Search(context.Background(), "ledger-oncall", "", 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Live || len(res.Hits) == 0 || res.Hits[0].Path != "/Sahiplik" || !res.Hits[0].Live {
		t.Fatalf("canlı yedek sayfayı bulmalı: %+v", res)
	}
	if _, ok := st.pages["w-pay\x00/Sahiplik"]; !ok {
		t.Error("canlı okunan sayfa yerel depoya yazılmalı")
	}
	if s.searchStateNow() != SearchAvailable {
		t.Errorf("arama durumu: %s", s.searchStateNow())
	}

	// Uç yok (404) → unavailable önbelleğe alınır, ikinci sorgu uca GİTMEZ.
	g := runbookWiki()
	g.search = "absent"
	s2, _ := newTestService(t, g)
	r1, _ := s2.Search(context.Background(), "ledger-oncall", "", 5, 1)
	if r1.Live || !strings.Contains(r1.LiveNote, "Search") {
		t.Fatalf("uç yokken not: %+v", r1)
	}
	_, _ = s2.Search(context.Background(), "baska-terim", "", 5, 1)
	g.mu.Lock()
	calls := g.searchCalls
	g.mu.Unlock()
	if calls > len(wikiSearchVersionsForTest()) || s2.searchStateNow() != SearchUnavailable {
		t.Errorf("unavailable önbelleğe alınmalı: %d çağrı, durum %s", calls, s2.searchStateNow())
	}
	// DisableLiveSearch → hiç denenmez.
	h := runbookWiki()
	h.search = "present"
	s3, _ := newTestService(t, h)
	s3.Configure(Config{Enabled: true, DisableLiveSearch: true})
	_, _ = s3.Search(context.Background(), "ledger-oncall", "", 5, 1)
	if h.searchCalls != 0 {
		t.Error("canlı arama kapalıyken çağrılmamalı")
	}
}

// wikiSearchVersionsForTest — 404 ilk sürümde döner; tek çağrı beklenir.
func wikiSearchVersionsForTest() []string { return []string{"7.0"} }

func TestSearchLiveModeOnStaleOnly(t *testing.T) {
	f := runbookWiki()
	f.search = "present"
	s, _ := newTestService(t, f)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Taze indeks + zayıf sonuç: LiveOnStale canlıya gitmez, LiveOnWeak gider.
	_, _ = s.SearchWith(context.Background(), "tamamen bilinmeyen kavram", "", SearchOptions{Limit: 3, PerPage: 1, Live: LiveOnStale})
	if f.searchCalls != 0 {
		t.Fatalf("RAG kademesi (LiveOnStale) taze indekste canlı arama yapmamalı: %d", f.searchCalls)
	}
	_, _ = s.SearchWith(context.Background(), "tamamen bilinmeyen kavram", "", SearchOptions{Limit: 3, PerPage: 1, Live: LiveOnWeak})
	if f.searchCalls == 0 {
		t.Fatal("araç yolu (LiveOnWeak) zayıf sonuçta canlı aramayı denemeli")
	}
}

func TestSearchHybridWithEmbeddings(t *testing.T) {
	f := runbookWiki()
	dv, _ := newFakeDevOps(t, f)
	st := newMemStore()
	// Sahte embedder: "ödeme"/"payment" geçen metin [1,0], diğerleri [0,1].
	emb := func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, t := range texts {
			if strings.Contains(Fold(t), "odeme") || strings.Contains(Fold(t), "payment") {
				out[i] = []float32{1, 0}
			} else {
				out[i] = []float32{0, 1}
			}
		}
		return out, nil
	}
	s := New(st, func() API { return dv }, func() Embedder { return emb })
	s.rps = 1000
	s.Configure(Config{Enabled: true})
	res0, err := s.Sync(context.Background())
	if err != nil || !res0.Embedded {
		t.Fatalf("embedding üretilmeli: %v %+v", err, res0)
	}
	// "payment flow" sözcükleri korpusta yok (lexical boş) → semantik bulur.
	res, err := s.Search(context.Background(), "payment flow", "", 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "hybrid" || len(res.Hits) == 0 || res.Hits[0].Path != "/Mimari" {
		t.Fatalf("hibrit semantik isabet: %+v", res)
	}
}

func TestReadPageLocalLiveAndScope(t *testing.T) {
	f := runbookWiki()
	s, st := newTestService(t, f)
	// Yerelde yok → API'den okunur ve yazılır.
	rec, err := s.ReadPage(context.Background(), "Platform", "Platform.wiki", "Runbooks/Restart svc-orders")
	if err != nil || rec == nil || !strings.Contains(rec.Content, "rollout restart") {
		t.Fatalf("canlı okuma: %v %+v", err, rec)
	}
	if _, ok := st.pages["w-plat\x00/Runbooks/Restart svc-orders"]; !ok {
		t.Error("okunan sayfa yerel depoya yazılmalı")
	}
	if rec, err := s.ReadPage(context.Background(), "Platform", "Platform.wiki", "/Yok"); err == nil && rec != nil {
		t.Error("olmayan sayfa nil dönmeli")
	}
	s.Configure(Config{Enabled: true, Wikis: []string{"Payments/Payments.wiki"}})
	if _, err := s.ReadPage(context.Background(), "Platform", "Platform.wiki", "/Mimari"); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("kapsam dışı wiki reddedilmeli: %v", err)
	}
}

func TestConfigNormalizeAndAllowlist(t *testing.T) {
	c := Config{Projects: []string{" Platform ", "platform", ""}, Wikis: []string{"Payments/Payments.wiki"}, IntervalMin: 3, MaxPages: 999999}.Normalize()
	if len(c.Projects) != 1 || c.IntervalMin != MinIntervalMin || c.MaxPages != MaxMaxPages {
		t.Fatalf("normalize: %+v", c)
	}
	if (Config{}).PageCap() != DefaultMaxPages || (Config{}).Interval() != time.Hour {
		t.Error("varsayılanlar: 5000 sayfa, 60 dk")
	}
	a := Config{Projects: []string{"platform"}, Wikis: []string{"Docs.wiki", "Payments/Payments.wiki"}}
	if !a.WikiAllowed("Platform", "docs.wiki") || a.WikiAllowed("Payments", "Payments.wiki") || a.WikiAllowed("Platform", "Other") {
		t.Error("izin listesi: proje VE wiki eşleşmeli (harf-duyarsız)")
	}
}

func TestSearchLiveTransientErrorBacksOff(t *testing.T) {
	f := runbookWiki()
	f.search = "error" // 502: geçici
	s, _ := newTestService(t, f)
	clock := time.UnixMilli(50_000_000)
	s.now = func() time.Time { return clock }
	r1, _ := s.Search(context.Background(), "ledger-oncall", "", 5, 1)
	if r1.Live || !strings.Contains(r1.LiveNote, "15 dk") {
		t.Fatalf("geçici hata notu: %+v", r1)
	}
	_, _ = s.Search(context.Background(), "baska-terim", "", 5, 1)
	f.mu.Lock()
	calls := f.searchCalls
	f.mu.Unlock()
	if calls != 1 {
		t.Fatalf("geçici hatadan sonra 15 dk canlı arama denenmemeli: %d çağrı", calls)
	}
	if s.searchStateNow() == SearchUnavailable {
		t.Error("geçici hata 'unavailable' kararı DEĞİL")
	}
	clock = clock.Add(16 * time.Minute)
	_, _ = s.Search(context.Background(), "baska-terim", "", 5, 1)
	f.mu.Lock()
	calls = f.searchCalls
	f.mu.Unlock()
	if calls != 2 {
		t.Errorf("geri çekilme bitince yeniden denenmeli: %d", calls)
	}
}

func TestSearchNoSemanticSkipsEmbedding(t *testing.T) {
	f := runbookWiki()
	dv, _ := newFakeDevOps(t, f)
	st := newMemStore()
	var embeds atomic.Int32
	emb := func(_ context.Context, texts []string) ([][]float32, error) {
		embeds.Add(1)
		out := make([][]float32, len(texts))
		for i := range texts {
			out[i] = []float32{1, 0}
		}
		return out, nil
	}
	s := New(st, func() API { return dv }, func() Embedder { return emb })
	s.rps = 1000
	s.Configure(Config{Enabled: true})
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := embeds.Load()
	res, err := s.SearchWith(context.Background(), "svc-orders restart", "", SearchOptions{Limit: 3, PerPage: 2, Live: LiveOnStale, NoSemantic: true})
	if err != nil || res.Mode != "lexical" || embeds.Load() != before {
		t.Fatalf("RAG kademesi soruyu embed etmemeli ve kosinüs taramamalı: mode=%s embeds=%d→%d err=%v", res.Mode, before, embeds.Load(), err)
	}
}

func TestSyncCancelledWhenLeadershipLost(t *testing.T) {
	f := runbookWiki()
	s, st := newTestService(t, f)
	s.rps = 4 // istekler 250 ms aralıklı: geçiş ~2 sn sürer
	s.watch = 10 * time.Millisecond
	t0 := time.Now()
	res, _ := s.syncWhileLeader(context.Background(), func() bool { return false })
	if time.Since(t0) > time.Second {
		t.Fatalf("liderlik kaybı geçişi kesmeli: %v sürdü", time.Since(t0))
	}
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e, "iptal") || strings.Contains(e, "context canceled") {
			found = true
		}
	}
	if !found || len(st.pages) == 4 {
		t.Errorf("iptal raporlanmalı, geçiş yarıda kalmalı: %+v (%d sayfa)", res.Errors, len(st.pages))
	}
}
