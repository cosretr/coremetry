package chstore

// request_split_test.go — v0.10.2380: başarısız / başarılı istek bölümü.
// Sözleşme: sayılar tikte bir kez üretilir (NewRequestSplit), Problem
// açıklamasındaki işaret cümlesi geri okununca AYNI bölümü verir, standardın
// etki cümlesi yalnız sayılar BİLİNİYORSA değişir (bilinmiyorsa metin aynen),
// manşete girmez; öncelik / güven / bildirim tabanı ölçüleri değişmez.

import (
	"reflect"
	"testing"
	"time"
)

func TestNewRequestSplit(t *testing.T) {
	for _, c := range []struct {
		name                         string
		ratio, load, baseRatio, base float64
		ok                           bool
		failed, okPerMin             float64
		normal                       []float64 // nil = olağan düzey yazılmaz
	}{
		{"olağan düzey biliniyor", 0.0677, 3100, 0.004, 3102, true, 210, 2890, []float64{12, 3090}},
		{"başarısız yok", 0, 2890, 0.004, 3000, true, 0, 2890, []float64{12, 2988}},
		{"taban oranı 0", 0.05, 200, 0, 180, true, 10, 190, []float64{0, 180}},
		{"taban trafiği bilinmiyor", 0.05, 200, 0.004, 0, true, 10, 190, nil},
		{"istek yok", 0.5, 0, 0.004, 100, false, 0, 0, nil},
		{"istek yarımın altında", 1, 0.4, 0, 0, false, 0, 0, nil},
		{"oran 1'i aşamaz", 1.2, 10, 0, 0, true, 10, 0, nil},
	} {
		s, ok := NewRequestSplit(600, c.ratio, c.load, c.baseRatio, c.base)
		if ok != c.ok {
			t.Fatalf("%s: ok=%v", c.name, ok)
		}
		if !ok {
			continue
		}
		if s.Minute != 600 || s.FailedPerMin != c.failed || s.OKPerMin != c.okPerMin {
			t.Errorf("%s: %+v", c.name, s)
		}
		if (c.normal == nil) != (s.NormalFailedPerMin == nil) || (c.normal != nil && (*s.NormalFailedPerMin != c.normal[0] || *s.NormalOKPerMin != c.normal[1])) {
			t.Errorf("%s: olağan düzey %v / %v, beklenen %v", c.name, s.NormalFailedPerMin, s.NormalOKPerMin, c.normal)
		}
	}
}

func TestRequestSplitSentenceRoundTrip(t *testing.T) {
	full, _ := NewRequestSplit(0, 0.0677, 3100, 0.004, 3102)
	bare, _ := NewRequestSplit(0, 0.05, 1200, 0, 0)
	zero, _ := NewRequestSplit(0, 0, 2890, 0, 3000)
	for _, c := range []struct {
		s    StdRequestSplit
		want string
	}{
		{full, "Dakikada 210 başarısız, 2.890 başarılı istek (olağanı 12 başarısız, 3.090 başarılı)."},
		{bare, "Dakikada 60 başarısız, 1.140 başarılı istek."},
		{zero, "Dakikada 0 başarısız, 2.890 başarılı istek (olağanı 0 başarısız, 3.000 başarılı)."},
	} {
		got := RequestSplitSentence(c.s)
		if got != c.want {
			t.Errorf("cümle %q, beklenen %q", got, c.want)
		}
		desc := "orders servisinde hata oranı %6,8, olağanı %0,4. " + got + " orders servisinde 3 operasyon birden yavaşladı."
		back, ok := ParseRequestSplit(desc)
		if !ok || !reflect.DeepEqual(back, c.s) {
			t.Errorf("geri okuma %+v ok=%v, beklenen %+v", back, ok, c.s)
		}
		if h := StripRequestSplit(desc); h != "orders servisinde hata oranı %6,8, olağanı %0,4. orders servisinde 3 operasyon birden yavaşladı." {
			t.Errorf("cümle atılınca: %q", h)
		}
	}
	if s := RequestSplitSentence(StdRequestSplit{}); s != "" {
		t.Errorf("geçersiz bölüm cümle üretmemeli: %q", s)
	}
	for _, desc := range []string{"", "orders servisinde hata oranı %6,8, olağanı %0,4.", "Dakikada 0 başarısız, 0 başarılı istek.", "Dakikada çok başarısız, az başarılı istek."} {
		if _, ok := ParseRequestSplit(desc); ok {
			t.Errorf("cümle yok / istek yok: %q okunmamalı", desc)
		}
		if StripRequestSplit(desc) != desc && desc != "Dakikada 0 başarısız, 0 başarılı istek." {
			t.Errorf("cümlesiz açıklama değişmemeli: %q", desc)
		}
	}
}

