package anomaly

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/promptfmt"
)

// exception_context.go — exception kök-sebep girdi kurucusu (v0.9.415).
// v0.9.414'te copilot_exception.go içinde doğan prefetch buraya taşındı:
// operatör-tıklı explain (internal/api) ile proaktif ExceptionExplainer
// işçisi AYNI girdiyi kurar — iki kopya sürüklenmez.
//
// Sözleşme (v0.9.414'ten aynen): tüm zenginleştirmeler best-effort;
// kanıt trace/span'leri LLM'den bağımsız, girdiye giren veriden
// deterministik. Saf montaj (assembleExceptionPrompt) ve deploy seçimi
// (PickDeploysAroundStart) tablo-testli — exception_context_test.go.

// ExceptionExplainInput — kurulan girdi + deterministik kanıt.
// pickExceptionStack — v0.9.1225 saf çekirdek: prompt kopyası (1800
// kırpık, User bayt-parite) ile kod-çekici istihkakı (HAM) ayrımı +
// log-fallback kararı. Örneklerden ilk dolu stack kazanır (ham döner —
// "kesik bir satır konumlandırılamaz", v0.9.831 gerekçesi); hiçbiri
// yoksa loglardan yakalanan stack ve onu atan servis döner (v0.9.1182'nin
// exception yüzeyindeki eksik yarısı — prompt logdaki stack'i gösterip
// kod çekici "stacktrace yok" diyordu). Fallback'te forPrompt boş kalır:
// prompt'un stack bölümü eskiden de boştu, bayt-parite korunur.
//
// v0.10.1044 — sample: stack'i veren ÖRNEĞİN indeksi (log-fallback'te ya da
// stack yoksa -1). Çalışan sürüm AYNI olaydan okunmalı: bir olayın stack'i
// başka bir olayın sürümüyle eşleşirse satırlar yine kayar.
func pickExceptionStack(sampleStacks []string, logStack, logStackSvc string) (forPrompt, raw, svc string, sample int) {
	for i, st := range sampleStacks {
		if st != "" {
			return truncRunes(st, 1800), st, "", i
		}
	}
	if logStack != "" {
		return "", logStack, logStackSvc, -1
	}
	return "", "", "", -1
}

// exceptionStackVersion — SAF (v0.10.1044): exception explain'in stack'inin
// çalışan sürümü — YALNIZ stack'i veren olaydan.
//   - stack bir ÖRNEKTEN geldiyse: o örneğin trace'i eldeki trace ise o
//     örneğin span'i (SpanVersion); değilse "" (başka bir olayın sürümü
//     kullanılmaz → kod bugünkü gibi dal ucundan).
//   - stack eldeki trace'in LOGUNDAN geldiyse: StackVersion zinciri (logun
//     span'i → logun resource'u → o trace'te servisin çoğunluğu).
func exceptionStackVersion(samples []chstore.ExceptionSample, sample int, traceID string, traceSpans []chstore.SpanRow,
	service, logSpanID string, logRes map[string]string, haveStack bool) string {
	if sample >= 0 && sample < len(samples) {
		sm := samples[sample]
		if sm.TraceID == "" || sm.TraceID != traceID {
			return ""
		}
		return SpanVersion(traceSpans, sm.SpanID)
	}
	if !haveStack {
		return ""
	}
	return StackVersion(traceSpans, service, logSpanID, logRes)
}

// liteLog — prompt'a giren log satırı. Paket düzeyinde: Stack alanı
// artık döngüde değil, temsilî stack seçildikten SONRA dolduruluyor
// (v0.9.1239) ve tip iki yerden görünmek zorunda.
type liteLog struct {
	Sev    string `json:"sev,omitempty"`
	Svc    string `json:"svc,omitempty"`
	ExType string `json:"exType,omitempty"`
	Stack  string `json:"stack,omitempty"`
	Body   string `json:"body,omitempty"`
}

// dupStackRef — tekrar eden stack'in yerine geçen tek satır.
const dupStackRef = "(stack yukarıdakiyle aynı)"

