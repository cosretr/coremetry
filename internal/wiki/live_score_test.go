package wiki

// v0.10.1124 — operatör: "Wiki içeriğini search etmiyor eğer senkron değilse".
// Kök nedenler: (1) canlı sorgu soru cümlesiydi (ADO AND'ler → sıfır sonuç),
// (2) canlı isabet tam-jeton lexical skorla RAG tabanının altına düşüyordu,
// (3) soru sözcükleri kapsamı sulandırıyordu. Adlar sentetik.

import (
	"reflect"
	"testing"
	"time"
)

func TestQueryTermsQuestionStopwords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"svc-orders nasıl restart edilir mi?", []string{"svc-orders", "svc", "orders", "restart"}},
		{"ERR-1042 nedir, neden olur?", []string{"err-1042", "err", "1042"}},
		{"Hangi ekip için nöbet kanalı var mı", []string{"ekip", "nobet", "kanali"}},
		{"How do I rotate the kafka TLS certificate?", []string{"rotate", "kafka", "tls", "certificate"}},
		{"what is the svc-ledger owner", []string{"svc-ledger", "svc", "ledger", "owner"}},
		{"wikide ödeme akışı nasıl anlatılıyor", []string{"odeme", "akisi"}},
	}
	for _, c := range cases {
		if got := QueryTerms(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("QueryTerms(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLiveSearchQueries(t *testing.T) {
	cases := []struct{ in, and, or string }{
		{"svc-orders nasıl restart edilir?", "svc-orders restart", "svc-orders OR restart"},
		{"Ödeme servisini nasıl yeniden başlatırım", "Ödeme servisini yeniden başlatırım", "Ödeme OR servisini OR servi OR servis OR yeniden OR başlatırım"}, // v0.10.1127: kök seçenekleri ("-sı" belirsizliği iki yorum; "yeni" kısa → yok)
		{"ledger-oncall", "ledger-oncall", ""},
		{"nasıl ne nedir", "", ""},
		{`NOT (kafka) AND "tls":x*`, "not kafka tls", "not OR kafka OR tls"},
	}
	for _, c := range cases {
		a, o := LiveSearchQueries(c.in)
		if a != c.and || o != c.or {
			t.Errorf("LiveSearchQueries(%q) = (%q, %q), want (%q, %q)", c.in, a, o, c.and, c.or)
		}
	}
}

func TestStemMatch(t *testing.T) {
	cases := []struct {
		term, tok string
		want      bool
	}{
		{"servisini", "servis", true},
		{"servis", "servisinin", true},
		{"pod", "podu", true},
		{"pod", "podcast-uzun", false},
		{"svc-orders", "svc-orders", true},
		{"svc-orders", "svc-ordersx", false},
		{"1042", "10420", false},
		{"servis", "server", false},
		{"ab", "abc", false},
	}
	for _, c := range cases {
		if got := stemMatch(c.term, c.tok); got != c.want {
			t.Errorf("stemMatch(%q,%q)=%v want %v", c.term, c.tok, got, c.want)
		}
	}
}

// Türkçe ek uyuşmazlığı: sorudaki "yeniden başlatılmasını" / "servisinin"
// sayfada "yeniden başlatma" / "servis" olarak geçer — tam-jeton kapsamı düşük,
// ama ADO bulduysa ilk isabet RAG tabanının (0.5) üstünde kalmalı.
func TestScoreLivePageTurkishSuffixPassesFloor(t *testing.T) {
	rec := PageRecord{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: "/Runbooks/Orders", Title: "Orders"}
	chunks := BuildChunks(rec.Title, "# Yeniden başlatma\nOrders servis yeniden başlatma adımları: rollout restart çalıştırın.\n\n# Ek\nalakasız metin burada.")
	terms := QueryTerms("orders servisinin yeniden başlatılması nasıl yapılır")
	for _, mode := range []string{"and", "or"} {
		hits := scoreLivePage(rec, chunks, terms, Stats{}, 0, mode, "")
		if len(hits) == 0 || hits[0].Score < 0.5 || !hits[0].Live {
			t.Fatalf("%s: ilk canlı isabet tabanın üstünde olmalı: %+v", mode, hits)
		}
	}
	// Sıra söner: üçüncü isabet ilkten yüksek olamaz, yine taban civarı.
	// v0.10.1127: Türkçe kök eşleşmesiyle lexical skor sıra tabanını geçebilir
	// (o zaman iki sıra da aynı lexical skoru taşır) — sönüm yalnız taban.
	h0 := scoreLivePage(rec, chunks, terms, Stats{}, 0, "and", "")
	h2 := scoreLivePage(rec, chunks, terms, Stats{}, 2, "and", "")
	if h2[0].Score > h0[0].Score || h2[0].Score < 0.5 {
		t.Errorf("sıra sönümü: r0=%.3f r2=%.3f", h0[0].Score, h2[0].Score)
	}
	// Kanıtsız sayfa (ne kök eşleşmesi ne ADO vurgusu) yükseltilmez.
	none := scoreLivePage(rec, chunks, QueryTerms("kafka sertifika"), Stats{}, 0, "and", "")
	if len(none) != 0 {
		t.Errorf("kanıtsız canlı isabet skor almamalı: %+v", none)
	}
	// ADO vurgusu kanıttır (sunucunun kök/eşanlam eşleşmesi).
	hl := scoreLivePage(rec, chunks, QueryTerms("kafka sertifika"), Stats{}, 0, "or", "rollout restart")
	if len(hl) == 0 || hl[0].Score < 0.5 {
		t.Errorf("vurgulu isabet tabanın üstünde olmalı: %+v", hl)
	}
}

func TestLiveCacheBounds(t *testing.T) {
	c := newLiveCache()
	c.max, c.cap, c.ttl = 4, 1000, time.Minute
	now := time.UnixMilli(1_000_000)
	for i := 0; i < 5; i++ {
		rec := PageRecord{Project: "P", WikiID: "w", WikiName: "W", Path: "/p" + string(rune('a'+i)), Content: "x"}
		c.put(rec, nil, now)
	}
	if c.len() > 4 {
		t.Fatalf("girdi tavanı: %d", c.len())
	}
	if _, _, ok := c.get(liveKey("P", "w", "/pa"), now); ok {
		t.Error("en eski sayfa düşmeli (LRU)")
	}
	if _, _, ok := c.get(liveKey("p", "W", "/pe"), now); !ok {
		t.Error("son sayfa wiki ADIYLA da bulunmalı")
	}
	if _, _, ok := c.get(liveKey("P", "w", "/pe"), now.Add(2*time.Minute)); ok {
		t.Error("ömrü dolan kayıt dönmemeli")
	}
	big := PageRecord{Project: "P", WikiID: "w", Path: "/big", Content: string(make([]byte, 2000))}
	c.put(big, nil, now)
	if _, _, ok := c.get(liveKey("P", "w", "/big"), now); ok {
		t.Error("bayt tavanını aşan sayfa önbelleğe girmemeli")
	}
	c.mu.Lock()
	b := c.bytes
	c.mu.Unlock()
	if b > c.cap {
		t.Errorf("bayt tavanı aşıldı: %d", b)
	}
}
