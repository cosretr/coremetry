package ml

// explain_card_test.go — v0.10.1842 (öğrenme döngüsü Faz 2 dilim 2.2):
// ExplainCard sözleşmesi. Audit docs/anomaly/learning-loop-audit.md §7.3'ün
// İKİ örnek kartı BİREBİR (her alan), ardından her dedektör ailesinden bir
// kart (metrik, trace, log kapalı/batch, azalma, sıfır taban, az çağrılan,
// pod aykırısı) ve eski kaydın dürüst geri düşüşü (bant/seri yok → uydurma
// sayı yok). Metrik birimlerinin HEPSİ (s, ms, bytes, s/s, core, birimsiz)
// ayrı satır (v0.6.36 birim karışması dersi).

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var trLoc = time.FixedZone("Europe/Istanbul", 3*3600)

// 2026-11-03 salı (iş günü).
func at(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, trLoc) }

func bands(from time.Time, n int, lo, mid, hi float64) []CardBand {
	out := make([]CardBand, n)
	for i := range out {
		out[i] = CardBand{T: from.Add(time.Duration(i) * 5 * time.Minute).Unix(), Lo: lo, Mid: mid, Hi: hi}
	}
	return out
}

func boolp(b bool) *bool { return &b }

func labels(ls []ExplainLink) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Label
	}
	return out
}

func texts(xs []ExplainItem) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.Text
	}
	return out
}

func eq(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\n got %q\nwant %q", field, got, want)
	}
}

func eqList(t *testing.T, field string, got, want []string) {
	t.Helper()
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("%s:\n got %q\nwant %q", field, got, want)
	}
}

// example1 — audit §7.3 Örnek 1 (trace, 7/24 profil).
func example1() CardInput {
	return CardInput{
		EventID: "ev1", Detector: "op_latency", Signal: chstore.MLSignalTrace, Service: "payments-mobile",
		Code: "/payments/authorize", Part: "/payments/authorize|slow", Stage: chstore.MLStageShadow, Direction: "up",
		StartedAt: at(14, 10), LastSeen: at(14, 35), Now: at(14, 35), Loc: trLoc, BucketSec: 300,
		Observed: 0.12, Baseline: 0.04, Score: 5.2, SlowSec: 1,
		Band:    bands(at(14, 0), 8, 0.03, 0.04, 0.05),
		Impact:  &CardImpact{Affected: 4800, Total: 40000, WholeWindow: true, Channels: []string{"mobil"}},
		Profile: ProfileAlwaysOn, BusyHour: boolp(true),
		Rollouts: []CardRollout{{At: at(13, 58)}}, RolloutsKnown: true,
		Related:   []CardRelated{{Relation: "neighbor", Service: "orders-api", Fact: "hata oranı arttı", At: at(14, 12)}},
		CriticalZ: 4, EffectiveZ: 4, DwellBuckets: 2, BaselineDays: 21, DayClass: "weekday", ConfigHash: "a1b2",
	}
}

// example2 — audit §7.3 Örnek 2 (log, mesai profili).
func example2() CardInput {
	return CardInput{
		EventID: "ev2", Detector: "error_rate", Signal: chstore.MLSignalLog, Service: "branch-teller", Part: "_error",
		Stage: chstore.MLStageShadow, Direction: "up",
		StartedAt: at(10, 30), LastSeen: at(10, 45), Now: at(10, 45), Loc: trLoc, BucketSec: 300,
		Observed: 207, Baseline: 23, Score: 9.4,
		Series: []chstore.MLPoint{{T: at(10, 25).Unix(), V: 20}, {T: at(10, 30).Unix(), V: 180}, {T: at(10, 35).Unix(), V: 210}, {T: at(10, 40).Unix(), V: 230}},
		Band: []CardBand{{T: at(10, 25).Unix(), Lo: 16, Mid: 22, Hi: 30}, {T: at(10, 30).Unix(), Lo: 16, Mid: 23, Hi: 30},
			{T: at(10, 35).Unix(), Lo: 17, Mid: 23, Hi: 30}, {T: at(10, 40).Unix(), Lo: 17, Mid: 23, Hi: 30}},
		NewTemplate: &CardTemplate{Text: "Connection pool exhausted", FirstSeen: at(10, 32), Count: 410},
		Impact:      &CardImpact{Affected: 1150, Total: 14400, WholeWindow: true, PodsHit: 6, PodsTotal: 6},
		Profile:     ProfileBusinessHours, DayClass: "weekday",
		RolloutsKnown: true, RolloutLookback: 24 * time.Hour,
		Related: []CardRelated{{Relation: "db", Fact: "bağlantı süresi arttı", At: at(10, 31)}},
	}
}