// foldDuplicateLogStacks — aynı stack'in prompt'taki KOPYALARINI tek
// satırlık referansa katlar (v0.9.1239). Saf; dönen dilim girdiyle
// aynı uzunluktadır.
//
// # Neden
//
// Bir exception grubunun örnek trace'inde 12 loga kadar satır var ve
// retry fırtınasında hepsi AYNI exception'ın stack'ini taşıyor. Temsilî
// stack (1800 rune) + 12×900 rune = tek metnin 13 kopyası, ~12k rune.
// Taşma yeniden-denemesi (copilot_code.go) yalnız KOD bloğunu yarıya
// indiriyor: benzersiz kanıt küçülürken tekrar aynen kalıyordu.
// Öncelik doktrini: kod + taze kanıt > tekrarlanan stack.
//
// # İki tuzak
//
//  1. primary, prompt'ta GÖRÜNEN stack olmalı (stackForPrompt), ham
//     olan değil. Örnekler stack taşımıyorsa temsilî bölüm BOŞ basılır
//     ve stack yalnız logda vardır; ham stack'e karşı katlamak
//     prompt'taki TEK stack'i silerdi.
//  2. Kırpma farkı: temsilî kopya 1800, log kopyası 900 rune. Birebir
//     eşitlik aramak hiçbir şeyi katlamazdı — kısa olan, uzun olanın
//     ÖN EKİ ise aynı metindir.
func foldDuplicateLogStacks(stacks []string, primary string) []string {
	out := make([]string, len(stacks))
	// seen — prompt'ta TAM hâliyle görünen stack'ler; sonraki kopyalar
	// bunlardan herhangi birine katlanır ("yukarıdaki" hepsini kapsar).
	var seen []string
	if primary != "" {
		seen = append(seen, primary)
	}
	for i, st := range stacks {
		if st == "" {
			continue
		}
		dup := false
		for _, s := range seen {
			if sameStackText(st, s) {
				dup = true
				break
			}
		}
		if dup {
			out[i] = dupStackRef
			continue
		}
		out[i] = truncRunes(st, 900)
		seen = append(seen, st)
	}
	return out
}

// sameStackText — iki stack metni AYNI stack'in kopyası mı? Saf.
//
// Kırpılmış kopyalar için önek karşılaştırması yapılır; kısa olan
// metinlerde önek eşleşmesi yanlış katlama üretebileceği için
// (ör. iki farklı exception'ın ortak ilk satırı) minimum uzunluk
// altında BİREBİR eşitlik aranır.
func sameStackText(a, b string) bool {
	x, y := normStackText(a), normStackText(b)
	if x == "" || y == "" {
		return false
	}
	if x == y {
		return true
	}
	if len(x) > len(y) {
		x, y = y, x
	}
	// dupStackMinPrefix — önek eşleşmesinin geçerli sayıldığı en kısa
	// metin. Bir Java stack'inin ilk satırı + iki frame'i rahat aşar;
	// altında kalan metinler için kesme zaten olmamıştır.
	const dupStackMinPrefix = 120
	if len([]rune(x)) < dupStackMinPrefix {
		return false
	}
	return strings.HasPrefix(y, x)
}

// normStackText — karşılaştırma için normalleştirme: satır sonları,
// baş/son boşluk ve truncRunes'un eklediği "…" eki.
func normStackText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	return strings.TrimSuffix(s, "…")
}

