package api

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// answer_verify_test.go — v0.10.2327 (E1): tolerans tablosu. Her kural için
// eşleşen ve eşleşmeyen çift (audit §6.1 E1 "Test" sütunu).

func istanbul(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		return time.FixedZone("TRT", 3*3600)
	}
	return loc
}

// verifyOne — tek iddialı cevabın (kind, status) çifti; iddia sayısı 1 değilse hata.
func verifyOne(t *testing.T, tier, answer string, ev verifyEvidence, catalog ...string) verifyClaim {
	t.Helper()
	r := verifyAnswer(tier, answer, ev, catalog)
	if len(r.Claims) != 1 {
		t.Fatalf("%q: %d iddia bekleniyordu 1: %+v", answer, len(r.Claims), r.Claims)
	}
	return r.Claims[0]
}

func TestVerifyNumberTolerance(t *testing.T) {
	ev := func(s string) verifyEvidence { return verifyEvidence{Texts: []string{s}} }
	cases := []struct {
		name, answer, evidence, kind, want string
	}{
		// düz sayı: yazılan basamak kesin (yarım birim = 0,5)
		{"tamsayı aynen", "checkout 480 istek aldı.", `{"requests":480}`, verifyKindNumber, verifyVerified},
		{"tamsayı farklı", "checkout 490 istek aldı.", `{"requests":480}`, verifyKindNumber, verifyMismatch},
		{"ondalık yuvarlama içinde", "ortalama 3,6 istek/s.", `{"rps":3.64}`, verifyKindNumber, verifyVerified},
		{"ondalık yuvarlama dışında", "ortalama 3,6 istek/s.", `{"rps":3.66}`, verifyKindNumber, verifyMismatch},
		{"binlik nokta", "toplam 1.234 istek.", `{"requests":1234}`, verifyKindNumber, verifyVerified},
		// büyüklük sözcüğü: "74 bin" → 73.500–74.499
		{"bin alt sınır", "74 bin istek geldi.", `{"requests":73500}`, verifyKindNumber, verifyVerified},
		{"bin üst sınır içi", "74 bin istek geldi.", `{"requests":74499}`, verifyKindNumber, verifyVerified},
		{"bin üst sınır dışı", "74 bin istek geldi.", `{"requests":74500}`, verifyKindNumber, verifyMismatch},
		{"bin tipik", "74 bin istek geldi.", `{"requests":74167}`, verifyKindNumber, verifyVerified},
		{"milyon ondalık", "1,2 milyon span işlendi.", `{"count":1234567}`, verifyKindNumber, verifyVerified},
		{"milyon ondalık dışı", "1,2 milyon span işlendi.", `{"count":1290000}`, verifyKindNumber, verifyMismatch},
		// sayı sözcüğü
		{"sözcük + bin", "on bin istek geldi.", `{"requests":10234}`, verifyKindNumber, verifyVerified},
		{"sözcük + bin dışı", "on bin istek geldi.", `{"requests":11000}`, verifyKindNumber, verifyMismatch},
		{"yarım saniye", "p95 yarım saniye civarında.", `{"p95_ms":512}`, verifyKindDuration, verifyVerified},
		{"iki saniye içinde", "istek iki saniye sürdü.", `{"duration_ms":2400}`, verifyKindDuration, verifyVerified},
		{"iki saniye dışında", "istek iki saniye sürdü.", `{"duration_ms":2600}`, verifyKindDuration, verifyMismatch},
		// birim dönüşümü
		{"ms aynen", "p95 480 ms.", `{"p95_ms":480}`, verifyKindDuration, verifyVerified},
		{"ms → sn", "p95 1,2 sn.", `{"p95_ms":1234}`, verifyKindDuration, verifyVerified},
		{"ms → sn dışı", "p95 1,2 sn.", `{"p95_ms":1260}`, verifyKindDuration, verifyMismatch},
		{"saniye → ms", "p99 480 ms oldu.", `{"p99_s":0.48}`, verifyKindDuration, verifyVerified},
		{"dakika", "kesinti 15 dakika sürdü.", `{"range_s":900}`, verifyKindDuration, verifyVerified},
		{"dk bitişik ek", "kesinti 15 dk'lık.", `{"duration_ms":900000}`, verifyKindDuration, verifyVerified},
		// yüzde ↔ oran
		{"yüzde aynen", "hata oranı %3,6.", `{"error_pct":3.6}`, verifyKindPercent, verifyVerified},
		{"oran → yüzde", "hata oranı %12.", `{"error_rate":0.12}`, verifyKindPercent, verifyVerified},
		{"yüzde dışı", "hata oranı %12.", `{"error_rate":0.13}`, verifyKindPercent, verifyMismatch},
		{"yüzde sonda", "hata oranı 12%.", `{"error_rate":0.12}`, verifyKindPercent, verifyVerified},
		// yaklaşıklık: ±%5
		{"yaklaşık içinde", "yaklaşık 500 ms.", `{"p95_ms":480}`, verifyKindDuration, verifyVerified},
		{"yaklaşık dışında", "yaklaşık 500 ms.", `{"p95_ms":470}`, verifyKindDuration, verifyMismatch},
		{"civarı", "500 ms civarı.", `{"p95_ms":481}`, verifyKindDuration, verifyVerified},
		{"tilde", "~500 ms.", `{"p95_ms":478}`, verifyKindDuration, verifyVerified},
		{"yaklaşık yok", "500 ms.", `{"p95_ms":481}`, verifyKindDuration, verifyMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := verifyOne(t, verifyTierLoop, c.answer, ev(c.evidence))
			if got.Kind != c.kind || got.Status != c.want {
				t.Fatalf("%q kanıt %s → %s/%s, istenen %s/%s", c.answer, c.evidence, got.Kind, got.Status, c.kind, c.want)
			}
		})
	}
}