func TestExplainCardAuditExample1(t *testing.T) {
	c := BuildExplainCard(example1())
	eq(t, "summary", c.Summary, "payments-mobile: yavaş istek oranı normalin yaklaşık 3 katı, 25 dakikadır (14:10'dan beri).")
	eq(t, "what", c.What, "payments-mobile servisinde yavaş yanıt alan isteklerin oranı arttı.")
	eq(t, "howMuch", c.HowMuch, "İsteklerin %12'si 1 saniyeden uzun sürüyor; bu saatte normalde %3–5. Yaklaşık 3 kat.")
	eq(t, "since", c.Since, "Bugün 14:10'dan beri, 25 dakikadır sürüyor.")
	eq(t, "impact", c.Impact, "Bu sürede yaklaşık 4.800 istek yavaş yanıt aldı (toplam 40.000). Kanal: mobil.")
	eq(t, "why", c.WhyItMatters, "Bu servis günün her saati kullanılıyor ve şu an yoğun saatinde.")
	eqList(t, "whatChanged", texts(c.WhatChanged), []string{"13:58'de yeni sürüm yayına alındı.", "Aynı dakikalarda orders-api'de hata oranı arttı."})
	eqList(t, "whereToLook", labels(c.WhereToLook), []string{"Yavaş trace'ler", "Endpoint dağılımı", "Sürüm karşılaştırması"})
	eq(t, "status", c.Header.Status, "Sürüyor · 25 dk")
	eq(t, "profile", c.Header.ProfileLabel, "7/24")
	if c.Header.Subject == nil || c.Header.Subject.Label != "/payments/authorize" || !strings.HasPrefix(c.Header.Subject.Href, "/endpoint?") {
		t.Errorf("subject: %+v", c.Header.Subject)
	}
	// Yavaş trace linki: servis + route süzgeci + eşik + olay penceresi (−15 dk … şimdi).
	u, err := url.Parse(c.WhereToLook[0].Href)
	if err != nil || u.Path != "/traces" {
		t.Fatalf("traces href %q", c.WhereToLook[0].Href)
	}
	q := u.Query()
	wantRange := rangeParamOf(at(13, 55), at(14, 35))
	if q.Get("service") != "payments-mobile" || q.Get("minMs") != "1000" || q.Get("rootOnly") != "false" || q.Get("range") != wantRange ||
		!strings.Contains(q.Get("filters"), `"http.route"`) {
		t.Errorf("traces query %v (range want %s)", q, wantRange)
	}
	if c.WhatChanged[0].Link == nil || !strings.Contains(c.WhatChanged[0].Link.Href, "tab=gitops") {
		t.Errorf("rollout link %+v", c.WhatChanged[0].Link)
	}
	eq(t, "technical.score", c.Technical.Score, "5,2 (etkin eşik 4)")
	eq(t, "technical.signature", c.Technical.Signature, "payments-mobile | trace | op_latency | /payments/authorize|slow")
}