type ExceptionExplainInput struct {
	User     string   // narration user prompt'u
	EvTraces []string // kanıt trace id'leri (örnek tablosu kutulaması)
	EvSpans  []string // kanıt span id'leri
	// LogsBlock — User'ın İÇİNDEKİ log bölümü, ayrıca taşınır (v0.9.482).
	// AI çekmecesi sohbeti bu paketi narration bütçesine sığdırırken önce
	// span/trace listesini budar, LOGLARI KORUR — operatörün takip
	// soruları ("logda ne yazıyor") log içeriğine dairdir. Explain
	// yolunda kullanılmaz; User bayt-bayt eskisidir.
	LogsBlock string
	// Stack — User'ın İÇİNDEKİ temsilî stacktrace, ayrıca ham olarak
	// taşınır (v0.9.831). "Kodu da incele" yolu bunu stackparse'a
	// verip kaynak penceresi çeker.
	//
	// Yeniden okumak yerine taşınıyor: örnek seçimi (ilk stack'li
	// örnek) burada yapılıyor ve iki yerde tekrarlanırsa model bir
	// stack'i, kod çekici BAŞKA bir stack'i görebilir — sessizce
	// yanlış dosya. User bayt-bayt eskisidir. v0.9.1225'ten beri HAM
	// (kırpıksız) taşınır — prompt kendi 1800'lük kopyasını ayrı alır.
	Stack string
	// StackService — Stack log-fallback'ten geldiyse logu atan servis
	// (v0.9.1225): depo çözümü grubun servisi yerine buna gitmeli.
	// Boşsa grup servisi geçerli.
	StackService string
	// StackVersion (v0.10.1044, operatör: "Kod, dalın ucundan değil çalışan
	// sürümden okunsun") — stack'i basan servisin örnek trace'teki ÇALIŞAN
	// sürümü (StackServiceVersion). Yeni okuma YOK: aşağıda zaten yüklenen
	// örnek trace'in span'lerinden. Boş = bilinmiyor → kod dal ucundan.
	StackVersion string
	// DBStatements / ErrorText (v0.10.115) — örnek trace'in hata
	// span'larındaki SQL ifadeleri (≤3) ve grup tipi+mesajı+stack başı:
	// şema kanıtının girdisi (api buildSchemaEvidence).
	DBStatements []string
	ErrorText    string

	// ── v0.9.1129 (AI Faz 2.1) — insight kartının YAPISAL yarısı ──
	//
	// Kart, prose'dan bağımsız deterministik sinyaller çiziyor
	// (internal/ai/insight). O sinyaller prompt'un GÖRDÜĞÜ veriden
	// türemek ZORUNDA: ikinci bir okuma, kartta "deploy v5" yazarken
	// modelin v4'ten bahsettiği bir hâl üretebilir. Stack/LogsBlock
	// alanlarının gerekçesiyle aynı — burada seçim yapılıyor, o yüzden
	// seçilen şey ham olarak da taşınıyor.
	//
	// Hepsi opsiyonel: veri yoksa nil/boş, kart o satırı çizmez.
	TraceID string          // örnek hata trace'i (kartın "Örnek trace" çipi)
	Trend   *ExceptionTrend // occurrence özeti (User içindeki trend satırının kaynağı)
	Deploys []NearbyDeploy  // deployBlock'un YAPISAL hâli (yön bayraklı)

	// Pods (v0.10.847, operatör-bildirimli) — pod/instance yoğunlaşması:
	// User içindeki bloğun YAPISAL hâli. Stack/Trend/Deploys ile aynı
	// gerekçe: seçim ve karar BURADA veriliyor, o yüzden karar ham
	// olarak da taşınıyor — kart ile modelin gördüğü ayrışamasın.
	// Kind boşsa hiç hesaplanmadı (prompt bloğu da basılmaz).
	Pods PodConcentration

	// Oracle (v0.10.1100) — `ora:` grubunda girdi Oracle verisinden kuruldu
	// (BuildOracleExceptionExplainInput); span alanları boş. nil = span grubu.
	Oracle *OracleExplainContext
}

// CodeService — "Kodu da incele"nin depoyu çözdüğü servis (v0.9.1225 kuralı):
// stack log-fallback'ten geldiyse logu atan servis (StackService), değilse
// grubun servisi. StackVersion YALNIZ bu servisin sürümüdür. v0.10.1053 —
// explain (api/copilot_exception.go) ile exception takibinin kod kapsamı
// (api/chat_exception_followup.go) kuralı buradan okur; ikinci kopya yok.
func (in ExceptionExplainInput) CodeService(groupService string) string {
	if in.StackService != "" {
		return in.StackService
	}
	return groupService
}

