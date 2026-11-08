package api

// chat_trace_drill.go — v0.10.1327 "C-core": CoSRE guided cevaplarına
// deterministik EN YAVAŞ TRACE kırılımı + YALNIZ o trace id'lerinin logları.
//
// Operator-reported: "bsa-loan-legalproceeding-prod neden yavaş" sorusu
// SocketTimeoutException dedi ama NEREDE demedi; takip sorusu "nereye giderken
// socketimeoutexception almış" Log kademesine gitti ve "son 500 hata logunda
// yok" dedi. En yavaş trace cevabı anında veriyordu: kök SERVER span
// POST /BSAWEB/legalproceeding/… 50.02 s; çocuk CLIENT span
// "POST osbprod.bank.example" 50015 ms, öz süre %100, status error, HTTP 500,
// url.full …/TibcoESB/ExternalXServices/Uyap/DosyaListesiSorgulama?wsdl,
// operasyonun 24 sa p50'si 13 ms → ×3825; kaynak Tempo. Operatör: "en yavaş
// bir iki trace'e bak, ve O TRACE ID'LERİNİN loglarına (tüm loglara değil)".
//
// Saf çekirdek (tablo testli, chat_trace_drill_test.go): dominantSpan,
// peerOf, spanFailure, renderTraceDrillTR/renderTraceDrillsTR, pickDrillTraces,
// groupTraceLogs, hasPeerSignal, prevTurnMentionsLatencyFailure, traceDrillRoute,
// rootCauseWantsTraceDrill, intentTraceDrillUpgrade. Sunucu yarısı: trace
// çözümü Tempo-öncelikli resolveTraceSpans (≤3 sn Tempo, sonra CH), taban
// çizgisi operation_summary_5m (SpanOperationRED p50), loglar TEK okuma
// (logstore.Filter.TraceIDs) — s.logs yapılandırılmış Switchable depo (ES
// seçiliyse ES); arka uç ASLA env'den tahmin edilmez. Tüm kırılım 10 sn tavanlı;
// goroutine'ler emit ETMEZ, olaylar yalnız demet goroutine'inden çıkar.
// Prompt değişmedi: kanıt satırları kendini anlatır.

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/tracechain"
)

// guidedTraceDrill — "nereye giderken timeout almış": servisin en yavaş
// trace(ler)inin kritik yolundaki baskın span + o trace'lerin logları. Router'ın
// saf yarısı geçmiş görmez; rota copilotChatGuided'da geçmişle (traceDrillRoute)
// ve sınıflandırıcı log_errors + hedef sorusunda (intentTraceDrillUpgrade)
// kurulur. Sınıflandırıcı beyaz listesinde DEĞİL (prompt değişmedi).
const guidedTraceDrill guidedIntent = "trace_drill"

const (
	traceDrillBudget       = 10 * time.Second // tüm kırılım (trace'ler + taban + loglar)
	traceDrillLogsTimeout  = 5 * time.Second
	traceDrillLogsLimit    = 60
	traceDrillLogsPerTrace = 8
	traceDrillLogBodyRunes = 200
	// traceDrillRecordsBodyRunes — traceRecords gövdeli kayıtların tavanı (v0.10.1988).
	traceDrillRecordsBodyRunes = 1500
	// traceDrillRecordsMaxRunes — kayıt başı üst sınır (gövdenin kendi tavanı; v0.10.1990).
	traceDrillRecordsMaxRunes = 16000
	traceDrillBaselineWin     = 24 * time.Hour
	traceDrillMax             = 2
)

// drillPick — kritik yoldaki baskın span (öz süresi en büyük).
type drillPick struct {
	Span   chstore.SpanRow
	SelfNs int64
	WallNs int64   // kök süresi (kritik yolun duvar saati)
	Share  float64 // SelfNs / WallNs (0..1)
	Depth  int
	OK     bool
}

// drillKindRank — v0.10.1379: tracechain.KindRank'e devreder.
func drillKindRank(kind string) int { return tracechain.KindRank(kind) }

// dominantSpan — SAF: kritik yol span'leri arasında öz süresi en büyük olan;
// eşitlikte hata durumlu, sonra CLIENT/PRODUCER türlü span kazanır (dış çağrı
// "nereye" sorusunun cevabıdır). Kritik yol boşsa OK=false.
func dominantSpan(spans []chstore.SpanRow, an chstore.TraceAnalysis) drillPick {
	byID := make(map[string]int, len(spans))
	for i := range spans {
		if _, dup := byID[spans[i].SpanID]; !dup && spans[i].SpanID != "" {
			byID[spans[i].SpanID] = i
		}
	}
	node := make(map[string]chstore.TraceNode, len(an.Nodes))
	for _, n := range an.Nodes {
		node[n.SpanID] = n
	}
	var best drillPick
	better := func(sp chstore.SpanRow, self int64) bool {
		if !best.OK || self > best.SelfNs {
			return true
		}
		if self < best.SelfNs {
			return false
		}
		be, ce := best.Span.StatusCode == "error", sp.StatusCode == "error"
		if be != ce {
			return ce
		}
		return drillKindRank(sp.Kind) > drillKindRank(best.Span.Kind)
	}
	for _, id := range an.CriticalIDs {
		i, ok := byID[id]
		if !ok {
			continue
		}
		n := node[id]
		if better(spans[i], n.SelfNs) {
			best = drillPick{Span: spans[i], SelfNs: n.SelfNs, Depth: int(n.Depth), OK: true}
		}
	}
	if best.OK {
		best.WallNs = an.CriticalNs
		if best.WallNs > 0 {
			best.Share = float64(best.SelfNs) / float64(best.WallNs)
		}
	}
	return best
}

