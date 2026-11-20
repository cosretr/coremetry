package chstore

// request_split.go — v0.10.2380 (triage mockup dilim 1; operatör 2026-11-19:
// "hata oranı artan problemler için de başarılı / başarısız gösterebilirsin").
// Hata oranı Problem'inin etkisi İSTEK SAYISIYLA söylenir: en yeni ölçülen
// dakikada kaç istek başarısız, kaç istek başarılı; yanında olağan düzey.
//
// TEK ÇEKİRDEK: sayılar bir kez, dedektörün tikinde üretilir (ml
// svcRequestSplit → NewRequestSplit; dedektörün zaten attığı 1 dakikalık
// istek + hata sayısı çifti, ek sorgu yok) ve üç yere AYNI değerle yazılır:
// olay kanıtı (`requests`), olay kaydı (ml.EventRecord.Requests) ve Problem
// açıklaması (RequestSplitSentence). Problem standardı (problemImpact) cümleyi
// açıklamadan geri okur (ParseRequestSplit) — problems tablosunda sayı kolonu
// yok ve şema göçü istenmiyor; işaret cümlesi burada bir kez TANIMLANIR ve
// üretici aynı işlevi kullanır (REDEvidenceVM kaynak işaretiyle aynı desen:
// tek tanım, ayrışamaz). Liste satırı ve detay başlığı aynı standardı okur.
//
// Sayı yoksa (eski olay, başka dedektör, ölçülmemiş dakika) hiçbir şey
// üretilmez: etki metni bugünkü haliyle kalır, sayı UYDURULMAZ.

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// StdRequestSplit — dakikada başarısız / başarılı istek. Değerler tam sayıya
// yuvarlıdır (üretici yuvarlar; cümle, kanıt ve kayıt aynı sayıyı taşır).
type StdRequestSplit struct {
	// Minute — ölçülen dakikanın başı (unix sn). 0 = bilinmiyor (açıklamadan
	// geri okunan bölüm dakikayı taşımaz).
	Minute       int64   `json:"minute,omitempty"`
	FailedPerMin float64 `json:"failedPerMin"`
	OKPerMin     float64 `json:"okPerMin"`
	// Olağan düzey: taban trafiği × taban oranı. nil = taban trafiği bilinmiyor.
	NormalFailedPerMin *float64 `json:"normalFailedPerMin,omitempty"`
	NormalOKPerMin     *float64 `json:"normalOkPerMin,omitempty"`
}

// Total — dakikadaki toplam istek.
func (s StdRequestSplit) Total() float64 { return s.FailedPerMin + s.OKPerMin }

// FailedPct — başarısız isteklerin payı (yüzde); toplam 0 ise 0.
func (s StdRequestSplit) FailedPct() float64 {
	if t := s.Total(); t > 0 {
		return s.FailedPerMin / t * 100
	}
	return 0
}