// ExceptionTrend — occurrence serisinin sıkıştırılmış hâli (v0.9.1129).
// Eskiden yalnız prompt satırı olarak vardı; kart aynı sayıları
// göstereceği için sayılar tek yerde hesaplanıp iki yere veriliyor.
type ExceptionTrend struct {
	Total    uint64
	Last24   uint64
	Peak     uint64
	PeakAtNs int64
	Buckets  int
}

// SummarizeExceptionOccurrences — occurrence noktalarından trend özeti.
// SAF (nowNs parametre — time.Now() okuyan bir özetleyici test
// edilemez). Tablo-testli.
func SummarizeExceptionOccurrences(occ []chstore.OccurrencePoint, nowNs int64) ExceptionTrend {
	t := ExceptionTrend{Buckets: len(occ)}
	cut := nowNs - int64(24*time.Hour)
	for _, p := range occ {
		t.Total += p.Count
		if p.Time >= cut {
			t.Last24 += p.Count
		}
		if p.Count > t.Peak {
			t.Peak, t.PeakAtNs = p.Count, p.Time
		}
	}
	return t
}

// PromptLine — trendin prompt'a giren satırı. Tepe damgası verilen
// konumda ve konum ADIYLA yazılır (v0.10.745, operatör bildirimi:
// "tepe 22:30" dedi, ekran 01:30 gösteriyordu — damga UTC'ydi ve
// dilimsizdi; model onu yerel saat sanıp aktardı). nil → UTC, ama o da
// artık etiketli: arka plan açıklayıcının tarayıcısı yok, en azından
// "UTC" der. Şekil bunun dışında v0.9.1129 ile aynı.
func (t ExceptionTrend) PromptLine(loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return fmt.Sprintf("toplam=%d son24h=%d tepe=%d@%s (%s) bucket=%d",
		t.Total, t.Last24, t.Peak,
		time.Unix(0, t.PeakAtNs).In(loc).Format("2006-01-02 15:04"), loc.String(), t.Buckets)
}