// peerRef / drillFailure — v0.10.1379: saf çekirdek internal/tracechain'de
// (exception açıklaması da aynı zinciri basar); takma ad + ince sarmalayıcı,
// davranış bayt bayt aynı.
type (
	peerRef      = tracechain.Peer
	drillFailure = tracechain.Failure
)

// peerOf — SAF: öncelik peer.service → server.address / net.peer.name
// (+ url.full yolu) → db.system + db.name → messaging.system + hedef → span
// adı (tracechain.PeerOf).
func peerOf(sp chstore.SpanRow) peerRef { return tracechain.PeerOf(sp) }

// spanFailure — SAF: status + HTTP kodu + ilk exception event'i
// (tracechain.SpanFailure; get_trace aracıyla aynı çözücü).
func spanFailure(sp chstore.SpanRow) drillFailure { return tracechain.SpanFailure(sp) }

// traceDrill — tek trace'in kırılım sonucu (render girdisi).
type traceDrill struct {
	TraceID   string
	Label     string // en yavaş | en yavaş hatalı | 2. en yavaş
	Source    string // traceSourceTempo | traceSourceCH | traceSourceAllReplicas
	Pick      drillPick
	Peer      peerRef
	Fail      drillFailure
	BaseP50Ms float64
	BaseOK    bool
	BaseErr   error
	Err       error
	NotFound  bool
	RootSvc   string // kök span'in servisi (baskın span başka servisteyse adı yazılır)
	RootName  string // v0.10.1337 — kök span adı (servis raporunun trace_list satırı)
	MinNs     int64  // span zarfı (log penceresi için)
	MaxNs     int64
	// Shape — v0.10.1373: trace şekli (tekrar eden span'ler, DB/istemci payı);
	// BuildTraceAnalysis'ten, ek okuma yok. Spans==0 → render yazmaz.
	Shape traceShapeStats
	// Scope — v0.10.1399: trace'teki hatanın kapsamı (drillFailureScope; ""
	// = hatasız ya da sınıflanamadı → render yazmaz, metin bayt bayt eski).
	Scope string
}

// v0.10.1399 (operator-reported: "operasyonu soruyorum ama bana kafka timeout
// diyor") — hata kapsamı etiketleri. Kök/SERVER span'i hatalıysa hata
// operasyonun yolundadır; tek hata kök hatasızken mesajlaşma PRODUCER/CLIENT
// span'indeyse (log/olay yayını) yan yayındır — operasyonun sebebi değil.
const (
	drillScopeOpPath      = "operasyon yolu"
	drillScopeSidePublish = "yan yayın (PRODUCER/messaging, kök hatasız)"
)

// drillMessagingSide — SAF: span bir mesajlaşma yayını mı (PRODUCER; ya da
// messaging.* taşıyan CLIENT — tracechain.PeerOf messaging türü dahil).
func drillMessagingSide(sp chstore.SpanRow) bool {
	switch strings.ToLower(sp.Kind) {
	case "producer":
		return true
	case "client":
		return tracechain.PeerOf(sp).Kind == "messaging" || tracechain.SpanAttr(sp, "messaging.system", "messaging.destination.name") != ""
	}
	return false
}

// drillFailureScope — SAF (v0.10.1399): trace'in hata kapsamı. Kök (rootID;
// boşsa ebeveyni trace'te olmayan ilk span) ya da herhangi bir SERVER span
// hatalı → drillScopeOpPath; kök hatasız ve hatalı span'lerin HEPSİ
// mesajlaşma yayını → drillScopeSidePublish; hatasız ya da karışık → "".
func drillFailureScope(spans []chstore.SpanRow, rootID string) string {
	ids := make(map[string]bool, len(spans))
	for _, sp := range spans {
		ids[sp.SpanID] = true
	}
	failed, side := 0, 0
	for _, sp := range spans {
		if sp.StatusCode != "error" {
			continue
		}
		failed++
		isRoot := sp.SpanID == rootID || (rootID == "" && (sp.ParentSpanID == "" || !ids[sp.ParentSpanID]))
		if isRoot || strings.EqualFold(sp.Kind, "server") {
			return drillScopeOpPath
		}
		if drillMessagingSide(sp) {
			side++
		}
	}
	if failed > 0 && side == failed {
		return drillScopeSidePublish
	}
	return ""
}

func traceSourceTR(src string) string {
	switch src {
	case traceSourceTempo:
		return "Tempo"
	case traceSourceCH:
		return "ClickHouse"
	case traceSourceAllReplicas:
		return "ClickHouse (tüm replikalar)"
	}
	return src
}

func fmtDrillWall(ns int64) string {
	if ns >= int64(time.Second) {
		return fmt.Sprintf("%.2f s", float64(ns)/1e9)
	}
	return fmt.Sprintf("%.0f ms", float64(ns)/1e6)
}