func TestMLRequestSplitOf(t *testing.T) {
	if rs := MLRequestSplitOf(`{"rule":"impact","requests":{"minute":600,"failedPerMin":210,"okPerMin":2890,"normalFailedPerMin":12,"normalOkPerMin":3090}}`); rs == nil ||
		rs.Minute != 600 || rs.FailedPerMin != 210 || rs.OKPerMin != 2890 || *rs.NormalFailedPerMin != 12 || *rs.NormalOKPerMin != 3090 {
		t.Fatalf("kanıttan bölüm: %+v", rs)
	}
	for _, ev := range []string{"", `{"rule":"impact","ratios":[0.05],"loads":[200]}`, `{"requests":null}`, `{"requests":{"failedPerMin":0,"okPerMin":0}}`,
		`{"requests":{"failedPerMin":-1,"okPerMin":5}}`, `{"requests":{"failedPerMin":1,"okPerMin":5,"normalOkPerMin":4}}`, `{"requests":"bozuk"}`} {
		if rs := MLRequestSplitOf(ev); rs != nil {
			t.Errorf("bölüm yok / bozuk: %s → %+v", ev, rs)
		}
	}
}

func TestSharePctAndPossessive(t *testing.T) {
	for _, c := range []struct {
		pct  float64
		want string
	}{
		{6.774, "6,8'i"}, {0, "0'ı"}, {7, "7'si"}, {3.04, "3'ü"}, {10, "10'u"}, {20, "20'si"}, {40, "40'ı"}, {50, "50'si"}, {60.02, "60'ı"},
		{70, "70'i"}, {90, "90'ı"}, {100, "100'ü"}, {0.04, "0,04'ü"}, {12.56, "12,6'sı"}, {99.95, "100'ü"}, {30.5, "30,5'i"}, {2.19, "2,2'si"}, {9.9, "9,9'u"},
	} {
		p := sharePct(c.pct)
		got := trNumComma(p) + trPossessive(p)
		if got != c.want {
			t.Errorf("%%%v → %q, beklenen %q", c.pct, got, c.want)
		}
	}
}

func trNumComma(s string) string {
	out := []byte(s)
	for i := range out {
		if out[i] == '.' {
			out[i] = ','
		}
	}
	return string(out)
}

