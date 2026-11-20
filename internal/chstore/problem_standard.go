package chstore

// problem_standard.go — v0.10.1523 (Problem standardı, dilim R1; operatör onayı
// 2026-10-24, docs/DECISIONS.md "Problem standardı + triage + bildirim
// politikası"). GÖRÜNÜR DEĞİŞİKLİK YOK: bu dosya yalnız SAF bir çekirdek.
//
// Neden: satır metni bugün dedektörün kendi cümlesi (karışık TR/EN, ham metrik
// adları) ve bildirim önceliği ekran önceliğinden ayrı hesaplanıyor. Her
// Problem / exception grubu / anomali olayı / incident TEK bir yapıya
// (ProblemStandard) indirgenir: Ne bozuk (Family/Subject/Operation/Headline),
// Etki (Impact), Ne zamandan beri (Since), Kanıt (Evidence + kaynak), Güven
// (Confidence — YALNIZ kapılardan, signal_gates.go) ve Sonraki adım. R2 satır
// ve başlık UI'sini, R4 bildirim şablonunu bu yapıdan çizecek; R1'de yalnız
// gölge bildirim politikası (notify_policy.go NotifiableAt) ve replay ucu okur.
//
// Neden SAF ve neden StdFacts dışarıdan: aynı işlev bildirim hunisinden
// (CH okuması bütçesiz), replay ucundan (toplu service_summary_5m okuması) ve
// R2'de liste ucundan çağrılacak. Okumayı içeride yapsaydık her çağıran farklı
// gerçeklerle farklı güven üretirdi; burada gerçek eksikse "bilinmiyor" olur
// ve bilinmiyor HİÇBİR ZAMAN güveni düşürmez (signal_gates.go).
//
// Neden açıklamayı ayrıştırmıyoruz (kaynak satırı hariç): dedektör cümleleri
// sürümden sürüme değişiyor; manşet Problem ALANLARINDAN (RuleID öneki,
// Metric, Value, Threshold, Comparator) kurulur. Tek istisna kanıt kaynağı:
// "kaynak: …" işaretleri burada sabit olarak TANIMLANIR ve dedektör
// (anomaly/red_vm_select.go) bunları kullanır — tek tanım, ayrışamaz.