func TestExplainCardAuditExample2(t *testing.T) {
	c := BuildExplainCard(example2())
	eq(t, "summary", c.Summary, "branch-teller: hata logları normalin yaklaşık 9 katı, 15 dakikadır (10:30'dan beri).")
	eq(t, "what", c.What, "branch-teller servisinde hata logları arttı ve daha önce görülmemiş bir hata mesajı çıkmaya başladı.")
	eq(t, "howMuch", c.HowMuch, "Son 15 dakikada 620 hata satırı; bu saatte normalde 50–90. Yaklaşık 9 kat. Yeni mesaj: \"Connection pool exhausted\" (ilk kez 10:32'de, 410 satır).")
	eq(t, "since", c.Since, "Bugün 10:30'dan beri, 15 dakikadır sürüyor.")
	eq(t, "impact", c.Impact, "Aynı sürede yaklaşık 1.150 istek hata aldı (toplam 14.400). 6 pod'un 6'sında görülüyor.")
	eq(t, "why", c.WhyItMatters, "Bu servis mesai saatlerinde kullanılıyor ve şu an mesai içinde.")
	eqList(t, "whatChanged", texts(c.WhatChanged), []string{"Son 24 saatte yayın yok.", "Bağlı olduğu veritabanında bağlantı süresi arttı."})
	eqList(t, "whereToLook", labels(c.WhereToLook), []string{"Bu mesajı içeren loglar", "Hatalı trace'ler", "Servis sayfası"})
	u, _ := url.Parse(c.WhereToLook[0].Href)
	if u.Path != "/logs" || u.Query().Get("q") != `"Connection pool exhausted"` || u.Query().Get("service") != "branch-teller" {
		t.Errorf("logs href %q", c.WhereToLook[0].Href)
	}
	eq(t, "profile", c.Header.ProfileLabel, "mesai")
}