func fmtDrillSelf(ns int64) string {
	if ns >= int64(time.Second) {
		return fmt.Sprintf("%.1f s", float64(ns)/1e9)
	}
	return fmt.Sprintf("%.0f ms", float64(ns)/1e6)
}

// drillCallLabel — SAF: "POST" + host (adda yoksa) + " → " + yol (tracechain).
func drillCallLabel(name string, p peerRef) string { return tracechain.CallLabel(name, p) }

// renderTraceDrillTR — SAF: tek trace'in kanıt cümlesi. Hedef cümle (prod):
// "50.02 s'nin 50.0 s'i (%100) CLIENT çağrısında: POST osbprod.bank.example →
// /TibcoESB/…/DosyaListesiSorgulama (HTTP 500, java.net.SocketTimeoutException:
// Read timed out); bu operasyonun 24 sa p50'si 13 ms (×3825). Trace: <id>
// (kaynak: Tempo)". Taban yoksa ×N yazılmaz; okunamadıysa SÖYLENİR.
func renderTraceDrillTR(d traceDrill) string {
	if d.Err != nil {
		return fmt.Sprintf("Trace %s span'leri OKUNAMADI (%s) — kritik yol bilinmiyor; hedef UYDURMA.", d.TraceID, sourcestate.Classify(d.Err))
	}
	if d.NotFound || !d.Pick.OK {
		return fmt.Sprintf("Trace %s trace deposunda BULUNAMADI (Tempo/ClickHouse) — kritik yol çıkarılamadı; hedef UYDURMA.", d.TraceID)
	}
	sp := d.Pick.Span
	kind := strings.ToUpper(sp.Kind)
	where := kind + " span'inde"
	if r := drillKindRank(sp.Kind); r > 0 {
		where = kind + " çağrısında"
	} else if kind == "" {
		where = "span'de"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s'nin %s'i (%%%.0f) %s: %s", fmtDrillWall(d.Pick.WallNs), fmtDrillSelf(d.Pick.SelfNs), d.Pick.Share*100, where, drillCallLabel(sp.Name, d.Peer))
	if sp.ServiceName != "" && d.RootSvc != "" && sp.ServiceName != d.RootSvc {
		fmt.Fprintf(&b, " [servis: %s]", sp.ServiceName)
	}
	var fail []string
	if d.Fail.HTTPCode > 0 {
		fail = append(fail, fmt.Sprintf("HTTP %d", d.Fail.HTTPCode))
	}
	switch {
	case d.Fail.ExType != "" && d.Fail.ExMsg != "":
		fail = append(fail, d.Fail.ExType+": "+oneLineSample(d.Fail.ExMsg, 200))
	case d.Fail.ExType != "":
		fail = append(fail, d.Fail.ExType)
	case d.Fail.ExMsg != "":
		fail = append(fail, oneLineSample(d.Fail.ExMsg, 200))
	}
	if len(fail) == 0 && d.Fail.Status == "error" {
		fail = append(fail, "status error")
	}
	if len(fail) > 0 {
		b.WriteString(" (" + strings.Join(fail, ", ") + ")")
	}
	if d.Scope != "" { // v0.10.1399 — hata kapsamı
		b.WriteString(" [hata kapsamı: " + d.Scope + "]")
	}
	durMs := float64(spanDurNs(sp)) / 1e6
	switch {
	case d.BaseErr != nil:
		fmt.Fprintf(&b, "; bu operasyonun 24 sa p50'si OKUNAMADI (%s)", sourcestate.Classify(d.BaseErr))
	case d.BaseOK && d.BaseP50Ms > 0:
		fmt.Fprintf(&b, "; bu operasyonun 24 sa p50'si %s (×%.0f)", fmtDrillMs(d.BaseP50Ms), durMs/d.BaseP50Ms)
	}
	fmt.Fprintf(&b, ". Trace: %s (kaynak: %s)", d.TraceID, traceSourceTR(d.Source))
	if d.Shape.Spans > 0 { // v0.10.1373
		b.WriteString(". " + renderTraceShapeTR(d.Shape))
	}
	return b.String()
}

func fmtDrillMs(ms float64) string { return tracechain.FmtMs(ms) } // v0.10.1379

func spanDurNs(sp chstore.SpanRow) int64 { return tracechain.SpanDurNs(sp) } // v0.10.1379

// renderTraceDrillsTR — SAF: başlık + trace başına bir satır + paylaşılan
// hedef ("ortak hedef: osbprod.bank.example (2/2)").
func renderTraceDrillsTR(ds []traceDrill) string {
	if len(ds) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("EN YAVAŞ TRACE KRİTİK YOLU (deterministik — kritik yolda öz süresi en büyük span; 'nereye' sorusunun cevabı bu hedeftir):\n")
	count := map[string]int{}
	var order []string
	ok := 0
	for _, d := range ds {
		fmt.Fprintf(&b, "- %s: %s\n", d.Label, renderTraceDrillTR(d))
		if d.Err != nil || d.NotFound || !d.Pick.OK {
			continue
		}
		ok++
		if t := d.Peer.Target; t != "" {
			if count[t] == 0 {
				order = append(order, t)
			}
			count[t]++
		}
	}
	for _, t := range order {
		if count[t] >= 2 {
			fmt.Fprintf(&b, "ortak hedef: %s (%d/%d)\n", t, count[t], ok)
		}
	}
	return b.String()
}

// drillCandidate — kırılacak trace + etiketi.
type drillCandidate struct {
	Row   chstore.TraceRow
	Label string
}

// pickDrillTraces — SAF: en yavaş + en yavaş HATALI (farklıysa); en yavaş
// zaten hatalıysa ikinci en yavaş. En çok traceDrillMax, tekrarsız.
func pickDrillTraces(rows []chstore.TraceRow) []drillCandidate {
	if len(rows) == 0 {
		return nil
	}
	rs := append([]chstore.TraceRow(nil), rows...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].DurationMs > rs[j].DurationMs })
	out := []drillCandidate{{Row: rs[0], Label: "en yavaş"}}
	for _, r := range rs[1:] {
		if r.HasError && !rs[0].HasError && r.TraceID != rs[0].TraceID {
			return append(out, drillCandidate{Row: r, Label: "en yavaş hatalı"})
		}
	}
	for _, r := range rs[1:] {
		if r.TraceID != rs[0].TraceID {
			return append(out, drillCandidate{Row: r, Label: "2. en yavaş"})
		}
	}
	return out
}

