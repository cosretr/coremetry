package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// v0.10.1324 — operator-reported: gemma4 31b (yerel openai-compat) CoSRE
// sohbetinde "INSURANCE_DOCUMENT_TRX_GET_EXAMPLECO_POLICY_PDF_PRINT_REST
// operasyonu neden hata veriyor?" sorusuna sistem prompt'undaki kapsam
// cümlesini yankıladı ve "Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları …" satırını
// max_tokens (4096) dolana dek onlarca kez yazdı. Akışta yineleme kapısı
// yoktu. Pinlenen sözleşmeler:
//   - prod örneği tetikler, CleanText ilk yinelemeden önce kesilir,
//   - meşru içerik (benzer önekli liste, tablo satırları, kod çiti, iki kez
//     geçen cümle) tetiklemez,
//   - akan turda muhafız üst akışı keser ve bu HATA DEĞİLDİR: Text = temiz
//     önek, Degenerated=true, tool çağrısı yok.

// prodDegenSample — prod çıktısının biçimi: kısa giriş, yankılanan önsöz
// cümlesi, sonra aynı satırın yinelemesi.
func prodDegenSample(n int) string {
	var b strings.Builder
	b.WriteString("INSURANCE_DOCUMENT_TRX_GET_EXAMPLECO_POLICY_PDF_PRINT_REST operasyonunun hatalarını inceledim.\n")
	b.WriteString("soru aksini söylemedikçe bu kapsam geçerli; başka ortam/servis/pencere sorulursa argümanda açıkça yaz.\n")
	for i := 0; i < n; i++ {
		b.WriteString("Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları (cluster/namespace/problem/filtreler) tool argümanlarında kullan.\n")
	}
	return b.String()
}

func feedGuard(text string, chunk int) *degenGuard {
	d := &degenGuard{}
	for _, c := range splitEvery(text, chunk) {
		if d.Feed(c) {
			break
		}
	}
	return d
}

func TestDegenGuardTriggers(t *testing.T) {
	prodClean := "INSURANCE_DOCUMENT_TRX_GET_EXAMPLECO_POLICY_PDF_PRINT_REST operasyonunun hatalarını inceledim.\n" +
		"soru aksini söylemedikçe bu kapsam geçerli; başka ortam/servis/pencere sorulursa argümanda açıkça yaz.\n" +
		"Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları (cluster/namespace/problem/filtreler) tool argümanlarında kullan."
	cases := []struct {
		name, in, clean string
	}{
		{"prod örneği (operator-reported)", prodDegenSample(40), prodClean},
		{"aynı cümle tek satırda", "Özet: hata yok. " + strings.Repeat("Bu kapsam varsayılan olarak geçerlidir. ", 8),
			"Özet: hata yok. Bu kapsam varsayılan olarak geçerlidir."},
		{"madde imli yineleme (önek farklı, değer aynı)",
			"Bulgular:\n1. Veritabanı bağlantı havuzu doldu\n2. Veritabanı bağlantı havuzu doldu\n- Veritabanı bağlantı havuzu doldu\n",
			"Bulgular:\n1. Veritabanı bağlantı havuzu doldu"},
		{"iki satır dönüşümlü (A B A B A B)",
			"Giriş.\n" + strings.Repeat("hata oranı yüksek\np99 artışta\n", 3),
			"Giriş.\nhata oranı yüksek\np99 artışta"},
		// v0.10.1452 (operatör-bildirimli) — noktasız tek kelime yinelemesi.
		{"tek kelime noktasız (prod)", "Sütunlar ve tablo adları maskeli gelmiş mi? la" + strings.Repeat(" lükste", 60),
			"Sütunlar ve tablo adları maskeli gelmiş mi? la lükste"},
		{"iki kelimelik öbek noktasız", "Sonuç:" + strings.Repeat(" hata yok", 12),
			"Sonuç: hata yok"},
		{"dört kelimelik öbek, noktalama farkı", "Özet" + strings.Repeat(" bağlantı havuzu dolu, tekrar", 9),
			"Özet bağlantı havuzu dolu, tekrar"},
		{"büyük/küçük harf ve boşluk farkı", "Toplam üç çağrı hatalı döndü bugün\n  toplam ÜÇ   çağrı hatalı döndü bugün\n**Toplam üç çağrı hatalı döndü bugün**\n",
			"Toplam üç çağrı hatalı döndü bugün"},
	}
	for _, tc := range cases {
		for _, n := range []int{1, 5, 64, 4096} {
			d := feedGuard(tc.in, n)
			if !d.Tripped() {
				t.Errorf("%s (parça=%d): tetiklenmedi", tc.name, n)
				continue
			}
			if got := d.CleanText(); got != tc.clean {
				t.Errorf("%s (parça=%d): CleanText\n%q\nbeklenen\n%q", tc.name, n, got, tc.clean)
			}
		}
	}
}