// BuildExceptionExplainInput — grup meta + occurrence trendi + temsilî
// stacktrace + en yeni örneğin TAM trace'i + o trace'in logları +
// FirstSeen-merkezli deploy penceresi. logs nil olabilir (CH-only
// kurulum ya da işçi bağlamı) — log bloğu atlanır.
//
// loc (v0.10.745) — operatörün saat dilimi: prompt'taki her mutlak damga
// (firstSeen/lastSeen/tepe) bu konumda yazılır ve konum adı meta'da
// gider; nil → UTC (etiketli). İstek yolları tarayıcıdan alır
// (explainOptions.location), arka plan açıklayıcı UTC verir.
func BuildExceptionExplainInput(ctx context.Context, store *chstore.Store, logs logstore.Store, g *chstore.ExceptionGroup, loc *time.Location) ExceptionExplainInput {
	if loc == nil {
		loc = time.UTC
	}
	sres, _ := store.GetExceptionGroupSamples(ctx, g.Fingerprint, 5)
	samples := sres.Samples

	trend := ""
	var trendRef *ExceptionTrend
	if occ, oerr := store.GetExceptionOccurrences(ctx, g.Fingerprint); oerr == nil && len(occ) > 0 {
		t := SummarizeExceptionOccurrences(occ, time.Now().UnixNano())
		trend, trendRef = t.PromptLine(loc), &t
	}

	// En yeni trace'li örnek → tam trace + o trace'in logları + temsilî stack.
	// v0.10.1103 — sampleTraceEvidence (aşağıda; okumalar trace_evidence.go'nun
	// ortak yardımcılarında, Oracle grubu da onları kullanır); çıktı bayt bayt
	// eski — pin trace_evidence_test.go eski satır içi kodun kopyasına karşı.
	traceID := ""
	for _, sm := range samples {
		if sm.TraceID != "" {
			traceID = sm.TraceID
			break
		}
	}
	sampleStacks := make([]string, 0, len(samples))
	for _, sm := range samples {
		sampleStacks = append(sampleStacks, sm.Stacktrace)
	}
	st := sampleTraceEvidence(ctx, store, logs, traceID, sampleStacks)

	// Deploy penceresi — FirstSeen'e YAKINLIĞA göre seçim + önce/sonra
	// açık etiket (v0.9.414 verify bulguları).
	deployBlock := ""
	var nearby []NearbyDeploy
	if g.Service != "" {
		dFrom := time.Unix(0, g.FirstSeen).Add(-6 * time.Hour)
		dTo := time.Unix(0, g.LastSeen)
		if deps, derr := store.GetServiceDeploys(ctx, g.Service, dFrom, dTo); derr == nil && len(deps) > 0 {
			// TEK seçim, iki gösterim: prompt satırları ve kartın yapısal
			// adayları AYNI listeden türer (v0.9.1129).
			nearby = PickDeploysAroundStartRefs(deps, g.FirstSeen)
			if parts := renderNearbyDeploys(nearby); len(parts) > 0 {
				deployBlock = "\n\nAynı servisin yakın DEPLOY'ları: " + fmt.Sprintf("%v", parts) +
					"\nGrubun başlangıcı bir deploy'un hemen SONRASINA denk geliyorsa o deploy'u kök neden adayı olarak öne al."
			}
		}
	}

	// v0.10.847 (operatör-bildirimli) — pod/instance yoğunlaşması. En
	// sonda: iki okuma da yumuşak düşer ve düştüklerinde yalnız bu
	// kanıt "ölçülemedi" olur; prompt'un geri kalanı etkilenmez.
	pods := buildPodConcentration(ctx, store, g.Fingerprint, g.Service)

	// v0.10.1044 — kod çekici depoyu stackSvc'den (boşsa grup servisi)
	// çözer; çalışan sürüm stack'i veren OLAYDAN (exceptionStackVersion).
	verSvc := st.StackService
	if verSvc == "" {
		verSvc = g.Service
	}
	stackVersion := exceptionStackVersion(samples, st.StackSample, traceID, st.Trace.Spans,
		verSvc, st.Logs.StackSpan, st.Logs.StackRes, st.StackRaw != "")

	return ExceptionExplainInput{
		User: assembleExceptionPrompt(g, loc, trend, st.StackForPrompt, st.TraceBlock, st.LogsBlock, deployBlock,
			renderPodConcentration(pods)),
		EvTraces:     st.EvTraces,
		EvSpans:      st.Trace.EvSpans,
		LogsBlock:    st.LogsBlock,
		Stack:        st.StackRaw,
		StackService: st.StackService,
		StackVersion: stackVersion,
		DBStatements: st.Trace.DBStatements,
		ErrorText:    truncRunes(g.Type+": "+g.Message+"\n"+st.StackRaw, 2000),
		TraceID:      traceID,
		Trend:        trendRef,
		Deploys:      nearby,
		Pods:         pods,
	}
}

// sampleTraceOut — span yolunun örnek trace kanıtı (v0.10.1103 çıkarımı).
type sampleTraceOut struct {
	TraceBlock, LogsBlock string
	EvTraces              []string
	Trace                 traceEvidenceResult
	Logs                  traceLogsResult
	// pickExceptionStack çıktısı (prompt kopyası / HAM / log-fallback servisi / örnek indeksi).
	StackForPrompt, StackRaw, StackService string
	StackSample                            int
}