// groupTraceLogs — SAF: tek çoklu-trace log sayfasını trace id'ye göre
// gruplar (traceIDs sırasıyla), trace başına ≤perTrace satır, önce şiddet
// (azalan) sonra zaman (artan). Listede olmayan trace'in logu atılır.
// v0.10.1370 — gövde logstore.GroupByTrace'e taşındı (exception açıklaması da
// aynı gruplamayı kullanır); burası yalnız devreder.
func groupTraceLogs(logs []*logstore.LogRecord, traceIDs []string, perTrace int) [][]*logstore.LogRecord {
	return logstore.GroupByTrace(logs, traceIDs, perTrace)
}

func drillSevTR(lg *logstore.LogRecord) string {
	if t := strings.TrimSpace(lg.SeverityText); t != "" {
		return strings.ToUpper(t)
	}
	switch n := lg.Severity; {
	case n >= 21:
		return "FATAL"
	case n >= 17:
		return "ERROR"
	case n >= 13:
		return "WARN"
	case n >= 9:
		return "INFO"
	case n >= 1:
		return "DEBUG"
	}
	return "-"
}

func drillTraceID12(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

// renderTraceLogLineTR — SAF: "KANIT: log 10:42:01 ERROR svc <gövde≤200> (trace 0af7651916cd…)".
func renderTraceLogLineTR(lg *logstore.LogRecord) string {
	return renderTraceLogLineBodyTR(lg, traceDrillLogBodyRunes)
}

// renderTraceLogLineBodyTR — SAF (v0.10.1343): gövde tavanı parametreli hâl
// (operasyon/endpoint ön-getirmesinin kırılımı model tavanına sığsın diye).
func renderTraceLogLineBodyTR(lg *logstore.LogRecord, bodyRunes int) string {
	return renderTraceLogLineBudgetTR(lg, bodyRunes, traceDrillRecordsBodyRunes)
}

// renderTraceLogLineBudgetTR — v0.10.1988/1990 (operatör: "traceRecords uzun bir JSON olabilir ama
// anlamlı; CoSRE'ye giderken gerekirse uzun prompt olsun, modelin context'i ayarlasın") — gövdesi
// traceRecords dizisinden kurulan kayıtta (istek / yanıt / müşteri / cihaz satırları) tavan
// recordsRunes: modelin bağlam penceresinden türer (traceRecordsPerLog).
func renderTraceLogLineBudgetTR(lg *logstore.LogRecord, bodyRunes, recordsRunes int) string {
	if _, ok := lg.Attributes["traceRecords"]; ok && bodyRunes < recordsRunes {
		bodyRunes = recordsRunes
	}
	body := oneLineSample(lg.Body, bodyRunes)
	if et := strings.TrimSpace(lg.Attributes["exception.type"]); et != "" && !strings.Contains(body, et) {
		body += " [exception.type=" + et + "]"
	}
	return fmt.Sprintf("KANIT: log %s %s %s %s (trace %s)",
		time.Unix(0, lg.Timestamp).UTC().Format("15:04:05"), drillSevTR(lg), lg.ServiceName, body, drillTraceID12(lg.TraceID))
}

// renderTraceDrillLogsTR — SAF: log bölümü. Depo yok / okunamadı / boş ayrı
// cümleler (okunamadı ≠ yok).
func renderTraceDrillLogsTR(noStore bool, page *logstore.Page, err error, traceIDs []string) string {
	return renderTraceDrillLogsBodyTR(noStore, page, err, traceIDs, traceDrillLogBodyRunes)
}

// renderTraceDrillLogsBodyTR — SAF (v0.10.1343): gövde tavanı parametreli hâl.
func renderTraceDrillLogsBodyTR(noStore bool, page *logstore.Page, err error, traceIDs []string, bodyRunes int) string {
	return renderTraceDrillLogsBudgetTR(noStore, page, err, traceIDs, bodyRunes, 0)
}

// traceRecordsPerLog — SAF (v0.10.1990): traceRecords gövdeli kayıt başına karakter tavanı.
// recordsTotal = bu kayıtlara ayrılan toplam bütçe (modelin penceresinden; 0 = bilinmiyor → eski
// sabit 1500). Bütçe kayıtlar arasında eşit bölünür, kayıt başına [1500, 16000] (16000 = gövdenin
// kendi tavanı, logstore.traceRecordsMaxTotal).
func traceRecordsPerLog(recordsTotal, n int) int {
	if recordsTotal <= 0 || n <= 0 {
		return traceDrillRecordsBodyRunes
	}
	per := recordsTotal / n
	if per < traceDrillRecordsBodyRunes {
		return traceDrillRecordsBodyRunes
	}
	if per > traceDrillRecordsMaxRunes {
		return traceDrillRecordsMaxRunes
	}
	return per
}

// renderTraceDrillLogsBudgetTR — renderTraceDrillLogsBodyTR + traceRecords toplam bütçesi.
func renderTraceDrillLogsBudgetTR(noStore bool, page *logstore.Page, err error, traceIDs []string, bodyRunes, recordsTotal int) string {
	switch {
	case len(traceIDs) == 0:
		return ""
	case noStore:
		return "Loglar: log deposu yapılandırılmamış — bu trace'lerin logları okunamadı.\n"
	case err != nil:
		return fmt.Sprintf("Loglar OKUNAMADI (%s) — bu 'log yok' DEMEK DEĞİL; log içeriği UYDURMA.\n", sourcestate.Classify(err))
	}
	var logs []*logstore.LogRecord
	if page != nil {
		logs = page.Logs
	}
	var b strings.Builder
	b.WriteString("O TRACE'LERİN LOGLARI (yalnız bu trace id'leri, trace zarfı ±1 dk):\n")
	groups := groupTraceLogs(logs, traceIDs, traceDrillLogsPerTrace)
	nRec := 0
	for _, g := range groups {
		for _, lg := range g {
			if _, ok := lg.Attributes["traceRecords"]; ok {
				nRec++
			}
		}
	}
	recRunes := traceRecordsPerLog(recordsTotal, nRec)
	for i, g := range groups {
		if len(g) == 0 {
			fmt.Fprintf(&b, "- trace %s: bu trace'e bağlı log kaydı YOK.\n", drillTraceID12(traceIDs[i]))
			continue
		}
		for _, lg := range g {
			b.WriteString(renderTraceLogLineBudgetTR(lg, bodyRunes, recRunes) + "\n")
		}
	}
	if len(logs) >= traceDrillLogsLimit {
		fmt.Fprintf(&b, "Not: log okuması %d kayıt tavanına ulaştı; trace başına en çok %d satır gösterildi.\n", traceDrillLogsLimit, traceDrillLogsPerTrace)
	}
	return b.String()
}

// hasPeerSignal — SAF: "nereye / hangi servise / hangi hedefe / giderken /
// downstream / kime / hangi url / hangi endpoint" — hedef soran şekil.
func hasPeerSignal(toks []string) bool {
	if tokenHasPrefix(toks, "nereye", "giderken", "downstream") {
		return true
	}
	for i, t := range toks {
		if t == "kime" {
			return true
		}
		if t == "hangi" && i+1 < len(toks) {
			n := toks[i+1]
			for _, p := range []string{"servise", "hedef", "url", "endpoint"} {
				if strings.HasPrefix(n, p) {
					return true
				}
			}
		}
	}
	return false
}

// latencyFailureStems — önceki turda gecikme/hata konuşulduğunu gösteren
// alt-dizgiler (normalize metin; "sockettimeoutexception" içindeki "timeout"
// da yakalanır).
var latencyFailureStems = []string{"timeout", "timed out", "zaman aşım", "exception", "istisna",
	"yavaş", "latency", "gecikme", "slow", "p99"}

// prevTurnMentionsLatencyFailure — SAF: aktif soru HARİÇ en yakın asistan
// cevabı ya da bir önceki kullanıcı sorusu timeout/exception/yavaşlık andı mı;
// ya da önceki rota kök-neden / yavaş-trace / drill miydi.
func prevTurnMentionsLatencyFailure(msgs []copilot.ChatMessage, lastIntent guidedIntent) bool {
	switch lastIntent {
	case guidedRootCause, guidedSlowTraces, guidedTraceDrill:
		return true
	}
	seenLast, seenAsst, seenUser := false, false, false
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		txt := strings.TrimSpace(m.Text)
		if txt == "" {
			continue
		}
		if m.Role == "user" && !seenLast {
			seenLast = true
			continue
		}
		if !seenLast {
			continue
		}
		switch {
		case m.Role == "assistant" && !seenAsst:
			seenAsst = true
		case m.Role == "user" && !seenUser:
			seenUser = true
		default:
			continue
		}
		norm := normalizeGuidedMsg(txt)
		for _, st := range latencyFailureStems {
			if strings.Contains(norm, st) {
				return true
			}
		}
		if seenAsst && seenUser {
			break
		}
	}
	return false
}

