package api

import (
	"strings"
	"testing"
)

// narrativeIdeal — fikstür başına elle yazılmış örnek cevap (yalnız ölçünün
// kendisini sınar; model çıktısı değil).
var narrativeIdeal = map[string]string{
	"trace-narrative-business-rejection": "Business hata\nKARAR: orders-api'nin POST /orders isteği 60 ms içinde HTTP 400 ile bitti. Ret payments-api'nin POST /charge cevabından geliyor: \"amount must be positive\". Bu bir arıza değil, business hata; payments-api ayakta ve 30 ms'de yanıt verdi. Tutar payments-api'ye sıfır ya da boş ulaştı.\nSonraki adım: İsteği gönderen kanalda tutar alanının nasıl dolduğuna bakın.",
	"trace-narrative-dependency-5xx":     "Bağımlılık hatası\nKARAR: checkout-api'nin POST /checkout isteği 70 ms içinde 502 ile bitti. Kaynak inventory-api'nin GET /stock çağrısının 500 cevabı ve \"stock cache not initialized\" exception'ı. Çağrı 42 ms sürdü, 24 saatlik p50'nin iki katından fazla. checkout-api'deki hata bu çağrının sonucu.\nSonraki adım: inventory-api'de stok önbelleğinin başlatılmasına bakın; checkout-api'de bu hatayı yakalayan savunmayı ekleyin.",
	"trace-narrative-timeout":            "Zaman aşımı\nKARAR: orders-api'nin POST /orders/ship isteği 5.04 s sonra hata ile bitti. shipping-api'ye giden POST /quote çağrısı 5.00 s sonra \"Read timed out\" ile kesildi. Bu çağrının 24 saatlik p50'si 120 ms. Kök süresinin neredeyse tamamı bu beklemede geçti.\nSonraki adım: shipping-api'nin o anki yanıt sürelerine ve orders-api'deki okuma zaman aşımı ayarına bakın.",
	"trace-narrative-slow-external-wait": "Yavaşlık\nKARAR: orders-api'nin POST /orders/score isteği 12.46 s sürdü ve HTTP 200 ile bitti. Sürenin %98'i gateway.ext.example'a giden çağrıda, 12.21 s dış bağımlılık beklemesinde geçti. Hata yok; yavaşlık orders-api'nin kendi kodunda değil. Dış hedef bu istekte yavaş yanıt verdi.\nSonraki adım: gateway.ext.example tarafının yanıt sürelerine ve çağrının zaman aşımı ayarına bakın.",
	"trace-narrative-auth-error":         "Yetki hatası\nKARAR: billing-api'nin POST /invoices/close isteği 48 ms içinde 500 ile bitti. Kaynak ledger-api'nin POST /entries çağrısına verdiği 403: \"principal billing-svc is not allowed to write ledger entries\". Bu bir yetki reddi; ledger-api ayakta ve hızlı yanıt verdi. billing-api 403'ü 500'e çevirdi.\nSonraki adım: billing-svc kimliğine ledger kayıtları için yazma iznini verin.",
	"trace-narrative-success":            "Başarılı\nKARAR: orders-api'nin GET /orders/{id} isteği 45 ms içinde HTTP 200 ile bitti. Veritabanı okuması 12 ms, customers-api çağrısı 18 ms sürdü. Hata ya da belirgin yavaşlık yok.\nSonraki adım: Bu trace için işlem gerekmiyor.",
	"trace-narrative-unclear":            "Belirsiz\nKARAR: reports-api'nin POST /reports/run isteği 80 ms içinde hata durumuyla bitti. Span'de mesaj, exception ya da HTTP kodu yok. Bu trace için log da bulunamadı. Kanıt hatanın türünü söylemeye yetmiyor.\nSonraki adım: reports-api'nin bu istek için hata logu yazıp yazmadığına bakın.",
}