// sampleTraceEvidence — örnek trace → kompakt span bloğu + o trace'in logları
// + temsilî stack seçimi. v0.10.1103'e dek BuildExceptionExplainInput'un satır
// içi gövdesiydi; okumalar artık ortak yardımcılarda (traceEvidence /
// traceLogsEvidence — Oracle grubu da onları kullanır), biçim dizeleri ve
// sıralama AYNEN (prompt bayt bayt eski; pin trace_evidence_test.go).
func sampleTraceEvidence(ctx context.Context, store TraceSpanLoader, logs logstore.Store, traceID string, sampleStacks []string) sampleTraceOut {
	var o sampleTraceOut
	// Tam trace + kanıt (error span'ler GARANTİLİ — v0.9.414 verify bulgusu:
	// düz head-cap derindeki hatayı düşürüyordu). Trace.Spans çalışan sürüm
	// seçimine gider (v0.10.1044; ek okuma yok).
	o.Trace = traceEvidence(ctx, store, traceID)
	if o.Trace.Loaded() {
		o.EvTraces = append(o.EvTraces, traceID)
		if tp, ok := o.Trace.compactJSON(); ok {
			o.TraceBlock = fmt.Sprintf("\n\nÖrnek hata TRACE'i (%s, %d span):\n```json\n%s\n```",
				traceID, len(o.Trace.Compact), tp)
		}
	}
	// Trace'in logları — tek pivot sorgusu; trace yüklenemediyse (1970
	// penceresi) HİÇ gitme (v0.9.414 verify bulgusu, ES maliyet disiplini).
	// İlk stack'li logun stack'i + servisi (v0.9.1225) ve span'i + resource'u
	// (v0.10.1044) Logs.Stack* alanlarında.
	o.Logs = traceLogsEvidence(ctx, logs, traceID, o.Trace.MinT, o.Trace.MaxT)

	// v0.9.1225 — prompt kopyası ile kod-çekici istihkakı AYRILDI. User
	// bayt-bayt eski (1800 rune'luk kırpık); Stack ise HAM taşınır —
	// kardeş yol explain_trace_input.go v0.9.831'de aynı gerekçeyle
	// ("kesik bir satır konumlandırılamaz") ham taşıyordu, burası kırpığı
	// veriyordu: derin JBoss stack'lerinde Caused-by uygulama frame'leri
	// 1800'ün altında kalıp pencereleme hiç isabet etmiyordu.
	o.StackForPrompt, o.StackRaw, o.StackService, o.StackSample = pickExceptionStack(sampleStacks, o.Logs.Stack, o.Logs.StackService)

	// v0.9.1239 — log bloğu ANCAK ŞİMDİ kurulabilir: her logun stack'i
	// prompt'ta GÖRÜNEN temsilî stack'e karşı katlanıyor ve o seçim
	// (pickExceptionStack) log döngüsünün kendi çıktısına bağlı.
	if lp, ok := o.Logs.foldedJSON(o.StackForPrompt); ok {
		o.LogsBlock = fmt.Sprintf("\n\nBu trace'in ilişkili LOGLARI (yüksek severity önce):\n```json\n%s\n```", lp)
	}
	return o
}

// NearbyDeploy — grubun başlangıcı çevresinde SEÇİLMİŞ deploy
// (v0.9.1129). OffsetSec MUTLAK uzaklık; yön ayrı bayrak.
//
// Yönü işaretle kodlamak yanlış olurdu: sub-saniyelik bir "sonra"
// deploy'u ns→sn kırpmasında 0'a düşer ve 0'ın işareti YOK. Seçimi
// yapan yer yönü BİLİYOR — tahmine bırakmıyoruz.
// (insight.DeployCandidate aynı şekil; dönüşüm internal/api'de.)
type NearbyDeploy struct {
	Version   string
	OffsetSec int64
	After     bool // true = grubun başlangıcından SONRA → kök neden OLAMAZ
}

// PickDeploysAroundStart — GetServiceDeploys ASC döner; FirstSeen
// ÖNCESİNDEN son 3 + SONRASINDAN ilk 2 seçilir ve yön açıkça yazılır.
// Saf — exception_context_test.go: düz "son 5" kesimi uzun ömürlü
// gruplarda asıl adayı (başlangıçtan hemen önceki deploy) düşürüyordu,
// negatif "önce" ise LLM'e yanlış kanıt oluyordu (v0.9.414 bulguları).
//
// v0.9.1129: seçim PickDeploysAroundStartRefs'e, biçimleme
// renderNearbyDeploys'a ayrıldı; bu sarmalayıcı prompt satırlarını
// BAYT BAYT eskisi gibi üretir (mevcut testler tam metni pinliyor).
func PickDeploysAroundStart(deps []chstore.Deploy, firstSeen int64) []string {
	return renderNearbyDeploys(PickDeploysAroundStartRefs(deps, firstSeen))
}

