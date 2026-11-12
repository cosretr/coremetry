package ml

// explain_card.go — v0.10.1842, öğrenme döngüsü Faz 2 dilim 2.2 (audit
// docs/anomaly/learning-loop-audit.md §7.3, mockup
// docs/frontend/mockups/anomaly-detail.html görünüm 1–4): ML olayının yazılı
// açıklama kartı. TEK üretici: liste satırının tek satır özeti, detay
// sayfasının kartı (ve sonraki partide CoSRE kartı + bildirim konusu) aynı
// SAF işlevden gelir. LLM YOK; şablonlar deterministik, sözlükler dedektör ×
// ölçü × birim.
//
// Dürüstlük kuralı: girdide olmayan sayı uydurulmaz — bant yoksa "normalde
// yaklaşık <taban>", etki verisi yoksa Etki satırı boş (UI satırı gizler),
// yayın verisi bilinmiyorsa "yayın yok" cümlesi de yazılmaz.
//
// Neden FE'de değil: üç ayrı FE metin üreticisi (mlReplay.ts) aynı olayı üç
// ayrı dille anlatıyordu ve liste satırında özet alanı yoktu (audit §2
// "Anlatım üreticileri" satırı). Sunucuda tek üretici = liste, detay, CoSRE,
// bildirim aynı cümle.

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ── çıktı ────────────────────────────────────────────────────────────────

// ExplainLink — filtreli pivot (href pencereyi taşır).
type ExplainLink struct {
	Label string `json:"label"`
	Href  string `json:"href,omitempty"`
}

// ExplainItem — "Yakında ne değişti" maddesi.
type ExplainItem struct {
	Text string       `json:"text"`
	Link *ExplainLink `json:"link,omitempty"`
}

// ExplainTechnical — katlı "Teknik detay" (mockup etiketleriyle aynı sıra).
type ExplainTechnical struct {
	Detector   string `json:"detector"`
	Score      string `json:"score"`
	Signature  string `json:"signature"`
	Thresholds string `json:"thresholds"`
	Expected   string `json:"expected"`
	Revision   string `json:"revision"`
	Series     string `json:"series,omitempty"`
	// Measured — v0.10.2044: "Dedektörün ölçtüğü" (metrik olayı; cümle panel tepesini söyleyebilir).
	Measured string `json:"measured,omitempty"`
}

// ExplainHeader — başlık çipleri (sinyal türü, servis, durum + süre, profil).
type ExplainHeader struct {
	Kind         string       `json:"kind"` // metric | trace | log
	Detector     string       `json:"detector"`
	Service      string       `json:"service"`
	ServiceHref  string       `json:"serviceHref"`
	Open         bool         `json:"open"`
	Status       string       `json:"status"` // "Sürüyor · 14 dk" / "Bitti · 30 dk sürdü"
	Profile      string       `json:"profile"`
	ProfileLabel string       `json:"profileLabel,omitempty"`
	Subject      *ExplainLink `json:"subject,omitempty"` // endpoint / pod / bağımlılık
}

// ExplainCard — kartın tamamı. Boş Impact = satır yok.
type ExplainCard struct {
	Summary      string           `json:"summary"`
	What         string           `json:"what"`
	HowMuch      string           `json:"howMuch"`
	Since        string           `json:"since"`
	Impact       string           `json:"impact,omitempty"`
	WhyItMatters string           `json:"whyItMatters"`
	WhatChanged  []ExplainItem    `json:"whatChanged"`
	WhereToLook  []ExplainLink    `json:"whereToLook"`
	Technical    ExplainTechnical `json:"technical"`
	Header       ExplainHeader    `json:"header"`
	// Play — v0.10.1846 olayı oynat (adımlar + kova başına cümle); seri yoksa yok.
	Play *ExplainPlay `json:"play,omitempty"`
	// Value — v0.10.2044: metrik özetinin değer parçası (yalnız metrik olayı).
	Value *ExplainValue `json:"value,omitempty"`
}

// ── girdi ────────────────────────────────────────────────────────────────

// CardBand — kova başına beklenen aralık (unix sn; Lo/Mid/Hi = q25/q50/q75).
type CardBand struct {
	T           int64
	Lo, Mid, Hi float64
}

// CardRollout — servisin bir yayını.
type CardRollout struct {
	At                   time.Time
	Version, PrevVersion string
}

// CardTemplate — aynı serviste ilk kez görülen log mesajı.
type CardTemplate struct {
	Text      string
	Query     string // v0.10.2026: /logs arama metni (kesilmemiş ifadeden); boşsa Text tırnaklanır
	FirstSeen time.Time
	Count     float64
	Href      string
}

// CardRelated — yakında olan başka bir anomali. Relation: same_service |
// neighbor | callee | db. Fact geçmiş zaman olgu ("hata oranı arttı").
type CardRelated struct {
	Relation, Service, Fact string
	At                      time.Time
	Href, Label             string
}

// CardImpact — etki. WholeWindow=false: yalnız son kova biliniyor.
type CardImpact struct {
	Affected, Total    float64
	WholeWindow        bool
	Channels           []string
	PodsHit, PodsTotal int
}

