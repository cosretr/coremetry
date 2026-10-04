package anomaly

import (
	"context"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// LogPatternAnomaly is one production-grade signal pattern that
// either started firing for the first time within the window
// (Kind="new") or jumped 2x+ over its trailing baseline
// (Kind="spike"). What an SRE wants to see in their morning
// inbox: not the raw log volume, just what changed.
type LogPatternAnomaly struct {
	Pattern       string  `json:"pattern"` // human-readable name
	Regex         string  `json:"regex"`   // the raw re2 used
	Kind          string  `json:"kind"`    // "new" | "spike"
	CurrentCount  uint64  `json:"currentCount"`
	BaselineCount uint64  `json:"baselineCount"` // trailing window
	Ratio         float64 `json:"ratio"`         // current / max(baseline,1)
	Service       string  `json:"service"`       // service emitting most matches in current window
	Sample        string  `json:"sample"`        // representative log body, truncated
	LastSeenNs    int64   `json:"lastSeenNs"`
	// TopServices — v0.5.287. Per-service breakdown of current
	// window hits, top 5, count desc. The /logs LogPatternStrip
	// renders these as a rosette under the pattern chip so the
	// operator can see "OOMKilled fires on foo-svc (12) and
	// bar-svc (3)" without expanding or filtering.
	TopServices []logstore.PatternServiceHit `json:"topServices,omitempty"`
	// Tokens — v0.5.306. The lowercase body substrings any of
	// which guarantees a regex match. Exposed to the frontend
	// so the /anomalies "logs ↗" link can build a precise OR
	// query that lands on the actual log lines, instead of
	// the previous behaviour (link only narrowed to the
	// service). E.g. "Disk full" carries
	//   ["no space left", "disk full", "enospc"]
	// → link becomes /logs?service=X&q=("no space left" OR
	// "disk full" OR "enospc"). Curated per pattern in the
	// patterns[] slice below.
	Tokens []string `json:"tokens,omitempty"`
	// VerifiedRatio — v0.10.1080: ES'te token sayımının örneklemle regex'e
	// karşı doğrulanma oranı (0 < r ≤ 1). 0 = örneklenmedi: CH (sayım regex'i
	// zaten içerir) ya da tik bütçesi dışında kalan desen. r < 1 iken
	// CurrentCount / BaselineCount r ile ölçeklenmiş TAHMİNDİR (açıklama notu:
	// logstore.VerifiedRatioNote).
	VerifiedRatio float64 `json:"verifiedRatio,omitempty"`
}

type logPattern struct {
	Name  string
	Regex string
	// Tokens is a list of substrings — at least one must appear
	// in the log body for the regex `match()` to even be tried.
	// Drives a `multiSearchAny(body, [tokens])` prefilter that
	// hits the tokenbf_v1 skip index on `body`, so granules
	// containing none of the tokens are pruned before the
	// expensive regex evaluation. Critical at billion-logs/day
	// where a naked `match()` would scan every granule. Tokens
	// are all-lowercase substrings the body is guaranteed to
	// contain when the regex matches; case-insensitivity is
	// handled by `multiSearchAnyCaseInsensitive`.
	Tokens []string
}

// patterns are intentionally curated — every line here corresponds
// to a real production failure shape that an SRE wants paged on.
// Order doesn't matter; the response is sorted by ratio desc.
// Qualification floors — v0.9.327, operator (prod): "daha sıkı kuralları
// olsun, prodta çok daha az tetiklensin. Anomaly olmasa da şu an event
// oluşuyor."
//
// Measured before the change: the median firing log_pattern event carried
// current_count = 3, i.e. the floor WAS the trigger. Three matching lines is
// not an incident at 10B logs/day; and a "12×" over a baseline of a quarter
// line per window is small-number arithmetic, not a spike.
//
// No denominator exists here the way trace_ops has calls — a pattern count
// has no natural per-request base — so the two levers are the count and the
// ratio, both raised. Kept numerically identical to the trace_op floors so
// the two detectors don't disagree about what "enough to matter" means.
const (
	logPatternMinCount = 10
	logPatternMinRatio = 3.0

	// logPatternMinNewCount — v0.9.334. "Yeni" dalı için AYRI, daha yüksek
	// taban.
	//
	// Operatör (prod, v0.9.327'den sonra): "Prodta hâlâ çok anomali var."
	// Sertleştirme `base > 0` dalını kısıyordu ama "new" dalında oran şartı
	// HİÇ yok: taban penceresi boşsa yalnız sayı yetiyor. Ve taban penceresi
	// 1 saat, yani iki saatte bir görünen bir desen her seferinde "yeni".
	//
	// Trace tarafında bunu taban penceresini 24 saate genişleterek çözdüm
	// (ClickHouse MV — ölçülen maliyet 0.050s → 0.058s). BURADA AYNISINI
	// YAPMIYORUM: log sayımları harici Elasticsearch'e gidiyor ve pencereyi
	// 24× açmak taranan doküman sayısını 24× artırırdı — operatörün duran
	// kısıtı "elastice çok sorgu yükü oluşturma sakın". Aynı hedefe sıfır ek
	// ES maliyetiyle giden kaldıraç: kanıtsız bir iddiadan daha fazlasını
	// istemek. Bilinen bir tabanı 3× aşmak ile hiç taban olmadan ortaya
	// çıkmak aynı kanıt değildir.
	logPatternMinNewCount = 30
)

// qualifyLogPattern decides whether one pattern's current-window count is an
// event, and of which kind. Returns ("", 0) when it does not qualify.
//
// Extracted pure in v0.9.327 so the thresholds are table-testable — the same
// contract classifyTraceOps already had ("kesin eşik/kind sınıflaması Go'da,
// tablo-testli"). Living inside a closure is how the old floors drifted apart
// between the two branches without anything noticing.
//
// basePerWindow is the trailing baseline already normalized to the current
// window's length, so both branches compare like with like.
func qualifyLogPattern(cur uint64, basePerWindow float64) (kind string, ratio float64) {
	if cur < logPatternMinCount {
		return "", 0
	}
	if basePerWindow == 0 {
		// Brand-new pattern this window: nothing to divide by, so there is no
		// ratio evidence at all — only the raw count. That is weaker evidence
		// than "3× a known baseline", so it has to clear a higher bar
		// (v0.9.334).
		if cur < logPatternMinNewCount {
			return "", 0
		}
		return "new", float64(cur)
	}
	if r := float64(cur) / basePerWindow; r >= logPatternMinRatio {
		return "spike", r
	}
	return "", 0
}

var patterns = []logPattern{
	{"Oracle errors (ORA-)", `ORA-[0-9]+`, []string{"ora-"}},
	{"Oracle TNS errors", `TNS-[0-9]+`, []string{"tns-"}},
	{"Out of memory", `OutOfMemoryError|out of memory|OOMKilled|cannot allocate memory`, []string{"outofmemoryerror", "out of memory", "oomkilled", "cannot allocate"}},
	{"Null pointer", `NullPointerException|null pointer dereference|null pointer exception`, []string{"nullpointer", "null pointer"}},
	{"Database deadlock", `[Dd]eadlock|deadlock detected`, []string{"deadlock"}},
	{"Connection refused", `ECONNREFUSED|[Cc]onnection refused`, []string{"econnrefused", "connection refused"}},
	// v0.9.316 — java.util.concurrent.TimeoutException (operatörün
	// panosunda 13,1 K/12sa) bu regex'e UYMUYORDU. Alternasyona
	// eklendi: aynı alt-sorgu, sıfır ek ES maliyeti.
	{"Read / write timeout", `i/o timeout|read timeout|write timeout|context deadline exceeded|java\.util\.concurrent\.TimeoutException`, []string{"timeout", "deadline exceeded"}},
	{"Go panic", `^panic:|runtime error:`, []string{"panic:", "runtime error"}},
	{"TLS / certificate", `x509:|certificate has expired|SSL handshake failed|tls: handshake failure`, []string{"x509:", "certificate", "ssl handshake", "tls: handshake"}},
	{"Auth failures", `(?i)401 Unauthorized|invalid credentials|access denied|forbidden`, []string{"401", "credentials", "access denied", "forbidden"}},
	{"Disk full", `no space left on device|disk full|ENOSPC`, []string{"no space left", "disk full", "enospc"}},
	{"Java exceptions", `(ClassCast|IllegalState|IllegalArgument|UnsupportedOperation|ArrayIndexOutOfBounds|ConcurrentModification|NumberFormat|StackOverflow)Exception`, []string{"exception"}},
	// v0.5.284 — JBoss / WildFly / Spring Boot / JDBC stack
	// patterns. Operator runs a Java estate (JBoss + Spring
	// Boot + Oracle); the generic Java patterns above missed
	// the framework-specific shapes that come up on prod
	// failures — bean wiring, deployment hooks, Hikari pool
	// exhaustion. Each token list is lowercase and represents
	// substrings the body MUST contain when the regex matches
	// (case-insensitive prefilter).
	//
	// v0.10.1098 — eski `(WFLY|JBAS)[0-9]+` önekten hemen sonra rakam
	// istiyordu; gerçek WildFly kodu önek + 2–6 harf alt sistem + 4–6
	// rakam (`WFLYCTL0013`, `WFLYEJB0034`, `WFLYMSGAMQ0001`), JBoss AS
	// kodu `JBAS` + 6 rakam (`JBAS014612`). WFLY kodlarını CH hiç
	// saymıyor, ES'te 1080 örneklemi bastırıyordu: desen iki arka uçta da
	// ölüydü. Kodun kendisi seviye taşımaz — aynı önek açılıştaki INFO /
	// WARN satırlarında da var (`WFLYSRV0049 … starting`, `WFLYUT0021
	// Registered web context`); ad "errors" dediği için kodun ARDINDAN bir
	// arıza işareti aranır (harf duyarsız, kod duyarlı kalır). Aksi hâlde
	// her yeniden başlatma bir spike olurdu. `\b` RE2'de (Go + CH re2)
	// ASCII sözcük sınırı: `xWFLYCTL0013`, `JBAS0146120` eşlenmez.
	{"JBoss / WildFly errors", `\b(WFLY[A-Z]{2,6}[0-9]{4,6}|JBAS[0-9]{6})\b.*(?i:fail|error|exception|unable to|could not|cannot|missing)`, []string{"wfly", "jbas"}},
	{"JBoss deployment fail", `Failed to start service|Deployment ".*" was rolled back|service .* in service registry has failed`, []string{"failed to start service", "was rolled back", "service registry"}},
	{"Spring app failed", `APPLICATION FAILED TO START|Error starting ApplicationContext`, []string{"application failed to start", "error starting applicationcontext"}},
	{"Spring bean failure", `(BeanCreation|NoSuchBeanDefinition|BeanInstantiation|UnsatisfiedDependency|CircularDependency)Exception`, []string{"beancreation", "nosuchbeandefinition", "beaninstantiation", "unsatisfieddependency", "circulardependency"}},
	// v0.9.316 — "Unable to get managed connection" ve MQJCA1011 (JMS
	// bağlantı ayırma hatası) aynı arızanın JBoss/MQ yüzü; IJ000655
	// zaten buradaydı. Alternasyon genişledi, alt-sorgu sayısı aynı.
	{"JDBC pool exhausted", `HikariPool-.* - Connection is not available|connection pool .*exhausted|IJ000453|IJ000655|Could not acquire JDBC Connection|Unable to get managed connection|No managed connections available|MQJCA1011`, []string{"hikaripool", "exhausted", "ij000453", "ij000655", "could not acquire jdbc", "managed connection", "mqjca"}},
	{"Hibernate / JPA", `(LazyInitialization|OptimisticLock|StaleObjectState|TransactionTimedOut|TransactionRequired)Exception`, []string{"lazyinitialization", "optimisticlock", "staleobjectstate", "transactiontimedout", "transactionrequired"}},

	// ── v0.9.316 — operatörün kendi Grafana panosundan ("ops board -
	// OCP Watcher Errors Metrics") gelen üretim arıza şekilleri.
	//
	// MALİYET KURALI, operatörün kısıtı: recorder DAKİKADA BİR koşuyor,
	// yani her desen 1.440 alt-sorgu/gün demek. Panodaki 24 kalemi
	// olduğu gibi eklemek deseni 19'dan 43'e çıkarır — ES yükü 2,3x.
	// Onun yerine üç kural uygulandı:
	//
	//   1. Zaten kapsananlar EKLENMEDİ (ORA-*, NullPointerException,
	//      Connection refused, NoSuchBeanDefinition, IJ000655).
	//   2. AYNI ARIZANIN farklı yüzleri TEK desende toplandı — tema
	//      benzerliği değil, arıza aynılığı ölçüt: sınıf yükleme üç
	//      istisnanın da tek sebebi, JNDI arama dört mesajın da.
	//   3. Kuyruk EKLENMEDİ: 12 saatte 5 kez düşen bir şey (Async -
	//      Execution hatası), 1.440 sorgu/gün'ü hak etmiyor. Panonun
	//      sorgusu istendiğinde koşuyor, detektör sonsuza dek koşuyor.
	//
	// Net: 19 → 24 desen (+26%), panodaki hacmin ~%97'si kapsandı.
	//
	// v0.10.1071 (operatör, prod: "'OR <sistem adı>' ibaresi yanlış olmuş,
	// o bir hata değil." — ad depo kuralı gereği yazılmadı) — kurum içi bir
	// sistem ADI alternasyondan ve token listesinden çıkarıldı: hata değil,
	// o sistemin adı geçen her satır (DEBUG/INFO dahil) desene sayılıyordu;
	// ES dedektörü regex'i yok sayıp yalnız token'larla saydığından orada
	// etkisi tam token kadar genişti.
	{"External system rejected", `ExternalSystemException|Request not allowed for URI|Service Unavailable`, []string{"externalsystemexception", "not allowed for uri", "service unavailable"}},
	{"JNDI / lookup failure", `NameNotFoundException|Service (endpoint|definition) not found|Queue connection definition not found`, []string{"namenotfound", "endpoint not found", "definition not found"}},
	{"Service quota", `Service quota (warning|error)|quota exceeded`, []string{"service quota", "quota exceeded"}},
	{"Class init / load failure", `NoClassDefFoundError|ExceptionInInitializerError|ClassNotFoundException`, []string{"noclassdeffound", "exceptionininitializer", "classnotfound"}},
	{"SQL exception", `SqlException|SQLException`, []string{"sqlexception"}}, {"DB constraint violation", `(DataIntegrityViolation|ConstraintViolation|SQLIntegrityConstraintViolation)Exception`, []string{"dataintegrityviolation", "constraintviolation", "sqlintegrityconstraint"}},
}

// esPrefixForms — v0.10.1087 (operatör, prod ES: "Elastic'te eksik."): desen
// adı → YALNIZ ES'te `prefix` sorgusuyla Tokens'a eklenen terim başları
// (logstore.PatternSpec.ESPrefixes). CH listesi (Tokens) ve CH yüklemi
// değişmez: alt-dize eşleşmesi bunları zaten kapsar.
//
// Neden ayrı liste: standart çözümleyici iki harf arasındaki noktayı ayırmaz —
// `java.lang.NullPointerException` TEK terimdir (`java.lang.nullpointerexception`),
// `nullpointer` öneki bile onu bulamaz. Paket-nitelikli sınıf adı Java yığın
// izinin asıl biçimi ("Caused by: java.sql.SQLException: …"); prefix ancak
// terimin BAŞINDAN eşler, dolayısıyla paket yazılmalı. İçeriden arama
// (`*nullpointer*`) terim sözlüğünün tamamını gezer — yok.
//
//   - kısa kod önekleri: otomatik kural ≥5 karakter ister (`ora*` "oracle"ı
//     sayardı); `wfly` / `jbas` yalnız WildFly/JBoss mesaj kodlarının başı,
//     açıkça buraya yazıldı.
//   - paket-nitelikli biçimler: yalnız kararlı JDK / Spring / Hibernate /
//     JPA adları. Kurum içi paket (ExternalSystemException) bilinmiyor, yok.
//   - "Java exceptions": tek token `exception` sözcüğü; sınıf adları
//     (`classcast…`) ES'te ayrıca aranmazsa hiç sayılmaz.
//
// Her girdi küçük harf, tek terim biçimli ve son noktadan sonraki parçası
// desen regex'inin (küçük harfli) alt-dizesi — test: TestESPrefixForms_*.
var esPrefixForms = map[string][]string{
	"Out of memory":        {"java.lang.outofmemoryerror"},
	"Null pointer":         {"java.lang.nullpointer"},
	"Database deadlock":    {"org.springframework.dao.deadlockloser"},
	"Read / write timeout": {"java.util.concurrent.timeoutexception"},
	"Java exceptions": {
		"classcast", "illegalstate", "illegalargument", "unsupportedoperation",
		"arrayindexoutofbounds", "concurrentmodification", "numberformat", "stackoverflow",
		"java.lang.classcast", "java.lang.illegalstate", "java.lang.illegalargument",
		"java.lang.unsupportedoperation", "java.lang.arrayindexoutofbounds",
		"java.util.concurrentmodification", "java.lang.numberformat",
	},
	"JBoss / WildFly errors": {"wfly", "jbas"},
	"Spring bean failure": {
		"org.springframework.beans.factory.beancreation",
		"org.springframework.beans.factory.nosuchbeandefinition",
		"org.springframework.beans.beaninstantiation",
		"org.springframework.beans.factory.unsatisfieddependency",
	},
	"Hibernate / JPA": {
		"org.hibernate.lazyinitialization", "org.hibernate.staleobjectstate",
		"javax.persistence.optimisticlock", "jakarta.persistence.optimisticlock",
		"org.springframework.transaction.transactiontimedout",
		"javax.persistence.transactionrequired", "jakarta.persistence.transactionrequired",
	},
	"JNDI / lookup failure": {"javax.naming.namenotfound"},
	"Class init / load failure": {
		"java.lang.noclassdeffound", "java.lang.exceptionininitializer", "java.lang.classnotfound",
	},
	"SQL exception": {"java.sql.sqlexception"},
	"DB constraint violation": {
		"org.springframework.dao.dataintegrityviolation", "java.sql.sqlintegrityconstraint",
		"org.hibernate.exception.constraintviolation",
		"javax.validation.constraintviolation", "jakarta.validation.constraintviolation",
	},
}

// spec — v0.10.1087: desenin logstore karşılığı; dedektör, grafik (1060),
// `pattern=` pivotu (1071) ve örneklem (1080) aynı spec'i görür.
func (p logPattern) spec() logstore.PatternSpec {
	return logstore.PatternSpec{Name: p.Name, Regex: p.Regex, Tokens: p.Tokens, ESPrefixes: esPrefixForms[p.Name]}
}

// LogPatternSpecByName — v0.10.1060: kayıtlı log_pattern olayının desen ADI
// (anomaly_events.pattern = logPattern.Name) → dedektörün kendi eşleşme
// tanımı. Anomali detayının "desen sayısı" grafiği bununla sayar: grafik
// dedektörün saydığını çizer. Bilinmeyen ad (yeniden adlandırılmış /
// çıkarılmış desen, eski satır) → false; çağıran uydurmaz. Ad listesi
// küratörlü ve küçük, yani bu ad önbellek anahtarının sınırlı bir boyutu.
func LogPatternSpecByName(name string) (logstore.PatternSpec, bool) {
	for _, p := range patterns {
		if p.Name == name {
			return p.spec(), true
		}
	}
	return logstore.PatternSpec{}, false
}

// DetectLogPatterns runs each pattern against the raw `logs` CH
// table over a current window + a much longer trailing baseline
// (default: 5-min current vs 1-hour trailing). Returns only the
// patterns that changed significantly: brand new, or 2x+ over the
// per-window-length baseline rate.
//
// The asymmetric windows keep an anomaly visible for ~1 hour
// after it first fires — a 5m-vs-5m comparison has the spike fall
// into baseline within minutes, which makes the anomaly section
// flicker. With a 1h baseline the same spike stays visible until
// the baseline absorbs it.
//
// Performance:
//   - One query per pattern (combines current + baseline via
//     countIf), N=11 → 11 round-trips total instead of 22.
//   - All N queries fire in parallel; total cold-cache cost is
//     bounded by the slowest single query, not the sum.
//   - Each query is partition-pruned to ~10 minutes of logs and
//     the regex runs against the LowCardinality body column.
//   - Caller should cache the result for 60s; the detector is
//     idempotent and the cache absorbs page reloads.
//
// At 1B logs/day this completes in well under a second cold;
// warm requests serve directly from Redis.
//
// v0.5.241 — refactored to take a logstore.Store instead of
// *chstore.Store so the detector works against BOTH the CH and
// the ES log backend. CH path remains the regex+tokenbf prefilter
// route; ES path uses query_string token-OR against the body
// field (regex is ignored; tokens must be zero-false-negative
// vs the regex). Cross-backend correctness depends on detector
// authors keeping the Tokens list synchronized with the Regex.
//
// v0.10.1087 — ES'te query_string ifadesi yerine token başına prefix /
// match_phrase (logstore es_pattern_clause.go) + esPrefixForms: kod benzeri
// sözcüğün içindeki token'lar ("Elastic'te eksik") artık sayılır.
//
// v0.10.1080 — ES'in token sayımı regex'in ÜST kümesidir (çıplak `tns`
// terimi); tetiklemek üzere olan adaylar verifyLogPatternCands ile örneklemle
// regex'e karşı doğrulanır (CH'de no-op).
func DetectLogPatterns(ctx context.Context, store logstore.Store, window time.Duration) ([]LogPatternAnomaly, error) {
	now := time.Now()
	curStart := now.Add(-window)
	// Trailing baseline = 1h (or the longer of 1h vs 12×window).
	// Cap the lookback so a freshly deployed instance doesn't try
	// to scan more than a day on the first call.
	baseLookback := time.Hour
	if 12*window > baseLookback {
		baseLookback = 12 * window
	}
	if baseLookback > 24*time.Hour {
		baseLookback = 24 * time.Hour
	}
	baseStart := now.Add(-baseLookback)

	// One batched call covers all patterns. CH iterates
	// internally; ES batches via _msearch so we pay one HTTP
	// round-trip total — at billion-log/day on an external ES
	// cluster, that's the only way to keep wall time bounded.
	specs := make([]logstore.PatternSpec, len(patterns))
	for i, p := range patterns {
		specs[i] = p.spec()
	}
	stats, err := store.CountPatterns(ctx, specs, curStart, baseStart, now)
	if err != nil {
		return nil, err
	}

	// Normalise the trailing baseline to the same window length as `cur`
	// so a spike is "current rate is 2x+ the trailing-window rate"
	// regardless of how long the baseline lookback is. Without this, a 12x
	// longer baseline window inflates `base` by 12x and the spike check
	// would never fire.
	windowRatio := float64(window) / float64(baseLookback)
	cands := make([]logPatternCand, 0, 4)
	for i := range patterns {
		if i >= len(stats) || stats[i].Cur == 0 {
			continue
		}
		basePerWindow := float64(stats[i].Base) * windowRatio
		kind, ratio := qualifyLogPattern(stats[i].Cur, basePerWindow)
		if kind == "" {
			continue
		}
		cands = append(cands, logPatternCand{
			idx: i, cur: stats[i].Cur, base: basePerWindow,
			kind: kind, ratio: ratio, sample: stats[i].Sample,
		})
	}

	// v0.10.1080 — ES'te token sayımı regex'e karşı örneklemle doğrulanır
	// (CH'de VerifyPatterns nil döner, sorgu yok; adaylar aynen kalır).
	cands = verifyLogPatternCands(ctx, store, cands, curStart, now, logPatternVerifyBudget, time.Now())

	out := []LogPatternAnomaly{}
	for _, c := range cands {
		p, st := patterns[c.idx], stats[c.idx]
		out = append(out, LogPatternAnomaly{
			Pattern:      p.Name,
			Regex:        p.Regex,
			Kind:         c.kind,
			CurrentCount: c.cur,
			// Rendered to the operator as the per-window-equivalent baseline
			// rate so the UI's "cur vs base" reads intuitively.
			BaselineCount: uint64(c.base),
			Ratio:         c.ratio,
			Service:       st.Service,
			Sample:        truncateSample(c.sample, 240),
			LastSeenNs:    st.LastSeenNs,
			TopServices:   st.TopServices,
			Tokens:        p.Tokens,
			VerifiedRatio: c.verified,
		})
	}

	// Sort: new ones first (most operationally interesting), then
	// spikes by ratio desc, ties broken by current count.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "new"
		}
		if out[i].Ratio != out[j].Ratio {
			return out[i].Ratio > out[j].Ratio
		}
		return out[i].CurrentCount > out[j].CurrentCount
	})
	return out, nil
}