// traceDrillOverridable — hedef sorusunun ezebileceği rotalar: Log/hata/sağlık
// kademeleri (prod'da soru log_errors'a düşüp "son 500 hata logunda yok"
// dedi). Daha spesifik rotalar (trace id, çift, endpoint, yavaş-trace,
// kök-neden — son ikisi zaten kırılım taşır) dokunulmaz.
var traceDrillOverridable = map[guidedIntent]bool{
	guidedNone: true, guidedLogErrors: true, guidedProblems: true, guidedServiceHealth: true,
	guidedServiceExceptions: true, guidedFindEntity: true,
}

// traceDrillRoute — SAF: hedef sinyali + servis (sorudan ya da önceki
// turdan) + önceki turda gecikme/hata → trace_drill. Geçmiş yoksa rota AYNEN.
func traceDrillRoute(route guidedRoute, norm, inheritedSvc string, prevLatency bool) (guidedRoute, bool) {
	if !prevLatency || !traceDrillOverridable[route.Intent] || !hasPeerSignal(guidedTokens(norm)) {
		return route, false
	}
	svc := route.Service
	if svc == "" {
		svc = inheritedSvc
	}
	if svc == "" {
		return route, false
	}
	return guidedRoute{Intent: guidedTraceDrill, Service: svc, Env: route.Env}, true
}