// CardLowTraffic — az çağrılan servis sayımı (son N çağrının K'sı eşiği aştı).
type CardLowTraffic struct {
	N, K    int
	LimitMs float64
}

// CardInput — üreticinin tek girdisi (adaptörler explain_input.go).
type CardInput struct {
	EventID, Detector, Signal, Service, Part, Code, Pod, Stage, Direction string
	StartedAt, LastSeen                                                   time.Time
	BucketSec                                                             int
	Observed, Baseline, Score                                             float64
	Series                                                                []chstore.MLPoint
	Dwell                                                                 []int64 // tetiklenen kova başları (oynatmanın ilk sapması)
	Band                                                                  []CardBand
	SlowSec                                                               float64
	MetricLabel, MetricUnit                                               string
	CriticalZ, EffectiveZ, ImpactMinRel                                   float64
	DwellBuckets, BaselineDays                                            int
	ConfigHash, DayClass                                                  string
	BaselineKind                                                          string // v0.10.2065: evidence.baselineKind (seasonal | recent | none; "" = eski olay)
	Profile                                                               string
	BusyHour                                                              *bool
	Now                                                                   time.Time
	Loc                                                                   *time.Location
	Rollouts                                                              []CardRollout
	RolloutsKnown                                                         bool
	RolloutLookback                                                       time.Duration
	NewTemplate                                                           *CardTemplate
	Related                                                               []CardRelated
	Impact                                                                *CardImpact
	LowTraffic                                                            *CardLowTraffic
	Contrib                                                               *Contrib // v0.10.2037 — suçlu seri (evidence.contrib)
	MetricWindowed                                                        bool     // v0.10.2044 — aile ifadesi pencereli (rate/increase)
	LogService                                                            string   // v0.10.2045 — log tarafındaki servis adı (katalog adından farklıysa)
}

// ── sözlük ───────────────────────────────────────────────────────────────

const (
	unitRatio  = "ratio"
	unitMs     = "ms"
	unitCount  = "count"
	unitMetric = "metric"
)

type detSpec struct {
	label    string // teknik detay
	fact     string // özet olgusu ({code} {pod} yer tutucuları)
	what     string // "Ne oldu" ({svc} {code} {pod} {dir})
	past     string // başka olayın maddesi için geçmiş zaman olgu
	unit     string
	noun     string // sayım birimi ("hata satırı")
	additive bool   // pencere toplamı anlamlı (sayım)
	measure  string // ms/metrik "Ne kadar" öznesi
}

// mlDetSpecs — dedektör sözlüğü (chstore.MLDetectorNames ile aynı küme;
// explain_card_test TestDetSpecsCoverDetectors pinler).
var mlDetSpecs = map[string]detSpec{
	"op_latency":            {label: "Endpoint gecikmesi · yavaş istek oranı", fact: "yavaş istek oranı", what: "{svc} servisinde yavaş yanıt alan isteklerin oranı arttı.", past: "yavaş istek oranı arttı", unit: unitRatio},
	"op_error_rate":         {label: "Endpoint hata oranı", fact: "hata oranı", what: "{svc} servisinde hata alan isteklerin oranı arttı.", past: "hata oranı arttı", unit: unitRatio},
	"throughput_drop":       {label: "Trafik düşüşü", fact: "istek sayısı", what: "{svc} servisine gelen istek sayısı düştü.", past: "istek sayısı düştü", unit: unitCount, noun: "istek", additive: true},
	"dependency_latency":    {label: "Bağımlılık gecikmesi (p95)", fact: "{code} çağrı süresi", what: "{svc} servisinin {code} çağrıları yavaşladı.", past: "{code} çağrıları yavaşladı", unit: unitMs, measure: "Çağrı süresi (p95)"},
	"dependency_error_rate": {label: "Bağımlılık hata oranı (VM client, error_type)", fact: "{code} çağrılarında hata oranı", what: "{svc} servisinin {code} çağrılarında hata oranı arttı.", past: "{code} çağrılarında hata arttı", unit: unitRatio},
	"trace_change_point":    {label: "Kırılma (trace, p95)", fact: "yanıt süresi (p95)", what: "{svc} servisinde yanıt süresi kalıcı olarak {dir}.", past: "yanıt süresi kalıcı olarak değişti", unit: unitMs, measure: "Yanıt süresi (p95)"},
	"trace_error_count":     {label: "Operasyon hata sayısı (CH, örneklenmemiş hata span'leri)", fact: "{code} hata sayısı", what: "{svc} servisinin {code} operasyonunda hata sayısı arttı.", past: "hata sayısı arttı", unit: unitCount, noun: "hata", additive: true},
	"trace_pod_outlier":     {label: "Aykırı pod (trace)", fact: "{pod} pod'unda hata payı", what: "{svc} servisinde {pod} pod'u diğerlerinden daha çok hata veriyor.", past: "bir pod diğerlerinden ayrıştı", unit: unitRatio},
	"log_volume":            {label: "Log hacmi", fact: "log hacmi", what: "{svc} servisinin log hacmi {dirn}.", past: "log hacmi değişti", unit: unitCount, noun: "log satırı", additive: true},
	"error_rate":            {label: "Hata oranı (log)", fact: "hata logları", what: "{svc} servisinde hata logları arttı.", past: "hata logları arttı", unit: unitCount, noun: "hata satırı", additive: true},
	"service_silent":        {label: "Servis sustu", fact: "log akışı", what: "{svc} servisi log üretmeyi kesti.", past: "log akışı durdu", unit: unitCount, noun: "log satırı", additive: true},
	"template_spike":        {label: "Şablon artışı", fact: "bir log mesajı", what: "{svc} servisinde bir log mesajı arttı: \"{code}\".", past: "bir log mesajı arttı", unit: unitCount, noun: "satır", additive: true},
	"rare_template":         {label: "Nadir şablon", fact: "yeni bir log mesajı", what: "{svc} servisinde daha önce görülmemiş bir log mesajı çıktı: \"{code}\".", past: "yeni bir log mesajı çıktı", unit: unitCount, noun: "satır", additive: true},
	"rare_error_code":       {label: "Nadir hata kodu", fact: "{code} hata kodu", what: "{svc} servisinde yeni bir hata kodu görüldü: {code}.", past: "yeni bir hata kodu görüldü", unit: unitCount, noun: "satır", additive: true},
	"pod_outlier":           {label: "Aykırı pod (log)", fact: "{pod} pod'unda hata payı", what: "{svc} servisinde {pod} pod'u diğerlerinden fazla hata logu üretiyor.", past: "bir pod diğerlerinden fazla hata üretti", unit: unitRatio},
	"change_point":          {label: "Kırılma (log)", fact: "log seviyesi", what: "{svc} servisinin log seviyesi kalıcı olarak {dir}.", past: "log seviyesi kalıcı olarak değişti", unit: unitCount, noun: "satır"},
}

