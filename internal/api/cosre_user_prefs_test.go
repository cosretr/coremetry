package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestCorrectionCue(t *testing.T) {
	yes := []string{"o değil, payments", "O değil payments'ı soruyorum", "onu değil inventory", "hayır, payments",
		"payments demek istedim", "kart tarafındaki", "yanlış servis, payments", "inventory'yi kastettim"}
	no := []string{"hayır", "payments nasıl", "kart tarafındaki hatalar son bir saatte nasıl gidiyor", "evet bak",
		"bu değer normal mi", "hata var mı"}
	for _, q := range yes {
		if !correctionCue(q) {
			t.Errorf("ipucu kaçtı: %q", q)
		}
	}
	for _, q := range no {
		if correctionCue(q) {
			t.Errorf("yanlış ipucu: %q", q)
		}
	}
}

func TestAliasPhraseOf(t *testing.T) {
	svcs := []string{"checkout", "orders", "payments", "inventory"}
	envs := []string{"prod", "uat"}
	cases := map[string]string{
		"kart tarafı nasıl":                    "kart tarafı",
		"kart tarafında hata var mı?":          "kart tarafında",
		"bugün sepet işleri nasıl gidiyor":     "sepet işleri",
		"checkout'ta hata var mı":              "", // açık ad eş ad değildir
		"nasıl":                                "",
		"uat ortamında kart ödeme akışı yavaş": "kart ödeme akışı",
		"son 2 saatte bir gecikme var mı":      "",
	}
	for q, want := range cases {
		if got := aliasPhraseOf(q, svcs, envs); got != want {
			t.Errorf("aliasPhraseOf(%q) = %q, beklenen %q", q, got, want)
		}
	}
	long := strings.Repeat("çokuzunsözcük", 4) + " ikinci üçüncü"
	if got := aliasPhraseOf(long, svcs, envs); len([]rune(got)) > correctionPhraseMaxRunes {
		t.Fatalf("deyim 40 rune'u aştı: %q", got)
	}
}

func TestWithCorrectionDedupAndCap(t *testing.T) {
	var l []chatCorrection
	l = withCorrection(l, chatCorrection{Phrase: "kart tarafı", Wrong: "orders", Right: "payments"})
	l = withCorrection(l, chatCorrection{Phrase: "kart tarafında", Wrong: "orders", Right: "payments"})
	if len(l) != 1 || l[0].Phrase != "kart tarafında" {
		t.Fatalf("aynı deyim tekilleşmedi: %+v", l)
	}
	for i := 0; i < 8; i++ {
		l = withCorrection(l, chatCorrection{Phrase: fmt.Sprintf("deyim%d", i), Wrong: "orders", Right: "payments"})
	}
	if len(l) != chatCorrectionsMax || l[len(l)-1].Phrase != "deyim7" {
		t.Fatalf("tavan: %+v", l)
	}
}

// oldAliasBlob — v0.10.2462 öncesi satır: kaldırılan kişisel eş adlar `m`de.
const oldAliasBlob = `{"v":1,"addr":"siz","m":[{"p":"kart tarafı","id":"svc:payments","not":"svc:orders","at":1}]}`

// TestCosreUserPrefsOldBlobParses — eş adlı eski satır okunur (hitap gelir,
// `m` yok sayılır) ve hitap yazımı `m`yi silmeden aynen taşır.
func TestCosreUserPrefsOldBlobParses(t *testing.T) {
	p := decodeCosreUserPrefs(oldAliasBlob)
	if p.Addr != "siz" {
		t.Fatalf("eski satırın hitabı okunmadı: %+v", p)
	}
	if got := decodeCosreUserPrefs("bozuk"); got.Addr != "" {
		t.Fatalf("bozuk gövde boş kayıt olmalı: %+v", got)
	}
	st := installMemPrefStore(t)
	st.rows[cosreUserRowID("u-a")] = chstore.SavedView{ID: cosreUserRowID("u-a"), OwnerID: "u-a", Name: cosreUserPage, Page: cosreUserPage, QueryString: oldAliasBlob}
	cosreUserWriteAddress(context.Background(), st, "u-a", "sen")
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(st.rows[cosreUserRowID("u-a")].QueryString), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["addr"]) != `"sen"` || string(got["m"]) != `[{"p":"kart tarafı","id":"svc:payments","not":"svc:orders","at":1}]` {
		t.Fatalf("yazım saklı anahtarı korumadı: %s", st.rows[cosreUserRowID("u-a")].QueryString)
	}
}

// TestCosreUserAddressPersist — açık hitap değişince yazılır; kaydı olmayan
// kullanıcıda varsayılan "sen" satır açmaz.
func TestCosreUserAddressPersist(t *testing.T) {
	st := installMemPrefStore(t)
	s := &Server{}
	run := func(addr string) {
		if job := s.cosreUserAddressJob("u-a", addr); job != nil {
			job()
		}
	}
	if s.cosreUserAddressJob("u-a", "sen") != nil {
		t.Fatal("varsayılan hitap satır açacaktı")
	}
	run("siz")
	if st.prefs("u-a").Addr != "siz" {
		t.Fatalf("siz yazılmadı: %+v", st.rows)
	}
	run("sen")
	if st.prefs("u-a").Addr != "sen" {
		t.Fatal("değişen hitap yazılmadı")
	}
}