func TestVerifyDerived(t *testing.T) {
	ev := verifyEvidence{Texts: []string{`{"current":{"p95_ms":1230},"previous":{"p95_ms":480}}`}}
	r := verifyAnswer(verifyTierLoop, "p95 480 ms'den 1.230 ms'ye çıktı; artış 750 ms, yani %156.", ev, nil)
	want := map[string]string{"480 ms": verifyVerified, "1.230 ms": verifyVerified, "750 ms": verifyDerived, "%156": verifyDerived}
	for _, c := range r.Claims {
		if w, ok := want[c.Text]; ok && c.Status != w {
			t.Errorf("%s → %s, istenen %s", c.Text, c.Status, w)
		}
		delete(want, c.Text)
	}
	if len(want) > 0 {
		t.Errorf("bulunmayan iddialar: %v (%+v)", want, r.Claims)
	}
	// türetilemeyen sayı uyuşmaz
	r = verifyAnswer(verifyTierLoop, "p95 480 ms, p99 2.950 ms.", ev, nil)
	if c := r.Claims[len(r.Claims)-1]; c.Text != "2.950 ms" || c.Status != verifyMismatch {
		t.Errorf("uydurma sayı: %+v", r.Claims)
	}
}

func TestVerifyNotClaims(t *testing.T) {
	// kimlikler, tek basamak, liste imi, sürüm, HTTP kodu, bağlantı → iddia değil
	answer := "1. checkout p95 ve [T1] ile v2 sürümü 1.4.2; 3 servis; HTTP 503; 5xx; " +
		"trace 4bf92f3577b34da6a3ce929d0e0e4736; https://example.test/a/b?x=12345"
	r := verifyAnswer(verifyTierLoop, answer, verifyEvidence{}, nil)
	if len(r.Claims) != 0 {
		t.Fatalf("iddia beklenmiyordu: %+v", r.Claims)
	}
}