func specFor(in CardInput) detSpec {
	if sp, ok := mlDetSpecs[in.Detector]; ok {
		// v0.10.2059 (Davis S1): op_latency'nin p95 ölçüsü (part "route|p95").
		if in.Detector == "op_latency" && strings.HasSuffix(in.Part, "|p95") {
			sp.label, sp.fact, sp.what, sp.past, sp.unit, sp.measure = "Endpoint gecikmesi · p95", "{code} yanıt süresi (p95)",
				"{svc} servisinin {code} endpoint'i yavaşladı.", "yanıt süresi arttı", unitMs, "Yanıt süresi (p95)"
		}
		if in.Detector == "trace_pod_outlier" && in.Code == "p95" {
			sp.fact, sp.what, sp.unit, sp.measure = "{pod} pod'unda yanıt süresi", "{svc} servisinde {pod} pod'u diğerlerinden yavaş.", unitMs, "Bu pod'da yanıt süresi (p95)"
		}
		return sp
	}
	if strings.HasPrefix(in.Detector, "metric_") || in.Signal == chstore.MLSignalMetric {
		lbl := in.MetricLabel
		if lbl == "" {
			lbl = in.Code
		}
		if t := partTopic(in.Part); t != "" { // v0.10.2064 (Davis S3): suçlu topic adıyla
			lbl += " (topic " + t + ")"
		}
		return detSpec{label: "Metrik · " + strings.TrimPrefix(in.Detector, "metric_"), fact: lbl, what: "{svc} servisinde " + lbl + " {dirn}.",
			past: lbl + " {dirn}", unit: unitMetric, measure: capFirst(lbl)}
	}
	return detSpec{label: in.Detector, fact: in.Detector, what: "{svc} servisinde " + in.Detector + " sapması.", past: in.Detector + " sapması", unit: unitCount, noun: "değer"}
}

var profileLabelsTR = map[string]string{ProfileAlwaysOn: "7/24", ProfileBusinessHours: "mesai", ProfileLowTraffic: "az çağrılan", ProfileBatch: "batch"}

// ProfileLabelTR — SAF: profil → başlık çipi / liste "Servis tipi" etiketi
// (v0.10.1874; bilinmiyor = "").
func ProfileLabelTR(p string) string { return profileLabelsTR[p] }

var dayClassTR = map[string]string{"holiday": "tatil", "half_day": "yarım gün", "sunday": "pazar", "saturday": "cumartesi", "weekday": "iş günü", "month_end": "ay sonu benzeri"}

var signalTR = map[string]string{chstore.MLSignalLog: "Log", chstore.MLSignalTrace: "Trace", chstore.MLSignalMetric: "Metrik"}

func (in CardInput) loc() *time.Location {
	if in.Loc != nil {
		return in.Loc
	}
	return time.UTC
}

func (in CardInput) bucket() time.Duration {
	if in.BucketSec <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(in.BucketSec) * time.Second
}