// Her dedektör ailesinden bir kart + kenar durumları.
func TestExplainCardFamilies(t *testing.T) {
	cases := []struct {
		name    string
		in      CardInput
		summary string
		what    string
		howMuch string
		since   string
		why     string
		where   []string
	}{
		{
			name: "metric http client p95 (birim s)",
			in: CardInput{Detector: "metric_http_client", Signal: chstore.MLSignalMetric, Service: "orders-api", Code: "metric_http_client|client_p95",
				MetricLabel: "HTTP client p95", MetricUnit: "s", Direction: "up", StartedAt: at(14, 30), LastSeen: at(14, 40), Now: at(14, 44), Loc: trLoc,
				Observed: 2.1, Baseline: 0.65, Band: bands(at(14, 30), 3, 0.5, 0.65, 0.8), Profile: ProfileAlwaysOn},
			summary: "orders-api: HTTP client p95 normalin yaklaşık 3 katı, 14 dakikadır (14:30'dan beri).",
			what:    "orders-api servisinde HTTP client p95 arttı.",
			howMuch: "HTTP client p95 2,1 sn; bu saatte normalde 0,5–0,8 sn. Yaklaşık 3 kat.",
			since:   "Bugün 14:30'dan beri, 14 dakikadır sürüyor.",
			why:     "Bu servis günün her saati kullanılıyor.",
			where:   []string{"Yavaş trace'ler", "Endpoint dağılımı", "Servis sayfası"},
		},
		{
			name: "trace op_latency yayınsız",
			in: CardInput{Detector: "op_latency", Signal: chstore.MLSignalTrace, Service: "notify-gateway", Code: "/notify/order-status", Direction: "up",
				StartedAt: at(14, 33), LastSeen: at(14, 43), Now: at(14, 44), Loc: trLoc, Observed: 0.12, Baseline: 0.04, SlowSec: 1,
				Band: bands(at(14, 30), 3, 0.03, 0.04, 0.05), RolloutsKnown: true, Profile: ProfileAlwaysOn, BusyHour: boolp(false)},
			summary: "notify-gateway: yavaş istek oranı normalin yaklaşık 3 katı, 11 dakikadır (14:33'ten beri).",
			what:    "notify-gateway servisinde yavaş yanıt alan isteklerin oranı arttı.",
			howMuch: "İsteklerin %12'si 1 saniyeden uzun sürüyor; bu saatte normalde %3–5. Yaklaşık 3 kat.",
			since:   "Bugün 14:33'ten beri, 11 dakikadır sürüyor.",
			why:     "Bu servis günün her saati kullanılıyor; olay yoğun saat dışında.",
			where:   []string{"Yavaş trace'ler", "Endpoint dağılımı", "Servis sayfası"},
		},
		{
			name: "log batch kapandı",
			in: CardInput{Detector: "error_rate", Signal: chstore.MLSignalLog, Service: "branch-teller-batch", Stage: chstore.MLStageClosed, Direction: "up",
				StartedAt: at(2, 10), LastSeen: at(2, 40), Now: at(9, 0), Loc: trLoc, Observed: 14, Baseline: 7, Profile: ProfileBatch},
			summary: "branch-teller-batch: hata logları normalin yaklaşık 2 katı, 30 dakika sürdü (02:10–02:40).",
			what:    "branch-teller-batch servisinde hata logları arttı.",
			howMuch: "Son 5 dakikada 14 hata satırı; bu saatte normalde yaklaşık 7. Yaklaşık 2 kat.",
			since:   "Bugün 02:10–02:40 arasında, 30 dakika sürdü.",
			why:     "Bu bir batch servisi: yük artışı değerlendirilmez, hata artışı önemlidir.",
			where:   []string{"Hata logları", "Hatalı trace'ler", "Servis sayfası"},
		},
		{
			name: "trafik düşüşü (azalma dili), dün başladı",
			in: CardInput{Detector: "throughput_drop", Signal: chstore.MLSignalTrace, Service: "orders-api", Code: "/orders", Direction: "down",
				StartedAt: time.Date(2026, 11, 2, 23, 40, 0, 0, trLoc), LastSeen: at(0, 25), Now: at(0, 30), Loc: trLoc, Observed: 1000, Baseline: 3000},
			summary: "orders-api: istek sayısı normalden yaklaşık 3 kat az, 50 dakikadır (dün 23:40'tan beri).",
			what:    "orders-api servisine gelen istek sayısı düştü.",
			howMuch: "Son 5 dakikada 1.000 istek; bu saatte normalde yaklaşık 3.000. Normalden yaklaşık 3 kat az.",
			since:   "Dün 23:40'tan beri, 50 dakikadır sürüyor.",
			why:     "Bu servisin kullanım düzeni henüz öğrenilmedi; değer bu saat için beklenen aralığın dışında.",
			where:   []string{"Trace'ler", "Endpoint dağılımı", "Servis sayfası"},
		},
		{
			name: "nadir şablon (taban 0)",
			in: CardInput{Detector: "rare_template", Signal: chstore.MLSignalLog, Service: "payments-mobile", Code: "Timeout calling ledger", Direction: "up",
				StartedAt: at(9, 3), LastSeen: at(9, 13), Now: at(9, 15), Loc: trLoc, Observed: 40, Baseline: 0},
			summary: "payments-mobile: yeni bir log mesajı normalde hiç görülmez, 12 dakikadır (09:03'ten beri).",
			what:    "payments-mobile servisinde daha önce görülmemiş bir log mesajı çıktı: \"Timeout calling ledger\".",
			howMuch: "Son 5 dakikada 40 satır; bu saatte normalde yaklaşık 0. Normalde hiç görülmez.",
			since:   "Bugün 09:03'ten beri, 12 dakikadır sürüyor.",
			why:     "Bu servisin kullanım düzeni henüz öğrenilmedi; değer bu saat için beklenen aralığın dışında.",
			where:   []string{"Bu mesajı içeren loglar", "Hatalı trace'ler", "Servis sayfası"},
		},
		{
			name: "az çağrılan servis (son 10 çağrının 6'sı)",
			in: CardInput{Detector: "op_latency", Signal: chstore.MLSignalTrace, Service: "branch-teller", Code: "/teller/receipt", Direction: "up",
				StartedAt: at(12, 44), LastSeen: at(14, 40), Now: at(14, 44), Loc: trLoc, Observed: 0.6, Baseline: 0.1,
				LowTraffic: &CardLowTraffic{N: 10, K: 6, LimitMs: 1200}, Profile: ProfileLowTraffic},
			summary: "branch-teller: son 10 çağrının 6'sı normalden yavaş, 2 saattir (12:44'ten beri).",
			what:    "branch-teller servisinde yavaş yanıt alan isteklerin oranı arttı.",
			howMuch: "Bu serviste normalde 10 çağrıdan en çok 1'i 1,2 saniyeyi aşar; son 10 çağrının 6'sı aştı.",
			since:   "Bugün 12:44'ten beri, 2 saattir sürüyor.",
			why:     "Bu servis az çağrılıyor; birkaç yavaş ya da hatalı çağrı bile oranı belirgin biçimde değiştirir.",
			where:   []string{"Yavaş trace'ler", "Endpoint dağılımı", "Servis sayfası"},
		},
		{
			name: "trace pod aykırısı (p95, seri yok)",
			in: CardInput{Detector: "trace_pod_outlier", Signal: chstore.MLSignalTrace, Service: "payments-mobile", Code: "p95", Pod: "payments-mobile-7f9c",
				Direction: "up", StartedAt: at(9, 0), LastSeen: at(9, 5), Now: at(9, 7), Loc: trLoc, Observed: 840, Baseline: 210},
			summary: "payments-mobile: payments-mobile-7f9c pod'unda yanıt süresi diğer pod'ların yaklaşık 4 katı, 7 dakikadır (09:00'dan beri).",
			what:    "payments-mobile servisinde payments-mobile-7f9c pod'u diğerlerinden yavaş.",
			howMuch: "Bu pod'da yanıt süresi (p95) 840 ms; diğer pod'larda yaklaşık 210 ms. Yaklaşık 4 kat.",
			since:   "Bugün 09:00'dan beri, 7 dakikadır sürüyor.",
			why:     "Bu servisin kullanım düzeni henüz öğrenilmedi; değer bu saat için beklenen aralığın dışında.",
			where:   []string{"Trace'ler", "Servis sayfası"},
		},
		{
			name: "eski kayıt: bant yok, oran ölçüsü",
			in: CardInput{Detector: "op_error_rate", Signal: chstore.MLSignalTrace, Service: "orders-api", Code: "/orders", Direction: "up",
				StartedAt: at(9, 0), LastSeen: at(9, 10), Now: at(9, 12), Loc: trLoc, Observed: 0.08, Baseline: 0.02},
			summary: "orders-api: hata oranı normalin yaklaşık 4 katı, 12 dakikadır (09:00'dan beri).",
			what:    "orders-api servisinde hata alan isteklerin oranı arttı.",
			howMuch: "İsteklerin %8'i hata alıyor; bu saatte normalde yaklaşık %2. Yaklaşık 4 kat.",
			since:   "Bugün 09:00'dan beri, 12 dakikadır sürüyor.",
			why:     "Bu servisin kullanım düzeni henüz öğrenilmedi; değer bu saat için beklenen aralığın dışında.",
			where:   []string{"Hatalı trace'ler", "Endpoint dağılımı", "Servis sayfası"},
		},
		{
			name: "4 kova görülmeyen açık olay 'sürdü' der",
			in: CardInput{Detector: "log_volume", Signal: chstore.MLSignalLog, Service: "orders-api", Stage: chstore.MLStageShadow, Direction: "up",
				StartedAt: at(8, 0), LastSeen: at(8, 20), Now: at(9, 0), Loc: trLoc, Observed: 900, Baseline: 100},
			summary: "orders-api: log hacmi normalin yaklaşık 9 katı, 20 dakika sürdü (08:00–08:20).",
			what:    "orders-api servisinin log hacmi arttı.",
			howMuch: "Son 5 dakikada 900 log satırı; bu saatte normalde yaklaşık 100. Yaklaşık 9 kat.",
			since:   "Bugün 08:00–08:20 arasında, 20 dakika sürdü.",
			why:     "Bu servisin kullanım düzeni henüz öğrenilmedi; değer bu saat için beklenen aralığın dışında.",
			where:   []string{"Loglar", "Hatalı trace'ler", "Servis sayfası"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BuildExplainCard(c.in)
			eq(t, "summary", got.Summary, c.summary)
			eq(t, "summary (liste yolu)", ExplainSummary(c.in), c.summary)
			eq(t, "what", got.What, c.what)
			eq(t, "howMuch", got.HowMuch, c.howMuch)
			eq(t, "since", got.Since, c.since)
			eq(t, "why", got.WhyItMatters, c.why)
			eqList(t, "where", labels(got.WhereToLook), c.where)
			if got.WhatChanged == nil {
				t.Error("whatChanged nil (JSON null olur)")
			}
		})
	}
}

