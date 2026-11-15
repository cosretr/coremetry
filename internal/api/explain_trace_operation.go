package api

// explain_trace_operation.go — v0.10.1329 (operatör-bildirimli): trace'in
// ANA operasyonu, kesin kaynaktan.
//
// Olay: trace 6008dded… kökü POST /ACMWEB/application/expressOrderPreValidate,
// kendi Oracle satırı LOAN_…_APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST;
// Trace sayfası çipi ise fonksiyon kodu sözlüğünün (bankanın tamamında son 7
// günde en çok satırlı aday) LOAN_…_APPLICATIONINQ_… adını "(+15)" ile "bu
// trace'in operasyonu" gibi gösteriyordu. Frontend düzeltmesi
// (lib/oracleFunctionOps.ts pickTraceOperation) önce trace'in KENDİ Oracle
// satırlarını okur; bu dosya aynı öncelik sırasının backend ikizi.
//
// Bu sürümde YALNIZ dikiş: traceExplainInput'a MainOperation /
// OtherOperations / OperationSource / RootVersion girer ama prompt'a girmez
// (TestTraceExplainUserGolden bayt-bayt aynı kalır). Sözlük BİLEREK yok:
// 1:n tahmin modele "bu trace'in operasyonu" diye verilmez.

import (
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// traceOperationSpanKeys — span attribute'unda operasyon kodunu taşıyan
// yazımlar, öncelik sırasıyla (FE OPERATION_CODE_KEYS ile aynı liste).
var traceOperationSpanKeys = []string{"operation.code", "OPERATION_CODE", "operation_code"}

// Operasyonun kaynağı — FE TraceOperationPick.source ile aynı yazım.
const (
	traceOpSourceOracle = "oracle"
	traceOpSourceSpan   = "span"
	// traceOpSourceLog — v0.10.1349: trace'in KENDİ log kaydındaki operasyon
	// kodu attribute'u (kesin; explain'in zaten okuduğu loglardan).
	traceOpSourceLog = "log"
)

func spanOperationCode(sp chstore.SpanRow) string {
	for _, k := range traceOperationSpanKeys {
		if v := strings.TrimSpace(sp.Attributes[k]); v != "" {
			return v
		}
	}
	return ""
}

func isEntrySpanKind(kind string) bool {
	k := strings.ToLower(kind)
	return k == "server" || k == "consumer"
}

// pickMainOperation — SAF. Öncelik:
//
//	(a) oracle — bu trace'in Oracle satırlarındaki tekil OperationCode.
//	    Sıra: satırı kök servisin bir span'ine bağlı olan önce → en çok
//	    satırlı → en erken satır → ad (determinizm). others = BU trace'teki
//	    diğer gerçek operasyonlar.
//	(b) span — kök span'in, sonra giriş (server/consumer) span'lerinin,
//	    sonra herhangi bir span'in operasyon attribute'u (zaman sırasıyla);
//	    others = span'lerdeki diğer tekil değerler.
//	(c) log — v0.10.1349: bu trace'in log kayıtlarında operasyon kodu
//	    attribute'u (logOperationCode); en çok kayıtta geçen → en erken →
//	    ad. others = loglardaki diğer tekil değerler.
//	(d) hiçbiri → "", nil, "".
//
// (a)/(b) v0.10.1329'daki gibi bayt bayt; (c) yalnız ikisi de boşken.
func pickMainOperation(rows []chstore.OracleErrorRow, spans []chstore.SpanRow, logs []*logstore.LogRecord) (main string, others []string, source string) {
	var rootSvc string
	if len(spans) > 0 {
		rootSvc = traceRootSpan(spans).ServiceName
	}
	rootSvcSpans := map[string]bool{}
	for _, sp := range spans {
		if sp.ServiceName == rootSvc && sp.SpanID != "" {
			rootSvcSpans[sp.SpanID] = true
		}
	}
	type opGroup struct {
		op         string
		rows       int
		first      int64
		rootLinked bool
	}
	groups := map[string]*opGroup{}
	for _, r := range rows {
		op := strings.TrimSpace(r.OperationCode)
		if op == "" {
			continue
		}
		t := r.Time.UnixNano()
		g := groups[op]
		if g == nil {
			g = &opGroup{op: op, first: t}
			groups[op] = g
		}
		g.rows++
		if t < g.first {
			g.first = t
		}
		if r.SpanID != "" && rootSvcSpans[r.SpanID] {
			g.rootLinked = true
		}
	}
	if len(groups) > 0 {
		ranked := make([]*opGroup, 0, len(groups))
		for _, g := range groups {
			ranked = append(ranked, g)
		}
		sort.Slice(ranked, func(i, j int) bool {
			a, b := ranked[i], ranked[j]
			if a.rootLinked != b.rootLinked {
				return a.rootLinked
			}
			if a.rows != b.rows {
				return a.rows > b.rows
			}
			if a.first != b.first {
				return a.first < b.first
			}
			return a.op < b.op
		})
		for _, g := range ranked[1:] {
			others = append(others, g.op)
		}
		return ranked[0].op, others, traceOpSourceOracle
	}
	if len(spans) == 0 {
		return pickLogOperation(logs)
	}
	ordered := append([]chstore.SpanRow(nil), spans...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartTime < ordered[j].StartTime })
	pick := spanOperationCode(traceRootSpan(spans))
	if pick == "" {
		for _, sp := range ordered {
			if isEntrySpanKind(sp.Kind) {
				if pick = spanOperationCode(sp); pick != "" {
					break
				}
			}
		}
	}
	if pick == "" {
		for _, sp := range ordered {
			if pick = spanOperationCode(sp); pick != "" {
				break
			}
		}
	}
	if pick == "" {
		return pickLogOperation(logs)
	}
	seen := map[string]bool{pick: true}
	for _, sp := range ordered {
		if c := spanOperationCode(sp); c != "" && !seen[c] {
			seen[c] = true
			others = append(others, c)
		}
	}
	return pick, others, traceOpSourceSpan
}