func TestDegenGuardLegitContentDoesNotTrigger(t *testing.T) {
	var list, table, code strings.Builder
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&list, "%d. checkout-service pod-%d: hata oranı %%%d, p95 %d ms\n", i, i, i*2, 100+i*10)
		fmt.Fprintf(&table, "| checkout-%d | prod | %d | %d ms |\n", i, i*3, 200+i)
	}
	code.WriteString("Örnek sorgu:\n```go\n")
	for i := 0; i < 10; i++ {
		code.WriteString("\t}\n\treturn nil, fmt.Errorf(\"bağlantı havuzu doldu\")\n")
	}
	code.WriteString("```\nSon.\n")
	bullets := "- **Servis:** checkout-service, hata oranı %4.1\n- **Servis:** payment-service, hata oranı %2.3\n" +
		"- **Servis:** cart-service, hata oranı %0.4\n- **Servis:** auth-service, hata oranı %1.9\n" +
		"- **Servis:** search-service, hata oranı %0.1\n- **Servis:** ledger-service, hata oranı %3.0\n"
	twice := "Durum: normal aralıkta seyrediyor.\nAyrıntı yok.\nDurum: normal aralıkta seyrediyor.\nKapanış.\n"
	cases := map[string]string{
		"numaralı liste, farklı değerler": list.String(),
		"markdown tablo satırları":        "| servis | env | hata | p95 |\n|---|---|---|---|\n" + table.String(),
		"kod çiti":                        code.String(),
		"benzer önekli madde listesi":     bullets,
		"iki kez geçen cümle":             twice,
		"ayraç satırları":                 "Bölüm A\n---\nmetin bir\n---\nmetin iki\n---\nmetin üç\n---\n",
		"ondalıklı sayılar":               "p95 1.5 s, p99 2.5 s, ortalama 0.5 s. Önceki pencere 1.5 s idi.\n",
		// v0.10.1452 — kural 3 yanlış pozitifleri.
		"tek haneli sayı dizisi":    "Bucket sayıları: 0 0 0 0 0 0 0 0 0 0 0 0 1 3\n",
		"yedi kez yinelenen kelime": "Durum" + strings.Repeat(" tamam", 7) + ", bitti.\n",
		"tablo satırında yineleme":  "| a | " + strings.Repeat("yok | ", 12) + "\n",
		"kod çitinde yineleme":      "```\n" + strings.Repeat("nop ", 20) + "\n```\nSon.\n",
	}
	for name, in := range cases {
		for _, n := range []int{1, 7, 4096} {
			if d := feedGuard(in, n); d.Tripped() {
				t.Errorf("%s (parça=%d): meşru içerikte tetiklendi; CleanText=%q", name, n, d.CleanText())
			}
		}
	}
}