// Metrik birimlerinin HEPSİ — aralık ve tek değer aynı ölçekte.
func TestExplainCardMetricUnits(t *testing.T) {
	for _, c := range []struct {
		unit              string
		obs, base, lo, hi float64
		want              string
	}{
		{"s", 2.1, 0.65, 0.5, 0.8, "X 2,1 sn; bu saatte normalde 0,5–0,8 sn. Yaklaşık 3 kat."},
		{"s", 0.42, 0.14, 0.1, 0.18, "X 420 ms; bu saatte normalde 100–180 ms. Yaklaşık 3 kat."},
		{"ms", 1800, 600, 500, 700, "X 1,8 sn; bu saatte normalde 0,5–0,7 sn. Yaklaşık 3 kat."},
		{"ms", 120, 40, 30, 50, "X 120 ms; bu saatte normalde 30–50 ms. Yaklaşık 3 kat."},
		{"bytes", 3 << 30, 1 << 30, 900 << 20, 1200 << 20, "X 3 GB; bu saatte normalde 0,9–1,2 GB. Yaklaşık 3 kat."},
		{"bytes", 300 << 20, 100 << 20, 90 << 20, 110 << 20, "X 300 MB; bu saatte normalde 90–110 MB. Yaklaşık 3 kat."},
		{"s/s", 0.42, 0.14, 0.1, 0.2, "X 0,42 sn/sn; bu saatte normalde 0,1–0,2 sn/sn. Yaklaşık 3 kat."},
		{"core", 1.5, 0.5, 0.4, 0.6, "X 1,5 çekirdek; bu saatte normalde 0,4–0,6 çekirdek. Yaklaşık 3 kat."},
		{"", 4500, 1500, 1200, 1800, "X 4.500; bu saatte normalde 1.200–1.800. Yaklaşık 3 kat."},
	} {
		in := CardInput{Detector: "metric_jvm", Signal: chstore.MLSignalMetric, Service: "orders-api", MetricLabel: "x", MetricUnit: c.unit, Direction: "up",
			StartedAt: at(9, 0), LastSeen: at(9, 10), Now: at(9, 10), Loc: trLoc, Observed: c.obs, Baseline: c.base, Band: bands(at(9, 0), 2, c.lo, c.base, c.hi)}
		eq(t, "unit "+c.unit, BuildExplainCard(in).HowMuch, c.want)
	}
}

// Sözlük her dedektörü kapsar (yeni dedektör = yeni sözlük satırı).
func TestDetSpecsCoverDetectors(t *testing.T) {
	for _, d := range chstore.MLDetectorNames {
		if strings.HasPrefix(d, "metric_") {
			continue
		}
		if _, ok := mlDetSpecs[d]; !ok {
			t.Errorf("mlDetSpecs eksik: %s", d)
		}
	}
}

func TestRangeParamOf(t *testing.T) {
	from := time.Unix(1000, 500)
	to := time.Unix(2000, 1)
	eq(t, "range", rangeParamOf(from, to), "custom:1000000-2000001")
	eq(t, "ters", rangeParamOf(to, from), "")
}
