package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
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
	if got := aliasPhraseOf(long, svcs, envs); len([]rune(got)) > cosreUserPhraseMaxRunes {
		t.Fatalf("deyim 40 rune'u aştı: %q", got)
	}
}

func TestWithCorrectionDedupAndCap(t *testing.T) {
	var l []chatCorrection
	l = withCorrection(l, chatCorrection{Phrase: "kart tarafı", Wrong: "orders", Right: "payments", Proposed: true})
	l = withCorrection(l, chatCorrection{Phrase: "kart tarafında", Wrong: "orders", Right: "payments"})
	if len(l) != 1 || !l[0].Proposed || l[0].Phrase != "kart tarafında" {
		t.Fatalf("aynı deyim tekilleşmedi / önerildi işareti kayboldu: %+v", l)
	}
	for i := 0; i < 8; i++ {
		l = withCorrection(l, chatCorrection{Phrase: fmt.Sprintf("deyim%d", i), Wrong: "orders", Right: "payments"})
	}
	if len(l) != chatCorrectionsMax || l[len(l)-1].Phrase != "deyim7" {
		t.Fatalf("tavan: %+v", l)
	}
}

// TestCosreUserPrefsCaps — 51. eşleme en eskiyi düşürür; 4 KiB tavanı tutar.
func TestCosreUserPrefsCaps(t *testing.T) {
	p := cosreUserPrefs{}
	for i := 0; i < cosreUserMaxAliases+1; i++ {
		p = withCosreUserAlias(p, cosreUserAlias{P: fmt.Sprintf("d%02d", i), ID: "svc:payments", Not: "svc:orders", At: int64(i)})
	}
	if len(p.M) != cosreUserMaxAliases && len(encodeCosreUserPrefs(p)) <= cosreUserMaxBytes {
		t.Fatalf("eşleme tavanı: %d", len(p.M))
	}
	if p.M[0].P == "d00" || p.M[len(p.M)-1].P != "d50" {
		t.Fatalf("en eski düşmedi: ilk %q son %q", p.M[0].P, p.M[len(p.M)-1].P)
	}
	if n := len(encodeCosreUserPrefs(p)); n > cosreUserMaxBytes {
		t.Fatalf("4 KiB aşıldı: %d", n)
	}
	q := cosreUserPrefs{Addr: "siz"}
	big := strings.Repeat("ü", cosreUserPhraseMaxRunes)
	for i := 0; i < cosreUserMaxAliases; i++ {
		q = withCosreUserAlias(q, cosreUserAlias{P: fmt.Sprintf("%s%02d", big[:len(big)-4], i), ID: "svc:" + strings.Repeat("x", 60), Not: "svc:" + strings.Repeat("y", 60), At: 1_800_000_000})
	}
	if n := len(encodeCosreUserPrefs(q)); n > cosreUserMaxBytes || len(q.M) >= cosreUserMaxAliases || q.Addr != "siz" {
		t.Fatalf("bayt tavanı: %d bayt, %d eşleme, hitap %q", n, len(q.M), q.Addr)
	}
	if q.M[len(q.M)-1].P != big[:len(big)-4]+"49" {
		t.Fatalf("bayt tavanında en yeni düştü: %q", q.M[len(q.M)-1].P)
	}
}

func prefsReq(method, target, uid, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if uid != "" {
		r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: uid, Email: uid + "@example.test", Role: "viewer"}))
	}
	return r
}