// ── v0.10.1080 — ES token sayımının regex doğrulaması ─────────────────────
//
// Operatör (prod, ES): "Oracle TNS error diyor ama loglarda öyle bir şey yok,
// hatalı desen buluyor." ES CountPatterns regex'i uygulamaz; `message:"tns-"`
// standart çözümleyicide çıplak `tns` terimidir. Tetiklemek üzere olan
// desenlerden örnek çekilir (logstore.VerifyPatterns, tek _msearch) ve regex
// Go'da uygulanır. Sayım sonrası, karar anında: tetiklemeyen desen için hiç
// istek yok.

// logPatternVerifyBudget — tik başına en çok kaç aday örneklenir (oran
// sırasıyla ilk N). Bütçe dışı aday doğrulanmadan, bugünkü gibi yazılır
// (VerifiedRatio 0) — on aynı anda tetikleyen desen zaten olağan dışı.
const logPatternVerifyBudget = 10

// logPatternCand — eşikleri geçmiş, henüz yazılmamış bir desen.
type logPatternCand struct {
	idx      int // patterns / stats indeksi
	cur      uint64
	base     float64 // pencereye normalize taban
	kind     string
	ratio    float64
	verified float64 // 0 = örneklenmedi
	sample   string
}

// applyLogPatternVerification — SAF, tablo testli: doğrulama oranının
// sayımlara etkisi.
//
//	örnek yok (Sampled 0) → sayımlar aynen, r 0 (bilinmiyor; bastırma YOK)
//	r == 0                → suppress: token eşleşti, regex hiç doğrulanmadı
//	0 < r < 1             → cur VE taban r ile ölçeklenir
//	r == 1                → aynen, r 1
//
// Taban da token sayımıdır (aynı yüklem, aynı yanlılık); ikisini aynı r ile
// ölçeklemek oranı dürüst tutar — spike oranı (cur/taban) değişmez, yalnız
// mutlak tabanlar (logPatternMinCount / logPatternMinNewCount) ve "new"
// dalının oranı (= sayı) tahmini regex sayısına göre yeniden sınanır.
func applyLogPatternVerification(cur uint64, base float64, v logstore.PatternVerification) (uint64, float64, float64, bool) {
	r, ok := v.Ratio()
	switch {
	case !ok:
		return cur, base, 0, false
	case r <= 0:
		return 0, 0, 0, true
	case r >= 1:
		return cur, base, 1, false
	}
	return uint64(math.Round(float64(cur) * r)), base * r, r, false
}

