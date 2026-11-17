package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
)

// chat_alias_accept_test.go — v0.10.2335 (conversation-audit §6.1 G, kabul
// 1–6): öneri kartı, onay, kişisel kapsam, kapatma, bayat hedef, tavanlar —
// GERÇEK v2 handler'ı ve gerçek uçlar (buildMux) üstünden; sahte model her
// kullanıcıda "kart tarafı" için orders tahmin eder.

// v2AliasActions — akıştaki cosre_alias aksiyon blokları (yük).
func v2AliasActions(sse string) []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(sse, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var p struct {
			Type string `json:"type"`
			Data struct {
				Payload map[string]any `json:"payload"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p) == nil && p.Type == "data-block-action" && p.Data.Payload["kind"] == "cosre_alias" {
			out = append(out, p.Data.Payload)
		}
	}
	return out
}

// api — gerçek uç (buildMux) kullanıcı uid ile.
func (ar *aliasRig) api(uid, method, target string, body any) (int, map[string]any) {
	ar.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, target, rd)
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: uid, Email: uid + "@example.test", Role: "viewer"}))
	w := httptest.NewRecorder()
	ar.s.buildMux().ServeHTTP(w, r)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

// saveCard — kartın Kaydet'i: istemcinin göndereceği gövde yükten.
func (ar *aliasRig) saveCard(uid string, card map[string]any) {
	ar.t.Helper()
	code, m := ar.api(uid, http.MethodPost, "/api/cosre/user-prefs/aliases",
		map[string]any{"phrase": card["phrase"], "entityId": card["entityId"], "notId": card["notId"]})
	if code != http.StatusOK {
		ar.t.Fatalf("Kaydet %d %v", code, m)
	}
}

// correct — u kullanıcısı conv'da "kart tarafı" sorar (orders okunur) ve düzeltir.
func (ar *aliasRig) correct(uid, conv string) string {
	ar.t.Helper()
	ar.say(uid, conv, "kart tarafı nasıl")
	if st := ar.stateOf(uid, conv); st.Service != "orders" {
		ar.t.Fatalf("%s: tur 1 durumu %+v", uid, st)
	}
	return ar.say(uid, conv, "o değil, payments")
}

func TestAliasAcceptance(t *testing.T) {
	ar := newAliasRig(t)

	// 1. "kart tarafı" → orders; "o değil, payments" → durum payments, öneri; kullanıcı kaydeder.
	body := ar.correct("u-a", "a1")
	if st := ar.stateOf("u-a", "a1"); st.Service != "payments" {
		t.Fatalf("1: durum payments'a geçmedi: %+v", st)
	}
	cards := v2AliasActions(body)
	if len(cards) != 1 {
		t.Fatalf("1: tek öneri bekleniyor: %v", cards)
	}
	card := cards[0]
	if card["phrase"] != "kart tarafı" || card["entityId"] != "svc:payments" || card["notId"] != "svc:orders" ||
		card["text"] != "«kart tarafı» dediğinde payments servisini kastettiğini hatırlayayım mı?" {
		t.Fatalf("1: kart yükü: %v", card)
	}
	if len(ar.prefs.rows) != 0 {
		t.Fatal("1: Kaydet'ten önce kalıcı kayıt yazıldı")
	}
	ar.saveCard("u-a", card)
	if p := ar.prefs.prefs("u-a"); len(p.M) != 1 || p.M[0].P != "kart tarafı" || p.M[0].ID != "svc:payments" || p.M[0].Not != "svc:orders" {
		t.Fatalf("1: kayıt: %+v", p)
	}

	// 2. Aynı kullanıcı, YENİ konuşma: doğrudan payments; orders okunmaz.
	n := ar.mark()
	body = ar.say("u-a", "a2", "kart tarafı nasıl")
	if got := ar.servicesRead(n); !allEqual(got, "payments") {
		t.Fatalf("2: okumalar %v (yalnız payments bekleniyor)", got)
	}
	if st := ar.stateOf("u-a", "a2"); st.Service != "payments" {
		t.Fatalf("2: durum %+v", st)
	}
	if len(v2AliasActions(body)) != 0 {
		t.Fatal("2: kayıtlı eşleme için yeniden öneri çıktı")
	}

	// 3. Başka kullanıcı: katalog sıralaması (modelin tahmini) aynen.
	n = ar.mark()
	ar.say("u-b", "b1", "kart tarafı nasıl")
	if got := ar.servicesRead(n); len(got) != 1 || got[0] != "orders" {
		t.Fatalf("3: u-b etkilendi: %v", got)
	}

	// 4. Kapatılan öneri: hiçbir şey saklanmaz; aynı konuşmada ikinci öneri yok.
	body = ar.correct("u-c", "c1")
	if len(v2AliasActions(body)) != 1 {
		t.Fatalf("4: öneri çıkmadı")
	}
	// "Hayır" yalnız istemcide kartı kapatır — sunucuya istek gitmez.
	for _, q := range []string{"kart tarafında hata var mı", "o değil, payments", "kart tarafındaki", "peki inventory"} {
		if b := ar.say("u-c", "c1", q); len(v2AliasActions(b)) != 0 {
			t.Fatalf("4: %q turunda ikinci öneri", q)
		}
	}
	if _, ok := ar.prefs.rows[cosreUserRowID("u-c")]; ok {
		t.Fatal("4: kapatılan öneri saklandı")
	}

	// 5. Hedefi kalkmış eşleme: yok sayılır, bayat listelenir.
	ar.mc.m["copilot:guided:svcnames"] = []byte(`["checkout","orders","inventory"]`)
	n = ar.mark()
	ar.say("u-a", "a3", "kart tarafı nasıl")
	if got := ar.servicesRead(n); len(got) != 1 || got[0] != "orders" {
		t.Fatalf("5: bayat eşleme uygulandı: %v", got)
	}
	_, m := ar.api("u-a", http.MethodGet, "/api/cosre/user-prefs", nil)
	if l := m["aliases"].([]any); len(l) != 1 || l[0].(map[string]any)["stale"] != true {
		t.Fatalf("5: bayat işareti: %v", m)
	}
	ar.mc.m["copilot:guided:svcnames"] = []byte(`["checkout","orders","payments","inventory"]`)

	// 6. 51. eşleme en eskiyi düşürür; 4 KiB tavanı tutar.
	for i := 0; i < cosreUserMaxAliases; i++ {
		ar.saveCard("u-a", map[string]any{"phrase": fmt.Sprintf("taraf %02d", i), "entityId": "svc:inventory", "notId": "svc:checkout"})
	}
	_, m = ar.api("u-a", http.MethodGet, "/api/cosre/user-prefs", nil)
	l := m["aliases"].([]any)
	if len(l) != cosreUserMaxAliases || m["bytes"].(float64) > cosreUserMaxBytes {
		t.Fatalf("6: %d eşleme, %v bayt", len(l), m["bytes"])
	}
	for _, a := range l {
		if a.(map[string]any)["phrase"] == "kart tarafı" {
			t.Fatal("6: en eski eşleme düşmedi")
		}
	}
	if l[0].(map[string]any)["phrase"] != "taraf 49" {
		t.Fatalf("6: en yeni başta değil: %v", l[0])
	}
	if code, _ := ar.api("u-a", http.MethodDelete, "/api/cosre/user-prefs/aliases?phrase=taraf%2049", nil); code != http.StatusOK {
		t.Fatalf("6: silme %d", code)
	}
}

// TestAliasNoProposalForExplicitName — önceki mesaj servisi açıkça andıysa
// düzeltme durumu değiştirir ama eş ad önerilmez (açık ad eş ad değildir).
func TestAliasNoProposalForExplicitName(t *testing.T) {
	ar := newAliasRig(t)
	ar.say("u-a", "e1", "checkout'ta hata var mı")
	body := ar.say("u-a", "e1", "o değil, payments'ı soruyorum")
	if st := ar.stateOf("u-a", "e1"); st.Service != "payments" {
		t.Fatalf("durum: %+v", st)
	}
	if len(v2AliasActions(body)) != 0 {
		t.Fatal("açık ad için eş ad önerildi")
	}
}