func TestDegenNormalize(t *testing.T) {
	cases := map[string]string{
		"  - **AKSİNİ** söyle  ": "aksini söyle",
		"12) Iğdır  ılık.":       "iğdir ilik",
		"### Başlık":             "başlik", // ı → i (İ/I/ı tek biçim)
		"> alıntı `kod`":         "alinti kod",
		"* madde\t\tiki":         "madde iki",
	}
	for in, want := range cases {
		if got := DegenNormalize(in); got != want {
			t.Errorf("DegenNormalize(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

// Kapı: muhafız tetiklenince yayın temiz öneke uzlaşır, iptal bir kez çağrılır,
// sonraki parçalar (tool çağrısı dâhil) yok sayılır.
func TestToolTurnGateDegenerates(t *testing.T) {
	for _, guard := range []bool{true, false} {
		r := &gateRec{}
		g := newToolTurnGate(r.sink, guard, offeredT)
		cancels := 0
		g.onDegenerate = func() { cancels++ }
		for _, c := range splitEvery(prodDegenSample(40), 9) {
			g.Content(c)
		}
		g.ToolCall()
		g.Content("devam eden çöp metin\n")
		if !g.Degenerated() || cancels != 1 {
			t.Fatalf("guard=%v: degenerated=%v cancels=%d", guard, g.Degenerated(), cancels)
		}
		want := g.CleanText()
		if strings.Count(want, "AKSİNİ SÖYLEMEDİKÇE") != 1 {
			t.Fatalf("guard=%v: temiz önek yinelemeyi taşıyor: %q", guard, want)
		}
		if r.visible() != want {
			t.Fatalf("guard=%v: görünen %q ≠ temiz %q", guard, r.visible(), want)
		}
		g.Finish(want, false)
		if r.visible() != want {
			t.Fatalf("guard=%v: Finish sonrası görünen %q", guard, r.visible())
		}
	}
}

// Uçtan uca: SSE sunucusu yinelemeyi sonsuza dek yazar; muhafız üst akışı
// keser, StreamOpenAITools HATASIZ döner.
func TestStreamOpenAIToolsDegenerationCancelsUpstream(t *testing.T) {
	var served atomic.Int64
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		send := func(s string) bool {
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": s}}}})
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return false
			}
			fl.Flush()
			served.Add(1)
			return true
		}
		for _, c := range splitRunes(prodDegenSample(0), 13) {
			send(c)
		}
		for i := 0; i < 5000; i++ { // max_tokens dolana dek yineleme
			select {
			case <-r.Context().Done():
				return
			default:
			}
			if !send("Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları (cluster/namespace/problem/filtreler) tool argümanlarında kullan.\n") {
				return
			}
			time.Sleep(time.Millisecond)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	r := &gateRec{}
	resp, err := StreamOpenAITools(context.Background(), Config{BaseURL: srv.URL, HTTPClient: srv.Client()}, toolReq(), r.sink)
	if err != nil {
		t.Fatalf("muhafız iptali hata olarak döndü: %v", err)
	}
	if !resp.Degenerated || len(resp.ToolCalls) != 0 {
		t.Fatalf("yanıt: degenerated=%v calls=%d", resp.Degenerated, len(resp.ToolCalls))
	}
	if strings.Count(resp.Text, "AKSİNİ SÖYLEMEDİKÇE") != 1 || !strings.HasPrefix(resp.Text, "INSURANCE_DOCUMENT") {
		t.Fatalf("temiz önek: %q", resp.Text)
	}
	if r.visible() != resp.Text {
		t.Fatalf("akan %q ≠ metin %q", r.visible(), resp.Text)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("üst akış kesilmedi — sunucu yazmaya devam ediyor")
	}
	if n := served.Load(); n > 500 {
		t.Fatalf("üst akış geç kesildi: %d parça", n)
	}
}

// Anthropic ikizi aynı sözleşme (muhafız kapıda; iptal hata değil).
func TestAnthropicGateDegenerationFinishes(t *testing.T) {
	r := &gateRec{}
	g := newToolTurnGate(r.sink, true, nil)
	g.onDegenerate = func() {}
	for _, c := range splitEvery(prodDegenSample(30), 17) {
		g.Content(c)
	}
	resp := degeneratedResponse(ChatResponse{InputTokens: 3}, g)
	if !resp.Degenerated || resp.InputTokens != 3 || resp.Text != g.CleanText() || r.visible() != resp.Text {
		t.Fatalf("yanıt %+v / görünen %q", resp, r.visible())
	}
}
