package ml

// record.go — v0.10.2199, anomali kartı sözleşmesi H1
// (docs/ml/anomaly-card-audit.md §9–§10): TEK olay-kaydı sözleşmesi ve
// yazım-öncesi doğrulayıcı. Kart, kapılar (A) ve replay (E) yalnız bu kaydı
// okuyacak; alanlar burada bir kez tanımlanır, sonraki dilimler yenisini
// eklemez. Saklama: ml_events.class / publish_state / onset_at tipli
// kolonları + record (bu yapının JSON'u). Seri ml_event_snapshots'ta kalır.
//
// H2 (v0.10.2215): consumer lag kaydı doldurur (kafka_lag_record.go);
// applyRecordContract kaydı OLAN olaya uygulanır, olmayan eski kayıt aynen
// geçer. Doğrulayıcıdan kalan kayıt publish_state=invalid yazılır (yayımlanmaz)
// ve dedektör başına sayaca girer (stats.go recordInvalid, tik log satırı).

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// RecordVersion — record JSON'unun sürümü (alan eklenince artar).
const RecordVersion = 1

// Sınıf ve yayın durumu — ml_events.class / publish_state kapalı kümeleri.
const (
	ClassServiceAnomaly  = "service_anomaly"
	ClassTelemetryVolume = "telemetry_volume"
	ClassInfo            = "info"

	PublishPublished = "published"
	PublishHeld      = "held"
	PublishInvalid   = "invalid"

	// SeriesKindOccurrence — sayısal serisi olmayan dedektör (new_template,
	// rare_error_code): seri/tepe/onset kuralları uygulanmaz (audit §9 S19).
	SeriesKindOccurrence = "occurrence"
)

// Doğrulayıcı sınırları (audit §9 "Snapshot boyutu ve sınır"). Kod sabiti (K2).
const (
	recordSnapshotStepSec = 60
	recordMaxPoints       = 720
	recordMaxBytes        = 64 << 10
)

// RecordSubject — özne: entity kimliği; çözülemezse ad + Resolved=false.
type RecordSubject struct {
	EntityID  string `json:"entityId,omitempty"`
	Service   string `json:"service"`
	Operation string `json:"operation,omitempty"`
	Pod       string `json:"pod,omitempty"`
	Resolved  bool   `json:"resolved"`
}

// RecordSeries — dedektörün KOŞTUĞU seri; render'da yeniden kurulmaz.
type RecordSeries struct {
	Source      string `json:"source"` // vm | thanos:<cluster> | ch:<mv> | es
	Query       string `json:"query"`  // çözülmüş ifade
	Aggregation string `json:"aggregation"`
	Unit        string `json:"unit"`
	StepSec     uint32 `json:"stepSec"`
	Kind        string `json:"kind,omitempty"` // '' = sayısal | occurrence
}

// RecordBaseline — alarmı veren bandın kendisi.
type RecordBaseline struct {
	Expected float64 `json:"expected"`
	Lo       float64 `json:"lo"`
	Hi       float64 `json:"hi"`
	Method   string  `json:"method"`
	StepSec  uint32  `json:"stepSec"`
}

// RecordEvidence — kanıt satırı; kaynak + ölçü seriyle aynıysa bağımsız değil.
type RecordEvidence struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Summary     string `json:"summary"`
	Link        string `json:"link,omitempty"`
	Independent bool   `json:"independent"`
}