// cardOpen — SAF: olay sürüyor mu? Kapandı damgası ya da 4 kova boyunca
// görülmemek "sürdü" dilini seçer (ML kapalıyken "sürüyor" yalanı büyümesin).
func cardOpen(in CardInput) bool {
	if in.Stage == chstore.MLStageClosed {
		return false
	}
	return in.Now.Sub(in.LastSeen) <= 4*in.bucket()
}

// cardEnd — SAF: açık olayda şimdi, kapalıda son görülme.
func cardEnd(in CardInput, open bool) time.Time {
	if open && in.Now.After(in.StartedAt) {
		return in.Now
	}
	if in.LastSeen.After(in.StartedAt) {
		return in.LastSeen
	}
	return in.StartedAt.Add(in.bucket())
}

func fill(tpl string, in CardInput) string {
	dir, dirn := "yükseldi", "arttı"
	if in.Direction == "down" {
		dir, dirn = "düştü", "azaldı"
	}
	code := in.Code
	if r := []rune(code); len(r) > 80 {
		code = string(r[:80]) + "…"
	}
	return strings.NewReplacer("{svc}", in.Service, "{code}", code, "{pod}", in.Pod, "{dir}", dir, "{dirn}", dirn).Replace(tpl)
}

// ── değer biçimi ─────────────────────────────────────────────────────────

// valueFmt — birim + ölçek; aralıklar aynı ölçekte yazılır ("0,5–0,8 sn").
type valueFmt struct {
	unit   string // ratio | ms | count | bytes | s/s | core | raw
	scaleS bool   // ms → sn
	byteSc float64
	byteU  string
}

func newValueFmt(sp detSpec, in CardInput, ref float64) valueFmt {
	u := sp.unit
	if u == unitMetric {
		switch in.MetricUnit {
		case "s":
			return valueFmt{unit: unitMs, scaleS: ref*1000 >= 1000}
		case "ms":
			return valueFmt{unit: unitMs, scaleS: ref >= 1000}
		case "bytes":
			f := valueFmt{unit: "bytes", byteSc: 1, byteU: "B"}
			for _, s := range []struct {
				sc float64
				u  string
			}{{1 << 30, "GB"}, {1 << 20, "MB"}, {1 << 10, "KB"}} {
				if ref >= s.sc {
					f.byteSc, f.byteU = s.sc, s.u
					break
				}
			}
			return f
		case "s/s", "core":
			return valueFmt{unit: in.MetricUnit}
		}
		return valueFmt{unit: "raw"}
	}
	if u == unitMs {
		return valueFmt{unit: unitMs, scaleS: ref >= 1000}
	}
	return valueFmt{unit: u}
}

// num — birimsiz sayı metni (aralığın parçası).
func (f valueFmt) num(v float64, metricS bool) string {
	switch f.unit {
	case unitRatio:
		p := v * 100
		if p < 10 && math.Abs(p-math.Round(p)) > 0.05 {
			return trDec(p, 1)
		}
		return trInt(p)
	case unitMs:
		if metricS {
			v *= 1000
		}
		if f.scaleS {
			s := v / 1000
			if s < 10 {
				return trDec(s, 1)
			}
			return trInt(s)
		}
		return trInt(v)
	case unitCount:
		return trInt(v)
	case "bytes":
		return trDec(v/f.byteSc, 1)
	case "s/s", "core", "raw":
		return trDec(v, 2)
	}
	return trDec(v, 2)
}

func (f valueFmt) suffix() string {
	switch f.unit {
	case unitMs:
		if f.scaleS {
			return " sn"
		}
		return " ms"
	case "bytes":
		return " " + f.byteU
	case "s/s":
		return " sn/sn"
	case "core":
		return " çekirdek"
	}
	return ""
}

// one — tek değer ("%12", "2,1 sn", "4.800").
func (f valueFmt) one(v float64, metricS bool) string {
	if f.unit == unitRatio {
		return "%" + f.num(v, metricS)
	}
	return f.num(v, metricS) + f.suffix()
}

// rng — aralık ("%3–5", "0,5–0,8 sn", "50–90").
func (f valueFmt) rng(lo, hi float64, metricS bool) string {
	a, b := f.num(lo, metricS), f.num(hi, metricS)
	if a == b {
		return "yaklaşık " + f.one(lo, metricS)
	}
	if f.unit == unitRatio {
		return "%" + a + "–" + b
	}
	return a + "–" + b + f.suffix()
}

// ── bant ve pencere ──────────────────────────────────────────────────────

func (in CardInput) bandAt(t int64) (CardBand, bool) {
	step := int64(in.bucket().Seconds())
	for _, b := range in.Band {
		if b.T == t || (step > 0 && b.T == t-t%step) {
			return b, true
		}
	}
	return CardBand{}, false
}

// refBand — SAF: "bu saatte normalde" için bant: penceredeki SON bantlı kova.
func refBand(in CardInput, end time.Time) (CardBand, bool) {
	var best CardBand
	ok := false
	for _, b := range in.Band {
		if b.T >= in.StartedAt.Unix()-int64(in.bucket().Seconds()) && b.T < end.Unix() && (!ok || b.T > best.T) {
			best, ok = b, true
		}
	}
	return best, ok
}

