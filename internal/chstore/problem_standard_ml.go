package chstore

// problem_standard_ml.go — v0.10.2383 (triage mockup dilim 2; dilim 1
// denetiminin bulguları). ML servis düzeyi olayının sahip olduğu Problem'de
// (`anomaly-auto:svc:*`, grup satırı `anomaly-auto:group:*`) standart üç yerde
// doğru olmayanı söylüyordu; bu dosya üçünün SAF çekirdeği:
//
//  1. MANŞET — "orders servisinde hata oranı normalde %0,4, şimdi %6,8 (17
//     kat)". Cümle burada bir kez TANIMLANIR; ML tiki Problem açıklamasına bu
//     işlevle yazar (ml svcEventSentence), standart açıklamadan okur
//     (MLPromotedHeadline) — satır ve detay aynı cümle. Kat OLAYIN gerçek
//     tabanından: Problem'in Threshold'u %0,5 tabanlıdır (öncelik için),
//     Value/Threshold kat DEĞİLDİR. İngilizce arayüz için cümle anahtara geri
//     okunur (mlErrRateMsg; request_split.go ile aynı desen).
//  2. NEDEN — etki kuralı olayının gerçekleri (StdImpactRule): son 5 dakikanın
//     kaçında eşik aşıldı, eşik nasıl oluştu, yük, kaynak. Gerçekleri çağıran
//     verir (StdFacts.ImpactRule; yalnız detay ucu — liste yolunda okuma yok).
//     Kuralın sabitleri (kat, puan, taban) BURADA YOK: tek tanım
//     ml/impact_rule.go, değerler ml.ImpactRuleFacts ile gelir. v0.10.2391:
//     yanıt süresi (ateşleyen ölçü adıyla, ms / sn) ve consumer lag (kayıt,
//     yük satırı yok) da aynı satırlarla (StdImpactRule.Measure).
//  3. GÜVEN / BİLDİRİM — kaynak işareti güveni mevcut kuraldan (D12:
//     örneklenmemiş + büyük ihlal → yüksek) yükseltebilir. Bildirim politikası
//     "yüksek güvenli P1" baypasını (D3) bu yüzden yeni kararlarla işletirdi;
//     canlı gönderim değişmesin diye politika, operatör kararına kadar
//     işaretten ÖNCEKİ seviyeyi okur (StdConfidence.PolicyLevel).

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ── Manşet ──────────────────────────────────────────────────────────────

// swapNumSep — "1.800" ↔ "1,800", "0,4" ↔ "0.4" (TR ↔ dilden bağımsız biçim).
var swapNumSep = strings.NewReplacer(".", ",", ",", ".")