// intentTraceDrillUpgrade — SAF: sınıflandırıcı log_errors dedi ama soru
// hedef soruyor ("payments hangi servise giderken hata alıyor") → trace_drill.
func intentTraceDrillUpgrade(route guidedRoute, question string) guidedRoute {
	if route.Intent != guidedLogErrors || route.Service == "" {
		return route
	}
	if !hasPeerSignal(guidedTokens(normalizeGuidedMsg(question))) {
		return route
	}
	return guidedRoute{Intent: guidedTraceDrill, Service: route.Service, Env: route.Env}
}

// hasLatencyAsk — SAF: soru yavaşlık/timeout soruyor mu (kök-neden demetinin
// kırılım kapısı).
func hasLatencyAsk(norm string) bool {
	if hasSlowTraceSignal(norm) {
		return true
	}
	if tokenHasPrefix(guidedTokens(norm), "yavaş", "yavas", "slow", "gecikme", "latency", "p99", "p95", "takıl") {
		return true
	}
	for _, s := range []string{"timeout", "timed out", "zaman aşım"} {
		if strings.Contains(norm, s) {
			return true
		}
	}
	return false
}

// rootCauseWantsTraceDrill — SAF: kök-neden demeti kırılımı YALNIZ p99
// tabana göre yükseldiyse (≥1.5×) ya da soru yavaşlık/timeout soruyorsa koşar.
func rootCauseWantsTraceDrill(cur, base aiRED, curErr error, slowAsk bool) bool {
	if slowAsk {
		return true
	}
	return curErr == nil && cur.Spans > 0 && base.P99Ms > 0 && cur.P99Ms >= 1.5*base.P99Ms
}

// ─── Sunucu yarısı ──────────────────────────────────────────────────────

// traceDrillFor — tek trace: Tempo-öncelikli span çözümü, analiz, baskın span,
// 24 sa p50 tabanı. Emit ETMEZ (goroutine'den çağrılır).
func (s *Server) traceDrillFor(ctx context.Context, c drillCandidate) traceDrill {
	d := s.traceDrillCore(ctx, c)
	if !d.Pick.OK {
		return d
	}
	if s.store != nil && d.Pick.Span.Name != "" {
		end := time.Unix(0, d.Pick.Span.EndTime)
		if d.Pick.Span.EndTime <= 0 {
			end = time.Now()
		}
		red, berr := s.store.SpanOperationRED(ctx, d.Pick.Span.ServiceName, d.Pick.Span.Name, end.Add(-traceDrillBaselineWin), end)
		switch {
		case berr != nil:
			d.BaseErr = berr
		case red.Calls > 0 && red.P50Ms > 0:
			d.BaseP50Ms, d.BaseOK = red.P50Ms, true
		}
	}
	return d
}

// traceDrillCore — v0.10.1374: traceDrillFor'un taban okumasız gövdesi (span
// çözümü, analiz, şekil, baskın span, hedef, hata) — traceDrillFor'dan MEKANİK
// ayrıldı (metin bayt bayt aynı). Küme kıyası (chat_cluster_compare.go) ≤8
// trace kırar ve 24 sa p50 tabanını yazmaz; tabanın ≤8 ek MV okuması boşa
// gitmesin diye bunu çağırır. Emit ETMEZ.
func (s *Server) traceDrillCore(ctx context.Context, c drillCandidate) traceDrill {
	d := traceDrill{TraceID: c.Row.TraceID, Label: c.Label}
	spans, src, err := s.resolveTraceSpans(ctx, c.Row.TraceID)
	if err != nil {
		d.Err = err
		return d
	}
	if len(spans) == 0 {
		d.NotFound = true
		return d
	}
	d.Source = src
	for _, sp := range spans {
		if sp.StartTime > 0 && (d.MinNs == 0 || sp.StartTime < d.MinNs) {
			d.MinNs = sp.StartTime
		}
		if sp.EndTime > d.MaxNs {
			d.MaxNs = sp.EndTime
		}
	}
	an := chstore.BuildTraceAnalysis(spans, false)
	for i := range spans {
		if spans[i].SpanID == an.RootSpanID {
			d.RootSvc, d.RootName = spans[i].ServiceName, spans[i].Name
			break
		}
	}
	d.Shape = traceShape(spans, an)                   // v0.10.1373 — aynı analizden, ek I/O yok
	d.Scope = drillFailureScope(spans, an.RootSpanID) // v0.10.1399 — ek I/O yok
	d.Pick = dominantSpan(spans, an)
	if !d.Pick.OK {
		return d
	}
	d.Peer, d.Fail = peerOf(d.Pick.Span), spanFailure(d.Pick.Span)
	return d
}