// RecordGate — yayın kapısı satırı (A1 doldurur).
type RecordGate struct {
	Name      string  `json:"name"`
	Passed    bool    `json:"passed"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Reason    string  `json:"reason,omitempty"`
	// NotEvaluable — v0.10.2241 (A1): kayıtta kapının gerektirdiği veri yok
	// (Passed=true, neden Reason'da); kapı sorgu atmaz.
	NotEvaluable bool `json:"notEvaluable,omitempty"`
}

// RecordSnapshot — sütunlu görüntü: nokta i = T0 + i·StepSec (unix sn, kova
// başı). Lo/Hi/N boş ya da V ile aynı uzunlukta.
type RecordSnapshot struct {
	T0          int64     `json:"t0"`
	StepSec     uint32    `json:"step"`
	Aggregation string    `json:"aggregation"`
	V           []float64 `json:"v"`
	Lo          []float64 `json:"lo,omitempty"`
	Hi          []float64 `json:"hi,omitempty"`
	N           []float64 `json:"n,omitempty"`
}

// EventRecord — tek olay-kaydı sözleşmesi (audit §9 alan tablosu). Zamanlar
// unix ns; EndedAt 0 = sürüyor.
type EventRecord struct {
	V          int            `json:"v"`
	Subject    RecordSubject  `json:"subject"`
	Series     RecordSeries   `json:"series"`
	Baseline   RecordBaseline `json:"baseline"`
	Direction  string         `json:"direction"` // up | down
	OnsetAt    int64          `json:"onsetAt"`
	DetectedAt int64          `json:"detectedAt"`
	EndedAt    int64          `json:"endedAt,omitempty"`
	// EndReason — v0.10.2359: "" = banda döndü; no_data = veri gelmedi, izlenemedi.
	EndReason        string           `json:"endReason,omitempty"`
	PeakValue        float64          `json:"peakValue"`
	ValueAtDetection float64          `json:"valueAtDetection"`
	ExpectedValue    float64          `json:"expectedValue"`
	Volume           *float64         `json:"volume"`
	VolumeReason     string           `json:"volumeReason,omitempty"`
	Evidence         []RecordEvidence `json:"evidence"`
	Class            string           `json:"class"`
	Gates            []RecordGate     `json:"gates,omitempty"`
	Snapshot         RecordSnapshot   `json:"snapshot"`
	// Context — v0.10.2242 (Dilim B): aynı penceredeki yan seriler (hacim
	// kaydı: hata oranı + gecikme). Snapshot ızgarasıyla aynı t0/adım/uzunluk;
	// A kapıları servis etkisini buradan okur. Boş = yok (diğer dedektörler).
	Context []RecordContext `json:"context,omitempty"`
	Title   string          `json:"title"`
}

// RecordContext — yan seri: ölçü (error_rate | latency), tanımı ve bantlı
// görüntüsü. Bant olay öncesi referanstan (Method), alarm bandı değil.
type RecordContext struct {
	Measure  string         `json:"measure"`
	Source   string         `json:"source"`
	Query    string         `json:"query"`
	Unit     string         `json:"unit"`
	Method   string         `json:"method"`
	Snapshot RecordSnapshot `json:"snapshot"`
}

// DetectionDelay — detected − onset (türetilir, saklanmaz).
func (r EventRecord) DetectionDelay() time.Duration {
	return time.Duration(r.DetectedAt - r.OnsetAt)
}

// RecordViolation — doğrulayıcının bir kural ihlali (Rule sabit ad, Reason insan metni).
type RecordViolation struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

// Kural adları — test ve sayaç bu adlarla konuşur.
const (
	RuleVersion        = "version"
	RuleSeriesDefined  = "series_defined"
	RuleOnsetOrder     = "onset_order"
	RuleSnapshotShape  = "snapshot_shape"
	RuleSnapshotAgg    = "snapshot_aggregation"
	RulePeakFromSeries = "peak_from_series"
	RuleDetectionValue = "detection_value_from_series"
	RuleOnsetFromSerie = "onset_from_series"
	RuleBandMatches    = "band_matches_alarm"
	RuleVolume         = "volume"
	RuleTitleNumbers   = "title_numbers"
	RuleEvidenceTitle  = "evidence_repeats_title"
	RuleClass          = "class"
	RuleSize           = "size"
	RuleContextShape   = "context_shape" // v0.10.2242: yan seri ızgarası ana snapshot'la aynı
)

// ValidateRecord — SAF yazım-öncesi doğrulayıcı. Boş dönüş = geçti.
// Kurallar (audit §9 "Doğrulayıcı"): tepe = olay penceresinde serinin
// uç değeri; onset ≤ detected; snapshot toplaması = seri toplaması; başlıktaki
// sayılar alanlarla aynı; kanıtta başlık tekrarı yok. Ek (§11 beklenen
// sonuçlar): seri tanımı, onset sapmanın başı, bant = alarm bandı, hacim ya
// da nedeni, ≤ 1 dk adım.
func ValidateRecord(r EventRecord) []RecordViolation {
	var out []RecordViolation
	add := func(rule, f string, a ...any) { out = append(out, RecordViolation{rule, fmt.Sprintf(f, a...)}) }
	if r.V != RecordVersion {
		add(RuleVersion, "sürüm %d, beklenen %d", r.V, RecordVersion)
	}
	switch r.Class {
	case ClassServiceAnomaly, ClassTelemetryVolume, ClassInfo:
	default:
		add(RuleClass, "sınıf %q tanımsız", r.Class)
	}
	s := r.Series
	if s.Source == "" || s.Query == "" || s.Aggregation == "" || s.Unit == "" || s.StepSec == 0 {
		add(RuleSeriesDefined, "test edilen seri tanımı eksik (kaynak/sorgu/toplama/birim/adım)")
	}
	switch {
	case r.OnsetAt <= 0 || r.DetectedAt <= 0:
		add(RuleOnsetOrder, "onset_at / detected_at yok")
	case r.OnsetAt > r.DetectedAt:
		add(RuleOnsetOrder, "onset_at detected_at'ten sonra (%s)", -r.DetectionDelay())
	case r.EndedAt != 0 && r.EndedAt < r.DetectedAt:
		add(RuleOnsetOrder, "ended_at detected_at'ten önce")
	}
	if r.Volume == nil && r.VolumeReason == "" {
		add(RuleVolume, "hacim yok ve nedeni yazılmamış")
	}
	if s.Kind != SeriesKindOccurrence {
		out = append(out, validateSnapshot(r)...)
	}
	for _, c := range r.Context {
		cs, sn := c.Snapshot, r.Snapshot
		if cs.T0 != sn.T0 || cs.StepSec != sn.StepSec || len(cs.V) != len(sn.V) || len(cs.Lo) != len(cs.V) || len(cs.Hi) != len(cs.V) || c.Measure == "" || c.Query == "" {
			add(RuleContextShape, "yan seri %q ızgarası/bandı ana snapshot'la uyuşmuyor", c.Measure)
		}
	}
	if bad := titleNumbersOutsideFields(r); len(bad) > 0 {
		add(RuleTitleNumbers, "başlıktaki sayı(lar) hiçbir alanla eşleşmiyor: %s", strings.Join(bad, ", "))
	}
	if t := normText(r.Title); t != "" {
		for _, e := range r.Evidence {
			if normText(e.Summary) == t {
				add(RuleEvidenceTitle, "kanıt satırı başlığın tekrarı (%s)", e.Kind)
				break
			}
		}
	}
	if b, err := json.Marshal(r); err == nil && len(b) > recordMaxBytes {
		add(RuleSize, "kayıt %d bayt > %d", len(b), recordMaxBytes)
	}
	return out
}

// validateSnapshot — seri kuralları: biçim, toplama, tepe, tespit değeri,
// onset sapmanın başı, bant.
func validateSnapshot(r EventRecord) []RecordViolation {
	var out []RecordViolation
	add := func(rule, f string, a ...any) { out = append(out, RecordViolation{rule, fmt.Sprintf(f, a...)}) }
	sn := r.Snapshot
	n := len(sn.V)
	if n == 0 || sn.StepSec == 0 {
		add(RuleSnapshotShape, "snapshot serisi yok")
		add(RulePeakFromSeries, "tepe seriden türetilemiyor (seri yok)")
		add(RuleOnsetFromSerie, "onset seriden türetilemiyor (seri yok)")
		return out
	}
	if (len(sn.Lo) != 0 && len(sn.Lo) != n) || (len(sn.Hi) != 0 && len(sn.Hi) != n) || (len(sn.N) != 0 && len(sn.N) != n) {
		add(RuleSnapshotShape, "snapshot sütun uzunlukları farklı")
		return out
	}
	if n > recordMaxPoints {
		add(RuleSnapshotShape, "snapshot %d nokta > %d", n, recordMaxPoints)
	}
	// Adım 60 sn; yalnız 720 nokta tavanı aşılacaksa 60'ın katına büyür.
	if sn.StepSec != recordSnapshotStepSec &&
		(sn.StepSec%recordSnapshotStepSec != 0 || int64(n)*int64(sn.StepSec/recordSnapshotStepSec) <= recordMaxPoints) {
		add(RuleSnapshotShape, "snapshot adımı %d sn (≤ 1 dk olmalı)", sn.StepSec)
	}
	if sn.Aggregation != r.Series.Aggregation {
		add(RuleSnapshotAgg, "snapshot toplaması %q ≠ seri toplaması %q", sn.Aggregation, r.Series.Aggregation)
	}
	if r.OnsetAt <= 0 || r.DetectedAt <= 0 || r.OnsetAt > r.DetectedAt {
		return out // pencere tanımsız; onset_order zaten raporladı
	}
	idx := func(ns int64) int { return int(math.Floor(float64(ns/1e9-sn.T0) / float64(sn.StepSec))) }
	from, det := idx(r.OnsetAt), idx(r.DetectedAt)
	to := n - 1
	if r.EndedAt > 0 {
		to = idx(r.EndedAt)
	}
	if from < 0 || det >= n || det < 0 || to >= n || from > to {
		add(RulePeakFromSeries, "olay penceresi snapshot dışında")
		return out
	}
	ext := sn.V[from]
	for _, v := range sn.V[from : to+1] {
		if (r.Direction == "down" && v < ext) || (r.Direction != "down" && v > ext) {
			ext = v
		}
	}
	if !sameValue(r.PeakValue, ext) {
		add(RulePeakFromSeries, "tepe %s ≠ olay penceresinde serinin uç değeri %s", fmtNum(r.PeakValue), fmtNum(ext))
	}
	if !sameValue(r.ValueAtDetection, sn.V[det]) {
		add(RuleDetectionValue, "tespit anı değeri %s ≠ seri %s", fmtNum(r.ValueAtDetection), fmtNum(sn.V[det]))
	}
	if len(sn.Lo) == n && len(sn.Hi) == n {
		out = append(out, onsetAndBand(r, from, det)...)
	} else {
		add(RuleBandMatches, "snapshot bandı yok")
	}
	return out
}

// onsetAndBand — onset noktası bant dışında, bir önceki nokta bant içinde
// (sapmanın BAŞI); tespit anındaki snapshot bandı = alarm bandı.
func onsetAndBand(r EventRecord, from, det int) []RecordViolation {
	var out []RecordViolation
	sn := r.Snapshot
	outside := func(i int) bool { return sn.V[i] < sn.Lo[i] || sn.V[i] > sn.Hi[i] }
	switch {
	case !outside(from):
		out = append(out, RecordViolation{RuleOnsetFromSerie, "onset noktası bant içinde"})
	case from == 0:
		out = append(out, RecordViolation{RuleOnsetFromSerie, "onset snapshot'ın ilk noktası; öncesi görünmüyor"})
	case outside(from - 1):
		out = append(out, RecordViolation{RuleOnsetFromSerie, fmt.Sprintf("onset'ten önceki nokta (%s) de bant dışında — sapmanın başı değil", fmtNum(sn.V[from-1]))})
	}
	if !sameValue(sn.Lo[det], r.Baseline.Lo) || !sameValue(sn.Hi[det], r.Baseline.Hi) {
		out = append(out, RecordViolation{RuleBandMatches, fmt.Sprintf("snapshot bandı [%s, %s] ≠ alarm bandı [%s, %s] (%s)",
			fmtNum(sn.Lo[det]), fmtNum(sn.Hi[det]), fmtNum(r.Baseline.Lo), fmtNum(r.Baseline.Hi), r.Baseline.Method)})
	}
	return out
}

func sameValue(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func normText(s string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(s), " ")), ".")
}

var (
	titleTimeRe = regexp.MustCompile(`\b\d{1,2}:\d{2}\b|\b\d{1,2}\.\d{1,2}\.\d{2,4}\b`)
	titleNumRe  = regexp.MustCompile(`\d+(?:[.,]\d+)*`)
	// süre sözcükleri — "son 24 sa", "30 dk" pencere/süre, ölçü değil.
	titleDurRe = regexp.MustCompile(`^\s?(dakika\p{L}*|saat\p{L}*|gün\p{L}*|dk|sa)(\P{L}|$)`)
)

// titleNumbersOutsideFields — SAF: başlıktaki her ölçü sayısı (TR biçimi:
// "." binlik, "," ondalık) bir alanla eşleşmeli: tepe, tespit anı, beklenen,
// alarm bandı, hacim. Saat/tarih, süre ("24 sa") ve harfe yapışık sayılar
// ("p95", "v2") atlanır. Gösterim yuvarlaması kadar tolerans.
func titleNumbersOutsideFields(r EventRecord) []string {
	fields := []float64{r.PeakValue, r.ValueAtDetection, r.ExpectedValue, r.Baseline.Lo, r.Baseline.Hi}
	if r.Volume != nil {
		fields = append(fields, *r.Volume)
	}
	title := titleTimeRe.ReplaceAllString(r.Title, " ")
	var bad []string
	for _, loc := range titleNumRe.FindAllStringIndex(title, -1) {
		if loc[0] > 0 && isLetter(title[loc[0]-1]) {
			continue
		}
		if titleDurRe.MatchString(title[loc[1]:]) {
			continue
		}
		tok := title[loc[0]:loc[1]]
		v, dec, ok := parseTRNumber(tok)
		if !ok {
			bad = append(bad, tok)
			continue
		}
		tol := 0.5 * math.Pow(10, -float64(dec))
		hit := false
		for _, f := range fields {
			if math.Abs(f-v) <= tol+1e-9 {
				hit = true
				break
			}
		}
		if !hit {
			bad = append(bad, tok)
		}
	}
	return bad
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80 }

// parseTRNumber — "74.167" → 74167; "4,2" → 4.2 (1 ondalık); "1.234,5" → 1234.5.
// Yalnız nokta varsa ve her grup 3 haneyse binlik; değilse ondalık.
func parseTRNumber(tok string) (v float64, decimals int, ok bool) {
	intPart, frac := tok, ""
	if i := strings.LastIndexByte(tok, ','); i >= 0 {
		intPart, frac = tok[:i], tok[i+1:]
	}
	if strings.Contains(intPart, ".") {
		parts := strings.Split(intPart, ".")
		thousands := len(parts[0]) <= 3
		for _, p := range parts[1:] {
			thousands = thousands && len(p) == 3
		}
		if !thousands {
			if frac != "" || len(parts) != 2 {
				return 0, 0, false
			}
			intPart, frac = parts[0], parts[1]
		} else {
			intPart = strings.Join(parts, "")
		}
	}
	s := intPart
	if frac != "" {
		s += "." + frac
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, len(frac), err == nil
}

// applyRecordContract — yazım öncesi: kaydı olan olayı doğrular; kalan
// kayıt publish_state=invalid (yayımlanmaz) ve dedektör sayacına girer.
// Kaydı olmayan (eski) olay aynen döner — H1'de her olay böyle.
//
// v0.10.2241 (A1): geçen kayıt yayın kapılarından geçirilir (publish_gate.go);
// sonuç YALNIZ record.gates[]'e ve gölge sayacına yazılır — sınıf,
// publish_state, bildirim, Problem ve kart değişmez. Kaydı olmayan / kalan
// olay gölgede "değerlendirilmedi" sayılır.
func applyRecordContract(ev chstore.MLEvent) chstore.MLEvent {
	if ev.Record == "" {
		global.observeGate(ev.ID, ev.Detector, ev.Service, gateOutcome{})
		return ev
	}
	rec, vs, o := contractVerdict(ev.Record)
	if len(vs) == 0 {
		if b, err := json.Marshal(rec); err == nil {
			ev.Record = string(b)
		}
		if publishGatesEnforce { // v0.10.2343 (A2, kapalı gelir): karar satıra
			ev = enforceVerdict(ev, rec.Class, o)
		}
		global.observeGate(ev.ID, ev.Detector, ev.Service, o)
		return ev
	}
	ev.PublishState = PublishInvalid
	global.observeGate(ev.ID, ev.Detector, ev.Service, gateOutcome{Invalid: true})
	global.observeRecordInvalid(ev.Detector)
	log.Printf("[ml] record invalid %s %s: %s — %s (+%d kural)", ev.Detector, ev.ID, vs[0].Rule, vs[0].Reason, len(vs)-1)
	return ev
}

// contractVerdict — SAF (v0.10.2252, Dilim E): kayıt JSON'unun sözleşme +
// gölge kapı sonucu, yan etkisiz. applyRecordContract (yazım yolu) ve replay
// (replay.go, geliştirici aracı) AYNI kararı buradan alır. raw == "" → kayıt
// yok (değerlendirilmedi); çözülemeyen ya da doğrulayıcıdan kalan kayıt
// Invalid; geçen kaydın Gates'i doldurulur.
func contractVerdict(raw string) (EventRecord, []RecordViolation, gateOutcome) {
	var rec EventRecord
	if raw == "" {
		return rec, nil, gateOutcome{}
	}
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return rec, []RecordViolation{{"decode", err.Error()}}, gateOutcome{Invalid: true}
	}
	if vs := ValidateRecord(rec); len(vs) > 0 {
		return rec, vs, gateOutcome{Invalid: true}
	}
	rec.Gates = EvaluateGates(rec)
	return rec, nil, gateVerdict(rec, rec.Gates)
}

// recordTitle — v0.10.2215 (H2): yayımlanabilir kaydın başlığı; kayıt yok,
// çözülemiyor ya da doğrulayıcıdan kalmışsa "" (kart eski yola düşer).
func recordTitle(ev chstore.MLEvent) string {
	if ev.Record == "" || ev.PublishState == PublishInvalid {
		return ""
	}
	var rec EventRecord
	if json.Unmarshal([]byte(ev.Record), &rec) != nil {
		return ""
	}
	return rec.Title
}