// windowSums — SAF: [başlangıç, bitiş) kovalarının toplamı + aynı kovaların
// bant toplamları. ok=false: seri yoksa ya da bir kovanın bandı eksikse.
func windowSums(in CardInput, end time.Time) (obs, lo, hi float64, ok bool) {
	from, to := in.StartedAt.Unix(), end.Unix()
	n := 0
	for _, p := range in.Series {
		if p.T < from || p.T >= to {
			continue
		}
		b, has := in.bandAt(p.T)
		if !has {
			return 0, 0, 0, false
		}
		obs += p.V
		lo += b.Lo
		hi += b.Hi
		n++
	}
	return obs, lo, hi, n > 0
}

// ── bölümler ─────────────────────────────────────────────────────────────

func ratioRef(in CardInput) string {
	if strings.HasSuffix(in.Detector, "pod_outlier") {
		return "diğer pod'ların"
	}
	return ""
}

func summaryOf(in CardInput, sp detSpec, open bool, end time.Time) string {
	if in.LogService != "" && in.LogService != in.Service { // v0.10.2045 — "orders-api (logda: Api)"
		in.Service = fmt.Sprintf("%s (logda: %s)", in.Service, in.LogService)
	}
	loc := in.loc()
	st := in.StartedAt.In(loc)
	var head string
	if lt := in.LowTraffic; lt != nil && lt.N > 0 {
		head = fmt.Sprintf("%s: son %d çağrının %s normalden yavaş", in.Service, lt.N, numSfx(int64(lt.K), sfxPoss3))
	} else if in.Detector == detTraceErrorCount {
		head = traceErrHead(in)
	} else if in.Signal == chstore.MLSignalMetric {
		// v0.10.2032 (operatör: "cümle değeri söylemiyor") — metrik olayı
		// ölçülen değeri ve normalini birimiyle söyler; cluster varsa adıyla.
		return metricSummary(in, sp, open, end)
	} else if in.Detector == "log_volume" {
		head = logVolumeHead(in) // v0.10.2044 — değer + normal (oran değil)
	} else {
		head = fmt.Sprintf("%s: %s %s", in.Service, fill(sp.fact, in), RatioPhrase(in.Observed, in.Baseline, in.Direction, RatioLong, ratioRef(in)))
	}
	day := ""
	switch w := dayWordTR(st, in.Now.In(loc)); w {
	case "Bugün":
	case "Dün":
		day = "dün "
	default:
		day = w + " "
	}
	plain, since := durationTR(end.Sub(in.StartedAt))
	if open {
		return fmt.Sprintf("%s, %s (%s%s beri).", head, since, day, clockSfx(st, sfxAbl))
	}
	return fmt.Sprintf("%s; %s%s başladı, %s bitti (%s sürdü).", head, day, clockSfx(st, sfxLoc), clockSfx(end.In(loc), sfxLoc), plain) // v0.10.2034 bitiş saati açık
}

// logVolumeHead — SAF (v0.10.2044; operatör: "normalin 20 katından fazla"
// değeri söylemiyordu): "X: log hacmi dakikada ~880 satıra çıktı (normalde
// ~40)". Değer = kova sayımı / kova dakikası.
func logVolumeHead(in CardInput) string {
	m := in.bucket().Minutes()
	if m <= 0 {
		m = 5
	}
	verb := "çıktı"
	if in.Direction == "down" {
		verb = "düştü"
	}
	normal := "normalde neredeyse hiç yok"
	if in.Baseline/m >= 0.5 {
		normal = "normalde ~" + trInt(in.Baseline/m)
	}
	return fmt.Sprintf("%s: log hacmi dakikada ~%s satıra %s (%s)", in.Service, trInt(in.Observed/m), verb, normal)
}

// traceErrHead — SAF (v0.10.2018 T-err): mutlak hata sayısı cümlesi; oran
// söylenmez. "X servisinin Y operasyonunda hata sayısı son 5 dakikada 40'a
// çıktı; bu saatte normali en çok 4". Baseline = normalin üst sınırı.
func traceErrHead(in CardInput) string {
	return fmt.Sprintf("%s servisinin %s operasyonunda hata sayısı son %d dakikada %s çıktı; bu saatte normali en çok %d",
		in.Service, in.Code, int(in.bucket().Minutes()), numSfx(int64(math.Round(in.Observed)), sfxDat), int64(math.Ceil(in.Baseline)))
}

func sinceOf(in CardInput, open bool, end time.Time) string {
	loc := in.loc()
	st := in.StartedAt.In(loc)
	day := dayWordTR(st, in.Now.In(loc))
	plain, since := durationTR(end.Sub(in.StartedAt))
	if open {
		return fmt.Sprintf("%s %s beri, %s sürüyor.", day, clockSfx(st, sfxAbl), since)
	}
	return fmt.Sprintf("%s %s–%s arasında, %s sürdü.", day, st.Format("15:04"), end.In(loc).Format("15:04"), plain)
}