// traceDrillEvidence — adayları paralel kırar (goroutine'ler emit etmez),
// sonra TEK log okuması; adım çipleri + trace_list bloğu yalnız buradan.
func (s *Server) traceDrillEvidence(ctx context.Context, emit func(string, any), cands []drillCandidate, service string, from, to time.Time) string {
	return s.traceDrillEvidenceBody(ctx, emit, cands, service, from, to, traceDrillLogBodyRunes)
}

// traceDrillEvidenceBody — v0.10.1343: log gövdesi tavanı parametreli hâl;
// operasyon/endpoint ön-getirmesi opDrillLogBodyRunes ile çağırır.
func (s *Server) traceDrillEvidenceBody(ctx context.Context, emit func(string, any), cands []drillCandidate, service string, from, to time.Time, bodyRunes int) string {
	if len(cands) == 0 {
		return ""
	}
	dctx, cancel := context.WithTimeout(ctx, traceDrillBudget)
	defer cancel()
	steps := make([]int, len(cands))
	for i, c := range cands {
		args, _ := json.Marshal(map[string]any{"trace_id": c.Row.TraceID, "pick": c.Label})
		steps[i] = emitGuidedStep(emit, "trace_drill", string(args))
	}
	res := make([]traceDrill, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c drillCandidate) {
			defer wg.Done()
			res[i] = s.traceDrillFor(dctx, c)
		}(i, c)
	}
	wg.Wait()
	ids := make([]string, 0, len(res))
	var minNs, maxNs int64
	for i, d := range res {
		emitGuidedStepResult(emit, steps[i], "trace_drill", renderTraceDrillTR(d), d.Err)
		ids = append(ids, d.TraceID)
		lo, hi := d.MinNs, d.MaxNs
		if lo == 0 { // span okunamadı: liste satırının zarfı
			lo = cands[i].Row.StartTime
			hi = lo + int64(cands[i].Row.DurationMs*1e6)
		}
		if lo > 0 && (minNs == 0 || lo < minNs) {
			minNs = lo
		}
		if hi > maxNs {
			maxNs = hi
		}
	}
	largs, _ := json.Marshal(map[string]any{"trace_ids": ids, "limit": traceDrillLogsLimit})
	nLog := emitGuidedStep(emit, "trace_drill_logs", string(largs))
	page, lerr := s.readDrillLogs(dctx, ids, minNs, maxNs, from, to)
	logText := renderTraceDrillLogsBudgetTR(s.logs == nil, page, lerr, ids, bodyRunes, s.traceRecordsBudget(ctx))
	emitGuidedStepResult(emit, nLog, "trace_drill_logs", logText, lerr)
	rows := make([]chstore.TraceRow, 0, len(cands))
	for _, c := range cands {
		rows = append(rows, c.Row)
	}
	href := "/traces?sort=duration"
	if service != "" {
		href = "/traces?service=" + url.QueryEscape(service) + "&sort=duration"
	}
	emit("trace_list", endpointTraceListPayload("en yavaş trace kritik yolu", rows, from, to, href))
	return composeDrillEvidenceTR(res, logText)
}

// composeDrillEvidenceTR — SAF (v0.10.1343'te traceDrillEvidence'tan
// çıkarıldı; metin bayt bayt aynı): kırılımlar + log bölümü + kural satırı.
// Operasyon/endpoint ön-getirmesi bu metni ikinci sentetik tool sonucu
// olarak verir; tavan pini TestOpPrefetchDrillUnderModelClamp.
func composeDrillEvidenceTR(res []traceDrill, logText string) string {
	var b strings.Builder
	b.WriteString(renderTraceDrillsTR(res))
	b.WriteString(logText)
	b.WriteString("KURAL: Kritik yol satırları HESAPLANMIŞ ölçümdür — hedefi, süreyi ve hatayı aynen aktar; log satırı yoksa log içeriği UYDURMA.\n")
	if drillsHaveSidePublish(res) { // v0.10.1399 — yalnız yan yayın hatası varsa (aksi hâlde metin bayt bayt eski)
		b.WriteString(drillSidePublishRuleTR)
	}
	return b.String()
}

// drillSidePublishRuleTR — v0.10.1399: yan yayın hatasının kuralı.
const drillSidePublishRuleTR = "KURAL: \"hata kapsamı: yan yayın\" hatası (kök hatasızken mesajlaşma PRODUCER/CLIENT span'i) operasyonun hata sebebi DEĞİLDİR; sebep diye sunma, yan bulgu olarak an.\n"

// drillsHaveSidePublish — SAF: kırılımlardan biri yan yayın kapsamlı mı.
func drillsHaveSidePublish(res []traceDrill) bool {
	for _, d := range res {
		if d.Scope == drillScopeSidePublish {
			return true
		}
	}
	return false
}