// TestCosreUserPrefsRoutes — viewer kendi kaydını yazar, okur, siler; başka
// kullanıcı görmez; bayat hedef listelenir; doğrulama.
func TestCosreUserPrefsRoutes(t *testing.T) {
	st := installMemPrefStore(t)
	s := &Server{cache: &ctxMemCache{m: map[string][]byte{"copilot:guided:svcnames": []byte(`["checkout","orders","payments","inventory"]`)}}}
	sigc := make(chan chstore.CosreSignal, 4)
	setSystemSignalWriter(func(_ context.Context, g chstore.CosreSignal) error { sigc <- g; return nil })
	t.Cleanup(func() { setSystemSignalWriter(nil) })
	mux := http.NewServeMux()
	s.registerCosreUserPrefsRoutes(mux)
	do := func(r *http.Request) (int, map[string]any) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	if c, _ := do(prefsReq("GET", "/api/cosre/user-prefs", "", "")); c != http.StatusUnauthorized {
		t.Fatalf("kimliksiz GET %d", c)
	}
	for _, bad := range []string{
		`{"phrase":"kart tarafı","entityId":"svc:unknown"}`,
		`{"phrase":"payments","entityId":"svc:payments"}`,
		`{"phrase":"ka","entityId":"svc:payments"}`,
		`{"phrase":"kart tarafı","entityId":"svc:payments","notId":"svc:payments"}`,
		`{"phrase":"kart tarafı","entityId":"ns:c1:shop"}`,
	} {
		if c, _ := do(prefsReq("POST", "/api/cosre/user-prefs/aliases", "u-a", bad)); c != http.StatusBadRequest {
			t.Errorf("%s → %d (400 bekleniyor)", bad, c)
		}
	}
	c, m := do(prefsReq("POST", "/api/cosre/user-prefs/aliases", "u-a", `{"phrase":" kart   tarafı ","entityId":"svc:payments","notId":"svc:orders","exchangeId":"x1"}`))
	if c != http.StatusOK || len(m["aliases"].([]any)) != 1 {
		t.Fatalf("kayıt: %d %v", c, m)
	}
	row := st.rows[cosreUserRowID("u-a")]
	if row.OwnerID != "u-a" || row.Page != cosreUserPage || !strings.Contains(row.QueryString, `"p":"kart tarafı"`) {
		t.Fatalf("satır: %+v", row)
	}
	select {
	case g := <-sigc:
		if g.Kind != "alias_saved" || g.Detail != "kind=service" || g.UserEmail != "system:cosre" || g.ExchangeID != "x1" {
			t.Fatalf("sinyal: %+v", g)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("alias_saved sinyali yazılmadı")
	}
	if _, m := do(prefsReq("GET", "/api/cosre/user-prefs", "u-b", "")); len(m["aliases"].([]any)) != 0 {
		t.Fatalf("başka kullanıcı gördü: %v", m)
	}
	// Bayat: hedef katalogdan kalktı.
	st.rows[cosreUserRowID("u-a")] = chstore.SavedView{ID: cosreUserRowID("u-a"), OwnerID: "u-a", Name: cosreUserPage, Page: cosreUserPage,
		QueryString: encodeCosreUserPrefs(cosreUserPrefs{M: []cosreUserAlias{{P: "eski taraf", ID: "svc:legacy-cards", At: 1}, {P: "kart tarafı", ID: "svc:payments", At: 2}}})}
	_, m = do(prefsReq("GET", "/api/cosre/user-prefs", "u-a", ""))
	list := m["aliases"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["stale"] != false || list[1].(map[string]any)["stale"] != true {
		t.Fatalf("bayat işareti: %v", list)
	}
	if c, _ := do(prefsReq("DELETE", "/api/cosre/user-prefs/aliases?phrase=eski%20taraf", "u-a", "")); c != http.StatusOK {
		t.Fatalf("silme %d", c)
	}
	if c, _ := do(prefsReq("DELETE", "/api/cosre/user-prefs/aliases?phrase=eski%20taraf", "u-a", "")); c != http.StatusNotFound {
		t.Fatalf("ikinci silme %d", c)
	}
	// Sahip izolasyonu: başka sahipli satır okunmaz.
	st.rows[cosreUserRowID("u-c")] = chstore.SavedView{ID: cosreUserRowID("u-c"), OwnerID: "u-x", Name: cosreUserPage, Page: cosreUserPage,
		QueryString: encodeCosreUserPrefs(cosreUserPrefs{Addr: "siz"})}
	if _, m := do(prefsReq("GET", "/api/cosre/user-prefs", "u-c", "")); m["address"] != "" {
		t.Fatalf("başka sahipli satır okundu: %v", m)
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
