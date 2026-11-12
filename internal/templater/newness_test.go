package templater

// v0.10.2040 — Operator-reported: "Aslında benzer loglar var ama yeni log
// pattern gibi söyledin." Servisin en sık satırı (uzun `anahtar=değer, …`
// dökümü) "yeni log şablonu" diye işaretlendi. Kök neden üç kural:
//  1. maske `anahtar=değer` içindeki kısa rastgele kimliği (11 karakter,
//     büyük/küçük + rakam + `_`) sabit sayıyordu;
//  2. aile kuralı EŞİT belirteç sayısı istiyordu; değeri boşluk içeren
//     alanlar (tarayıcı kimliği, tarih metni, cihaz dizgesi) satırdan satıra
//     belirteç sayısını değiştiriyordu;
//  3. gövdesi JSON zarfı olan satırda şablon tüm zarftan çıkıyordu.
// Fikstürler nötr; biçimler prod örneklerinin aynısı.

import (
	"strings"
	"testing"
)

// tplOf — satırın maskeli şablonu (Drain'in ilk görüşteki şablonu).
func tplOf(line string) string { return strings.Join(Tokenize(line), " ") }

func authLine(id, ua, sms, os string) string {
	return "AuthContext(challengeCode=482913, tabId=" + id + ", clientUserAgent=" + ua +
		", smsTime=" + sms + ", deviceOs=" + os + ", channel=MOBILE, status=null, imageType=SECURITY_IMAGE, response=LoginStatusResponse)"
}

func TestMaskKeyValueRandomIDs(t *testing.T) {
	for _, id := range []string{"eISAG_fy28U", "JVNQbZ54lgk", "bZNnzw9iSFk", "z8CscvEYOzo"} {
		m := Mask("tabId=" + id + ", next=1")
		if !strings.Contains(m, "tabId=<*>") {
			t.Errorf("%s maskelenmedi: %q", id, m)
		}
		j := Mask(`{"tabId":"` + id + `","next":"x"}`)
		if !strings.Contains(j, `"tabId":"<*>"`) {
			t.Errorf("JSON %s maskelenmedi: %q", id, j)
		}
	}
	// Yanlış pozitif olmamalı: sürüm adları, sınıf/tür adları, büyük harfli
	// sabitler, sözlük belirteçleri.
	for _, v := range []string{
		"imageType=SECURITY_IMAGE", "response=LoginStatusResponse", "handler=OrderServiceImpl", "proto=HTTPServer",
		"build=release-2024.11", "version=v2.0.76", "algo=sha256", "charset=utf8mb4", "plugin=Log4j2Plugins",
		"status=ACCEPTED", "channel=MOBILE", "state=inProgress",
	} {
		// Sayı kuralı sürümdeki rakamları zaten maskeler (eski davranış);
		// yeni kural DEĞERİN TAMAMINI kimlik saymamalı.
		key := v[:strings.IndexByte(v, '=')]
		if m := Mask(v); strings.Contains(m, key+"=<*>") {
			t.Errorf("sabit kimlik sayıldı: %q → %q", v, m)
		}
	}
}

func TestNewnessFamilyKeyValueDump(t *testing.T) {
	a := tplOf(authLine("eISAG_fy28U", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148",
		"Thu Nov 12 14:48:06 TRT 2026", "ios 26.6.1|2.0.76|apple iPhone18,1"))
	b := tplOf(authLine("JVNQbZ54lgk", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120.0 Mobile",
		"Thu Nov 12 14:49:10 TRT 2026", "android 14|2.0.75|acme A55"))
	if countTemplateTokens(a) == countTemplateTokens(b) {
		t.Fatalf("fikstür belirteç sayısını değiştirmeli (%d)", countTemplateTokens(a))
	}
	if !ParseTemplate(a).SameNewnessFamily(ParseTemplate(b)) {
		t.Fatalf("aynı biçim aynı aile olmalı:\n%s\n%s", a, b)
	}
	// Defter okuması (SQL ön süzgeci) bu ailenin sabit başını taşır; kimlik yok.
	if lp := ParseTemplate(a).LiteralPrefix(); lp != "AuthContext(challengeCode=" {
		t.Fatalf("sabit baş %q", lp)
	}
	// Remask geçişi: eski maskeyle (kimlik açık) saklanmış defter satırı da
	// yeni adayla aynı aile (ParseTemplate yeniden maskeler).
	oldStored := strings.Replace(a, "tabId=<*>,", "tabId=z8CscvEYOzo,", 1)
	if oldStored == a || !ParseTemplate(oldStored).SameNewnessFamily(ParseTemplate(b)) {
		t.Fatal("eski maskeli defter satırı aile olmalı (Remask)")
	}
	// Aynı ön ekle başlayan ama anahtarları farklı döküm aile DEĞİL.
	other := tplOf("AuthContext(challengeCode=1, orderRef=1, warehouse=2, carrier=3, region=4, slot=5, dock=6)")
	if ParseTemplate(a).SameNewnessFamily(ParseTemplate(other)) {
		t.Fatal("farklı anahtar kümesi aile sayılmamalı")
	}
	// Kısa / genel ön ek aşırı birleştirmez.
	if ParseTemplate(tplOf("INFO started job <*>")).SameNewnessFamily(ParseTemplate(tplOf("INFO stopped worker <*> after retry"))) {
		t.Fatal("kısa ön ek aile sayılmamalı")
	}
	// Farklı uzun satırlar aile değil.
	x := tplOf("Payment authorization failed for customer account because the issuer declined the request, retrying later")
	y := tplOf("Inventory reservation succeeded for warehouse shipment after manual review by operator team")
	if ParseTemplate(x).SameNewnessFamily(ParseTemplate(y)) {
		t.Fatal("farklı satırlar aile sayılmamalı")
	}
}

func TestTemplateSourceJSONEnvelope(t *testing.T) {
	l1 := `{"request_id":"a1b2c3","service_name":"orders-api","channel":"web","thread":"default task-2","timestamp":"2026-11-12T14:48:06.123Z","message":"Order 4711 accepted for customer 99"}`
	l2 := `{"request_id":"ff0011","service_name":"orders-api","channel":"web","thread":"default task-7 worker","timestamp":"2026-11-12T14:49:10.001Z","message":"Order 4712 accepted for customer 12"}`
	if got := TemplateSource(l1); got != "Order 4711 accepted for customer 99" {
		t.Fatalf("message alanı: %q", got)
	}
	if tplOf(TemplateSource(l1)) != tplOf(TemplateSource(l2)) {
		t.Fatalf("zarf alanları şablona girmemeli: %q / %q", tplOf(TemplateSource(l1)), tplOf(TemplateSource(l2)))
	}
	for in, want := range map[string]string{
		`{"msg":"cache warmed"}`:                  "cache warmed",
		`{"log":"listener started on port 8080"}`: "listener started on port 8080",
		`{"level":"info","event":"no message"}`:   `{"level":"info","event":"no message"}`,
		`plain text line`:                         "plain text line",
		`{broken json`:                            "{broken json",
		`{"message":42}`:                          `{"message":42}`,
	} {
		if got := TemplateSource(in); got != want {
			t.Errorf("%q → %q, istenen %q", in, got, want)
		}
	}
}