// pickLogPatternVerify — SAF: bütçe dahilinde örneklenecek adayların
// indeksleri; oran azalan, eşitlikte sayı azalan (operatörün önüne ilk
// çıkacak olanlar önce doğrulanır).
func pickLogPatternVerify(cands []logPatternCand, budget int) []int {
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ca, cb := cands[order[a]], cands[order[b]]
		if ca.ratio != cb.ratio {
			return ca.ratio > cb.ratio
		}
		return ca.cur > cb.cur
	})
	if budget < 0 {
		budget = 0
	}
	if len(order) > budget {
		order = order[:budget]
	}
	return order
}

// verifyLogPatternCands — adayları doğrular; bastırılan / ölçeklenince eşiğin
// altına düşen aday listeden çıkar. VerifyPatterns tik başına EN ÇOK BİR kez
// çağrılır (aday yoksa hiç). Hata → adaylar doğrulanmadan aynen (bugünkü
// davranış; bir ES aksaması log desenlerini toptan susturmasın).
func verifyLogPatternCands(ctx context.Context, store logstore.Store, cands []logPatternCand,
	from, to time.Time, budget int, now time.Time) []logPatternCand {
	pick := pickLogPatternVerify(cands, budget)
	if len(pick) == 0 {
		return cands
	}
	specs := make([]logstore.PatternSpec, len(pick))
	for k, ci := range pick {
		specs[k] = patterns[cands[ci].idx].spec()
	}
	vs, err := store.VerifyPatterns(ctx, specs, from, to)
	if err != nil {
		log.Printf("[anomaly] log desen doğrulaması okunamadı, adaylar doğrulanmadan yazılıyor: %v", err)
		return cands
	}
	if vs == nil { // CH: sayım regex'i zaten içeriyor
		return cands
	}
	drop := make(map[int]bool)
	for k, ci := range pick {
		if k >= len(vs) {
			break
		}
		c := &cands[ci]
		cur, base, r, suppress := applyLogPatternVerification(c.cur, c.base, vs[k])
		if suppress {
			drop[ci] = true
			if unverifiedLogLimiter.allow(patterns[c.idx].Name, now) {
				log.Printf("[anomaly] log deseni %q: token eşleşti, regex doğrulanamadı (örneklem %d, eşleşen 0) — olay yazılmadı",
					patterns[c.idx].Name, vs[k].Sampled)
			}
			continue
		}
		if r == 0 {
			continue // örnek yok: aynen
		}
		kind, ratio := qualifyLogPattern(cur, base)
		if kind == "" {
			drop[ci] = true // tahmini regex sayısı tabanların altında
			continue
		}
		c.cur, c.base, c.kind, c.ratio, c.verified = cur, base, kind, ratio, r
		if vs[k].Sample != "" {
			c.sample = vs[k].Sample // desene gerçekten uyan satır
		}
	}
	if len(drop) == 0 {
		return cands
	}
	kept := cands[:0]
	for i, c := range cands {
		if !drop[i] {
			kept = append(kept, c)
		}
	}
	return kept
}

// unverifiedLogLimiter — "regex doğrulanamadı" satırı desen başına saatte bir
// (dedektör dakikada bir koşar; her tik aynı satırı basmasın).
var unverifiedLogLimiter = &perKeyLimiter{every: time.Hour, last: map[string]time.Time{}}

type perKeyLimiter struct {
	mu    sync.Mutex
	every time.Duration
	last  map[string]time.Time
}

func (l *perKeyLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.last[key]; ok && now.Sub(t) < l.every {
		return false
	}
	l.last[key] = now
	return true
}

func truncateSample(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
