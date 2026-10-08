package wiki

// v0.10.1124 — canlı yedeğin uçtan uca davranışı sahte Azure DevOps Server'a
// (on-prem şekli, koleksiyon adresi) karşı: soru sözcüksüz sorgu, AND→OR,
// Türkçe ek uyuşmazlığında taban, sürümle ilgisiz 400, paylaşılan durum,
// canlı mod (CH'ye yazmaz, önbellek), "Search yok" mesajı, tanı. Adlar sentetik.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// RAG tabanı (api.ragWikiFloor) — paket sınırı yüzünden burada kopya.
const testRAGFloor = 0.5

func TestLiveFallbackEmptyIndexTurkishQuestionPassesFloor(t *testing.T) {
	f := runbookWiki()
	f.search = "present"
	s, _ := newTestService(t, f)
	// İndeks BOŞ (senkron koşmadı). Soru eklerle: "servisinin", "başlatılması".
	q := "svc-orders servisinin yeniden başlatılması nasıl yapılır?"
	res, err := s.SearchWith(context.Background(), q, "", SearchOptions{Limit: 3, PerPage: 2, Live: LiveOnStale, NoSemantic: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Live || len(res.Hits) == 0 || res.Hits[0].Path != "/Runbooks/Restart svc-orders" {
		t.Fatalf("canlı yedek sayfayı bulmalı: %+v", res)
	}
	if res.Hits[0].Score < testRAGFloor {
		t.Fatalf("ADO'nun bulduğu ilk sayfa RAG tabanının altına düşmemeli: %.3f", res.Hits[0].Score)
	}
	f.mu.Lock()
	texts := append([]string(nil), f.searchTexts...)
	f.mu.Unlock()
	if len(texts) != 2 || strings.Contains(Fold(texts[0]), "nasil") || !strings.Contains(texts[1], " OR ") {
		t.Fatalf("önce soru sözcüksüz AND, boşsa OR: %q", texts)
	}
}

func TestLiveSearchBadRequestIsNotUnavailable(t *testing.T) {
	f := runbookWiki()
	f.search = "badreq"
	s, _ := newTestService(t, f)
	res, _ := s.Search(context.Background(), "ledger-oncall", "", 5, 1)
	if res.SearchUnavailable || s.searchStateNow() == SearchUnavailable {
		t.Fatalf("sürümle ilgisiz 400 'unavailable' kararı DEĞİL: %+v", res)
	}
	if res.LiveNote != NoteLiveSearchFailed || res.Diag == nil || !strings.Contains(res.Diag.Error, "400") {
		t.Errorf("sohbete genel not, ayrıntı yalnız tanıda: %q %+v", res.LiveNote, res.Diag)
	}
	f.mu.Lock()
	calls := f.searchCalls
	f.mu.Unlock()
	if calls != 1 {
		t.Errorf("sürüm reddi olmayan 400'de diğer sürümler denenmemeli: %d", calls)
	}
	// İnceleme F3: sorguya özgü 400 GENEL geri çekilme kurmaz — sonraki soru uca gider.
	_, _ = s.Search(context.Background(), "baska-terim", "", 5, 1)
	f.mu.Lock()
	calls = f.searchCalls
	f.mu.Unlock()
	if calls != 2 {
		t.Errorf("bad_request sonrası canlı arama susturulmamalı: %d çağrı", calls)
	}
	// AND 400 → bir kez düz OR denemesi.
	_, _ = s.Search(context.Background(), "ledger-oncall nöbet", "", 5, 1)
	f.mu.Lock()
	texts := append([]string(nil), f.searchTexts...)
	f.mu.Unlock()
	if len(texts) < 4 || !strings.Contains(texts[len(texts)-1], " OR ") {
		t.Errorf("AND sorgusu 400 alınca OR bir kez denenmeli: %q", texts)
	}
}

func TestSearchStatusSharedAcrossPods(t *testing.T) {
	f := runbookWiki()
	f.search = "present"
	dv, _ := newFakeDevOps(t, f)
	st := newMemStore()
	podA := New(st, func() API { return dv }, nil)
	podA.Configure(Config{Enabled: true})
	podB := New(st, func() API { return dv }, nil) // senkron lideri; hiç aramadı
	podB.Configure(Config{Enabled: true})
	if got := podB.Status(context.Background(), true).Search; got != SearchUnknown {
		t.Fatalf("başlangıç: %s", got)
	}
	if _, err := podA.Search(context.Background(), "ledger-oncall", "", 5, 1); err != nil {
		t.Fatal(err)
	}
	stB := podB.Status(context.Background(), true)
	if stB.Search != SearchAvailable || stB.SearchLast == nil || stB.SearchLast.APIVersion == "" || stB.SearchLast.Hits == 0 {
		t.Fatalf("B pod'u A'nın son aramasını görmeli: %+v %+v", stB.Search, stB.SearchLast)
	}
	// Senkron blobu arama kopyasını taşımaz.
	raw, _ := st.GetSetting(context.Background(), StatusKey)
	if strings.Contains(string(raw), "searchLast") {
		t.Errorf("senkron blobuna arama kopyası yazılmamalı: %s", raw)
	}
}

func TestLiveModeNoSyncNoCHWritesAndCache(t *testing.T) {
	f := runbookWiki()
	f.search = "present"
	s, st := newTestService(t, f)
	s.Configure(Config{Enabled: true, Mode: ModeLive})
	if SyncDue(s.Config(), Status{}, s.now()) {
		t.Fatal("canlı modda senkron hiç başlamamalı")
	}
	res, err := s.Search(context.Background(), "ledger-oncall nöbet kanalı", "", 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !res.LiveOnly || !res.Live || len(res.Hits) == 0 || res.Hits[0].Path != "/Sahiplik" {
		t.Fatalf("canlı mod doğrudan Search'ten bulmalı: %+v", res)
	}
	st.mu.Lock()
	ups, n := st.upserts, len(st.pages)
	st.mu.Unlock()
	if ups != 0 || n != 0 {
		t.Fatalf("canlı mod CH'ye YAZMAMALI: upserts=%d pages=%d", ups, n)
	}
	f.mu.Lock()
	gets := f.pageGets["w-pay/Sahiplik"]
	f.mu.Unlock()
	_, _ = s.Search(context.Background(), "ledger-oncall nöbet kanalı", "", 5, 1)
	rec, err := s.ReadPage(context.Background(), "Payments", "Payments.wiki", "/Sahiplik")
	if err != nil || rec == nil || !strings.Contains(rec.Content, "ledger-oncall") {
		t.Fatalf("read_wiki_page canlı modda önbellekten/API'den okumalı: %v %+v", err, rec)
	}
	f.mu.Lock()
	gets2 := f.pageGets["w-pay/Sahiplik"]
	f.mu.Unlock()
	if gets != 1 || gets2 != 1 {
		t.Errorf("tekrar eden okuma bellek önbelleğinden gelmeli: ilk=%d sonra=%d", gets, gets2)
	}
	// RAG kademesinin serbest-soru yolu (LiveOnStale) canlı modda wiki'ye gitmez.
	f.mu.Lock()
	before := f.searchCalls
	f.mu.Unlock()
	r2, _ := s.SearchWith(context.Background(), "ledger-oncall", "", SearchOptions{Limit: 3, PerPage: 1, Live: LiveOnStale})
	f.mu.Lock()
	after := f.searchCalls
	f.mu.Unlock()
	if after != before || len(r2.Hits) != 0 {
		t.Errorf("canlı modda işaretsiz serbest soru canlı arama yapmamalı: %d→%d", before, after)
	}
}

func TestLiveModeSearchUnavailableMessage(t *testing.T) {
	f := runbookWiki()
	f.search = "absent"
	s, _ := newTestService(t, f)
	s.Configure(Config{Enabled: true, Mode: ModeLive})
	res, _ := s.Search(context.Background(), "ledger-oncall", "", 5, 1)
	if !res.SearchUnavailable || res.LiveNote != NoteSearchUnavailableLive {
		t.Fatalf("canlı modda Search yok mesajı: %+v", res)
	}
	// İkinci soru uca gitmez ama mesaj aynı (önbelleğe alınmış karar).
	res2, _ := s.Search(context.Background(), "baska-terim", "", 5, 1)
	if !res2.SearchUnavailable || res2.LiveNote != NoteSearchUnavailableLive {
		t.Fatalf("önbellekli karar da açıkça söylenmeli: %+v", res2)
	}
}

func TestSyncModeNeverSearchesLive(t *testing.T) {
	f := runbookWiki()
	f.search = "present"
	s, _ := newTestService(t, f)
	s.Configure(Config{Enabled: true, Mode: ModeSync})
	_, _ = s.Search(context.Background(), "ledger-oncall", "", 5, 1)
	if f.searchCalls != 0 {
		t.Fatal("yalnız senkron modunda canlı arama yok")
	}
	if !(Config{Mode: "LIVE "}).Normalize().LiveSearchAllowed() || (Config{Mode: "bogus"}).Normalize().EffectiveMode() != ModeHybrid {
		t.Error("mod normalizasyonu")
	}
	if (Config{DisableLiveSearch: true}).EffectiveMode() != ModeSync {
		t.Error("eski bayrak mod boşken sync demek")
	}
}

func TestDiagnoseShape(t *testing.T) {
	f := runbookWiki()
	f.search = "present"
	s, _ := newTestService(t, f)
	d, err := s.Diagnose(context.Background(), "ledger-oncall sahibi kimdir", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Live.Attempted || d.Live.Info.Class != "ok" || d.Live.Info.APIVersion == "" || len(d.Final) == 0 {
		t.Fatalf("tanı: %+v", d)
	}
	for _, h := range d.Final {
		if len([]rune(h.Snippet)) > DiagSnippetRunes+2 {
			t.Errorf("kesit tavanı aşıldı: %d", len([]rune(h.Snippet)))
		}
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), fakePAT) {
		t.Error("tanı PAT taşımamalı")
	}
	if _, err := s.Diagnose(context.Background(), "nasıl ne", 5); err == nil {
		t.Error("terimsiz sorgu hata")
	}
}
