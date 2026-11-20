package ml

// red_selector.go — v0.10.2390: histogram ailesi seçicisinin TEK tanımı.
//
// HATA: ML motoru RED ailesinin metriğini çıplak yazıyordu (pin.Names +
// "_count" / "_bucket"). HTTP ailesinin pini mantıksal OTel adını taşır
// (http.server.request.duration; pin açıklaması: "seçici adayları için
// girdi"). Prometheus adlandırmalı VictoriaMetrics o metriği
// http_server_request_duration_seconds_count / _bucket diye saklar: etiket
// keşfi (pin.Names + "_count" → adaylar http.server.request.duration_count ve
// http_server_request_duration_count; "_seconds" yok) aileyi BULAMIYOR,
// sorgular da çıplak noktalı adı soruyordu. ML trace dedektörleri HTTP
// servislerini hiç görmedi; yalnız gRPC aileleri (pinleri tam VM adı) çalıştı.
//
// ÇÖZÜM: çalışan yolların mekanizması (anomaly/red_vm_query.go vmSelector,
// api/metric_chart_templates.go metricChartSelector): seçici
// vmetrics.NameCandidates'in yazım adaylarından kurulur,
// {__name__=~"^(…|…)$"}; hangi yazımın verisi varsa o yanıt verir. Keşif de
// aynı aileyi sorar (famDiscovery: taban ad; VM kendi keşif adaylarını üretir,
// sorgu adaylarının üst kümesi).
//
// TEK ADAY (gRPC aileleri, client aileleri: adları zaten Prometheus yazımı)
// çıplak biçimde kalır: `ad` / `ad{eşleyiciler}`. O ailelerin sorguları bu
// sürümde metin olarak da değişmez.
//
// BİRİM: aday kümesi `_seconds` ve `_milliseconds` yazımlarını birlikte
// taşır (vmetrics/names.go "unit-mixing corollary"); çalışan yol birimi
// YAZIMDAN değil ailenin pininden okur (HTTP: Unit "s", semconv) ve bunu
// korumaz, belgeler. ML aynısını yapar: ölçek pinMsScale / pinSlowLe, tek yer.
//
// ÇİFT SAYIM: kurulum aynı metriğin iki yazımını AYNI anda taşıyorsa
// alternasyon ikisini toplar (count iki kat; oran ve yüzdelik etkilenmez).
// Çalışan yolun kabul ettiği bedel (names.go "THE COST, NAMED"); yeni kural yok.
//
// Eşleyiciler thanos.WithEqualMatcher / WithRegexMatcher'dan GEÇMEZ (Thanos
// PromQL'i için yazıldı); seçici doğrudan kurulur. Etiket değeri kaçışı o
// yardımcılarla aynı (\ " satır sonu); eski sözdizimine uymayan etiket adı
// tırnaklanır.

import (
	"strings"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// Histogram parçaları (vmetrics.NameCandidates türleri).
const (
	partCount  = "count"
	partBucket = "bucket"
)

// famSelector — SAF: ailenin parça seçicisi. names = ailenin ad girdisi (HTTP:
// mantıksal OTel adı; gRPC / client: tam VM tabanı), part = partCount |
// partBucket. Boş eşleyici atlanır; sıra verildiği gibi.
func famSelector(names, part string, matchers ...string) string {
	cands := vmetrics.NameCandidates(part, names)
	bare := len(cands) == 1 && legacyPromName(cands[0])
	ms := make([]string, 0, len(matchers)+1)
	if !bare {
		ms = append(ms, vmetrics.NameSelector(cands))
	}
	for _, m := range matchers {
		if m != "" {
			ms = append(ms, m)
		}
	}
	switch {
	case !bare:
		return "{" + strings.Join(ms, ",") + "}"
	case len(ms) == 0:
		return cands[0]
	}
	return cands[0] + "{" + strings.Join(ms, ",") + "}"
}

// redSel — SAF: RED ailesinin (anomaly pini) parça seçicisi.
func redSel(pin anomaly.REDFamilyPin, part string, matchers ...string) string {
	return famSelector(pin.Names, part, matchers...)
}

// depSel — SAF: client (bağımlılık) ailesinin parça seçicisi.
func depSel(f depFamily, part string, matchers ...string) string {
	return famSelector(f.Metric, part, matchers...)
}

// famDiscovery — SAF: etiket keşfinde (MetricLabelNamesIn) sorulan ad: ailenin
// TABAN adı. VM keşif adaylarını kendi üretir (düz yazımlar + `_count`
// kardeşleri); bu küme famSelector'ın count adaylarını kapsar, yani keşfin
// bulduğu aile sorguların okuduğu ailedir.
func famDiscovery(names string) string { return names }

// REDDiscoveryMetric — famDiscovery'nin RED pini için dışa açık adı (api'nin
// kaynak serisi uçları aynı keşfi yapar).
func REDDiscoveryMetric(pin anomaly.REDFamilyPin) string { return famDiscovery(pin.Names) }

// pinSlowLe — SAF: yavaş istek eşiği (saniye) → ailenin bucket biriminde `le`.
func pinSlowLe(pin anomaly.REDFamilyPin, slowSec float64) float64 {
	if pin.Unit == "ms" {
		return slowSec * 1000
	}
	return slowSec
}

// legacyPromName — SAF: [a-zA-Z_:][a-zA-Z0-9_:]* (çıplak yazılabilen metrik /
// etiket adı; etiket adında ':' beklenmez ama geçerlidir).
func legacyPromName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (i > 0 && c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

var selValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)

// selLabel — SAF: eşleyicideki etiket adı (eski sözdizimi dışı ad tırnaklı).
func selLabel(label string) string {
	if legacyPromName(label) && !strings.Contains(label, ":") {
		return label
	}
	return `"` + selValueEscaper.Replace(label) + `"`
}

// mEq — SAF: etiket="değer". Boş etiket → "" (eşleyici yok).
func mEq(label, value string) string { return selMatcher(label, "=", value) }

// mRe — SAF: etiket=~"regex" (regex çağıranın; yalnız dize kaçışı yapılır).
func mRe(label, re string) string { return selMatcher(label, "=~", re) }

// mSet — SAF: etiket!="" (etiket dolu).
func mSet(label string) string { return selMatcher(label, "!=", "") }

func selMatcher(label, op, value string) string {
	if label == "" {
		return ""
	}
	return selLabel(label) + op + `"` + selValueEscaper.Replace(value) + `"`
}
