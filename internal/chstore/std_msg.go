package chstore

// std_msg.go — v0.10.2299 (sunucu metinleri S2; docs/i18n/server-text-design.md).
// Problem standardının sunucu cümlelerinin YANINA giden metin anahtarı + ham
// argümanlar. Türkçe metin alanları (Headline, Impact.Primary.Text, …)
// değişmez ve sözleşme olmaya devam eder: bildirim, MCP, CoSRE istemi, özet
// öğesi ve thread Topic'i yalnız onları okur. Anahtar EK alandır; İngilizce
// arayüz onu katalogdan çizer, Türkçe arayüz sunucu metnini aynen çizer.
// Sunucuya dil gelmez, önbellek anahtarlarına dil girmez.
//
// Argüman adının soneki biçimi söyler (FE lib/problemStandardView stdMsgText):
//
//	…Pct  yüzde       …Ms  milisaniye     …Num  sayı
//	…X    oran (×; "inf" = tam kayıp)     …K    katalog anahtarı (std.m.<değer>)
//	…Sec  süre, saniye (v0.10.2300 S3; Go shortDur'un dalları)
//	…M    iç içe mesaj (*StdMsg; v0.10.2300 S3 — "gerekçe · iç gerekçe")
//	diğer ham metin (özne öneki "sp", ad, tip, sunucunun dilden bağımsız
//	biçimlediği sayı: "2.5", "11,260", …)
//
// Sonlu olmayan sayı JSON'a giremez: argüman "inf" (+Inf) ya da "—" metni olur.

import (
	"math"
	"strings"
)

// StdMsg — metin anahtarı + ham argümanlar.
type StdMsg struct {
	K string         `json:"k"`
	A map[string]any `json:"a,omitempty"`
}

// stdM — SAF: anahtar + ad/değer çiftleri. Sonlu olmayan float metne döner.
func stdM(k string, kv ...any) *StdMsg {
	m := &StdMsg{K: k}
	if len(kv) == 0 {
		return m
	}
	m.A = make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		name, _ := kv[i].(string)
		v := kv[i+1]
		if f, ok := v.(float64); ok {
			switch {
			case math.IsInf(f, 1):
				v = "inf"
			case math.IsInf(f, 0) || math.IsNaN(f):
				v = "—"
			}
		}
		m.A[name] = v
	}
	return m
}

// NewStdMsg — stdM'nin paket dışı adı (api'nin öncelik gerekçeleri, S3).
func NewStdMsg(k string, kv ...any) *StdMsg { return stdM(k, kv...) }

// ruleValueMsg — SAF: "değer V (eşik T)" etkisinin anahtarı; birim trUnit
// ile aynı dallar.
func ruleValueMsg(v, th float64, unit string) *StdMsg {
	switch unit {
	case "%":
		return stdM("imp.rule.pct", "vPct", v, "tPct", th)
	case "ms":
		return stdM("imp.rule.ms", "vMs", v, "tMs", th)
	case "/sn":
		return stdM("imp.rule.rate", "vNum", v, "tNum", th)
	}
	return stdM("imp.rule", "vNum", v, "tNum", th)
}

// metricLabelArgs — SAF: metricLabelTR'nin anahtar ikizi. Bilinen metrik
// katalog anahtarı ("metricK"), bilinmeyen ham etiket ("metric") — Türkçe
// etiketle aynı dallar.
func metricLabelArgs(m string) (string, string) {
	switch {
	case m == "error_rate":
		return "metricK", "metric.error_rate"
	case m == "p99_ms":
		return "metricK", "metric.p99_ms"
	case m == "request_rate":
		return "metricK", "metric.request_rate"
	case strings.HasSuffix(m, "error_count") || strings.HasSuffix(m, ":ext:error_count"):
		return "metricK", "metric.error_count"
	case strings.HasSuffix(m, "fail_count"):
		return "metricK", "metric.fail_count"
	}
	return "metric", metricLabelTR(m)
}

// selfHeadlineKey — SAF: selfHeadlineTR'nin anahtar ikizi (aynı dallar).
func selfHeadlineKey(ruleID string) string {
	switch {
	case strings.HasPrefix(ruleID, "self-ingest-stall"):
		return "h.self.ingest"
	case strings.HasPrefix(ruleID, "self-spool-depth"):
		return "h.self.spool"
	case strings.HasPrefix(ruleID, "self-disk-eta"):
		return "h.self.disk"
	case strings.HasPrefix(ruleID, "self-channel-broken"):
		return "h.self.channel"
	case strings.HasPrefix(ruleID, "self-volume-spike"):
		return "h.self.volume"
	case strings.HasPrefix(ruleID, "self-redis-evict"):
		return "h.self.redis"
	}
	return "h.self.other"
}

// exceptionGroupMsg — SAF: exceptionGroupLabel'ın anahtar ikizi (aynı
// dallar; "t" = etiketten sonraki ham kuyruk).
func exceptionGroupMsg(sp, kind, ruleName string) *StdMsg {
	_, rest, ok := strings.Cut(ruleName, " · ")
	if !ok {
		rest = strings.TrimSpace(ruleName)
	}
	switch kind {
	case "exception_group.oracle":
		return stdM("h.ex.oracle", "sp", sp, "t", rest)
	case "exception_group.http":
		if strings.HasPrefix(rest, "5") {
			return stdM("h.ex.tech", "sp", sp, "t", "HTTP "+rest)
		}
		return stdM("h.ex.http", "sp", sp, "t", rest)
	}
	return stdM("h.ex.generic", "sp", sp, "t", rest)
}