// Etki cümlesi: sayılar biliniyorsa istek sayısıyla; bilinmiyorsa bugünkü metin.
func TestStandardizeProblemRequestSplitImpact(t *testing.T) {
	now := time.Date(2026, 11, 19, 21, 16, 0, 0, time.UTC)
	base := Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", RuleName: MLPromotedRuleNamePrefix + "servis anomalisi", Severity: "critical",
		Service: "orders", Metric: "error_rate", Comparator: ">=", Value: 0.068, Threshold: 0.005, Status: "open",
		Description: "orders servisinde hata oranı %6,8, olağanı %0,4.", StartedAt: now.Add(-14 * time.Minute).UnixNano()}
	f := StdFacts{Now: now, BigBreachRatio: 2}
	plain := StandardizeProblem(base, f)
	if plain.Impact.Requests != nil || plain.Impact.Primary.Msg == nil || plain.Impact.Primary.Msg.K == "imp.reqSplit" {
		t.Fatalf("sayı yokken etki bugünkü metin olmalı: %+v", plain.Impact)
	}
	for _, c := range []struct {
		name, sentence, impact, en string
		failed, ok                 float64
		normal                     bool
	}{
		{"olağan düzeyle", "Dakikada 210 başarısız, 2.890 başarılı istek (olağanı 12 başarısız, 3.090 başarılı).",
			"Dakikada 210 başarısız · 2.890 başarılı (isteklerin %6,8'i)", "6.8%", 210, 2890, true},
		{"olağan düzey yok", "Dakikada 60 başarısız, 1.140 başarılı istek.", "Dakikada 60 başarısız · 1.140 başarılı (isteklerin %5'i)", "5%", 60, 1140, false},
		{"başarısız yok", "Dakikada 0 başarısız, 2.890 başarılı istek.", "Dakikada 0 başarısız · 2.890 başarılı (isteklerin %0'ı)", "0%", 0, 2890, false},
		{"taban 0", "Dakikada 10 başarısız, 190 başarılı istek (olağanı 0 başarısız, 180 başarılı).", "Dakikada 10 başarısız · 190 başarılı (isteklerin %5'i)", "5%", 10, 190, true},
	} {
		p := base
		p.Description += " " + c.sentence
		std := StandardizeProblem(p, f)
		im := std.Impact
		if im.Primary.Text != c.impact {
			t.Errorf("%s: etki %q, beklenen %q", c.name, im.Primary.Text, c.impact)
		}
		if im.Requests == nil || im.Requests.FailedPerMin != c.failed || im.Requests.OKPerMin != c.ok || (im.Requests.NormalFailedPerMin != nil) != c.normal {
			t.Errorf("%s: bölüm %+v", c.name, im.Requests)
		}
		if im.Primary.Msg == nil || im.Primary.Msg.K != "imp.reqSplit" || im.Primary.Msg.A["share"] != c.en {
			t.Errorf("%s: anahtar %+v", c.name, im.Primary.Msg)
		}
		// manşet açıklamanın cümlesiz hali; ölçüler, güven ve taban aynen
		if std.Headline != plain.Headline {
			t.Errorf("%s: manşet değişmemeli: %q", c.name, std.Headline)
		}
		if im.Primary.Value != plain.Impact.Primary.Value || im.Primary.Normal != plain.Impact.Primary.Normal || im.Primary.Ratio != plain.Impact.Primary.Ratio ||
			im.FailedPerMin != plain.Impact.FailedPerMin || !reflect.DeepEqual(im.Floor, plain.Impact.Floor) || std.Confidence.Level != plain.Confidence.Level {
			t.Errorf("%s: ölçü / taban / güven değişmemeli: %+v", c.name, im)
		}
	}
	if en := StandardizeProblem(Problem{ID: "x", RuleID: "anomaly-auto:svc:orders", RuleName: base.RuleName, Service: "orders", Metric: "error_rate", Value: 0.07, Threshold: 0.005,
		Status: "open", Description: base.Description + " Dakikada 2.100 başarısız, 28.900 başarılı istek."}, f).Impact.Primary.Msg; en.A["f"] != "2,100" || en.A["o"] != "28,900" {
		t.Errorf("anahtar argümanları dilden bağımsız biçimli olmalı: %+v", en.A)
	}
	// başka aile / başka kural cümleyi OKUMAZ (yanlış yere sayı yazılmaz)
	lat := base
	lat.Metric, lat.Description = "p95_ms", base.Description+" Dakikada 210 başarısız, 2.890 başarılı istek."
	if std := StandardizeProblem(lat, f); std.Impact.Requests != nil {
		t.Errorf("gecikme ölçülü Problem istek bölümü taşımamalı: %+v", std.Impact)
	}
	rule := base
	rule.ID, rule.RuleID, rule.RuleName = "r1", "builtin-error-rate-15pct", "Error rate"
	rule.Description += " Dakikada 210 başarısız, 2.890 başarılı istek."
	if std := StandardizeProblem(rule, f); std.Impact.Requests != nil {
		t.Errorf("alarm kuralı istek bölümü taşımamalı: %+v", std.Impact)
	}
}

// ML olay satırı (anomali akışı): bölüm kanıttan gelir, etki aynı cümle.
func TestStandardizeAnomalyEventRequestSplit(t *testing.T) {
	now := time.Date(2026, 11, 19, 21, 16, 0, 0, time.UTC)
	ev := MLEvent{ID: "e1", Detector: "svc_error_rate", Signal: MLSignalTrace, Service: "orders", WindowSec: 300, Baseline: 0.004, Observed: 0.068, Direction: "up",
		StartedAt: now.Add(-14 * time.Minute).UnixNano(), LastSeen: now.Add(-time.Minute).UnixNano(),
		Evidence: `{"rule":"impact","requests":{"minute":600,"failedPerMin":210,"okPerMin":2890,"normalFailedPerMin":12,"normalOkPerMin":3090}}`}
	row := MLEventAsAnomaly(ev, now.UnixNano())
	if row.Requests == nil || row.Requests.FailedPerMin != 210 {
		t.Fatalf("akış satırı bölümü taşımalı: %+v", row.Requests)
	}
	std := StandardizeAnomalyEvent(row, StdFacts{Now: now, BigBreachRatio: 2})
	if std.Impact.Primary.Text != "Dakikada 210 başarısız · 2.890 başarılı (isteklerin %6,8'i)" || std.Impact.Requests == nil || !std.Impact.Known {
		t.Errorf("olay etkisi: %+v", std.Impact)
	}
	ev.Evidence = `{"rule":"impact"}`
	old := MLEventAsAnomaly(ev, now.UnixNano())
	if std := StandardizeAnomalyEvent(old, StdFacts{Now: now, BigBreachRatio: 2}); old.Requests != nil || std.Impact.Requests != nil || std.Impact.Primary.Text != "normalin 17×" {
		t.Errorf("bölümsüz olay bugünkü etkiyle kalmalı: %+v", std.Impact)
	}
}