// mlFactorText — SAF: kat metni (dilden bağımsız biçim: "17", "1.6",
// "1,800"). 10 ve üstü tam sayı, altı tek ondalık (sondaki sıfır atılır).
func mlFactorText(r float64) string {
	if r >= 9.95 {
		return groupInt(r, ',')
	}
	s := strconv.FormatFloat(r, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// mlErrRateParts — SAF: oranlar (0–1) → olağan / şimdi / kat metinleri
// (dilden bağımsız biçim, yüzde işaretsiz). n == "": taban yok ya da
// yuvarlanınca %0 ("neredeyse hiç yok"). x == "": kat yazılmaz (taban yok,
// gözlenen tabanın üstünde değil ya da kat 1'e yuvarlanıyor).
func mlErrRateParts(observed, baseline float64) (n, v, x string) {
	if !finiteNonNeg(observed) {
		observed = 0
	}
	v = sharePct(observed * 100)
	if !finiteNonNeg(baseline) || baseline <= 0 {
		return "", v, ""
	}
	if n = sharePct(baseline * 100); n == "0" {
		return "", v, ""
	}
	if r := observed / baseline; r > 1 && !math.IsInf(r, 0) {
		if x = mlFactorText(r); x == "1" {
			x = ""
		}
	}
	return n, v, x
}

// MLErrRateSentence — SAF: ML servis hata oranı Problem'inin cümlesi (manşet;
// TEK TANIM). observed / baseline oran (0–1), olayın kendi değerleri.
//
//	taban > 0:  "<svc> servisinde hata oranı normalde %0,4, şimdi %6,8 (17 kat)."
//	kat yoksa:  "<svc> servisinde hata oranı normalde %2, şimdi %1,5."
//	taban 0:    "<svc> servisinde hata oranı normalde neredeyse hiç yok, şimdi %6,8."
func MLErrRateSentence(service string, observed, baseline float64) string {
	n, v, x := mlErrRateParts(observed, baseline)
	s := service + " servisinde hata oranı normalde "
	if n == "" {
		return s + "neredeyse hiç yok, şimdi %" + swapNumSep.Replace(v) + "."
	}
	s += "%" + swapNumSep.Replace(n) + ", şimdi %" + swapNumSep.Replace(v)
	if x != "" {
		s += " (" + swapNumSep.Replace(x) + " kat)"
	}
	return s + "."
}

var mlErrRateRe = regexp.MustCompile(
	`^(.+) servisinde hata oranı normalde (?:%([0-9]+(?:,[0-9]+)?)|neredeyse hiç yok), şimdi %([0-9]+(?:,[0-9]+)?)(?: \(([0-9][0-9.]*(?:,[0-9]+)?) kat\))?\.$`)

// mlErrRateMsg — SAF: manşet TAM olarak MLErrRateSentence cümlesiyse metin
// anahtarı (İngilizce arayüz); değilse nil (serbest metin: ikinci cümle,
// kapanış eki, grup özeti, eski cümle). Argümanlar dilden bağımsız biçimli
// metin (std_msg.go "diğer" kuralı) — iki dil aynı sayıyı söyler.
func mlErrRateMsg(headline string) *StdMsg {
	m := mlErrRateRe.FindStringSubmatch(headline)
	if m == nil {
		return nil
	}
	v := swapNumSep.Replace(m[3]) + "%"
	switch {
	case m[2] == "":
		if m[4] != "" {
			return nil // "hiç yok" + kat: bu çekirdek yazmaz
		}
		return stdM("h.mlErrZero", "svc", m[1], "v", v)
	case m[4] == "":
		return stdM("h.mlErrFlat", "svc", m[1], "n", swapNumSep.Replace(m[2])+"%", "v", v)
	}
	return stdM("h.mlErr", "svc", m[1], "n", swapNumSep.Replace(m[2])+"%", "v", v, "x", swapNumSep.Replace(m[4]))
}

// ── Manşet: servis yanıt süresi (v0.10.2385, etki olayı I4) ─────────────

// Yanıt süresi ölçüleri (ML svc_response_time olayının ölçüsü).
const (
	MLRespMedian = "median" // medyan (p50)
	MLRespSlow   = "slow"   // en yavaş %10 (p90)
)

const (
	mlRespMedianTR = "medyan"
	mlRespSlowTR   = "en yavaş %10"
)

// mlRespMs — SAF: ms → tam sayı metni (TR binlik ayırıcı; en az 1).
func mlRespMs(v float64) string {
	if !finiteNonNeg(v) || v < 1 {
		v = 1
	}
	return groupInt(v, '.')
}

// MLResponseTimeSentence — SAF: ML servis yanıt süresi Problem'inin cümlesi
// (manşet; TEK TANIM). measure MLRespMedian | MLRespSlow; observed / baseline
// ms, olayın kendi değerleri (ateşleyen ölçünün gözleneni ve mevsimsel tabanı).
//
//	"<svc> servisinde yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms (5,1 kat)."
//	"<svc> servisinde yanıt süresi (en yavaş %10) normalde 800 ms, şimdi 2.900 ms (3,6 kat)."
//	kat yoksa (taban yok / gözlenen tabanın üstünde değil): "… normalde 180 ms, şimdi 170 ms."
func MLResponseTimeSentence(service, measure string, observed, baseline float64) string {
	word := mlRespMedianTR
	if measure == MLRespSlow {
		word = mlRespSlowTR
	}
	s := service + " servisinde yanıt süresi (" + word + ") normalde " + mlRespMs(baseline) + " ms, şimdi " + mlRespMs(observed) + " ms"
	if finiteNonNeg(baseline) && baseline >= 1 && finiteNonNeg(observed) {
		if r := observed / baseline; r > 1 {
			if x := mlFactorText(r); x != "1" {
				s += " (" + swapNumSep.Replace(x) + " kat)"
			}
		}
	}
	return s + "."
}

var mlRespTimeRe = regexp.MustCompile(
	`^(.+) servisinde yanıt süresi \((medyan|en yavaş %10)\) normalde ([0-9][0-9.]*) ms, şimdi ([0-9][0-9.]*) ms(?: \(([0-9][0-9.]*(?:,[0-9]+)?) kat\))?\.$`)

// mlRespTimeMsg — SAF: manşet TAM olarak MLResponseTimeSentence cümlesiyse
// metin anahtarı (İngilizce arayüz); değilse nil. mlErrRateMsg ile aynı desen.
func mlRespTimeMsg(headline string) *StdMsg {
	m := mlRespTimeRe.FindStringSubmatch(headline)
	if m == nil {
		return nil
	}
	key := "h.mlRtMed"
	if m[2] == mlRespSlowTR {
		key = "h.mlRtSlow"
	}
	n, v := swapNumSep.Replace(m[3])+" ms", swapNumSep.Replace(m[4])+" ms"
	if m[5] == "" {
		return stdM(key+"Flat", "svc", m[1], "n", n, "v", v)
	}
	return stdM(key, "svc", m[1], "n", n, "v", v, "x", swapNumSep.Replace(m[5]))
}

// ── Manşet: uç nokta (v0.10.2474, ml endpoint_impact) ───────────────────

// mlEndpointSubject — SAF: cümlenin öznesi "checkout · POST /pay".
func mlEndpointSubject(service, route string) string { return service + " · " + route }

// MLEndpointErrRateSentence — SAF: uç nokta hata oranı cümlesi (manşet; TEK
// TANIM; sayılar MLErrRateSentence ile aynı biçimde).
//
//	"checkout · POST /pay: hata oranı normalde %0,2, şimdi %14 (70 kat)."
//	"checkout · POST /pay: hata oranı normalde neredeyse hiç yok, şimdi %14."
func MLEndpointErrRateSentence(service, route string, observed, baseline float64) string {
	n, v, x := mlErrRateParts(observed, baseline)
	s := mlEndpointSubject(service, route) + ": hata oranı normalde "
	if n == "" {
		return s + "neredeyse hiç yok, şimdi %" + swapNumSep.Replace(v) + "."
	}
	s += "%" + swapNumSep.Replace(n) + ", şimdi %" + swapNumSep.Replace(v)
	if x != "" {
		s += " (" + swapNumSep.Replace(x) + " kat)"
	}
	return s + "."
}

var mlEpErrRe = regexp.MustCompile(
	`^(.+?) · (.+): hata oranı normalde (?:%([0-9]+(?:,[0-9]+)?)|neredeyse hiç yok), şimdi %([0-9]+(?:,[0-9]+)?)(?: \(([0-9][0-9.]*(?:,[0-9]+)?) kat\))?\.$`)

// mlEpErrMsg — SAF: manşet TAM olarak MLEndpointErrRateSentence ise anahtar.
func mlEpErrMsg(headline string) *StdMsg {
	m := mlEpErrRe.FindStringSubmatch(headline)
	if m == nil {
		return nil
	}
	v := swapNumSep.Replace(m[4]) + "%"
	switch {
	case m[3] == "":
		if m[5] != "" {
			return nil
		}
		return stdM("h.mlEpErrZero", "svc", m[1], "op", m[2], "v", v)
	case m[5] == "":
		return stdM("h.mlEpErrFlat", "svc", m[1], "op", m[2], "n", swapNumSep.Replace(m[3])+"%", "v", v)
	}
	return stdM("h.mlEpErr", "svc", m[1], "op", m[2], "n", swapNumSep.Replace(m[3])+"%", "v", v, "x", swapNumSep.Replace(m[5]))
}

// MLEndpointResponseTimeSentence — SAF: uç nokta yanıt süresi cümlesi
// (manşet; TEK TANIM; sayılar MLResponseTimeSentence ile aynı). measure
// MLRespMedian | MLRespSlow; ms.
//
//	"checkout · POST /pay: yanıt süresi (medyan) normalde 180 ms, şimdi 920 ms (5,1 kat)."
func MLEndpointResponseTimeSentence(service, route, measure string, observed, baseline float64) string {
	s := MLResponseTimeSentence("", measure, observed, baseline)
	return mlEndpointSubject(service, route) + ":" + strings.TrimPrefix(s, " servisinde")
}

var mlEpRtRe = regexp.MustCompile(
	`^(.+?) · (.+): yanıt süresi \((medyan|en yavaş %10)\) normalde ([0-9][0-9.]*) ms, şimdi ([0-9][0-9.]*) ms(?: \(([0-9][0-9.]*(?:,[0-9]+)?) kat\))?\.$`)

// mlEpRtMsg — SAF: manşet TAM olarak MLEndpointResponseTimeSentence ise anahtar.
func mlEpRtMsg(headline string) *StdMsg {
	m := mlEpRtRe.FindStringSubmatch(headline)
	if m == nil {
		return nil
	}
	key := "h.mlEpRtMed"
	if m[3] == mlRespSlowTR {
		key = "h.mlEpRtSlow"
	}
	n, v := swapNumSep.Replace(m[4])+" ms", swapNumSep.Replace(m[5])+" ms"
	if m[6] == "" {
		return stdM(key+"Flat", "svc", m[1], "op", m[2], "n", n, "v", v)
	}
	return stdM(key, "svc", m[1], "op", m[2], "n", n, "v", v, "x", swapNumSep.Replace(m[6]))
}

// mlPromotedMsg — SAF: ML servis Problem'i manşetinin metin anahtarı (hata
// oranı ya da yanıt süresi cümlesi; v0.10.2474 uç nokta cümleleri);
// serbest metinde nil.
func mlPromotedMsg(headline string) *StdMsg {
	for _, f := range []func(string) *StdMsg{mlErrRateMsg, mlRespTimeMsg, mlEpErrMsg, mlEpRtMsg, mlLagMsg, mlOOMMsg} {
		if m := f(headline); m != nil {
			return m
		}
	}
	return nil
}

// ── Neden: etki kuralı gerçekleri ───────────────────────────────────────

// StdImpactRule — etki kuralı olayının gerçekleri (ml.ImpactRuleFacts: olay
// kanıtı + ml/impact_rule.go sabitleri). Oranlar 0–1.
type StdImpactRule struct {
	Window        int     // karar penceresi (dakika)
	Violations    int     // penceredeki ihlal sayısı
	MinViolations int     // açılış için gereken asgari ihlal
	Threshold     float64 // uygulanan eşik
	// Kuralın eşiği nasıl kurduğu: max(Floor, taban × Rel, taban + Abs).
	// Rel == 0 = bilinmiyor (kanıttaki eşik bugünkü sabitlerle uyuşmuyor) →
	// satır yalnız eşiği söyler.
	Floor, Rel, Abs float64
	LoadPerMin      float64 // en yeni ihlal dakikasının isteği; 0 = bilinmiyor
	MinLoad         float64 // yük kapısı (istek/dk); 0 = kapı yok
	Unsampled       bool    // ölçü örneklenmemiş sunucu metriğinden (kanıt source=vm)
	// Measure — v0.10.2391: olayın ölçüsü ve eşiğin birimi. "" = servis hata
	// oranı (oran 0–1; v0.10.2383 satırları bayt bayt), MLRespMedian /
	// MLRespSlow = yanıt süresi (ms; ateşleyen ölçü satırda adıyla),
	// MLImpactLag = consumer lag (kayıt; yük kapısı yok).
	Measure string
}

// MLImpactLag — StdImpactRule.Measure: consumer lag etki olayı (v0.10.2391).
const MLImpactLag = "lag"

func (r StdImpactRule) valid() bool {
	return r.Window > 0 && r.MinViolations > 0 && r.Violations > 0 && r.Violations <= r.Window &&
		r.Threshold > 0 && !math.IsInf(r.Threshold, 0) && !math.IsNaN(r.Threshold)
}

// trCountLoc — SAF: "5'inde", "2'sinde", "3'ünde" (sayının OKUNUŞUNA göre
// iyelik + bulunma eki). n ≥ 0.
func trCountLoc(n int) string {
	s := strconv.Itoa(n)
	p := trPossessive(s)
	if strings.HasSuffix(p, "ı") || strings.HasSuffix(p, "u") {
		return s + p + "nda"
	}
	return s + p + "nde"
}

// plainNum — SAF: sabit için kısa sayı ("1.5", "1", "10"; dilden bağımsız).
func plainNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// impactRuleReasons — SAF: "Neden böyle değerlendiriyoruz" satırları + metin
// anahtarları (aynı sıra / uzunluk). srcVM: Problem'in standart kaynağı VM mi
// (kaynak satırı karttaki kaynakla çelişmesin diye yalnız o zaman yazılır).
// Geçersiz gerçek → nil (çağıran bugünkü tek cümleyi çizer).
func impactRuleReasons(r StdImpactRule, srcVM bool) ([]string, []StdMsg) {
	if !r.valid() {
		return nil, nil
	}
	var out []string
	var msgs []StdMsg
	add := func(text string, m *StdMsg) {
		out = append(out, text)
		msgs = append(msgs, *m)
	}
	w, n, need := strconv.Itoa(r.Window), strconv.Itoa(r.Violations), strconv.Itoa(r.MinViolations)
	// v0.10.2391 — yanıt süresinde ateşleyen ölçü adıyla ("medyan eşiği").
	what, kViol, kOpen := "eşik", "why.viol", "why.violOpen"
	switch r.Measure {
	case MLRespMedian:
		what, kViol, kOpen = mlRespMedianTR+" eşiği", "why.violMed", "why.violOpenMed"
	case MLRespSlow:
		what, kViol, kOpen = mlRespSlowTR+" eşiği", "why.violSlow", "why.violOpenSlow"
	}
	head := "Son " + w + " dakikanın " + trCountLoc(r.Violations) + " " + what + " aşıldı "
	if r.Violations >= r.MinViolations {
		add(head+"(en az "+need+" gerekir)", stdM(kViol, "n", n, "w", w, "min", need))
	} else {
		// Olay açıkken pencerede ihlal sürdükçe uzar (ml svcImpactDecideBase).
		add(head+"(açılış için en az "+need+" gerekir; ihlal sürdüğü için açık)", stdM(kOpen, "n", n, "w", w, "min", need))
	}
	switch r.Measure {
	case MLRespMedian, MLRespSlow:
		add(impactThresholdMs(r))
	case MLImpactLag:
		add(impactThresholdLag(r))
	default:
		add(impactThresholdRate(r))
	}
	if r.MinLoad > 0 && r.LoadPerMin >= r.MinLoad && !math.IsInf(r.LoadPerMin, 0) {
		add("Yük yeterli: dakikada yaklaşık "+groupInt(r.LoadPerMin, '.')+" istek (en az "+groupInt(r.MinLoad, '.')+" gerekir)",
			stdM("why.load", "load", groupInt(r.LoadPerMin, ','), "min", groupInt(r.MinLoad, ',')))
	}
	if r.Unsampled && srcVM {
		add("Kaynak örneklenmemiş sunucu metriği", stdM("why.srcVM"))
	}
	return out, msgs
}

// impactThresholdRate — SAF: hata oranı eşiği satırı (v0.10.2383 metni aynen).
func impactThresholdRate(r StdImpactRule) (string, *StdMsg) {
	th := sharePct(r.Threshold * 100)
	if r.Rel > 0 {
		floor, rel, abs := sharePct(r.Floor*100), plainNum(r.Rel), sharePct(r.Abs*100)
		return "Eşik %" + swapNumSep.Replace(th) + ": en az %" + swapNumSep.Replace(floor) + ", normalin " + swapNumSep.Replace(rel) + " katı ve " + swapNumSep.Replace(abs) + " puan üstü",
			stdM("why.threshold", "t", th+"%", "floor", floor+"%", "rel", rel, "abs", abs)
	}
	return "Eşik %" + swapNumSep.Replace(th), stdM("why.thrOnly", "t", th+"%")
}

// impactThresholdMs — SAF (v0.10.2391): yanıt süresi eşiği. Kuralın tabandan
// bağımsız alt sınırı yok (Floor 0); varsa formül iddia edilmez. Birim ms /
// sn (trMs; EN arayüz …Ms argümanını aynı dallarla çizer).
//
//	"Eşik 270 ms: normalin 1,5 katı ve 100 ms üstü"
//	"Eşik 1,75 sn: normalin 2 katı ve 1 sn üstü"
func impactThresholdMs(r StdImpactRule) (string, *StdMsg) {
	if r.Rel > 0 && r.Floor == 0 && r.Abs > 0 {
		rel := plainNum(r.Rel)
		return "Eşik " + trMs(r.Threshold) + ": normalin " + swapNumSep.Replace(rel) + " katı ve " + trMs(r.Abs) + " üstü",
			stdM("why.thrMs", "tMs", r.Threshold, "rel", rel, "absMs", r.Abs)
	}
	return "Eşik " + trMs(r.Threshold), stdM("why.thrOnlyMs", "tMs", r.Threshold)
}

// impactThresholdLag — SAF (v0.10.2391): consumer lag eşiği (kayıt; mutlak
// artış yok, tabandan bağımsız alt sınır var).
//
//	"Eşik 1.200 kayıt: en az 1.000 kayıt ve normalin 2 katı"
func impactThresholdLag(r StdImpactRule) (string, *StdMsg) {
	if r.Rel > 0 && r.Floor > 0 && r.Abs == 0 {
		rel := plainNum(r.Rel)
		return "Eşik " + groupInt(r.Threshold, '.') + " kayıt: en az " + groupInt(r.Floor, '.') + " kayıt ve normalin " + swapNumSep.Replace(rel) + " katı",
			stdM("why.thrLag", "t", groupInt(r.Threshold, ','), "floor", groupInt(r.Floor, ','), "rel", rel)
	}
	return "Eşik " + groupInt(r.Threshold, '.') + " kayıt", stdM("why.thrOnlyLag", "t", groupInt(r.Threshold, ','))
}

// ── Güven: bildirim politikasının okuduğu seviye ────────────────────────

// policyLevel — SAF: bildirim politikasının (NotifiableAt, NoiseSilences ve
// S4 / S5 baypasları) okuduğu güven. PolicyLevel boşsa ekrandaki seviye.
func (c StdConfidence) policyLevel() string {
	if c.PolicyLevel != "" {
		return c.PolicyLevel
	}
	return c.Level
}

// isREDFamily — SAF: G3'ün (VM karar) anlamlı olduğu aileler.
func isREDFamily(family string) bool {
	return family == StdFamilyError || family == StdFamilyLatency || family == StdFamilyTraffic
}