// ── v0.10.1349 (operatör-bildirimli) ─────────────────────────────────────
//
// Olay: HATASIZ bir trace'te "Kodu da incele" Code Search açıkken bile "⚠ Kod
// okunamadı — kodsuz analiz. (bu kayıtta stacktrace yok)" diyordu. Ana
// operasyon yalnız Oracle HATA satırlarından (sağlıklı trace'te yok) ve
// span'in operation.code attribute'undan okunuyordu; ACM span'leri ise
// FUNCTION_CODE taşır. İki ek: (c) log tier'ı (kesin) ve fonksiyon kodu
// listesi (sözlük adayları için — copilot_code_operation.go).

// pickLogOperation — SAF: pickMainOperation'ın (c) log tier'ı.
func pickLogOperation(logs []*logstore.LogRecord) (main string, others []string, source string) {
	type g struct {
		op    string
		n     int
		first int64
	}
	groups := map[string]*g{}
	for _, lg := range logs {
		if lg == nil {
			continue
		}
		op := logOperationCode(lg.Attributes)
		if op == "" {
			continue
		}
		e := groups[op]
		if e == nil {
			e = &g{op: op, first: lg.Timestamp}
			groups[op] = e
		}
		e.n++
		if lg.Timestamp < e.first {
			e.first = lg.Timestamp
		}
	}
	if len(groups) == 0 {
		return "", nil, ""
	}
	ranked := make([]*g, 0, len(groups))
	for _, e := range groups {
		ranked = append(ranked, e)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.n != b.n {
			return a.n > b.n
		}
		if a.first != b.first {
			return a.first < b.first
		}
		return a.op < b.op
	})
	for _, e := range ranked[1:] {
		others = append(others, e.op)
	}
	return ranked[0].op, others, traceOpSourceLog
}

// normOpKey — SAF: anahtar → büyük harf, "_" "." "-" soyulmuş.
func normOpKey(k string) string {
	return strings.NewReplacer("_", "", ".", "", "-", "").Replace(strings.ToUpper(k))
}

// logOperationCode — SAF, tablo-testli: log attribute'larında operasyon kodu.
// Anahtar normalize edilince (normOpKey) OPERATIONCODE ya da OPERATIONNAME
// ile bitmeli (operation_code, labels.OPERATION_CODE, operationName, …) ve
// değer devops.ValidOperationCode'dan geçmeli ("GET /x" gibi serbest bir
// operation.name elenir). Öncelik: …CODE anahtarları …NAME'den önce, sonra
// anahtar adı (harita sırası determinizmi bozmasın).
func logOperationCode(attrs map[string]string) string {
	keys := make([]string, 0, 4)
	for k := range attrs {
		n := normOpKey(k)
		if strings.HasSuffix(n, "OPERATIONCODE") || strings.HasSuffix(n, "OPERATIONNAME") {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		ci, cj := strings.HasSuffix(normOpKey(keys[i]), "CODE"), strings.HasSuffix(normOpKey(keys[j]), "CODE")
		if ci != cj {
			return ci
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		if v := strings.TrimSpace(attrs[k]); devops.ValidOperationCode(v) {
			return v
		}
	}
	return ""
}

// traceFunctionCodeKeys — span'de fonksiyon kodunu taşıyan yazımlar (FE
// FUNCTION_CODE_KEYS ile aynı ikili).
var traceFunctionCodeKeys = []string{"FUNCTION_CODE", "function_code"}

// traceFunctionCodeMax — sözlük sorgusuna giden kod tavanı (FE ile aynı 5).
const traceFunctionCodeMax = 5

// traceFunctionCodes — SAF: FE lib/oracleFunctionOps.ts traceFunctionCodes'un
// Go ikizi, KANIT sırasıyla: kök span → giriş (server/consumer) span'leri →
// diğerleri (ikisi de zaman sırasıyla); tekil, ≤ traceFunctionCodeMax.
func traceFunctionCodes(spans []chstore.SpanRow) []string {
	if len(spans) == 0 {
		return nil
	}
	ordered := append([]chstore.SpanRow(nil), spans...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartTime < ordered[j].StartTime })
	root := traceRootSpan(spans)
	var out []string
	seen := map[string]bool{}
	add := func(sp chstore.SpanRow) bool {
		for _, k := range traceFunctionCodeKeys {
			c := strings.TrimSpace(sp.Attributes[k])
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
			if len(out) >= traceFunctionCodeMax {
				return true
			}
		}
		return false
	}
	if add(root) {
		return out
	}
	for _, sp := range ordered {
		if isEntrySpanKind(sp.Kind) && add(sp) {
			return out
		}
	}
	for _, sp := range ordered {
		if add(sp) {
			return out
		}
	}
	return out
}
