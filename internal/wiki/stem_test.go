package wiki

// v0.10.1127 — operatör: "sbox sunucuları neler" ilgisiz sayfa getiriyor,
// "Sbox Sunucu Listesi" doğru sayfayı buluyor. Kök neden: jetonlar TAM
// sözcüktü; Türkçe çekim eki ("sunucuları") kökü ("sunucu") hiç eşlemiyordu.
// Hafif Türkçe kök bulucu + indeks/sorgu biçim genişlemesi + saklı içerikten
// yeniden jetonlama. Adlar sentetik (svc-orders, example.test).

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStemTurkish(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sunuculari", "sunucu"},
		{"sunucularin", "sunucu"},
		{"sunucularda", "sunucu"},
		{"sunucularinin", "sunucu"},
		{"sunucusunda", "sunucu"},
		{"sunucuya", "sunucu"},
		{"listesi", "liste"},
		{"servisleri", "servis"},
		{"servisin", "servis"},
		{"uygulamanin", "uygulama"},
		{"kullanicilar", "kullanici"},
		{"ortamdaki", "ortam"},
		{"bilgileri", "bilgi"},
		{"loglari", "log"}, // çoğulda kısa kök serbest
		{"kafkaya", "kafka"},
		{"sistemde", "sistem"},
		// Negatifler — teknik terim / kök zaten / kısa.
		{"svc-orders", "svc-orders"},
		{"wsbxakfp01", "wsbxakfp01"},
		{"err-1042", "err-1042"},
		{"kafka", "kafka"},
		{"redis", "redis"},
		{"linux", "linux"},
		{"api", "api"},
		{"prod", "prod"},
		{"sunucu", "sunucu"},
		{"liste", "liste"}, // -te soyulsa kök 3 harf kalırdı
		{"servis", "servis"},
		{"guvenlik", "guvenlik"}, // yapım eki -lik korunur
		{"login", "login"},       // -in: kök 4 harften kısa
		{"domain", "domain"},     // -in yalnız ünsüzden sonra
		{"update", "update"},     // -te yalnız sert ünsüzden sonra
		{"neler", "neler"},       // kök 2 harf kalırdı
		{"adlari", "adlari"},
	}
	for _, c := range cases {
		if got := Stem(c.in); got != c.want {
			t.Errorf("Stem(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormsCoverSuffixAmbiguity(t *testing.T) {
	// "servisi" = servis + i (belirtme) — "-sı" soyulup "servi" de olabilir;
	// iki yorum da biçim, böylece "servis" sayfası eşleşir.
	for _, c := range []struct {
		in   string
		want string
	}{
		{"servisi", "servis"},
		{"servisini", "servis"},
		{"ortami", "ortam"},
		{"sunucusu", "sunucu"},
	} {
		if f := Forms(c.in); f[0] != c.in || !contains(f, c.want) || len(f) > formsMax {
			t.Errorf("Forms(%q) = %q, %q içermeli (ilk eleman yüzey, ≤%d)", c.in, f, c.want, formsMax)
		}
	}
	for _, tech := range []string{"svc-orders", "wsbxakfp01", "api", "orders.v2"} {
		if f := Forms(tech); len(f) != 1 || f[0] != tech {
			t.Errorf("teknik terim genişlememeli: Forms(%q) = %q", tech, f)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestIndexTokensKeepsIdentifiersExact(t *testing.T) {
	got := IndexTokens("WSBXAKFP01 SUNUCULARI svc-orders Sunucuları")
	// Büyük harfli tanımlayıcı ve bileşik parçaları köklenmez; düz sözcük
	// yüzey + kök biçimleriyle girer.
	for _, want := range []string{"wsbxakfp01", "sunuculari", "svc-orders", "svc", "orders", "sunucu"} {
		if !contains(got, want) {
			t.Errorf("IndexTokens: %q eksik: %q", want, got)
		}
	}
	n := 0
	for _, x := range got {
		if x == "sunucu" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("TAMAMI BÜYÜK HARF 'SUNUCULARI' köklenmemeli, yalnız 'Sunucuları' kök vermeli: %q", got)
	}
	// Tokens (yüzey) değişmedi.
	if !reflect.DeepEqual(Tokens("svc-orders Sunucuları"), []string{"svc", "orders", "svc-orders", "sunuculari"}) {
		t.Errorf("Tokens yüzey biçimini korumalı: %q", Tokens("svc-orders Sunucuları"))
	}
}

func TestQueryTermsListQuestionStopwords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"sbox sunucuları neler", []string{"sbox", "sunuculari"}},
		{"Sbox Sunucu Listesi", []string{"sbox", "sunucu", "listesi"}},
		{"sbox sunucularının adları", []string{"sbox", "sunucularinin"}},
		{"sbox sunucuları hangileri, listele", []string{"sbox", "sunuculari"}},
		{"ödeme servisi bilgileri nelerdir", []string{"odeme", "servisi"}}, // kökü stopword ("bilgi")
	}
	for _, c := range cases {
		if got := QueryTerms(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("QueryTerms(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandTermsBounded(t *testing.T) {
	terms := QueryTerms("sunucuları servislerin uygulamaların kullanıcıları ortamlardaki bilgisayarları " +
		"yapılandırmaları dağıtımları kuyrukları tabloları sertifikaları kümeleri")
	if len(terms) != queryTermsMax {
		t.Fatalf("terim tavanı: %d", len(terms))
	}
	ex := expandTerms(terms)
	if len(ex.tokens) > expandedTermsMax {
		t.Fatalf("genişlemiş jeton tavanı aşıldı: %d", len(ex.tokens))
	}
	for i, g := range ex.groups {
		if ex.tokens[g[0]] != terms[i] {
			t.Errorf("grup %d ilk elemanı yüzey olmalı: %q", i, ex.tokens[g[0]])
		}
	}
	// Teknik terim genişlemez.
	if got := ExpandTerms([]string{"svc-orders", "wsbxakfp01"}); !reflect.DeepEqual(got, []string{"svc-orders", "wsbxakfp01"}) {
		t.Errorf("ExpandTerms teknik: %q", got)
	}
}

// sboxCorpus — sentetik sayfalar: hedef "Sbox Sunucu Listesi" + "sunucu"
// sözcüğünü geçen ama sbox'la ilgisiz sayfalar.
func sboxCorpus(t *testing.T) (*Service, *memStore) {
	t.Helper()
	st := newMemStore()
	s := New(st, func() API { return nil }, nil)
	s.Configure(Config{Enabled: true, Mode: ModeSync})
	pages := []PageRecord{
		{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: "/Ortamlar/Sbox Sunucu Listesi",
			Content: "# Sunucular\n| Ad | IP | Rol |\n|---|---|---|\n| WSBXAKFP01 | 10.0.0.11 | uygulama |\n| WSBXAKFP02 | 10.0.0.12 | uygulama |\n| WSBXDBP01 | 10.0.0.21 | veritabanı |\n"},
		{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: "/Redis/Kurulum",
			Content: "# Redis kurulumu\nRedis sunucusu için bellek ayarları ve kalıcılık seçenekleri. Sunucuların yeniden başlatılması nöbetçiye bildirilir."},
		{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: "/Kafka/Kümeler",
			Content: "# Kafka kümeleri\nÜretim kümesinde üç broker sunucusu vardır; svc-orders tüketici grubu orders-cg adını kullanır."},
		{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: "/Ödeme/Akış",
			Content: "# Ödeme akışı\nÖdeme isteği svc-payments üzerinden svc-ledger servisine iletilir; hata kodu ERR-1042 izlenir."},
	}
	for _, p := range pages {
		p.Title = PageTitle(p.Path)
		if err := st.UpsertWikiPage(context.Background(), p, BuildChunks(p.Title, p.Content), 0); err != nil {
			t.Fatal(err)
		}
	}
	return s, st
}

func TestRankTurkishInflectionFindsSboxPage(t *testing.T) {
	s, _ := sboxCorpus(t)
	for _, q := range []string{"sbox sunucuları neler", "sbox sunucu listesi", "sbox sunucularının adları", "Sbox sunucularında hangi IP'ler var"} {
		res, err := s.SearchWith(context.Background(), q, "", SearchOptions{Limit: 5, PerPage: 1, Live: LiveOff})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(res.Hits) == 0 || res.Hits[0].Path != "/Ortamlar/Sbox Sunucu Listesi" {
			t.Fatalf("%q: sbox sayfası ilk sırada olmalı: %+v", q, res.Hits)
		}
		// Skor = kapsama × (0.6+0.4×göreli bm25) ≤ kapsama: skor ≥ 0.5 ⇒ kapsama ≥ 0.5.
		if res.Hits[0].Score < 0.5 {
			t.Errorf("%q: kapsama tabanın altında: %.3f (terimler %q)", q, res.Hits[0].Score, res.Terms)
		}
	}
}

func TestRankExactTechnicalTokensUnaffected(t *testing.T) {
	s, _ := sboxCorpus(t)
	for q, want := range map[string]string{
		"WSBXAKFP01 nedir":    "/Ortamlar/Sbox Sunucu Listesi",
		"svc-orders tüketici": "/Kafka/Kümeler",
		"ERR-1042":            "/Ödeme/Akış",
	} {
		res, err := s.SearchWith(context.Background(), q, "", SearchOptions{Limit: 3, PerPage: 1, Live: LiveOff})
		if err != nil || len(res.Hits) == 0 || res.Hits[0].Path != want {
			t.Errorf("%q: %q ilk olmalı: %+v %v", q, want, res.Hits, err)
		}
	}
}

func TestRankSurfaceMatchBeatsStemOnly(t *testing.T) {
	terms := QueryTerms("sunucuları")
	cands := []Candidate{
		candidate("/kok", "Notlar", "Bölüm", "Bu bölümde sunucu adları ve görevleri yer alır, ayrıntı yoktur.", terms),
		candidate("/yuzey", "Notlar", "Bölüm", "Bu bölümde sunucuları adları ve görevleri yer alır, ayrıntı yoktur.", terms),
	}
	hits := RankLexical(cands, statsOf(cands, terms), terms)
	if len(hits) != 2 || hits[0].Path != "/yuzey" || hits[1].Score < 0.5 {
		t.Fatalf("tam yazım hafifçe önde, kök eşleşmesi yine tabanın üstünde: %+v", hits)
	}
}

func TestLiveSearchQueriesStemAlternatives(t *testing.T) {
	cases := []struct{ in, and, or string }{
		// Tek sözcükte de kök seçeneği → OR sorgusu.
		{"sbox sunucuları neler", "sbox sunucuları", "sbox OR sunucuları OR sunucu"},
		{"Kullanıcıların yetkileri", "Kullanıcıların yetkileri", "Kullanıcıların OR kullanıcı OR yetkileri OR yetki"},
		{"sunucuları", "sunucuları", "sunucuları OR sunucu"},
		// Teknik / büyük harfli tanımlayıcı / kısa kök genişlemez.
		{"svc-orders WSBXAKFP01 KULLANICILAR", "svc-orders WSBXAKFP01 KULLANICILAR", "svc-orders OR WSBXAKFP01 OR KULLANICILAR"},
		{"yeniden", "yeniden", ""},
	}
	for _, c := range cases {
		a, o := LiveSearchQueries(c.in)
		if a != c.and || o != c.or {
			t.Errorf("LiveSearchQueries(%q) = (%q, %q), want (%q, %q)", c.in, a, o, c.and, c.or)
		}
	}
}

// oldTokenize — tokenizerVersion 1'in indeks biçimi (yalnız yüzey).
func oldTokenize(st *memStore) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, cs := range st.chunks {
		p := st.pages[k]
		for i := range cs {
			head := p.Title
			if cs[i].Heading != "" {
				head += HeadingSep + cs[i].Heading
			}
			cs[i].HeadTokens = Tokens(head)
			cs[i].Tokens = append(Tokens(head), Tokens(cs[i].Text)...)
			cs[i].Embedding = []float32{float32(i) + 0.5, 1}
		}
	}
}

func chunkHas(st *memStore, path, tok string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, cs := range st.chunks {
		if !strings.HasSuffix(k, "\x00"+path) {
			continue
		}
		for _, c := range cs {
			if contains(c.Tokens, tok) {
				return true
			}
		}
	}
	return false
}

func TestReindexOnTokenizerVersionChangeUsesStoredContent(t *testing.T) {
	f := runbookWiki()
	f.wikis[0].pages["/Sbox Sunucu Listesi"] = &fakePage{content: "# Sunucular\nWSBXAKFP01 ve WSBXAKFP02 uygulama sunucularıdır.", obj: "o9", etag: "e9"}
	s, st := newTestService(t, f)
	var embeds atomic.Int32
	s.embed = func() Embedder {
		return func(_ context.Context, texts []string) ([][]float32, error) {
			embeds.Add(1)
			out := make([][]float32, len(texts))
			for i := range out {
				out[i] = []float32{1, 0}
			}
			return out, nil
		}
	}
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Status(context.Background(), true).TokenizerVersion; got != tokenizerVersion {
		t.Fatalf("ilk senkron sürümü kaydetmeli: %d", got)
	}
	// Eski jetonlayıcıyla kurulmuş indeksi taklit et.
	oldTokenize(st)
	if chunkHas(st, "/Sbox Sunucu Listesi", "liste") {
		t.Fatal("ön koşul: eski indekste kök yok")
	}
	raw := s.Status(context.Background(), true)
	raw.TokenizerVersion = 1
	s.saveStatus(context.Background(), raw)

	f.mu.Lock()
	getsBefore := 0
	for _, n := range f.pageGets {
		getsBefore += n
	}
	f.mu.Unlock()
	embedsBefore := embeds.Load()

	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	getsAfter := 0
	for _, n := range f.pageGets {
		getsAfter += n
	}
	f.mu.Unlock()
	if getsAfter != getsBefore {
		t.Errorf("yeniden jetonlama sayfa içeriğini ADO'dan OKUMAMALI: %d → %d", getsBefore, getsAfter)
	}
	if res.Reindexed != 5 || res.TokenizerVersion != tokenizerVersion || res.ReindexCursor != "" {
		t.Errorf("durum: reindexed=%d version=%d cursor=%q", res.Reindexed, res.TokenizerVersion, res.ReindexCursor)
	}
	if !chunkHas(st, "/Sbox Sunucu Listesi", "liste") {
		t.Error("saklı içerikten yeni jetonlar (kök) yazılmalı")
	}
	if embeds.Load() != embedsBefore {
		t.Error("metni değişmeyen parça yeniden embed edilmemeli")
	}
	st.mu.Lock()
	for k, cs := range st.chunks {
		for i, c := range cs {
			if len(c.Embedding) != 2 || c.Embedding[0] != float32(i)+0.5 {
				t.Errorf("%q #%d: saklı embedding taşınmalı: %v", k, i, c.Embedding)
			}
		}
	}
	st.mu.Unlock()

	// Sonraki geçiş yeniden jetonlamaz.
	reads := st.chunkReads
	if res, _ := s.Sync(context.Background()); res.Reindexed != 0 || st.chunkReads != reads {
		t.Errorf("sürüm güncelken yeniden jetonlama olmamalı: %d", res.Reindexed)
	}
}

func TestReindexBatchedWithCursorAndNoAPI(t *testing.T) {
	s, st := sboxCorpus(t)
	oldTokenize(st)
	old := reindexPerPass
	reindexPerPass = 3
	t.Cleanup(func() { reindexPerPass = old })

	// API yapılandırılmamış: senkron hata döner ama yeniden jetonlama koşar.
	res, err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("bağlantısız senkron hata dönmeli")
	}
	if res.Reindexed != 3 || res.TokenizerVersion == tokenizerVersion || res.ReindexCursor == "" {
		t.Fatalf("ilk parti: %+v", res)
	}
	res, _ = s.Sync(context.Background())
	if res.Reindexed != 1 || res.TokenizerVersion != tokenizerVersion || res.ReindexCursor != "" {
		t.Fatalf("ikinci parti işi bitirmeli: reindexed=%d v=%d cursor=%q", res.Reindexed, res.TokenizerVersion, res.ReindexCursor)
	}
	for p, stem := range map[string]string{
		"/Ortamlar/Sbox Sunucu Listesi": "liste",  // başlık "Listesi"
		"/Redis/Kurulum":                "sunucu", // "sunucusu"
		"/Kafka/Kümeler":                "kume",   // "kümeleri"
		"/Ödeme/Akış":                   "servis", // "servisine" (-sı belirsizliği)
	} {
		if !chunkHas(st, p, stem) {
			t.Errorf("%s yeniden jetonlanmalı (kök %q)", p, stem)
		}
	}
}

func TestApostropheSuffixDropped(t *testing.T) {
	if got := Tokens("WSBXAKFP01'de IP'ler svc-orders’ın O'Reilly2"); !reflect.DeepEqual(got, []string{"wsbxakfp01", "ip", "svc", "orders", "svc-orders", "reilly2"}) {
		t.Errorf("kesme işaretli ek jeton olmamalı: %q", got)
	}
	if a, _ := LiveSearchQueries("WSBXAKFP01'de hangi IP'ler var"); a != "WSBXAKFP01 IP" {
		t.Errorf("canlı sorgu kesme işaretli eki taşımamalı: %q", a)
	}
}