// locWord — SAF: süre metnine bulunma eki ("15 dakika" → "15 dakikada").
func locWord(plain string) string {
	switch {
	case strings.HasSuffix(plain, "dakika"):
		return plain + "da"
	case strings.HasSuffix(plain, "saat"):
		return plain + "te"
	case strings.HasSuffix(plain, "gün"):
		return plain + "de"
	}
	return plain
}

func phraseSentence(in CardInput) string {
	return capFirst(RatioPhrase(in.Observed, in.Baseline, in.Direction, RatioShort, ratioRef(in))) + "."
}

func howMuchOf(in CardInput, sp detSpec, end time.Time) string {
	loc := in.loc()
	if lt := in.LowTraffic; lt != nil && lt.N > 0 {
		lim := valueFmt{unit: unitMs, scaleS: lt.LimitMs >= 1000}
		word := " milisaniyeyi"
		if lim.scaleS {
			word = " saniyeyi"
		}
		return fmt.Sprintf("Bu serviste normalde 10 çağrıdan en çok 1'i %s%s aşar; son %d çağrının %s aştı.",
			lim.num(lt.LimitMs, false), word, lt.N, numSfx(int64(lt.K), sfxPoss3))
	}
	metricS := sp.unit == unitMetric && in.MetricUnit == "s"
	f := newValueFmt(sp, in, in.Observed)
	normal := func() string {
		if b, ok := refBand(in, end); ok {
			return "bu saatte normalde " + f.rng(b.Lo, b.Hi, metricS)
		}
		return "bu saatte normalde yaklaşık " + f.one(in.Baseline, metricS)
	}
	tail := " " + phraseSentence(in)
	switch {
	case strings.HasSuffix(in.Detector, "pod_outlier"):
		what := "hata payı"
		if sp.unit == unitMs {
			what = "yanıt süresi (p95)"
		}
		return fmt.Sprintf("Bu pod'da %s %s; diğer pod'larda yaklaşık %s.%s", what, f.one(in.Observed, false), f.one(in.Baseline, false), tail)
	case strings.HasSuffix(in.Detector, "change_point"):
		subj := capFirst(sp.measure)
		if subj == "" {
			subj = "Kova başına satır"
		}
		return fmt.Sprintf("%s %s; önceki seviye %s.%s", subj, f.one(in.Observed, metricS), f.one(in.Baseline, metricS), tail)
	case sp.unit == unitRatio:
		pct := f.one(in.Observed, false)
		verb := "hata alıyor"
		if in.Detector == "op_latency" {
			slow := in.SlowSec
			if slow <= 0 {
				slow = 1
			}
			verb = trDec(slow, 1) + " saniyeden uzun sürüyor"
		}
		if in.Detector == "dependency_error_rate" { // v0.10.2063: suçlu hedef adıyla
			return fmt.Sprintf("%s çağrılarının %s hata alıyor; %s.%s", fill("{code}", in), trNumWithSfx(pct, sfxPoss3), normal(), tail)
		}
		return fmt.Sprintf("İsteklerin %s %s; %s.%s", trNumWithSfx(pct, sfxPoss3), verb, normal(), tail)
	case sp.additive:
		if obs, lo, hi, ok := windowSums(in, end); ok {
			plain, _ := durationTR(end.Sub(in.StartedAt))
			s := fmt.Sprintf("Son %s %s %s; bu saatte normalde %s.%s", locWord(plain), f.one(obs, false), sp.noun, f.rng(lo, hi, false), tail)
			if t := in.NewTemplate; t != nil && in.Detector == "error_rate" {
				s += fmt.Sprintf(" Yeni mesaj: \"%s\" (ilk kez %s, %s satır).", t.Text, clockSfx(t.FirstSeen.In(loc), sfxLoc), trInt(t.Count))
			}
			return s
		}
		bm := int(in.bucket().Minutes())
		s := fmt.Sprintf("Son %d dakikada %s %s; bu saatte normalde yaklaşık %s.%s", bm, f.one(in.Observed, false), sp.noun, f.one(in.Baseline, false), tail)
		if t := in.NewTemplate; t != nil && in.Detector == "error_rate" {
			s += fmt.Sprintf(" Yeni mesaj: \"%s\" (ilk kez %s, %s satır).", t.Text, clockSfx(t.FirstSeen.In(loc), sfxLoc), trInt(t.Count))
		}
		return s
	}
	subj := sp.measure
	if subj == "" {
		subj = capFirst(fill(sp.fact, in))
	}
	return fmt.Sprintf("%s %s; %s.%s", subj, f.one(in.Observed, metricS), normal(), tail)
}

func whatOf(in CardInput, sp detSpec) string {
	s := fill(sp.what, in)
	if in.Detector == "error_rate" && in.NewTemplate != nil {
		s = strings.TrimSuffix(s, ".") + " ve daha önce görülmemiş bir hata mesajı çıkmaya başladı."
	}
	return s
}