func finiteNonNeg(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

// valid — SAF: sayılar sonlu, negatif değil ve en az bir istek var.
func (s StdRequestSplit) valid() bool {
	if !finiteNonNeg(s.FailedPerMin) || !finiteNonNeg(s.OKPerMin) || s.Total() <= 0 {
		return false
	}
	for _, p := range []*float64{s.NormalFailedPerMin, s.NormalOKPerMin} {
		if p != nil && !finiteNonNeg(*p) {
			return false
		}
	}
	return (s.NormalFailedPerMin == nil) == (s.NormalOKPerMin == nil)
}

// NewRequestSplit — SAF: bir dakikanın oranı (0–1) + istek sayısı → bölüm.
// baseRatio / basePerMin: mevsimsel taban oranı ve taban trafiği (dakikada
// istek); basePerMin ≤ 0 = taban trafiği bilinmiyor → olağan düzey yazılmaz.
// ok=false: istek yok ya da girdi bozuk (0/0 oran değildir).
func NewRequestSplit(minute int64, ratio, load, baseRatio, basePerMin float64) (StdRequestSplit, bool) {
	total := math.Round(load)
	if !finiteNonNeg(load) || !finiteNonNeg(ratio) || total <= 0 {
		return StdRequestSplit{}, false
	}
	failed := math.Min(math.Round(math.Min(ratio, 1)*load), total)
	s := StdRequestSplit{Minute: minute, FailedPerMin: failed, OKPerMin: total - failed}
	if nt := math.Round(basePerMin); finiteNonNeg(basePerMin) && finiteNonNeg(baseRatio) && nt > 0 {
		nf := math.Min(math.Round(math.Min(baseRatio, 1)*basePerMin), nt)
		s.NormalFailedPerMin, s.NormalOKPerMin = f64(nf), f64(nt-nf)
	}
	return s, true
}

// MLRequestSplitOf — SAF: ML olayının kanıtındaki `requests` bölümü. nil =
// yok / bozuk (eski olay, başka dedektör).
func MLRequestSplitOf(evidence string) *StdRequestSplit {
	if evidence == "" || !strings.Contains(evidence, `"requests"`) {
		return nil
	}
	var m struct {
		R *StdRequestSplit `json:"requests"`
	}
	if json.Unmarshal([]byte(evidence), &m) != nil || m.R == nil || !m.R.valid() {
		return nil
	}
	return m.R
}

// ── Türkçe / dilden bağımsız sayı ───────────────────────────────────────

// groupInt — SAF: tam sayı, binlik ayırıcıyla ("2.890" / "2,890").
func groupInt(v float64, sep byte) string {
	s := strconv.FormatInt(int64(math.Round(v)), 10)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(sep)
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// sharePct — SAF: pay yüzdesi, bir ondalık (hata oranı cümlesiyle aynı
// hassasiyet); sıfır olmayan ama %0,1'in altındaki pay iki ondalık ("0.04"),
// yoksa "%0" diye okunurdu. Ondalık ayırıcı nokta; sondaki sıfır atılır.
func sharePct(p float64) string {
	prec := 1
	if p > 0 && p < 0.1 {
		prec = 2
	}
	s := strconv.FormatFloat(p, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// trPossessive — SAF: sayının OKUNUŞUNA göre iyelik eki ("isteklerin %6,8'i",
// "%7'si", "%10'u"). Girdi sharePct çıktısı (yüzde ≤ 100; ondalıkta sondaki
// sıfır atılmış).
func trPossessive(num string) string {
	last := num[len(num)-1]
	if last != '0' {
		return [...]string{"", "'i", "'si", "'ü", "'ü", "'i", "'sı", "'si", "'i", "'u"}[last-'0']
	}
	switch num {
	case "0":
		return "'ı" // sıfır
	case "100":
		return "'ü" // yüz
	}
	// onlar basamağı: on, yirmi, otuz, kırk, elli, altmış, yetmiş, seksen, doksan
	tens := num[len(num)-2]
	return [...]string{"'ı", "'u", "'si", "'u", "'ı", "'si", "'ı", "'i", "'i", "'ı"}[tens-'0']
}

// ── Problem açıklamasındaki işaret cümlesi (tek tanım) ──────────────────

var requestSplitRe = regexp.MustCompile(
	`Dakikada ([0-9][0-9.]*) başarısız, ([0-9][0-9.]*) başarılı istek(?: \(olağanı ([0-9][0-9.]*) başarısız, ([0-9][0-9.]*) başarılı\))?\.`)

// RequestSplitSentence — SAF: Problem açıklamasına eklenen cümle:
// "Dakikada 210 başarısız, 2.890 başarılı istek (olağanı 12 başarısız, 3.090
// başarılı)." Olağan düzey bilinmiyorsa parantez yazılmaz. Geçersiz bölüm → "".
func RequestSplitSentence(s StdRequestSplit) string {
	if !s.valid() {
		return ""
	}
	out := "Dakikada " + groupInt(s.FailedPerMin, '.') + " başarısız, " + groupInt(s.OKPerMin, '.') + " başarılı istek"
	if s.NormalFailedPerMin != nil {
		out += " (olağanı " + groupInt(*s.NormalFailedPerMin, '.') + " başarısız, " + groupInt(*s.NormalOKPerMin, '.') + " başarılı)"
	}
	return out + "."
}

// ParseRequestSplit — SAF: açıklamadaki işaret cümlesinden bölüm
// (RequestSplitSentence'ın tersi). ok=false: cümle yok / bozuk.
func ParseRequestSplit(desc string) (StdRequestSplit, bool) {
	m := requestSplitRe.FindStringSubmatch(desc)
	if m == nil {
		return StdRequestSplit{}, false
	}
	num := func(tok string) (float64, bool) {
		v, err := strconv.ParseFloat(strings.ReplaceAll(tok, ".", ""), 64)
		return v, err == nil
	}
	f, ok1 := num(m[1])
	o, ok2 := num(m[2])
	s := StdRequestSplit{FailedPerMin: f, OKPerMin: o}
	if m[3] != "" {
		nf, ok3 := num(m[3])
		no, ok4 := num(m[4])
		if !ok3 || !ok4 {
			return StdRequestSplit{}, false
		}
		s.NormalFailedPerMin, s.NormalOKPerMin = f64(nf), f64(no)
	}
	if !ok1 || !ok2 || !s.valid() {
		return StdRequestSplit{}, false
	}
	return s, true
}

// StripRequestSplit — SAF: açıklamadan işaret cümlesini atar (manşet: ML
// Problem'inin manşeti açıklamanın kendisidir; sayılar Etki'de söylenir).
func StripRequestSplit(desc string) string {
	loc := requestSplitRe.FindStringIndex(desc)
	if loc == nil {
		return desc
	}
	return strings.TrimSpace(strings.TrimRight(desc[:loc[0]], " ") + desc[loc[1]:])
}

// requestSplitImpact — SAF: etki cümlesi + anahtarı. Türkçe:
// "Dakikada 210 başarısız · 2.890 başarılı (isteklerin %6,8'i)". Anahtarın
// argümanları dilden bağımsız biçimli metin (std_msg.go "diğer" kuralı).
func requestSplitImpact(s StdRequestSplit) (string, *StdMsg) {
	pct := sharePct(s.FailedPct())
	text := "Dakikada " + groupInt(s.FailedPerMin, '.') + " başarısız · " + groupInt(s.OKPerMin, '.') + " başarılı (isteklerin %" +
		strings.Replace(pct, ".", ",", 1) + trPossessive(pct) + ")"
	return text, stdM("imp.reqSplit", "f", groupInt(s.FailedPerMin, ','), "o", groupInt(s.OKPerMin, ','), "share", pct+"%")
}

// applyRequestSplit — SAF: etkinin metnini istek bölümüyle değiştirir; ölçü
// alanları (Value / Normal / Ratio / Floor) AYNEN kalır — öncelik, güven ve
// bildirim tabanı bu slice'ta değişmez.
func applyRequestSplit(im *StdImpact, s StdRequestSplit) {
	im.Primary.Text, im.Primary.Msg = requestSplitImpact(s)
	im.Requests = &s
	im.Known = true
}