// readDrillLogs — kırılan trace'lerin TEK çoklu-trace log okuması (zarf ±1 dk;
// zarf bilinmiyorsa sohbet penceresi). s.logs yapılandırılmış depo (Ayarlar →
// ES ise ES); depo yoksa (nil, nil) — çağıran "depo yok" cümlesini kurar.
// v0.10.1337 — servis raporu da AYNI yolu kullanır (ikinci log okuyucu yok).
func (s *Server) readDrillLogs(dctx context.Context, ids []string, minNs, maxNs int64, from, to time.Time) (*logstore.Page, error) {
	if s.logs == nil || len(ids) == 0 {
		return nil, nil
	}
	lf, lt := from, to
	if minNs > 0 {
		lf, lt = time.Unix(0, minNs).Add(-time.Minute), time.Unix(0, maxNs).Add(time.Minute)
	}
	return logstore.SearchWithTimeout(dctx, s.logs, logstore.Filter{TraceIDs: ids, From: lf, To: lt, Limit: traceDrillLogsLimit}, traceDrillLogsTimeout)
}

// slowTraceDrill — en yavaş trace listesi (guidedTraceFilter) + kırılım.
func (s *Server) slowTraceDrill(ctx context.Context, emit func(string, any), service, env string, from, to time.Time) ([]chstore.TraceRow, string, error) {
	rows, _, _, err := s.store.GetTraces(ctx, guidedTraceFilter(service, env, from, to))
	if err != nil {
		return nil, "", err
	}
	return rows, s.traceDrillEvidence(ctx, emit, pickDrillTraces(rows), service, from, to), nil
}

// guidedTraceDrillBundle — trace_drill rotasının kanıtı: en yavaş trace listesi
// + en yavaş 1–2 trace'in kritik yolu + yalnız o trace'lerin logları.
func (s *Server) guidedTraceDrillBundle(ctx context.Context, emit func(string, any), service, env string, from, to time.Time, rangeS int64) (string, string, error) {
	n := emitGuidedStep(emit, "slow_traces", withEnvArg(`{"service":"`+service+`","sort":"duration"}`, env))
	rows, _, _, err := s.store.GetTraces(ctx, guidedTraceFilter(service, env, from, to))
	if err != nil {
		emitGuidedStepResult(emit, n, "slow_traces", "", err)
		return "", "", err
	}
	list := renderSlowTracesEvidenceTR(rows, service, env, rangeS)
	emitGuidedStepResult(emit, n, "slow_traces", list, nil)
	var b strings.Builder
	fmt.Fprintf(&b, "SORU ŞEKLİ: hedef — %q servisinin yavaş/hatalı isteği NEREYE giderken takıldı.\n\n", service)
	b.WriteString(list)
	if drill := s.traceDrillEvidence(ctx, emit, pickDrillTraces(rows), service, from, to); drill != "" {
		b.WriteString("\n" + drill)
	}
	src := fmt.Sprintf("en yavaş trace kritik yolu + o trace'lerin logları (son %s)", fmtAgoTR(rangeS))
	if env != "" {
		src += fmt.Sprintf(", ortam: %s", env)
	}
	return b.String(), src, nil
}

// guidedTraceDrillOverride — copilotChatGuided'ın geçmiş-farkında yarısı:
// servis sorudan, ekrandan, sohbet bağlamından ya da en yakın önceki kullanıcı
// sorusundan devralınır; önceki tur gecikme/hata anmadıysa rota DEĞİŞMEZ.
func (s *Server) guidedTraceDrillOverride(ctx context.Context, route guidedRoute, norm string, msgs []copilot.ChatMessage, prior []string, ctxService string, svcNames, envNames []string) (guidedRoute, bool) {
	inherited := ctxService
	if st := chatContextFromCtx(ctx); st != nil {
		if inherited == "" {
			inherited = st.ctx.Service
		}
		if lr := st.ctx.LastRoute; lr != nil && inherited == "" {
			inherited = lr.Service
		}
	}
	if inherited == "" {
		inherited = latestPriorService(prior, svcNames, envNames)
	}
	return traceDrillRoute(route, norm, inherited, prevTurnMentionsLatencyFailure(msgs, chatLastIntent(ctx)))
}

// chatLastIntent — sohbet bağlamının son rotası ("" = yok).
func chatLastIntent(ctx context.Context) guidedIntent {
	if st := chatContextFromCtx(ctx); st != nil && st.ctx.LastRoute != nil {
		return st.ctx.LastRoute.Intent
	}
	return guidedNone
}

// latestPriorService — SAF: priorUserTexts YENİDEN ESKİYE sıralı; ilk servis
// adı taşıyan önceki soru kazanır.
func latestPriorService(prior []string, services, envs []string) string {
	for _, p := range prior {
		if svc := extractServiceEntity(normalizeGuidedMsg(p), services, envs); svc != "" {
			return svc
		}
	}
	return ""
}

// traceRecordsBudget — v0.10.1990: traceRecords gövdelerine ayrılan toplam karakter = modelin bağlam
// penceresinin %35'i (token ≈ 4 karakter). Pencere bilinmiyorsa 0 (kayıt başı eski sabit 1500).
func (s *Server) traceRecordsBudget(ctx context.Context) int {
	if s == nil || s.copilot == nil {
		return 0
	}
	w := modelcaps.ContextWindow(s.copilot.ModelFor(ctx))
	if w <= 0 {
		return 0
	}
	return w * 4 * 35 / 100
}