func impactOf(in CardInput) string {
	im := in.Impact
	if im == nil {
		return ""
	}
	var parts []string
	verb := "hata aldı"
	if in.Detector == "op_latency" {
		verb = "yavaş yanıt aldı"
	}
	if im.Affected > 0 {
		tot := ""
		if im.Total > 0 {
			tot = " (toplam " + trInt(im.Total) + ")"
		}
		if im.WholeWindow {
			pre := "Bu sürede"
			if in.Signal != chstore.MLSignalTrace {
				pre = "Aynı sürede"
			}
			parts = append(parts, fmt.Sprintf("%s yaklaşık %s istek %s%s.", pre, trInt(im.Affected), verb, tot))
		} else {
			parts = append(parts, fmt.Sprintf("Son %d dakikada yaklaşık %s istek %s%s.", int(in.bucket().Minutes()), trInt(im.Affected), verb, tot))
		}
	}
	if len(im.Channels) > 0 {
		lead := "Kanal: "
		if im.Affected <= 0 {
			lead = "En çok etkilenen kanal: "
		}
		parts = append(parts, lead+strings.Join(im.Channels, ", ")+".")
	}
	if im.PodsTotal > 0 && im.PodsHit > 0 {
		parts = append(parts, fmt.Sprintf("%d pod'un %s görülüyor.", im.PodsTotal, numSfx(int64(im.PodsHit), sfxPossLoc)))
	}
	return strings.Join(parts, " ")
}

// inBusinessHours — SAF: an mesai içinde mi (profile.go mesai tanımı;
// hafta içi, tatil/hafta sonu sınıfı değil, yarım günde 13:00 sonrası dışarıda).
func inBusinessHours(t time.Time, dayClass string) bool {
	switch dayClass {
	case "holiday", "saturday", "sunday":
		return false
	case "half_day":
		if t.Hour() >= 13 {
			return false
		}
	}
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	return profileBusinessHour(t.Hour())
}

func whyOf(in CardInput, open bool) string {
	at := in.StartedAt.In(in.loc())
	if open {
		at = in.Now.In(in.loc())
	}
	switch in.Profile {
	case ProfileAlwaysOn:
		if in.BusyHour == nil {
			return "Bu servis günün her saati kullanılıyor."
		}
		if *in.BusyHour {
			if open {
				return "Bu servis günün her saati kullanılıyor ve şu an yoğun saatinde."
			}
			return "Bu servis günün her saati kullanılıyor ve olay yoğun saatinde yaşandı."
		}
		return "Bu servis günün her saati kullanılıyor; olay yoğun saat dışında."
	case ProfileBusinessHours:
		in2 := inBusinessHours(at, in.DayClass)
		switch {
		case open && in2:
			return "Bu servis mesai saatlerinde kullanılıyor ve şu an mesai içinde."
		case open:
			return "Bu servis mesai saatlerinde kullanılıyor; şu an mesai dışında, etkisi sınırlı olabilir."
		case in2:
			return "Bu servis mesai saatlerinde kullanılıyor ve olay mesai içinde yaşandı."
		}
		return "Bu servis mesai saatlerinde kullanılıyor; olay mesai dışında yaşandı."
	case ProfileLowTraffic:
		return "Bu servis az çağrılıyor; birkaç yavaş ya da hatalı çağrı bile oranı belirgin biçimde değiştirir."
	case ProfileBatch:
		return "Bu bir batch servisi: yük artışı değerlendirilmez, hata artışı önemlidir."
	}
	return "Bu servisin kullanım düzeni henüz öğrenilmedi; değer bu saat için beklenen aralığın dışında."
}