import (
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ── Kanıt kaynağı işaretleri (tek tanım) ────────────────────────────────
//
// v0.10.1478'den beri dedektör açıklamaya "kaynak: …" satırı ekliyor
// (withREDEvidence). Metin buradan gelir; anomaly paketi bu sabitleri
// kullanır, ParseEvidenceSource aynı sabitlerle geri okur.
const (
	// REDEvidenceVM — VictoriaMetrics'in örneklenmemiş sunucu histogramı.
	REDEvidenceVM = "kaynak: VictoriaMetrics (örneklenmemiş)"
	// REDEvidenceSpan — span MV'si (örneklenmiş).
	REDEvidenceSpan = "kaynak: span (örneklenmiş)"
	// REDEvidenceVMHold — VM servisi biliyor ama bu tik karar veremedi.
	REDEvidenceVMHold = "kaynak: VictoriaMetrics — bu tik beklemede: "
	// EvidenceLogs — v0.10.2383: log kayıtlarının sayımı (Elasticsearch
	// agregasyonu; ML log dedektörleri). Kaynak adı mevcut StdSourceLogs.
	EvidenceLogs = "kaynak: log kayıtları"
)

// Kanıt kaynakları (ProblemStandard.Evidence.Source).
const (
	StdSourceVM        = "vm"
	StdSourceSpan      = "span"
	StdSourceDBMV      = "db_mv"
	StdSourceProbe     = "probe"
	StdSourceException = "exception"
	StdSourceOracle    = "oracle"
	StdSourceSelf      = "self"
	StdSourceExternal  = "external"
	StdSourceRule      = "rule"
	// StdSourceLogs — v0.10.2038: log olayı (desen sayımı / Drain kalıbı /
	// Elastic ML); "span · örneklenmiş" diye görünüyordu.
	StdSourceLogs = "logs"
)

// Aileler (ProblemStandard.Family) — replay raporu bu anahtarla gruplar.
const (
	StdFamilyError        = "error"
	StdFamilyLatency      = "latency"
	StdFamilyTraffic      = "traffic"
	StdFamilyAvailability = "availability"
	StdFamilyException    = "exception"
	StdFamilyDB           = "db"
	StdFamilyExternal     = "external"
	StdFamilyRuntime      = "runtime"
	StdFamilySelf         = "self"
	StdFamilyIncident     = "incident"
	StdFamilyCluster      = "cluster"
	StdFamilyRule         = "rule"
)

// StdHeadlineMax — manşet tavanı (rune).
const StdHeadlineMax = 90

// StdImpactUnverified — runtime (GC) sinyalinde kullanıcı etkisi metni.
const StdImpactUnverified = "kullanıcı etkisi doğrulanmadı"

// ProblemStandard — her sinyalin tek biçimi.
type ProblemStandard struct {
	Family     string        `json:"family"`
	Kind       string        `json:"kind"` // ince anahtar: "anomaly.error_rate.ramp", "db_health", …
	Subject    string        `json:"subject"`
	Operation  string        `json:"operation,omitempty"`
	Headline   string        `json:"headline"`
	Impact     StdImpact     `json:"impact"`
	Since      StdSince      `json:"since"`
	Evidence   StdEvidence   `json:"evidence"`
	Confidence StdConfidence `json:"confidence"`
	NextStep   StdNextStep   `json:"nextStep"`
	Context    []string      `json:"context,omitempty"`
	// v0.10.2299 (S2) — metin anahtarları: Türkçe alanların YANINDA (ek alan;
	// TR metin aynen). ContextMsgs, Context ile aynı sıra ve uzunlukta.
	HeadlineMsg *StdMsg   `json:"headlineMsg,omitempty"`
	ContextMsgs []StdMsg  `json:"contextMsgs,omitempty"`
	Notify      StdNotify `json:"notify"`
	// PriorityReason / PriorityReasonMsg — v0.10.2300 (S3): YALNIZ detay ucu
	// (GET /api/problem-standard, problem ref'i) doldurur — Problem'in
	// gerekçe anahtarı JSON'a çıkmadığı için (Problem.PriorityReasonMsg)
	// detay başlığına buradan gider. FE anahtarı yalnız buradaki metin
	// satırdaki gerekçeyle aynıysa kullanır. Standardize* doldurmaz;
	// bildirim / politika / özet bu alanları okumaz.
	PriorityReason    string  `json:"priorityReason,omitempty"`
	PriorityReasonMsg *StdMsg `json:"priorityReasonMsg,omitempty"`
}

// StdMeasure — birincil etki ölçüsü.
type StdMeasure struct {
	Text   string  `json:"text"`
	Value  float64 `json:"value"`
	Normal float64 `json:"normal"`
	Unit   string  `json:"unit,omitempty"`
	Ratio  float64 `json:"ratio,omitempty"` // değer / normal (aşağı yönlü kuralda ters çevrilmiş); 0 = bilinmiyor
	Msg    *StdMsg `json:"msg,omitempty"`   // v0.10.2299 — Text'in anahtarı
}

// StdFloorMeasures — etki tabanı (D6) ölçüleri. nil = ölçülmedi (bilinmiyor
// ≠ küçük; NotifiableAt bilinmeyeni GEÇİRİR).
type StdFloorMeasures struct {
	Calls5m         *float64 `json:"calls5m,omitempty"`
	Failed5m        *float64 `json:"failed5m,omitempty"`
	ErrorPct        *float64 `json:"errorPct,omitempty"`
	P99DeltaMs      *float64 `json:"p99DeltaMs,omitempty"`
	DropPct         *float64 `json:"dropPct,omitempty"`
	BaselineCalls5m *float64 `json:"baselineCalls5m,omitempty"`
}

// StdImpact — etki.
type StdImpact struct {
	Primary      StdMeasure       `json:"primary"`
	FailedPerMin float64          `json:"failedPerMin,omitempty"`
	Callers      []string         `json:"callers,omitempty"` // YALNIZ topolojiyle doğrulanmış çağıranlar
	Known        bool             `json:"known"`
	Floor        StdFloorMeasures `json:"floor"`
	// Requests — v0.10.2380: hata oranı Problem'i / olayında dakikada başarısız
	// ve başarılı istek (request_split.go). nil = sayılar bilinmiyor; Primary
	// bugünkü metniyle kalır.
	Requests *StdRequestSplit `json:"requests,omitempty"`
}

// StdSince — ne zamandan beri.
type StdSince struct {
	StartedAt   int64  `json:"startedAt"`
	LastSeen    int64  `json:"lastSeen,omitempty"`
	DurationSec int64  `json:"durationSec"`
	Ongoing     bool   `json:"ongoing"`
	Episode     uint32 `json:"episode,omitempty"`
}

// StdEvidence — neden.
type StdEvidence struct {
	Sentence string  `json:"sentence"`
	Msg      *StdMsg `json:"msg,omitempty"` // v0.10.2299 — Sentence'ın anahtarı
	Source   string  `json:"source"`
	Sampled  bool    `json:"sampled"`
	// Reasons / ReasonMsgs — v0.10.2383: "Neden böyle değerlendiriyoruz"
	// satırları (etki kuralı olayının gerçekleri; problem_standard_ml.go) ve
	// aynı sıra / uzunlukta metin anahtarları. Yalnız çağıran gerçekleri
	// verdiyse (StdFacts.ImpactRule — detay ucu) dolar; boşsa Sentence çizilir.
	// Sentence her zaman dolu kalır (bildirim, MCP ve liste onu okur).
	Reasons    []string `json:"reasons,omitempty"`
	ReasonMsgs []StdMsg `json:"reasonMsgs,omitempty"`
}

// StdConfidence — güven; YALNIZ kapılardan (signal_gates.go).
type StdConfidence struct {
	Level string       `json:"level"` // high | medium | low
	Gates []SignalGate `json:"gates"`
	// PolicyLevel — v0.10.2383: bildirim politikasının okuduğu seviye; boş =
	// Level. Yalnız ML canlı yolunun kaynak işareti ekrandaki seviyeyi
	// değiştirdiğinde dolar ve işaretten ÖNCEKİ seviyeyi taşır: doğru kaynak
	// ekranda (ve bildirim metninde) görünür, ama "yüksek güvenli P1" baypası
	// (D3) canlı gönderim kararlarını operatör onayı olmadan değiştirmez
	// (problem_standard_ml.go policyLevel; pin TestMLMarkerNotifyPolicyUnchanged).
	// JSON'a çıkmaz.
	PolicyLevel string `json:"-"`
}

// StdNextStep — sonraki adım.
type StdNextStep struct {
	Text string  `json:"text"`
	Href string  `json:"href,omitempty"`
	Msg  *StdMsg `json:"msg,omitempty"` // v0.10.2299 — Text'in anahtarı
}

// StdNotify — NotifiableAt sonucu (Standardize* doldurmaz).
type StdNotify struct {
	Notifiable bool   `json:"notifiable"`
	Hold       string `json:"hold,omitempty"`
}

// StdTraffic — öznenin son tamamlanmış 5 dk kovası (service_summary_5m) ve
// referans kovası (bir saat önce). Çağıran okur; nil = bilinmiyor.
type StdTraffic struct {
	Calls5m         float64
	Errors5m        float64
	P99Ms           float64
	BaselineCalls5m float64
	BaselineP99Ms   float64
}

// StdFacts — çağıranın sağladığı gerçekler. Sıfır değer = hiçbir şey
// bilinmiyor (her kapı "unknown" ya da "na").
type StdFacts struct {
	Now     time.Time
	Traffic *StdTraffic
	// VerifiedCallers — topology_edges_5m ile doğrulanmış çağıranlar.
	VerifiedCallers []string
	// RelationClaimed / RelationVerified — sinyal bir ilişki iddia ediyorsa
	// (küme, paylaşılan bağımlılık, incident bağı) G4. nil = doğrulanamadı.
	RelationClaimed  bool
	RelationVerified *bool
	RecentDeploy     string // G5 — yalnız bağlam
	CalendarKind     string // G6 — CalendarHoliday / CalendarHalfDay / CalendarMonthEnd
	Batch            bool   // G7
	// SameServiceErrOrP99Open — runtime (GC) sinyalinde kullanıcı etkisi:
	// aynı serviste açık error_rate / p99 problemi var mı.
	SameServiceErrOrP99Open bool
	LastSeen                int64
	BigBreachRatio          float64 // 0 = CurrentProblemPriority()
	IncidentProblems        int
	// ImpactRule — v0.10.2383: Problem'in en yeni uygun ML olayının etki kuralı
	// gerçekleri ("Neden" satırları). nil = bilinmiyor → bugünkü tek cümle.
	// Yalnız detay ucu doldurur (liste / bildirim yolunda ek okuma yok).
	ImpactRule *StdImpactRule
}

func (f StdFacts) now() time.Time {
	if f.Now.IsZero() {
		return time.Now()
	}
	return f.Now
}

func (f StdFacts) bigBreach() float64 {
	if f.BigBreachRatio > 0 {
		return f.BigBreachRatio
	}
	return CurrentProblemPriority().BigBreachRatio
}

// ParseEvidenceSource — SAF: açıklamadaki "kaynak: …" işaretinden kaynak.
// "" = işaret yok. hold = VM bu tik karar vermedi.
func ParseEvidenceSource(desc string) (source string, hold bool) {
	switch {
	case strings.Contains(desc, REDEvidenceVMHold):
		return StdSourceVM, true
	case strings.Contains(desc, REDEvidenceVM):
		return StdSourceVM, false
	case strings.Contains(desc, REDEvidenceSpan):
		return StdSourceSpan, false
	case strings.Contains(desc, EvidenceLogs):
		return StdSourceLogs, false
	}
	return "", false
}

// StripEvidenceSource — SAF (v0.10.2383): açıklamadan ML canlı yolunun
// eklediği kaynak cümlesini (" <işaret>.") atar. ML Problem'inin manşeti
// açıklamanın kendisidir; kaynak başlık kartında söylenir, manşette değil.
func StripEvidenceSource(desc string) string {
	for _, m := range []string{REDEvidenceVM, EvidenceLogs} {
		desc = strings.Replace(desc, " "+m+".", "", 1)
	}
	return desc
}

// stdSampled — SAF: kaynak örneklenmiş mi (D12: örneklenmiş span-yalnız
// sinyal en çok "medium").
func stdSampled(source string) bool {
	switch source {
	case StdSourceSpan, StdSourceDBMV, StdSourceException, StdSourceRule:
		return true
	}
	return false
}

// stdKindInfo — RuleID önekinin sınıflaması.
type stdKindInfo struct {
	family, kind, subject, operation, source string
	// dwell: dedektör sürdürmeyi kendisi istiyor mu ("pass"), tasarım gereği
	// tek kova mı ("na"), bilinmiyor mu ("unknown").
	dwell string
	// traffic: G1 bu türe uygulanır mı.
	traffic bool
	// vmDecides: G3 bu türe uygulanır mı (RED metrikleri).
	vmDecides bool
	relation  bool
	unit      string
}

var rampDescRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?) hata/sn`)
var exOccurDescRe = regexp.MustCompile(`— ([0-9]+) occurrence`)

func classifyProblem(p Problem) stdKindInfo {
	id := p.RuleID
	k := stdKindInfo{subject: p.Service, dwell: "unknown", source: StdSourceRule}
	switch {
	case p.Kind == NotifyKindIncident || strings.HasPrefix(id, "incident:"):
		k.family, k.kind, k.dwell, k.source = StdFamilyIncident, "incident", "na", StdSourceRule
		k.relation = true
	case id == "exception-storm":
		k.family, k.kind, k.dwell, k.source = StdFamilyException, "exception_storm", "pass", StdSourceException
		k.subject = ""
	case strings.HasPrefix(id, "exception:shared-dependency"):
		k.family, k.kind, k.dwell, k.source = StdFamilyException, "exception_shared", "pass", StdSourceException
		k.relation = true
	case strings.HasPrefix(id, "exception:fatal-infrastructure"):
		k.family, k.kind, k.dwell, k.source = StdFamilyException, "exception_fatal", "na", StdSourceException
	case strings.HasPrefix(id, ExceptionGroupRulePrefix):
		k.family, k.kind, k.dwell, k.source = StdFamilyException, "exception_group", "na", StdSourceException
		// Kimlik "exception-group:<fp>[:durum…]" (notify exceptionGroupID).
		if fp := strings.TrimPrefix(p.ID, ExceptionGroupRulePrefix); IsOracleGroup(fp) {
			k.kind, k.source = "exception_group.oracle", StdSourceOracle
		} else if strings.HasPrefix(p.RuleName, "HTTP hatası · ") {
			k.kind = "exception_group.http"
		}
	case strings.HasPrefix(id, RuleExtDownPrefix):
		k.family, k.kind, k.dwell, k.source = StdFamilyExternal, "anomaly.ext_down", "pass", StdSourceExternal
		k.subject = strings.TrimPrefix(id, RuleExtDownPrefix)
	case strings.HasPrefix(id, RuleExtCapPrefix):
		k.family, k.kind, k.dwell, k.source = StdFamilyExternal, "anomaly.ext_cap", "pass", StdSourceExternal
	case strings.HasPrefix(id, RuleExtClusterPrefix):
		k.family, k.kind, k.dwell, k.source = StdFamilyExternal, "anomaly_cluster.ext", "pass", StdSourceExternal
		k.relation = true
	case strings.HasPrefix(id, RuleExtSeriesPrefix):
		k.family, k.kind, k.dwell, k.source = StdFamilyExternal, "anomaly.ext", "pass", StdSourceExternal
	case strings.HasPrefix(id, "anomaly-cluster:"):
		k.family, k.kind, k.dwell, k.source = StdFamilyCluster, "anomaly_cluster", "pass", StdSourceSpan
		k.relation = true
	case strings.HasPrefix(id, "anomaly-auto:"):
		k.family, k.kind, k.dwell, k.source = familyOfMetric(p.Metric), "anomaly_auto", "pass", StdSourceSpan
	case strings.HasPrefix(id, "anomaly:"):
		k.dwell, k.source = "pass", StdSourceSpan
		switch p.Metric {
		case "error_rate":
			k.family, k.kind, k.traffic, k.vmDecides, k.unit = StdFamilyError, "anomaly.error_rate", true, true, "%"
			if strings.HasPrefix(p.Description, "Teknik hata (5xx)") {
				k.kind = "anomaly.error_rate.ramp"
			}
		case "p99_ms":
			k.family, k.kind, k.traffic, k.vmDecides, k.unit = StdFamilyLatency, "anomaly.p99_ms", true, true, "ms"
		case "request_rate":
			k.family, k.kind, k.traffic, k.vmDecides, k.unit = StdFamilyTraffic, "anomaly.request_rate", true, true, "/sn"
		case "service_silent":
			k.family, k.kind, k.traffic = StdFamilyAvailability, "anomaly.service_silent", true
		default:
			k.family, k.kind = familyOfMetric(p.Metric), "anomaly.other"
		}
	case strings.HasPrefix(id, RuleSvcSlowdownPrefix):
		k.family, k.kind, k.dwell, k.source, k.traffic, k.unit = StdFamilyLatency, "svc_slowdown", "na", StdSourceSpan, true, "ms"
		if strings.Contains(p.RuleName, "trafik çöküşü") {
			k.family, k.kind = StdFamilyTraffic, "svc_slowdown.collapse"
		}
	case strings.HasPrefix(id, RuleDBHealthPrefix):
		k.family, k.kind, k.dwell, k.source = StdFamilyDB, "db_health", "pass", StdSourceDBMV
		if sys, inst, db, ok := ParseDBHealthRuleID(id); ok {
			k.subject = strings.ToLower(sys) + "@" + inst + "/" + db
		}
	case strings.HasPrefix(id, RuleDBCapacityPrefix):
		k.family, k.kind, k.dwell, k.source, k.unit = StdFamilyDB, "db_capacity", "na", StdSourceExternal, "%"
	case id == "db-slow-stmt":
		k.family, k.kind, k.dwell, k.source, k.unit = StdFamilyDB, "db_slow_stmt", "na", StdSourceDBMV, "ms"
	case strings.HasPrefix(id, "runtime:jvm-gc"):
		k.family, k.kind, k.dwell, k.source = StdFamilyRuntime, "runtime.gc", "pass", StdSourceVM
		if p.Metric == "runtime.jvm_gc" {
			k.unit = "ms"
		} else {
			k.unit = "%"
		}
	case strings.HasPrefix(id, "monitor:"):
		k.family, k.kind, k.dwell, k.source = StdFamilyAvailability, "monitor", "na", StdSourceProbe
	case strings.HasPrefix(id, "self-"):
		k.family, k.kind, k.dwell, k.source = StdFamilySelf, "self", "na", StdSourceSelf
	case strings.HasPrefix(id, "slo:"):
		k.family, k.kind, k.dwell, k.source = StdFamilyRule, "slo", "pass", StdSourceSpan
	case strings.HasPrefix(id, "builtin-"):
		k.family, k.kind = familyOfRuleMetric(p.Metric), "rule.builtin"
		k.traffic = k.family == StdFamilyError || k.family == StdFamilyLatency
	default:
		k.family, k.kind = familyOfRuleMetric(p.Metric), "rule.user"
		k.traffic = k.family == StdFamilyError || k.family == StdFamilyLatency
	}
	if ProblemSubjectKind(p.Kind) == ProblemKindDB && k.family != StdFamilyDB && k.kind != "incident" {
		k.subject = p.Service
	}
	return k
}

// familyOfMetric — SAF: anomali / terfi satırının metriğinden aile.
func familyOfMetric(m string) string {
	switch {
	case m == "error_rate" || strings.Contains(m, "error"):
		return StdFamilyError
	case strings.HasSuffix(m, "_ms") || strings.Contains(m, "latency"):
		return StdFamilyLatency
	case m == "request_rate" || strings.Contains(m, "rate"):
		return StdFamilyTraffic
	}
	return StdFamilyRule
}

// familyOfRuleMetric — SAF: alarm kuralının metriğinden aile (bilinmeyen =
// rule; kural ailesi anlamlı bir etki tabanı taşımaz).
func familyOfRuleMetric(m string) string {
	switch {
	case strings.Contains(m, "error"):
		return StdFamilyError
	case strings.Contains(m, "p99") || strings.Contains(m, "p95") || strings.Contains(m, "latency") || strings.HasSuffix(m, "_ms"):
		return StdFamilyLatency
	}
	return StdFamilyRule
}

// stdBreachRatio — SAF: computePriority'nin oran hesabının ikizi (aşağı
// yönlü kuralda ters çevrilir, tam kayıp = +Inf). 0 = hesaplanamaz.
func stdBreachRatio(p Problem) float64 {
	if p.Value == 0 && p.Threshold > 0 {
		return math.Inf(1)
	}
	if p.Threshold == 0 {
		return 0
	}
	r := p.Value / p.Threshold
	if isBelowRule(p.Comparator) && r > 0 && r < 1 {
		r = 1 / r
	}
	if r < 0 || math.IsNaN(r) {
		return 0
	}
	return r
}

// ── Türkçe sayı biçimi ──────────────────────────────────────────────────

// trNum — SAF: ondalık virgüllü sayı; büyüklüğe göre hassasiyet.
func trNum(v float64) string {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return "—"
	}
	a := math.Abs(v)
	prec := 2
	switch {
	case a >= 100:
		prec = 0
	case a >= 10:
		prec = 1
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return strings.Replace(s, ".", ",", 1)
}

// trPct — "%3,2".
func trPct(v float64) string { return "%" + trNum(v) }

// trMs — "300 ms" / "1,2 sn".
func trMs(v float64) string {
	if v >= 1000 {
		return trNum(v/1000) + " sn"
	}
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64) + " ms"
}

// trUnit — değer + birim.
func trUnit(v float64, unit string) string {
	switch unit {
	case "%":
		return trPct(v)
	case "ms":
		return trMs(v)
	case "/sn":
		return trNum(v) + "/sn"
	}
	return trNum(v)
}

// trRatio — "8×".
func trRatio(r float64) string {
	if math.IsInf(r, 1) {
		return "tam kayıp"
	}
	return trNum(r) + "×"
}

// clipHeadline — SAF: rune tavanı, "…" ile.
func clipHeadline(s string) string {
	if utf8.RuneCountInString(s) <= StdHeadlineMax {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:StdHeadlineMax-1])) + "…"
}

func subjectPrefix(subject string) string {
	if subject == "" {
		return ""
	}
	return subject + ": "
}

func f64(v float64) *float64 { return &v }

// ── StandardizeProblem ──────────────────────────────────────────────────

// StandardizeProblem — SAF: Problem → standart. Notify alanı boş kalır
// (NotifiableAt doldurur).
func StandardizeProblem(p Problem, f StdFacts) ProblemStandard {
	k := classifyProblem(p)
	now := f.now()
	src, hold := ParseEvidenceSource(p.Description)
	// v0.10.2383 — ML canlı yolu (servis / grup satırı) lider olayın gerçek
	// kaynağını işaretler. Yalnız o iki aile: açıklamasını başka bir yazarın
	// kurduğu satırda işaretin bugünkü etkisi aynen kalır.
	mlMarked := src != "" && IsMLLiveProblem(p.RuleID)
	if src == "" {
		src = k.source
	}
	// G3 (VM karar) RED ölçüsüne uygulanır: işaret VM diyorsa ML servis
	// Problem'inde de ölçülmüş bir gerçektir (işaretsiz eski satırda aynen "na").
	vmDecides := k.vmDecides || (mlMarked && src == StdSourceVM && isREDFamily(k.family))
	ratio := stdBreachRatio(p)
	if k.family == StdFamilyException && p.Priority == "P1" {
		ratio = math.Inf(1) // merdiven P1'i = büyük ihlal (oran alanı yok)
	}
	std := ProblemStandard{
		Family:    k.family,
		Kind:      k.kind,
		Subject:   k.subject,
		Operation: k.operation,
	}
	std.Impact = problemImpact(p, k, f, ratio)
	head, hm := problemHeadline(p, k, std.Impact)
	std.Headline, std.HeadlineMsg = clipHeadline(head), hm
	std.Since = problemSince(p, f, now)
	sent, sm := problemEvidenceSentence(p, k, src, hold)
	std.Evidence = StdEvidence{Sentence: sent, Source: src, Sampled: stdSampled(src), Msg: sm}
	if f.ImpactRule != nil && k.kind == "anomaly_auto" {
		std.Evidence.Reasons, std.Evidence.ReasonMsgs = impactRuleReasons(*f.ImpactRule, src == StdSourceVM)
	}
	gin := GateInput{
		Family:           k.family,
		Traffic:          k.traffic,
		VMDecides:        vmDecides,
		Dwell:            k.dwell,
		Source:           src,
		VMHold:           hold,
		Floor:            std.Impact.Floor,
		TrafficKnown:     f.Traffic != nil,
		RelationClaimed:  k.relation || f.RelationClaimed,
		RelationVerified: f.RelationVerified,
		RecentDeploy:     f.RecentDeploy,
		CalendarKind:     f.CalendarKind,
		Batch:            f.Batch,
	}
	gates := ComputeSignalGates(gin)
	cin := ConfidenceInput{
		Sampled:          std.Evidence.Sampled,
		Ratio:            ratio,
		BigBreach:        f.bigBreach(),
		ImpactUnverified: k.family == StdFamilyRuntime && !std.Impact.Known,
	}
	std.Confidence = StdConfidence{Level: ConfidenceFromGates(gates, cin), Gates: gates}
	if mlMarked {
		// Bildirim politikası işaretten önceki seviyeyi okur (PolicyLevel).
		gin.Source, gin.VMDecides, gin.VMHold = k.source, k.vmDecides, false
		cin.Sampled = stdSampled(k.source)
		if old := ConfidenceFromGates(ComputeSignalGates(gin), cin); old != std.Confidence.Level {
			std.Confidence.PolicyLevel = old
		}
	}
	std.NextStep = problemNextStep(p, k)
	std.Context, std.ContextMsgs = stdContext(f, p.EpisodeCount)
	return std
}

func problemSince(p Problem, f StdFacts, now time.Time) StdSince {
	s := StdSince{StartedAt: p.StartedAt, LastSeen: f.LastSeen, Episode: p.EpisodeCount}
	end := now.UnixNano()
	if p.ResolvedAt != nil && *p.ResolvedAt > 0 {
		end = *p.ResolvedAt
	} else {
		s.Ongoing = p.Status != "resolved"
	}
	if p.StartedAt > 0 && end > p.StartedAt {
		s.DurationSec = (end - p.StartedAt) / int64(time.Second)
	}
	return s
}

// problemImpact — SAF: etki + taban ölçüleri.
func problemImpact(p Problem, k stdKindInfo, f StdFacts, ratio float64) StdImpact {
	im := StdImpact{Callers: f.VerifiedCallers}
	r := ratio
	if math.IsInf(r, 1) {
		r = 0
	}
	switch k.kind {
	case "anomaly.error_rate", "anomaly.error_rate.ramp":
		im.Primary = StdMeasure{Text: "hata oranı " + trPct(p.Value) + " (normal " + trPct(p.Threshold) + ")",
			Value: p.Value, Normal: p.Threshold, Unit: "%", Ratio: r, Msg: stdM("imp.err", "vPct", p.Value, "nPct", p.Threshold)}
		im.Known = true
		im.Floor.ErrorPct = f64(p.Value)
		if m := rampDescRe.FindStringSubmatch(p.Description); m != nil {
			if eps, err := strconv.ParseFloat(m[1], 64); err == nil {
				im.FailedPerMin = eps * 60
				im.Floor.Failed5m = f64(eps * 300)
			}
		}
	case "anomaly.p99_ms":
		im.Primary = StdMeasure{Text: "p99 " + trMs(p.Value) + " (normal " + trMs(p.Threshold) + ")",
			Value: p.Value, Normal: p.Threshold, Unit: "ms", Ratio: r, Msg: stdM("imp.p99", "vMs", p.Value, "nMs", p.Threshold)}
		im.Known = true
		if p.Threshold > 0 {
			im.Floor.P99DeltaMs = f64(p.Value - p.Threshold)
		}
	case "anomaly.request_rate":
		im.Primary = StdMeasure{Text: "trafik " + trNum(p.Value) + "/sn (normal " + trNum(p.Threshold) + "/sn)",
			Value: p.Value, Normal: p.Threshold, Unit: "/sn", Ratio: r, Msg: stdM("imp.rate", "vNum", p.Value, "nNum", p.Threshold)}
		im.Known = true
		if p.Threshold > 0 && p.Value < p.Threshold {
			im.Floor.DropPct = f64((1 - p.Value/p.Threshold) * 100)
			im.Floor.BaselineCalls5m = f64(p.Threshold * 300)
		}
	case "svc_slowdown", "svc_slowdown.collapse":
		im.Primary = StdMeasure{Text: "p99 " + trMs(p.Value) + " (taban " + trMs(p.Threshold) + ")",
			Value: p.Value, Normal: p.Threshold, Unit: "ms", Ratio: r, Msg: stdM("imp.p99Base", "vMs", p.Value, "nMs", p.Threshold)}
		im.Known = true
		if p.Threshold > 0 {
			im.Floor.P99DeltaMs = f64(p.Value - p.Threshold)
		}
	case "runtime.gc":
		if f.SameServiceErrOrP99Open {
			im.Primary = StdMeasure{Text: "aynı serviste açık hata/gecikme problemi var", Value: p.Value, Normal: p.Threshold, Unit: k.unit, Ratio: r, Msg: stdM("imp.gcOpen")}
			im.Known = true
		} else {
			im.Primary = StdMeasure{Text: StdImpactUnverified, Value: p.Value, Normal: p.Threshold, Unit: k.unit, Ratio: r, Msg: stdM("imp.unverified")}
		}
	case "monitor":
		im.Primary = StdMeasure{Text: "sentetik kontrol erişemiyor", Value: p.Value, Normal: p.Threshold, Ratio: r, Msg: stdM("imp.monitor")}
		im.Known = true
	case "exception_storm", "exception_shared":
		im.Primary = StdMeasure{Text: trNum(p.Value) + " serviste aynı anda", Value: p.Value, Normal: p.Threshold, Ratio: r, Msg: stdM("imp.services", "cNum", p.Value)}
		im.Known = true
	case "exception_group", "exception_group.http", "exception_group.oracle":
		// Sentetik Problem (notify exceptionGroupProblem): "<tip> — N occurrence · …".
		im.Primary = StdMeasure{Text: "hata grubu", Msg: stdM("imp.group")}
		if m := exOccurDescRe.FindStringSubmatch(p.Description); m != nil {
			if n, err := strconv.ParseFloat(m[1], 64); err == nil {
				im.Primary = StdMeasure{Text: trNum(n) + " oluşum", Value: n, Msg: stdM("imp.occur", "cNum", n)}
			}
		}
		im.Known = true
	case "exception_fatal":
		im.Primary = StdMeasure{Text: trNum(p.Value) + " oluşum", Value: p.Value, Normal: p.Threshold, Msg: stdM("imp.occur", "cNum", p.Value)}
		im.Known = true
	case "anomaly_cluster", "anomaly_cluster.ext":
		im.Primary = StdMeasure{Text: trNum(p.Value) + " sinyal birlikte", Value: p.Value, Normal: p.Threshold, Ratio: r, Msg: stdM("imp.signals", "cNum", p.Value)}
		im.Known = true
	case "incident":
		im.Primary = StdMeasure{Text: "olay ilan edildi", Msg: stdM("imp.incident")}
		if f.IncidentProblems > 0 {
			im.Primary.Text = strconv.Itoa(f.IncidentProblems) + " bağlı problem"
			im.Primary.Msg = stdM("imp.linked", "cNum", float64(f.IncidentProblems))
		}
		im.Known = true
	case "self":
		im.Primary = StdMeasure{Text: "Coremetry iç sağlığı", Value: p.Value, Normal: p.Threshold, Msg: stdM("imp.self")}
		im.Known = true
	case "db_health":
		switch p.Metric {
		case DBHealthMetricErrorPct:
			im.Primary = StdMeasure{Text: "hata oranı " + trPct(p.Value) + " (eşik " + trPct(p.Threshold) + ")", Value: p.Value, Normal: p.Threshold, Unit: "%", Ratio: r, Msg: stdM("imp.dbErr", "vPct", p.Value, "tPct", p.Threshold)}
		case DBHealthMetricP99Ms:
			im.Primary = StdMeasure{Text: "p99 " + trMs(p.Value) + " (eşik " + trMs(p.Threshold) + ")", Value: p.Value, Normal: p.Threshold, Unit: "ms", Ratio: r, Msg: stdM("imp.dbP99", "vMs", p.Value, "tMs", p.Threshold)}
		default:
			im.Primary = StdMeasure{Text: trNum(p.Value) + " hata / 5 dk (eşik " + trNum(p.Threshold) + ")", Value: p.Value, Normal: p.Threshold, Ratio: r, Msg: stdM("imp.dbCount", "vNum", p.Value, "tNum", p.Threshold)}
		}
		im.Known = true
	default:
		unit := k.unit
		if unit == "" {
			unit = guessRuleUnit(p.Metric)
		}
		im.Primary = StdMeasure{Text: "değer " + trUnit(p.Value, unit) + " (eşik " + trUnit(p.Threshold, unit) + ")",
			Value: p.Value, Normal: p.Threshold, Unit: unit, Ratio: r, Msg: ruleValueMsg(p.Value, p.Threshold, unit)}
		im.Known = p.Threshold != 0 || p.Value != 0
	}
	// v0.10.2380 — ML hata oranı Problem'i: etki istek sayısıyla (tikin
	// açıklamaya yazdığı işaret cümlesi; yoksa metin aynen kalır).
	if k.kind == "anomaly_auto" && k.family == StdFamilyError {
		if s, ok := ParseRequestSplit(p.Description); ok {
			applyRequestSplit(&im, s)
		}
	}
	if t := f.Traffic; t != nil && k.traffic {
		im.Floor.Calls5m = f64(t.Calls5m)
		if im.Floor.Failed5m == nil {
			im.Floor.Failed5m = f64(t.Errors5m)
		}
		if im.FailedPerMin == 0 && t.Errors5m > 0 {
			im.FailedPerMin = t.Errors5m / 5
		}
		if t.BaselineCalls5m > 0 {
			im.Floor.BaselineCalls5m = f64(t.BaselineCalls5m)
			if im.Floor.DropPct == nil && t.Calls5m < t.BaselineCalls5m && k.family == StdFamilyTraffic {
				im.Floor.DropPct = f64((1 - t.Calls5m/t.BaselineCalls5m) * 100)
			}
		}
		if im.Floor.P99DeltaMs == nil && k.family == StdFamilyLatency && t.BaselineP99Ms > 0 {
			im.Floor.P99DeltaMs = f64(t.P99Ms - t.BaselineP99Ms)
		}
	}
	return im
}

// guessRuleUnit — SAF: kural metriğinden birim.
func guessRuleUnit(m string) string {
	switch {
	case strings.HasSuffix(m, "_ms") || strings.Contains(m, "p99") || strings.Contains(m, "p95") || strings.Contains(m, "latency"):
		return "ms"
	case strings.Contains(m, "error_rate") || strings.HasSuffix(m, "_pct") || strings.Contains(m, "percent"):
		return "%"
	}
	return ""
}

// directionWord — SAF: aşağı yönlü kural mı.
func isDown(p Problem) bool { return isBelowRule(p.Comparator) }

// MLPromotedRuleNamePrefix — v0.10.1875: ML Problem'inin kural adı öneki.
// v0.10.2067'den beri yalnız ML grup Problem'i yazar (ml/group_live.go
// groupProblemRuleName); RuleID ailesi anomaly-auto:, yalnız manşet
// kaynağını seçer.
const MLPromotedRuleNamePrefix = "ML · "

// MLPromotedHeadline — SAF: ML terfi Problem'inde açıklama = kartın özeti.
func MLPromotedHeadline(p Problem) (string, bool) {
	if !strings.HasPrefix(p.RuleID, "anomaly-auto:") || !strings.HasPrefix(p.RuleName, MLPromotedRuleNamePrefix) {
		return "", false
	}
	// v0.10.2380 — istek bölümü cümlesi manşete girmez (Etki'de söylenir).
	// v0.10.2383 — kaynak işareti de (başlık kartının Kaynak'ı söyler).
	h := strings.TrimSpace(StripRequestSplit(StripEvidenceSource(p.Description)))
	return h, h != ""
}

// problemHeadline — SAF: Türkçe manşet (tavan uygulanmadan).
// v0.10.2299 — ikinci dönüş metnin anahtarı (nil = serbest metin: ML özeti, olay adı).
func problemHeadline(p Problem, k stdKindInfo, im StdImpact) (string, *StdMsg) {
	sp := subjectPrefix(k.subject)
	switch k.kind {
	case "anomaly.error_rate.ramp":
		return sp + "teknik hata (5xx) sürekli artıyor — " + trPct(p.Value) + ", normal " + trPct(p.Threshold), stdM("h.errRamp", "sp", sp, "vPct", p.Value, "nPct", p.Threshold)
	case "anomaly.error_rate":
		if isDown(p) {
			return sp + "hata oranı düştü — " + trPct(p.Value) + ", normal " + trPct(p.Threshold), stdM("h.errDown", "sp", sp, "vPct", p.Value, "nPct", p.Threshold)
		}
		return sp + "hata oranı arttı — " + trPct(p.Value) + ", normal " + trPct(p.Threshold), stdM("h.errUp", "sp", sp, "vPct", p.Value, "nPct", p.Threshold)
	case "anomaly.p99_ms":
		if isDown(p) {
			return sp + "yanıt süresi kısaldı — p99 " + trMs(p.Value) + ", normal " + trMs(p.Threshold), stdM("h.p99Down", "sp", sp, "vMs", p.Value, "nMs", p.Threshold)
		}
		return sp + "yanıt süresi uzadı — p99 " + trMs(p.Value) + ", normal " + trMs(p.Threshold), stdM("h.p99Up", "sp", sp, "vMs", p.Value, "nMs", p.Threshold)
	case "anomaly.request_rate":
		if isDown(p) || (p.Threshold > 0 && p.Value < p.Threshold) {
			if im.Floor.DropPct != nil {
				return sp + "trafik düştü — %" + trNum(*im.Floor.DropPct) + " azaldı (" + trNum(p.Value) + "/sn)", stdM("h.rateDropPct", "sp", sp, "dPct", *im.Floor.DropPct, "vNum", p.Value)
			}
			return sp + "trafik düştü — " + trNum(p.Value) + "/sn, normal " + trNum(p.Threshold) + "/sn", stdM("h.rateDrop", "sp", sp, "vNum", p.Value, "nNum", p.Threshold)
		}
		return sp + "trafik arttı — " + trNum(p.Value) + "/sn, normal " + trNum(p.Threshold) + "/sn", stdM("h.rateUp", "sp", sp, "vNum", p.Value, "nNum", p.Threshold)
	case "anomaly.service_silent":
		return sp + "servis sustu — son kovalarda hiç istek yok", stdM("h.silent", "sp", sp)
	case "anomaly.other", "anomaly_auto":
		// v0.10.1875 — ML olayının terfi kopyası: manşet (bildirim konusu)
		// kartın tek satır özetidir (ml.ExplainSummary — liste/detay/CoSRE ile
		// AYNI cümle; özet servis adıyla başlar, önek eklenmez). Yeni metin
		// üretici yok: evaluator açıklamaya özeti koyar.
		if h, ok := MLPromotedHeadline(p); ok {
			// v0.10.2383 — servis hata oranı cümlesi anahtarlıdır (EN arayüz);
			// v0.10.2385 — servis yanıt süresi cümlesi de.
			return h, mlPromotedMsg(h)
		}
		r := stdBreachRatio(p)
		ml, mv := metricLabelArgs(p.Metric)
		if r > 0 && !math.IsInf(r, 1) {
			return sp + metricLabelTR(p.Metric) + " olağandışı — normalin " + trRatio(r), stdM("h.unusualX", "sp", sp, ml, mv, "rX", r)
		}
		return sp + metricLabelTR(p.Metric) + " olağandışı", stdM("h.unusual", "sp", sp, ml, mv)
	case "anomaly.ext":
		ml, mv := metricLabelArgs(p.Metric)
		return sp + "dış kaynak " + metricLabelTR(p.Metric) + " arttı — " + trNum(p.Value) + ", normal " + trNum(p.Threshold), stdM("h.ext", "sp", sp, ml, mv, "vNum", p.Value, "nNum", p.Threshold)
	case "anomaly.ext_down":
		return sp + "dış kaynak erişilemiyor — ardışık sorgu hatası", stdM("h.extDown", "sp", sp)
	case "anomaly.ext_cap":
		return sp + "dış kaynakta çok sayıda eşzamanlı anomali (özet)", stdM("h.extCap", "sp", sp)
	case "anomaly_cluster.ext":
		return sp + "dış kaynakta yayılan anomali — " + trNum(p.Value) + " sinyal", stdM("h.extCluster", "sp", sp, "cNum", p.Value)
	case "anomaly_cluster":
		return sp + "yayılan sorun — " + trNum(p.Value) + " sinyal birlikte", stdM("h.cluster", "sp", sp, "cNum", p.Value)
	case "svc_slowdown":
		return sp + "yaygın yavaşlama — p99 " + trMs(p.Value) + ", taban " + trMs(p.Threshold), stdM("h.slowdown", "sp", sp, "vMs", p.Value, "nMs", p.Threshold)
	case "svc_slowdown.collapse":
		return sp + "trafik çöktü ve yavaşladı — p99 " + trMs(p.Value), stdM("h.collapse", "sp", sp, "vMs", p.Value)
	case "db_health":
		switch p.Metric {
		case DBHealthMetricErrorPct:
			return sp + "veritabanı hata oranı yüksek — " + trPct(p.Value), stdM("h.dbErr", "sp", sp, "vPct", p.Value)
		case DBHealthMetricP99Ms:
			return sp + "veritabanı yanıt süresi uzadı — p99 " + trMs(p.Value), stdM("h.dbP99", "sp", sp, "vMs", p.Value)
		}
		return sp + "veritabanı hataları arttı — 5 dk'da " + trNum(p.Value) + " hata", stdM("h.dbCount", "sp", sp, "cNum", p.Value)
	case "db_capacity":
		return sp + "kapasite " + trPct(p.Value) + " dolu (eşik " + trPct(p.Threshold) + ")", stdM("h.dbCap", "sp", sp, "vPct", p.Value, "tPct", p.Threshold)
	case "db_slow_stmt":
		return sp + "yavaş SQL ifadesi — p95 " + trMs(p.Value), stdM("h.slowStmt", "sp", sp, "vMs", p.Value)
	case "runtime.gc":
		if p.Metric == "runtime.jvm_gc" {
			return sp + "JVM GC duraklaması uzun — ort. " + trMs(p.Value), stdM("h.gcPause", "sp", sp, "vMs", p.Value)
		}
		return sp + "JVM zamanının " + trPct(p.Value) + "'i GC'de", stdM("h.gcPct", "sp", sp, "vPct", p.Value)
	case "monitor":
		name := strings.TrimPrefix(p.RuleName, "Monitor: ")
		if name == "" {
			name = p.Service
		}
		return subjectPrefix(name) + "sentetik kontrol başarısız", stdM("h.monitor", "sp", subjectPrefix(name))
	case "self":
		return "Coremetry: " + selfHeadlineTR(p.RuleID), stdM(selfHeadlineKey(p.RuleID))
	case "exception_storm":
		return "Exception fırtınası — " + trNum(p.Value) + " serviste yeni hata", stdM("h.storm", "cNum", p.Value)
	case "exception_shared":
		return sp + "paylaşılan bağımlılık hatası — " + trNum(p.Value) + " serviste aynı anda", stdM("h.shared", "sp", sp, "cNum", p.Value)
	case "exception_fatal":
		return sp + "ölümcül altyapı hatası", stdM("h.fatal", "sp", sp)
	case "exception_group", "exception_group.http", "exception_group.oracle":
		return sp + exceptionGroupLabel(k.kind, p.RuleName), exceptionGroupMsg(sp, k.kind, p.RuleName)
	case "incident":
		return "Olay: " + strings.TrimSpace(strings.TrimPrefix(p.RuleName, "Incident · ")), nil // olay adı serbest metin
	case "slo":
		return sp + "SLO hata bütçesi hızla tükeniyor — " + trRatio(stdBreachRatio(p)), stdM("h.slo", "sp", sp, "rX", stdBreachRatio(p))
	}
	name := strings.TrimSpace(p.RuleName)
	if name == "" {
		name = p.RuleID
	}
	return sp + "kural ihlali — " + name, stdM("h.rule", "sp", sp, "name", name)
}

// exceptionGroupLabel — SAF: sentetik exception Problem'inin adından
// (notify exceptionGroupProblem: "<tür> · <tip>[ · <op>]") etiket. D13:
// Oracle grubu "Oracle hata" (branding etiketi ne olursa olsun), "Teknik
// hata" yalnız 5xx HTTP grubu.
func exceptionGroupLabel(kind, ruleName string) string {
	_, rest, ok := strings.Cut(ruleName, " · ")
	if !ok {
		rest = strings.TrimSpace(ruleName)
	}
	switch kind {
	case "exception_group.oracle":
		return "Oracle hata · " + rest
	case "exception_group.http":
		if strings.HasPrefix(rest, "5") {
			return "Teknik hata · HTTP " + rest
		}
		return "HTTP hatası · " + rest
	}
	return "Exception · " + rest
}

// metricLabelTR — SAF: ham metrik adı → Türkçe etiket (bilinmeyen aynen).
func metricLabelTR(m string) string {
	switch {
	case m == "error_rate":
		return "hata oranı"
	case m == "p99_ms":
		return "yanıt süresi"
	case m == "request_rate":
		return "trafik"
	case strings.HasSuffix(m, "error_count") || strings.HasSuffix(m, ":ext:error_count"):
		return "hata sayısı"
	case strings.HasSuffix(m, "fail_count"):
		return "başarısız işlem sayısı"
	}
	if i := strings.LastIndex(m, ":"); i >= 0 && i+1 < len(m) {
		return m[i+1:]
	}
	return m
}

// selfHeadlineTR — SAF: self-health kimliği → cümle.
func selfHeadlineTR(ruleID string) string {
	switch {
	case strings.HasPrefix(ruleID, "self-ingest-stall"):
		return "veri alımı durdu"
	case strings.HasPrefix(ruleID, "self-spool-depth"):
		return "yerel kuyruk birikiyor"
	case strings.HasPrefix(ruleID, "self-disk-eta"):
		return "disk dolmak üzere"
	case strings.HasPrefix(ruleID, "self-channel-broken"):
		return "bildirim kanalı çalışmıyor"
	case strings.HasPrefix(ruleID, "self-volume-spike"):
		return "telemetri hacmi sıçradı"
	}
	return "iç sağlık uyarısı"
}

// problemEvidenceSentence — SAF: kısa Türkçe kanıt cümlesi.
// v0.10.2299 — ikinci dönüş anahtar: "baseK" (dwell) + "srcK" (kaynak).
func problemEvidenceSentence(p Problem, k stdKindInfo, src string, hold bool) (string, *StdMsg) {
	var s string
	baseK, srcK := "ev.base.rule", "ev.src.none"
	switch k.dwell {
	case "pass":
		s = "Dedektör sürdürme koşulunu geçti"
		baseK = "ev.base.dwell"
	case "na":
		s = "Tek ölçümde eşik aşıldı"
		baseK = "ev.base.single"
	default:
		s = "Kural eşiği aşıldı"
	}
	switch src {
	case StdSourceVM:
		if hold {
			s += "; VictoriaMetrics bu turda karar vermedi"
			srcK = "ev.src.vmHold"
		} else {
			s += "; ölçü VictoriaMetrics'ten (örneklenmemiş)"
			srcK = "ev.src.vm"
		}
	case StdSourceSpan:
		s += "; ölçü span'lerden (örneklenmiş)"
		srcK = "ev.src.span"
	case StdSourceDBMV:
		s += "; ölçü veritabanı çağrı özetinden (örneklenmiş span)"
		srcK = "ev.src.dbmv"
	case StdSourceProbe:
		s += "; sentetik kontrol sonucu"
		srcK = "ev.src.probe"
	case StdSourceException:
		s += "; exception kayıtlarından"
		srcK = "ev.src.exception"
	case StdSourceOracle:
		s += "; Oracle hata tablosundan"
		srcK = "ev.src.oracle"
	case StdSourceSelf:
		s += "; Coremetry'nin kendi ölçümü"
		srcK = "ev.src.self"
	case StdSourceExternal:
		s += "; dış kaynak sorgusundan"
		srcK = "ev.src.external"
	case StdSourceLogs: // v0.10.2383 — ML log dedektörünün liderliğindeki Problem
		s += "; log kayıtlarından"
		srcK = "ev.src.logs"
	}
	return s + ".", stdM("ev.sentence", "baseK", baseK, "srcK", srcK)
}

// problemNextStep — SAF.
func problemNextStep(p Problem, k stdKindInfo) StdNextStep {
	svc := url.QueryEscape(p.Service)
	switch k.family {
	case StdFamilyError:
		return StdNextStep{Text: "Servisin hata kırılımına bak", Href: "/service?name=" + svc, Msg: stdM("next.errBreakdown")}
	case StdFamilyLatency:
		return StdNextStep{Text: "Yavaş operasyonları ve trace'leri aç", Href: "/service?name=" + svc, Msg: stdM("next.slowOps")}
	case StdFamilyTraffic:
		return StdNextStep{Text: "Trafiğin nereden düştüğünü/arttığını kontrol et", Href: "/service?name=" + svc, Msg: stdM("next.trafficWhere")}
	case StdFamilyDB:
		return StdNextStep{Text: "Veritabanı detayını aç", Href: "/databases", Msg: stdM("next.db")}
	case StdFamilyException:
		return StdNextStep{Text: "Hata grubunu ve örnek trace'i incele", Href: "/exceptions", Msg: stdM("next.exGroup")}
	case StdFamilyExternal:
		return StdNextStep{Text: "Dış kaynak serisini aç", Href: "/external", Msg: stdM("next.external")}
	case StdFamilyRuntime:
		return StdNextStep{Text: "Aynı serviste kullanıcı etkisi olup olmadığını kontrol et", Href: "/service?name=" + svc, Msg: stdM("next.userImpact")}
	case StdFamilySelf:
		return StdNextStep{Text: "Coremetry sistem sayfasını aç", Href: "/system", Msg: stdM("next.system")}
	case StdFamilyIncident:
		return StdNextStep{Text: "Olay sayfasını aç", Href: "/incident?id=" + url.QueryEscape(strings.TrimPrefix(p.RuleID, "incident:")), Msg: stdM("next.incident")}
	case StdFamilyAvailability:
		if k.kind == "monitor" {
			return StdNextStep{Text: "Kontrolün son sonuçlarını aç", Href: "/monitors", Msg: stdM("next.monitor")}
		}
		return StdNextStep{Text: "Servisin son trafiğini kontrol et", Href: "/service?name=" + svc, Msg: stdM("next.recentTraffic")}
	case StdFamilyCluster:
		return StdNextStep{Text: "Kök neden panelinden yayılımı incele", Href: "/problems?problem=" + url.QueryEscape(p.ID), Msg: stdM("next.rootCause")}
	}
	return StdNextStep{Text: "Kural ayrıntısını aç", Href: "/problems?problem=" + url.QueryEscape(p.ID), Msg: stdM("next.rule")}
}

// stdContext — SAF: bağlam satırları (önceliğe ve güvene KARIŞMAZ; G5).
// v0.10.2299 — ikinci dönüş satır başına anahtar (aynı sıra / uzunluk).
func stdContext(f StdFacts, episode uint32) ([]string, []StdMsg) {
	var out []string
	var msgs []StdMsg
	if f.RecentDeploy != "" {
		out = append(out, "yakın deploy: "+f.RecentDeploy)
		msgs = append(msgs, *stdM("ctx.deploy", "name", f.RecentDeploy))
	}
	switch f.CalendarKind {
	case CalendarHoliday:
		out = append(out, "takvim: tatil günü")
		msgs = append(msgs, *stdM("ctx.holiday"))
	case CalendarHalfDay:
		out = append(out, "takvim: yarım gün")
		msgs = append(msgs, *stdM("ctx.halfDay"))
	case CalendarMonthEnd:
		out = append(out, "takvim: ay sonu")
		msgs = append(msgs, *stdM("ctx.monthEnd"))
	}
	if f.Batch {
		out = append(out, "batch servis")
		msgs = append(msgs, *stdM("ctx.batch"))
	}
	if episode > 1 {
		out = append(out, strconv.FormatUint(uint64(episode), 10)+". tekrar")
		msgs = append(msgs, *stdM("ctx.episode", "n", strconv.FormatUint(uint64(episode), 10)))
	}
	return out, msgs
}

// ── Exception grubu / anomali olayı / incident ──────────────────────────

var stdHTTPTypeRe = regexp.MustCompile(HTTPErrorTypeRe)

// StandardizeException — SAF: exception / HTTP / Oracle hata grubu.
// D13: Oracle grubu rozeti "Oracle hata"; "Teknik hata" yalnız 5xx.
func StandardizeException(g ExceptionGroup, f StdFacts) ProblemStandard {
	now := f.now()
	std := ProblemStandard{Family: StdFamilyException, Kind: "exception_group", Subject: g.Service}
	src := StdSourceException
	label := "Exception"
	labelK := "h.ex.generic" // v0.10.2299 — etiketin anahtar ikizi
	switch {
	case IsOracleGroup(g.Fingerprint):
		std.Kind, src, label, labelK = "exception_group.oracle", StdSourceOracle, "Oracle hata", "h.ex.oracle"
		std.Operation = strings.TrimSpace(g.Message)
	case stdHTTPTypeRe.MatchString(g.Type):
		std.Kind = "exception_group.http"
		if strings.HasPrefix(g.Type, "5") {
			label, labelK = "Teknik hata", "h.ex.tech"
		} else {
			label, labelK = "HTTP hatası", "h.ex.http"
		}
	}
	typ := g.Type
	if std.Kind == "exception_group.http" {
		typ = "HTTP " + typ // exceptionGroupLabel ile aynı biçim
	}
	h := subjectPrefix(g.Service) + label + " · " + typ
	tail := typ
	if std.Operation != "" {
		h += " · " + std.Operation
		tail += " · " + std.Operation
	}
	if g.State == ExStateRegressed {
		h += " (geri döndü)"
		labelK += ".reg"
	}
	std.Headline = clipHeadline(h)
	std.HeadlineMsg = stdM(labelK, "sp", subjectPrefix(g.Service), "t", tail)
	std.Impact = StdImpact{Primary: StdMeasure{Text: trNum(float64(g.Occurrences)) + " oluşum", Value: float64(g.Occurrences), Msg: stdM("imp.occur", "cNum", float64(g.Occurrences))},
		Known: true, Callers: f.VerifiedCallers}
	if t := f.Traffic; t != nil {
		std.Impact.Floor.Calls5m = f64(t.Calls5m)
		std.Impact.Floor.Failed5m = f64(t.Errors5m)
	}
	std.Since = StdSince{StartedAt: g.FirstSeen, LastSeen: g.LastSeen,
		Ongoing: g.State != ExStateResolved && g.State != ExStateIgnored}
	end := now.UnixNano()
	if g.LastSeen > 0 && g.LastSeen < end {
		end = g.LastSeen
	}
	if g.FirstSeen > 0 && end > g.FirstSeen {
		std.Since.DurationSec = (end - g.FirstSeen) / int64(time.Second)
	}
	std.Evidence = StdEvidence{Sentence: "Hata grubu " + trNum(float64(g.Occurrences)) + " kez görüldü.", Source: src, Sampled: stdSampled(src),
		Msg: stdM("ev.group", "cNum", float64(g.Occurrences))}
	gates := ComputeSignalGates(GateInput{
		Family: StdFamilyException, Dwell: "na", Source: src,
		Occurrences: int64(g.Occurrences), OccurrencesKnown: true,
		RelationClaimed: f.RelationClaimed, RelationVerified: f.RelationVerified,
		RecentDeploy: f.RecentDeploy, CalendarKind: f.CalendarKind, Batch: f.Batch,
	})
	std.Confidence = StdConfidence{Level: ConfidenceFromGates(gates, ConfidenceInput{Sampled: std.Evidence.Sampled, Ratio: exceptionMagnitude(g, f)}), Gates: gates}
	std.NextStep = StdNextStep{Text: "Hata grubunu ve örnek trace'i incele", Href: "/exceptions", Msg: stdM("next.exGroup")}
	std.Context, std.ContextMsgs = stdContext(f, 0)
	return std
}

// exceptionMagnitude — SAF: exception grubunda "büyüklük" oranı yok; P1
// merdivenini geçmiş grup (Priority P1) büyük ihlal sayılır.
func exceptionMagnitude(g ExceptionGroup, f StdFacts) float64 {
	if g.Priority == "P1" {
		return math.Inf(1)
	}
	return 0
}

// subjectAnomalyFamily — SAF (v0.10.1792): özne olayının ailesi metrikten
// (Pattern "metrik" ya da "metrik @ rota").
func subjectAnomalyFamily(pattern string) string {
	switch {
	case strings.HasPrefix(pattern, "p99_ms"):
		return StdFamilyLatency
	case strings.HasPrefix(pattern, "error_rate"):
		return StdFamilyError
	}
	return StdFamilyRule
}

// StandardizeAnomalyEvent — SAF: anomaly_events satırı (Problem değil; R1'de
// bildirim hunisine girmez — replay "huni dışı" diye ayrı sayar).
func StandardizeAnomalyEvent(e AnomalyEvent, f StdFacts) ProblemStandard {
	now := f.now()
	fam := StdFamilyRule
	label := e.Kind
	labelArg, labelVal := "label", e.Kind // v0.10.2299 — etiketin anahtar ikizi
	// v0.10.2082 — ML olayı aynı satır şeklinde gelir (kind "ml:<dedektör>",
	// ListAnomalyFeed): aile / etiket / kaynak dedektör adından, ayrıntı ML
	// olay sayfası.
	kind, isML := strings.CutPrefix(e.Kind, MLAnomalyKindPrefix)
	if isML {
		label = "ML " + kind
		labelVal = label
		if kind == "new_template" {
			fam = StdFamilyError
		}
	}
	switch kind {
	case "trace_op_latency":
		fam, label = StdFamilyLatency, "operasyon yavaşladı"
		labelArg, labelVal = "labelK", "evl.opSlow"
	case "trace_op":
		fam, label = StdFamilyError, "operasyon hataları arttı"
		labelArg, labelVal = "labelK", "evl.opErr"
	case "log_pattern":
		fam, label = StdFamilyError, "log deseni arttı"
		labelArg, labelVal = "labelK", "evl.logPattern"
	case "behavior_change":
		fam, label = StdFamilyRule, "davranış değişti"
		labelArg, labelVal = "labelK", "evl.behavior"
	case "new_template", "log_template_new":
		fam, label = StdFamilyError, "yeni log şablonu"
		labelArg, labelVal = "labelK", "evl.newTemplate"
	case SubjectAnomalyKind: // v0.10.1792 — özne tabanı (endpoint / db / queue)
		fam, label = subjectAnomalyFamily(e.Pattern), "özne sapması"
		labelArg, labelVal = "labelK", "evl.subject"
	}
	std := ProblemStandard{Family: fam, Kind: "anomaly_event." + e.Kind, Subject: e.Service, Operation: e.Pattern}
	h := subjectPrefix(e.Service) + label
	hk := "h.ev"
	if e.Pattern != "" {
		h += " — " + e.Pattern
		hk += ".p"
	}
	// v0.10.1792 — oran yalnız tabanı bilinen satırda (AnomalyRatioKnown);
	// tabansız satır "taban yok" der, "0×" değil (1587 tuzağı).
	known := AnomalyRatioKnown(e)
	if known {
		h += " (" + trRatio(e.PeakRatio) + ")"
		hk += ".x"
	}
	std.Headline = clipHeadline(h)
	std.HeadlineMsg = stdM(hk, "sp", subjectPrefix(e.Service), labelArg, labelVal, "pat", e.Pattern, "rX", e.PeakRatio)
	impactText := "normalin " + trRatio(e.PeakRatio)
	impactMsg := stdM("imp.ratio", "rX", e.PeakRatio)
	if AnomalyBaselineless(e) {
		impactText = "taban yok (ilk görülme)"
		impactMsg = stdM("imp.noBase")
	}
	std.Impact = StdImpact{Primary: StdMeasure{Text: impactText, Value: e.CurrentRatio, Ratio: e.PeakRatio, Msg: impactMsg},
		Known: known, Callers: f.VerifiedCallers}
	// v0.10.2380 — servis hata oranı olayı: etki istek sayısıyla (kanıttan).
	if isML && e.Requests != nil && e.Requests.valid() {
		applyRequestSplit(&std.Impact, *e.Requests)
	}
	std.Since = StdSince{StartedAt: e.StartedAt, LastSeen: e.LastSeen, Ongoing: e.Status != "cleared", Episode: e.EpisodeCount}
	end := now.UnixNano()
	if e.LastSeen > 0 && e.LastSeen < end {
		end = e.LastSeen
	}
	if e.StartedAt > 0 && end > e.StartedAt {
		std.Since.DurationSec = (end - e.StartedAt) / int64(time.Second)
	}
	src := anomalyEventSource(kind)
	if isML {
		src = mlAnomalySource(kind)
	}
	std.Evidence = StdEvidence{Sentence: "Anomali olayı (henüz problem değil).", Source: src, Sampled: true, Msg: stdM("ev.anomaly")}
	gates := ComputeSignalGates(GateInput{Family: fam, Dwell: "unknown", Source: src,
		RelationClaimed: f.RelationClaimed, RelationVerified: f.RelationVerified,
		RecentDeploy: f.RecentDeploy, CalendarKind: f.CalendarKind, Batch: f.Batch})
	std.Confidence = StdConfidence{Level: ConfidenceFromGates(gates, ConfidenceInput{Sampled: true, Ratio: e.PeakRatio, BigBreach: f.bigBreach()}), Gates: gates}
	std.NextStep = StdNextStep{Text: "Anomali detayını aç", Href: "/inbox?anomaly=" + url.QueryEscape(e.ID), Msg: stdM("next.anomaly")}
	if isML {
		std.NextStep.Href = "/anomalies/ml/" + url.PathEscape(e.ID)
	}
	std.Context, std.ContextMsgs = stdContext(f, e.EpisodeCount)
	return std
}

// anomalyEventSource — SAF (v0.10.2038): anomaly_events türünün kanıt
// kaynağı. Log türleri log kaynağı (Drain kalıbı örneklemle çıkarıldığından
// Sampled aynen kalır — güven tavanı değişmez); diğerleri span.
func anomalyEventSource(kind string) string {
	switch kind {
	case "log_pattern", "log_template_new", "new_template", "elastic_ml":
		return StdSourceLogs
	}
	return StdSourceSpan
}

// mlAnomalySource — SAF (v0.10.2082): ML dedektörünün kanıt kaynağı
// (sinyal türü dedektör adından; MLTraceDetectorNames dışı log sayılır,
// metric_ önekli VM metriği).
func mlAnomalySource(detector string) string {
	if strings.HasPrefix(detector, "metric_") {
		return StdSourceVM
	}
	if MLDetectorSignal(detector) == MLSignalTrace {
		return StdSourceSpan
	}
	return StdSourceLogs
}

// StandardizeIncident — SAF: incident.
func StandardizeIncident(inc Incident, f StdFacts) ProblemStandard {
	now := f.now()
	std := ProblemStandard{Family: StdFamilyIncident, Kind: "incident", Subject: inc.Service}
	std.Headline = clipHeadline("Olay: " + strings.TrimSpace(inc.Title))
	text := "olay ilan edildi"
	tm := stdM("imp.incident")
	if f.IncidentProblems > 0 {
		text = strconv.Itoa(f.IncidentProblems) + " bağlı problem"
		tm = stdM("imp.linked", "cNum", float64(f.IncidentProblems))
	}
	std.Impact = StdImpact{Primary: StdMeasure{Text: text, Msg: tm}, Known: true, Callers: f.VerifiedCallers}
	std.Since = StdSince{StartedAt: inc.StartedAt, Ongoing: inc.Status != "resolved" && inc.Status != "closed"}
	end := now.UnixNano()
	if inc.ResolvedAt != nil && *inc.ResolvedAt > 0 {
		end = *inc.ResolvedAt
	}
	if inc.StartedAt > 0 && end > inc.StartedAt {
		std.Since.DurationSec = (end - inc.StartedAt) / int64(time.Second)
	}
	std.Evidence = StdEvidence{Sentence: "Bağlı problemlerden oluşan olay.", Source: StdSourceRule, Sampled: true, Msg: stdM("ev.incident")}
	gates := ComputeSignalGates(GateInput{Family: StdFamilyIncident, Dwell: "na", Source: StdSourceRule,
		RelationClaimed: true, RelationVerified: f.RelationVerified,
		RecentDeploy: f.RecentDeploy, CalendarKind: f.CalendarKind, Batch: f.Batch})
	ratio := 0.0
	if inc.Severity == "critical" {
		ratio = math.Inf(1)
	}
	std.Confidence = StdConfidence{Level: ConfidenceFromGates(gates, ConfidenceInput{Sampled: true, Ratio: ratio}), Gates: gates}
	std.NextStep = StdNextStep{Text: "Olay sayfasını aç", Href: "/incident?id=" + url.QueryEscape(inc.ID), Msg: stdM("next.incident")}
	std.Context, std.ContextMsgs = stdContext(f, 0)
	return std
}