// TestTraceNarrativeFixturesGradeIdealAnswers — v0.10.2482: her anlatım
// fikstürünün örnek cevabı ölçüden geçer (sunucu bileşimi dahil); bölümlü
// cevap ve yanlış etiket kırmızı olur.
func TestTraceNarrativeFixturesGradeIdealAnswers(t *testing.T) {
	cases, err := loadEvalsetCases()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range cases {
		if c.Surface != evalSurfaceTraceNarrative {
			continue
		}
		n++
		ideal, ok := narrativeIdeal[c.ID]
		if !ok {
			t.Errorf("%s: örnek cevap yok", c.ID)
			continue
		}
		sys, _ := evalSystemPromptFor(c.Surface, false)
		ans := evalServerCompose(c.Surface, c.User, ideal)
		if fails, _ := scoreEvalCase(c, sys, ans, nil); len(fails) > 0 {
			t.Errorf("%s: örnek cevap kırmızı: %v", c.ID, fails)
		}
		if fails, _ := scoreEvalCase(c, sys, "**Özet:** "+ideal, nil); len(fails) == 0 {
			t.Errorf("%s: bölümlü / etiketsiz cevap geçmemeli", c.ID)
		}
	}
	if n < 6 || n > 8 {
		t.Fatalf("6–8 anlatım fikstürü bekleniyor, %d", n)
	}
	labels := map[string]bool{}
	for _, c := range cases {
		if c.Expect.Narrative != nil {
			labels[c.Expect.Narrative.Label] = true
		}
	}
	for _, want := range []string{"Business hata", "Bağımlılık hatası", "Zaman aşımı", "Yavaşlık", "Yetki hatası", "Başarılı", "Belirsiz"} {
		if !labels[want] {
			t.Errorf("fikstürlerde %q etiketi yok", want)
		}
	}
}

func TestEvalNarrativeFails(t *testing.T) {
	e := &evalNarrativeExpect{Label: "Business hata", Quotes: []string{"amount must be positive"}}
	good := narrativeIdeal["trace-narrative-business-rejection"]
	if f := evalNarrativeFails(e, good); len(f) != 0 {
		t.Fatalf("iyi cevap: %v", f)
	}
	for name, c := range map[string]struct {
		ans, want string
	}{
		"yanlış etiket":    {strings.Replace(good, "Business hata\n", "Bağımlılık hatası\n", 1), "label \"Bağımlılık hatası\""},
		"etiket yok":       {strings.Replace(good, "Business hata\n", "", 1), "label missing"},
		"kısa karar":       {"Business hata\nKARAR: \"amount must be positive\" döndü.\nSonraki adım: x.", "sentences"},
		"çevrilmiş alıntı": {strings.Replace(good, "amount must be positive", "tutar pozitif olmalı", 1), "verbatim"},
		"alan dökümü":      {strings.Replace(good, "Tutar payments-api'ye", "http.status_code=400 user_id=7 {\"a\":1} com.acme.orders.Svc getAmount trace_id=x span_id=y Tutar payments-api'ye", 1), "code-like tokens"},
		"sonraki adım yok": {good[:strings.Index(good, "\nSonraki adım")], "next step missing"},
	} {
		f := strings.Join(evalNarrativeFails(e, c.ans), " | ")
		if !strings.Contains(f, c.want) {
			t.Errorf("%s: %q bekleniyordu, ihlaller: %s", name, c.want, f)
		}
	}
	if evalNarrativeFails(nil, "x") != nil {
		t.Fatal("beklenti yoksa ihlal yok")
	}
}

func TestEvalNarrativeFixtureErr(t *testing.T) {
	ok := evalCase{Surface: evalSurfaceTraceNarrative, User: "x amount y", Expect: evalExpect{Narrative: &evalNarrativeExpect{Label: "Yavaşlık", Quotes: []string{"amount"}}}}
	if msg := evalNarrativeFixtureErr(ok); msg != "" {
		t.Fatal(msg)
	}
	for name, c := range map[string]evalCase{
		"başka yüzeyde":      {Surface: "Trace", Expect: evalExpect{Narrative: &evalNarrativeExpect{Label: "Yavaşlık"}}},
		"beklenti yok":       {Surface: evalSurfaceTraceNarrative},
		"küme dışı":          {Surface: evalSurfaceTraceNarrative, Expect: evalExpect{Narrative: &evalNarrativeExpect{Label: "Slow"}}},
		"alıntı kanıtta yok": {Surface: evalSurfaceTraceNarrative, User: "x", Expect: evalExpect{Narrative: &evalNarrativeExpect{Label: "Yavaşlık", Quotes: []string{"zz"}}}},
	} {
		if evalNarrativeFixtureErr(c) == "" {
			t.Errorf("%s: geçersiz sayılmalı", name)
		}
	}
}