func whatChangedOf(in CardInput, end time.Time, links cardLinks) []ExplainItem {
	loc := in.loc()
	out := []ExplainItem{}
	ro := rolloutInWindow(in, end)
	switch {
	case ro != nil:
		ver := ""
		if ro.Version != "" && ro.PrevVersion != "" {
			ver = fmt.Sprintf(" (%s → %s)", ro.PrevVersion, ro.Version)
		} else if ro.Version != "" {
			ver = " (" + ro.Version + ")"
		}
		out = append(out, ExplainItem{Text: fmt.Sprintf("%s yeni sürüm yayına alındı%s.", clockSfx(ro.At.In(loc), sfxLoc), ver),
			Link: &ExplainLink{Label: "Rollouts", Href: links.rollouts}})
	case in.RolloutsKnown:
		lb := in.RolloutLookback
		if lb <= 0 {
			lb = 24 * time.Hour
		}
		plain, _ := durationTR(lb)
		out = append(out, ExplainItem{Text: fmt.Sprintf("Son %s yayın yok.", locWord(plain))})
	}
	if t := in.NewTemplate; t != nil && in.Detector != "error_rate" {
		it := ExplainItem{Text: fmt.Sprintf("%s daha önce görülmemiş bir hata mesajı çıkmaya başladı: \"%s\".", clockSfx(t.FirstSeen.In(loc), sfxLoc), t.Text)}
		if t.Href != "" {
			it.Link = &ExplainLink{Label: "Log anomalisi", Href: t.Href}
		}
		out = append(out, it)
	}
	rel := append([]CardRelated(nil), in.Related...)
	sort.SliceStable(rel, func(i, j int) bool { return rel[i].At.Before(rel[j].At) })
	for _, r := range rel {
		var txt string
		switch r.Relation {
		case "neighbor":
			txt = fmt.Sprintf("Aynı dakikalarda %s %s.", nameSfx(r.Service, sfxLoc), r.Fact)
		case "db":
			txt = fmt.Sprintf("Bağlı olduğu veritabanında %s.", r.Fact)
		case "callee":
			txt = fmt.Sprintf("%s çağırdığı %s servisinde %s.", clockSfx(r.At.In(loc), sfxLoc), r.Service, r.Fact)
		default:
			txt = fmt.Sprintf("%s aynı serviste %s.", clockSfx(r.At.In(loc), sfxLoc), r.Fact)
		}
		it := ExplainItem{Text: txt}
		if r.Href != "" {
			lbl := r.Label
			if lbl == "" {
				lbl = "Anomali"
			}
			it.Link = &ExplainLink{Label: lbl, Href: r.Href}
		}
		out = append(out, it)
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func technicalOf(in CardInput, sp detSpec) ExplainTechnical {
	det := sp.label
	if s := signalTR[in.Signal]; s != "" {
		det += " · " + s
	}
	score := trDec(in.Score, 1)
	if z := in.EffectiveZ; z > 0 {
		score += " (etkin eşik " + trDec(z, 1) + ")"
	} else if z := in.CriticalZ; z > 0 {
		score += " (eşik " + trDec(z, 1) + ")"
	}
	sig := in.Service + " | " + in.Signal + " | " + in.Detector
	if in.Part != "" {
		sig += " | " + in.Part
	}
	thr := "—"
	if in.DwellBuckets > 0 {
		thr = fmt.Sprintf("Süreklilik %d kova (%d dk)", in.DwellBuckets, in.DwellBuckets*int(in.bucket().Minutes()))
		if in.ImpactMinRel > 0 {
			thr += fmt.Sprintf(" · etki ≥ %%%s göreli", trDec(in.ImpactMinRel*100, 1))
		}
	}
	exp := "Aynı saat dilimi"
	if in.BaselineDays > 0 {
		exp = fmt.Sprintf("Son %d günün aynı saat dilimi (±1 sa)", in.BaselineDays)
	}
	if c := dayClassTR[in.DayClass]; c != "" {
		exp += " · takvim sınıfı: " + c
	}
	if k := baselineKindTR[in.BaselineKind]; k != "" {
		exp += " · " + k
	}
	if len(in.Band) > 0 {
		exp += " · aralık: aynı kova diliminin q25–q75'i"
	} else {
		exp += " · bu kayıtta kova bandı yok; taban medyanı kullanıldı"
	}
	rev := "—"
	if in.ConfigHash != "" {
		rev = "ayar özeti " + in.ConfigHash + " · öğrenilen düzeltme yok"
	}
	return ExplainTechnical{Detector: det, Score: score, Signature: sig, Thresholds: thr, Expected: exp, Revision: rev, Series: in.Part}
}

func statusOf(in CardInput, open bool, end time.Time) string {
	d := durationShortTR(end.Sub(in.StartedAt))
	switch {
	case in.Stage == chstore.MLStageChronic:
		return "Kronik desen · " + d
	case in.Stage == chstore.MLStagePromote:
		return "Problem'e taşındı · " + d
	case open:
		return "Sürüyor · " + d
	}
	return "Bitti · " + d + " sürdü"
}

// ExplainSummary — SAF: tek satır özet (liste satırı, kart başlığı). Kartla
// AYNI işlev; liste yalnız bunu çağırır.
func ExplainSummary(in CardInput) string {
	open := cardOpen(in)
	return summaryOf(in, specFor(in), open, cardEnd(in, open))
}

// BuildExplainCard — SAF: girdiden kartın tamamı.
func BuildExplainCard(in CardInput) ExplainCard {
	sp := specFor(in)
	open := cardOpen(in)
	end := cardEnd(in, open)
	links := buildCardLinks(in, open, end)
	c := ExplainCard{
		Summary:      summaryOf(in, sp, open, end),
		What:         whatOf(in, sp),
		HowMuch:      howMuchOf(in, sp, end),
		Since:        sinceOf(in, open, end),
		Impact:       impactOf(in),
		WhyItMatters: whyOf(in, open),
		WhatChanged:  whatChangedOf(in, end, links),
		WhereToLook:  whereToLookOf(in, links),
		Technical:    technicalOf(in, sp),
		Header: ExplainHeader{Kind: in.Signal, Detector: in.Detector, Service: in.Service, ServiceHref: links.service, Open: open,
			Status: statusOf(in, open, end), Profile: in.Profile, ProfileLabel: profileLabelsTR[in.Profile], Subject: links.subject},
	}
	if c.Header.Profile == "" {
		c.Header.Profile = ProfileUnknown
	}
	c.Play = BuildPlay(in)
	if in.Signal == chstore.MLSignalMetric {
		v := metricValue(in, sp)
		c.Value = &v
		c.Technical.Measured = v.Measured
	}
	return c
}