func TestVerifyTimes(t *testing.T) {
	loc := istanbul(t)
	// kanıt UTC damgası 11:32Z → İstanbul 14:32
	ev := verifyEvidence{Texts: []string{`{"ts":"2026-11-18T11:32:10Z"}`}, Loc: loc}
	cases := []struct{ answer, want string }{
		{"hata 14:32'de başladı.", verifyVerified},
		{"hata 14:33'te başladı.", verifyVerified}, // ±1 dk
		{"hata 14:35'te başladı.", verifyMismatch},
		{"hata 11:32'de başladı.", verifyMismatch}, // UTC saati yazılmış: kullanıcı dilimi değil
		{"hata saat 14.32'de başladı.", verifyVerified},
	}
	for _, c := range cases {
		got := verifyOne(t, verifyTierLoop, c.answer, ev)
		if got.Kind != verifyKindTime || got.Status != c.want {
			t.Errorf("%q → %+v, istenen %s", c.answer, got, c.want)
		}
	}
	// epoch ms kanıt + kovalı seri (5 dk): kova içi saat doğrulanır
	base := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	var b strings.Builder
	b.WriteString(`{"points":[`)
	for i := 0; i < 6; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"t":`)
		b.WriteString(strconv.FormatInt(base.Add(time.Duration(i*5)*time.Minute).UnixMilli(), 10))
		b.WriteString(`,"v":1}`)
	}
	b.WriteString(`]}`)
	ev = verifyEvidence{Texts: []string{b.String()}, Loc: loc}
	if got := verifyOne(t, verifyTierLoop, "sıçrama 14:17 civarında.", ev); got.Status != verifyVerified {
		t.Errorf("kova içi: %+v", got)
	}
	if got := verifyOne(t, verifyTierLoop, "sıçrama 15:10'da.", ev); got.Status != verifyMismatch {
		t.Errorf("kova dışı: %+v", got)
	}
	// pencere sınırı ve tarih: dün 14:30–bugün 14:30
	now := time.Date(2026, 11, 18, 11, 30, 0, 0, time.UTC)
	ev = verifyEvidence{Times: []time.Time{now.Add(-24 * time.Hour), now}, Loc: loc}
	if got := verifyOne(t, verifyTierLoop, "pencere 14:30'da bitiyor.", ev); got.Status != verifyVerified {
		t.Errorf("pencere sonu: %+v", got)
	}
	if got := verifyOne(t, verifyTierLoop, "17 Kasım'da sakin.", ev); got.Kind != verifyKindDate || got.Status != verifyVerified {
		t.Errorf("pencere günü: %+v", got)
	}
	if got := verifyOne(t, verifyTierLoop, "12 Kasım'da sakin.", ev); got.Status != verifyMismatch {
		t.Errorf("pencere dışı gün: %+v", got)
	}
}

func TestVerifyNames(t *testing.T) {
	ev := verifyEvidence{
		Texts: []string{`{"service":"checkout","pod":"orders-7d9f8b6c5d-x2x9z","exception":"java.net.SocketTimeoutException","op":"GET /api/orders/{id}"}`},
		Names: []string{"payments"},
	}
	cat := []string{"checkout", "orders", "payments", "inventory", "api"}
	r := verifyAnswer(verifyTierLoop,
		"checkout ve payments etkilendi; `orders-7d9f8b6c5d-x2x9z` yeniden başladı, SocketTimeoutException görüldü. "+
			"inventory etkilenmedi. API çağrıları normal.", ev, cat)
	want := map[string]string{"checkout": verifyVerified, "payments": verifyVerified, "orders-7d9f8b6c5d-x2x9z": verifyVerified,
		"SocketTimeoutException": verifyVerified, "inventory": verifyMismatch}
	for _, c := range r.Claims {
		if c.Kind != verifyKindName {
			continue
		}
		w, ok := want[c.Text]
		if !ok {
			t.Errorf("beklenmeyen ad iddiası %+v", c)
			continue
		}
		if c.Status != w {
			t.Errorf("%s → %s, istenen %s", c.Text, c.Status, w)
		}
		delete(want, c.Text)
	}
	if len(want) > 0 {
		t.Errorf("bulunmayan: %v", want)
	}
	// operasyon: "GET /api/orders/42" kanıttaki yolun sayısal son parçasız hâli
	if got := verifyOne(t, verifyTierLoop, "`GET /api/orders` yavaş.", verifyEvidence{Texts: []string{`GET /api/orders`}}); got.Status != verifyVerified {
		t.Errorf("operasyon: %+v", got)
	}
	if got := verifyOne(t, verifyTierLoop, "`PaymentDeclinedError` arttı.", ev); got.Status != verifyMismatch {
		t.Errorf("uydurma exception: %+v", got)
	}
}

func TestVerifyGeneralTier(t *testing.T) {
	cat := []string{"checkout", "orders"}
	// genel bilgi: düz sayılar sayılmaz; canlı veri biçimleri (yüzde, süre, katalog adı) uyuşmaz
	r := verifyAnswer(verifyTierGeneral, "HTTP 503 geçici bir hatadır; varsayılan port 8080 olabilir.", verifyEvidence{}, cat)
	if len(r.Claims) != 0 {
		t.Fatalf("genel bilgi iddia sayılmamalı: %+v", r.Claims)
	}
	r = verifyAnswer(verifyTierGeneral, "checkout şu an %4 hata veriyor, p95 820 ms.", verifyEvidence{Texts: []string{"genel olarak ne olur?"}}, cat)
	_, by := r.totals()
	if by[verifyKindName].Mismatch != 1 || by[verifyKindPercent].Mismatch != 1 || by[verifyKindDuration].Mismatch != 1 {
		t.Fatalf("genel cevapta canlı veri: %+v", r.Claims)
	}
	// operatörün kendi yazdığı sayı uyuşmazlık değil
	if got := verifyOne(t, verifyTierGeneral, "%5 eşiği makul.", verifyEvidence{Texts: []string{"%5 eşiği makul mü?"}}); got.Status != verifyVerified {
		t.Errorf("operatör sayısı: %+v", got)
	}
}

func TestVerifyCompact(t *testing.T) {
	r := verifyResult{Tier: verifyTierGuided, Claims: []verifyClaim{
		{Kind: verifyKindNumber, Status: verifyVerified}, {Kind: verifyKindNumber, Status: verifyDerived},
		{Kind: verifyKindTime, Status: verifyMismatch}}}
	if got := r.compact(); got != "v1 tier=guided number=2/1/1/0 time=1/0/0/1" {
		t.Fatalf("compact: %q", got)
	}
	if got := (verifyResult{Tier: verifyTierGeneral}).compact(); got != "v1 tier=general" {
		t.Fatalf("boş: %q", got)
	}
}

// BenchmarkVerifyAnswerRealistic — v0.10.2327: tur başına eklenen CPU. Gerçekçi
// tur: ~1,3 KB cevap (sayı, yüzde, süre, saat, ad), 6 araç çıktısı × ~8 KB JSON
// (zaman serisi dahil), 2.000 servislik katalog.
func BenchmarkVerifyAnswerRealistic(b *testing.B) {
	loc := istanbul(b)
	base := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	var texts []string
	for k := 0; k < 6; k++ {
		var sb strings.Builder
		sb.WriteString(`{"service":"checkout","env":"prod","current":{"p95_ms":1830,"p99_ms":2950.5,"error_rate":0.0123,"requests":74167},"series":[`)
		for i := 0; i < 90; i++ {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(`{"t":"`)
			sb.WriteString(base.Add(time.Duration(i*5) * time.Minute).Format(time.RFC3339))
			sb.WriteString(`","p95_ms":`)
			sb.WriteString(strconv.Itoa(400 + i*7 + k))
			sb.WriteString(`,"errors":`)
			sb.WriteString(strconv.Itoa(i % 13))
			sb.WriteByte('}')
		}
		sb.WriteString(`],"top":[{"operation":"GET /api/orders","pod":"orders-7d9f8b6c5d-x2x9z","exception":"java.net.SocketTimeoutException"}]}`)
		texts = append(texts, sb.String())
	}
	ev := verifyEvidence{Texts: texts, Names: []string{"checkout", "orders"}, Numbers: []float64{21600},
		Times: []time.Time{base, base.Add(6 * time.Hour)}, Loc: loc}
	catalog := make([]string, 0, 2000)
	for i := 0; i < 1997; i++ {
		catalog = append(catalog, "svc-"+strconv.Itoa(i))
	}
	catalog = append(catalog, "checkout", "orders", "payments")
	answer := strings.Repeat("checkout bu sabah 09:15 civarında yavaşladı: p95 yaklaşık 1,8 sn, p99 2.950 ms; "+
		"hata oranı %1,2 ve toplam 74 bin istek. `GET /api/orders` en yavaş operasyon, "+
		"`orders-7d9f8b6c5d-x2x9z` podunda SocketTimeoutException arttı. Önceki pencereye göre 1,5 kat; "+
		"payments etkilenmemiş görünüyor, 10:20'den sonra toparlandı.\n", 3)
	if testing.Verbose() {
		b.Logf("%s", verifyAnswer(verifyTierLoop, answer, ev, catalog).compact())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := verifyAnswer(verifyTierLoop, answer, ev, catalog)
		if len(r.Claims) == 0 {
			b.Fatal("iddia yok")
		}
	}
}
