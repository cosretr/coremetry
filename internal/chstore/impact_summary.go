package chstore

// impact_summary.go — v0.10.2482 ("etki öncelikte" adım 1; operatör onayı:
// YALNIZ GÖSTERİM). Öncelik bugün yalnız değer / eşik oranına bakıyor:
// dakikada 12 istekte %14 hata ile 5.000 istekte %14 hata aynı sırada.
// Etki özeti gerçek büyüklüğü söyler — kaç istek başarısız, kaç istek
// yavaş, kaç mesaj birikmiş, kaç pod düşmüş — ve listede aynı öncelik
// içinde sıralamayı belirler. Sayılar YALNIZ kayıtlı kanıttan (etki kuralı
// olayının dakika dakika oran + yük dizileri; OOM'da Problem'in kendi
// sayıları); liste yolunda yeni VM / CH ölçü sorgusu yok.
//
// Öncelik, güven, bildirim, kapılar ve dedektörler bu özeti OKUMAZ
// (impact_summary_test.go çiviler). Adım 2 (P1 için etki tabanı) ayrı karar.

import "strconv"

// Etki özeti türleri.
const (
	ImpactSumErrors  = "errors"  // hata oranı: dakikada başarısız istek
	ImpactSumLatency = "latency" // yanıt süresi: dakikada etkilenen istek + gecikme
	ImpactSumLag     = "lag"     // consumer lag: biriken mesaj (+ dakikalık artış)
	ImpactSumOOM     = "oom"     // OOMKilled: düşen pod / iş yükünün pod'ları
)

// Etki kapsamı (hata oranı / yanıt süresi).
const (
	ImpactScopeService = "service"
	ImpactScopeRoute   = "route"
)

// StdImpactSummary — bir Problem'in etkisi, tek satırlık özet. Sayılar
// kanıttan; Text Türkçe insan cümlesi (CoSRE get_problem bunu okur; arayüz
// satırı sayılardan kendi dilinde kurar).
type StdImpactSummary struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope,omitempty"` // service | route (errors / latency)
	Route string `json:"route,omitempty"`
	// PerMin — errors: dakikada başarısız istek (oran × yük, son dakikaların
	// medyanı); latency: dakikada etkilenen istek (yük medyanı).
	PerMin float64 `json:"perMin,omitempty"`
	// SharePct — errors: kapsamın (servis / uç nokta) trafiğinde başarısız
	// payı, yüzde (oran medyanı × 100).
	SharePct float64 `json:"sharePct,omitempty"`
	// DelayMs — latency: ölçünün medyanı − taban (ms); ≤ 0 ise yazılmaz.
	DelayMs float64 `json:"delayMs,omitempty"`
	Measure string  `json:"measure,omitempty"` // latency: MLRespMedian | MLRespSlow
	// Backlog — lag: en yeni ölçülen dakikanın lag'i (mesaj).
	Backlog float64 `json:"backlog,omitempty"`
	// GrowthPerMin — lag: penceredeki ilk ve son ölçülen dakika arasındaki
	// dakikalık değişim. nil = < 2 ölçülmüş dakika.
	GrowthPerMin *float64 `json:"growthPerMin,omitempty"`
	Killed       int      `json:"killed,omitempty"` // oom
	Pods         int      `json:"pods,omitempty"`   // oom
	// Rank — aynı öncelik içinde sıralama büyüklüğü: dakikada etkilenen
	// birim (errors / latency: istek; lag: mesaj artışı, artmıyorsa 0;
	// oom: düşen pod). Aileler arası kaba ölçü — yalnız gösterim sırası.
	Rank float64 `json:"rank"`
	Text string  `json:"text"`
}

// FinishImpactSummary — SAF: Rank + Text'i sayılardan doldurur (tek tanım).
func FinishImpactSummary(s *StdImpactSummary) *StdImpactSummary {
	if s == nil {
		return nil
	}
	switch s.Kind {
	case ImpactSumErrors, ImpactSumLatency:
		s.Rank = s.PerMin
	case ImpactSumLag:
		s.Rank = 0
		if s.GrowthPerMin != nil && *s.GrowthPerMin > 0 {
			s.Rank = *s.GrowthPerMin
		}
	case ImpactSumOOM:
		s.Rank = float64(s.Killed)
	}
	s.Text = impactSummaryText(*s)
	return s
}

// approxPerMin — SAF: "~340" / "<1" (dakikalık sayı, binlik nokta).
func approxPerMin(v float64) string {
	if v < 0.5 {
		return "<1"
	}
	return "~" + groupInt(v, '.')
}

// impactSummaryText — SAF: Türkçe tek satır.
func impactSummaryText(s StdImpactSummary) string {
	switch s.Kind {
	case ImpactSumErrors:
		pct := sharePct(s.SharePct)
		scope := "trafiğin"
		if s.Scope == ImpactScopeRoute {
			scope = "uç nokta trafiğinin"
		}
		return approxPerMin(s.PerMin) + " istek/dk başarısız · " + scope + " %" + trDecimal(pct) + trPossessive(pct)
	case ImpactSumLatency:
		out := approxPerMin(s.PerMin) + " istek/dk"
		if d := int64(s.DelayMs + 0.5); d > 0 {
			out += " · +" + groupInt(float64(d), '.') + " ms"
		}
		return out
	case ImpactSumLag:
		out := groupInt(s.Backlog, '.') + " mesaj birikmiş"
		if s.GrowthPerMin != nil && *s.GrowthPerMin >= 0.5 {
			out += " · +" + groupInt(*s.GrowthPerMin, '.') + "/dk"
		}
		return out
	case ImpactSumOOM:
		return strconv.Itoa(s.Killed) + "/" + strconv.Itoa(s.Pods) + " pod"
	}
	return ""
}

// trDecimal — SAF: "6.8" → "6,8".
func trDecimal(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c == '.' {
			b[i] = ','
		}
	}
	return string(b)
}

// OOMImpactSummary — SAF: OOM Problem'inin etkisi (Problem'in kendi
// sayıları: manşetteki OOMKilled pod + Threshold'daki iş yükü pod sayısı).
// nil = OOM satırı değil.
func OOMImpactSummary(p Problem) *StdImpactSummary {
	killed, total, _, ok := oomProblemFacts(p)
	if !ok || total <= 0 {
		return nil
	}
	return FinishImpactSummary(&StdImpactSummary{Kind: ImpactSumOOM, Killed: killed, Pods: total})
}