// PickDeploysAroundStartRefs — seçim yarısı: yön + mutlak uzaklık.
func PickDeploysAroundStartRefs(deps []chstore.Deploy, firstSeen int64) []NearbyDeploy {
	split := len(deps)
	for i, d := range deps {
		if d.TimeUnixNs > firstSeen {
			split = i
			break
		}
	}
	before, after := deps[:split], deps[split:]
	if len(before) > 3 {
		before = before[len(before)-3:]
	}
	if len(after) > 2 {
		after = after[:2]
	}
	out := make([]NearbyDeploy, 0, len(before)+len(after))
	for _, d := range before {
		out = append(out, NearbyDeploy{
			Version: d.Version, OffsetSec: (firstSeen - d.TimeUnixNs) / int64(time.Second)})
	}
	for _, d := range after {
		out = append(out, NearbyDeploy{
			Version: d.Version, OffsetSec: (d.TimeUnixNs - firstSeen) / int64(time.Second),
			After: true})
	}
	return out
}

// renderNearbyDeploys — biçimleme yarısı. Dakika kırpması iç içe taban
// bölmesi (sn → dk) ile eski tek-adım (ns → dk) bölmesiyle AYNI sonucu
// verir; exception_context_test.go tam metinleri pinliyor.
func renderNearbyDeploys(nd []NearbyDeploy) []string {
	parts := make([]string, 0, len(nd))
	for _, d := range nd {
		mins := d.OffsetSec / 60
		if d.After {
			parts = append(parts, fmt.Sprintf("%s (grubun başlangıcından %d dk SONRA — kök neden OLAMAZ, olsa olsa etki/çözüm denemesi)", d.Version, mins))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (grubun başlangıcından %d dk ÖNCE)", d.Version, mins))
	}
	return parts
}

// assembleExceptionPrompt — saf montaj; boş bloklar atlanır. Damgalar
// loc'ta (RFC3339 ofsetli: "…T01:30:00+03:00") ve meta.timezone konumun
// adı — sistem prompt'u modele "dilim çevirme, olduğu gibi aktar" der.
// podsBlock (v0.10.847) deploy bloğundan SONRA, kapanış talimatından
// ÖNCE basılır: kapanış cümlesi "stack + trace + logları birlikte
// yorumla" diyor ve yeni kanıtın o cümlenin ardında kalması, modelin
// onu talimat kapsamı dışında saymasına yol açardı.
func assembleExceptionPrompt(g *chstore.ExceptionGroup, loc *time.Location, trend, stack, traceBlock, logsBlock, deployBlock, podsBlock string) string {
	if loc == nil {
		loc = time.UTC
	}
	meta := map[string]any{
		"type": g.Type, "message": truncRunes(g.Message, 400), "service": g.Service,
		"state": g.State, "occurrences": g.Occurrences,
		"firstSeen": time.Unix(0, g.FirstSeen).In(loc).Format(time.RFC3339),
		"lastSeen":  time.Unix(0, g.LastSeen).In(loc).Format(time.RFC3339),
		"timezone":  loc.String(),
	}
	mp, _ := json.Marshal(meta)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Exception GRUBU:\n```json\n%s\n```", promptfmt.FenceSafe(string(mp))) // v0.10.404 — çit kaçışı
	if trend != "" {
		sb.WriteString("\n\nOccurrence trendi: " + trend)
	}
	if stack != "" {
		sb.WriteString("\n\nTemsilî STACKTRACE:\n```\n" + promptfmt.FenceSafe(stack) + "\n```")
	}
	sb.WriteString(traceBlock)
	sb.WriteString(logsBlock)
	sb.WriteString(deployBlock)
	sb.WriteString(podsBlock)
	sb.WriteString("\n\nStacktrace + trace + logları BİRLİKTE yorumla: kök nedeni stack'in EN DERİN \"Caused by\" bölümündeki ilk uygulama-frame'ine (yoksa en üst uygulama-frame'ine) ve trace'te hatanın DOĞDUĞU (en derin error) span'a dayandır; yayılan (propagate) hataları kök sanma.")
	return sb.String()
}

// truncRunes — rune-güvenli kesme (api.truncate byte keserdi; Türkçe
// çok-baytlı mesajlarda U+FFFD üretiyordu — v0.9.414 verify notu).
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
