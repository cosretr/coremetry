package api

// chat_operation_prefetch.go — CoSRE operasyon tespiti Faz 2. Kullanıcı
// sohbete bir operasyon adı yazınca, model ÇAĞRILMADAN önce:
//   1. mesaj belirteçleri cosre_operation_catalog'la eşlenir (birebir →
//      harf duyarsız → önek/içerir; <4 karakter belirteç yok sayılır;
//      bulanık aşama yalnız tanımlayıcı-biçimli belirteçte — "servis" gibi
//      sıradan kelime operasyon adının içinde geçip tetiklemesin);
//   2. tek operasyon → sohbet penceresi (varsayılan 1 sa) için: ≤20 trace
//      (önce hatalılar), hata oranı / p99 / çağrı + önceki eşit pencereye
//      fark, trace'lere TRACEID ile bağlı Oracle teknik hataları;
//   3. özet modele HAM trace değil, kompakt metin olarak gider: serbest
//      döngünün konuşmasına sentetik bir tool çağrısı + sonucu eklenir
//      (model getirildiğini bilir, gerekirse başka tool çağırır);
//   4. birden çok operasyon → seçim çipleri, tahmin yok; eşleşme yok →
//      normal akış (bayt bayt eski).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

const (
	opTokenMin        = 4
	opTokenMax        = 12
	opCandidateLimit  = 60
	opChoiceMax       = 5
	opTraceLimit      = 20
	opDefaultRangeS   = 3600
	opPrefetchToolID  = "cosre-operation-prefetch"
	opPrefetchTool    = "operation_prefetch"
	opPrefetchTimeout = 15 * time.Second
	// v0.10.1331 — BSA/Oracle operasyonunda Exceptions grupları (chat_exception_groups.go):
	// ayrı adım çipi + operatör onaylı 24 sa geriye bakış (Oracle grubu durgun akış).
	opExceptionsTool     = "operation_exceptions"
	opExceptionsLookback = 24 * time.Hour
)

// opCodeShapeRe — v0.10.1331: BSA operasyon kodu biçimi (BÜYÜK_HARF_ALT_ÇİZGİ,
// en az iki parça) — katalogda Oracle satırı olmasa da Exceptions'a bakılır.
var opCodeShapeRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)

// opWantsExceptions — SAF: hedef BSA/Oracle operasyonu mu.
func opWantsExceptions(t opTarget) bool {
	return t.HasOra || opCodeShapeRe.MatchString(strings.TrimSpace(t.Name))
}

// opTokens — SAF: mesajdan aday belirteçler (tekil, sırayı korur). Ayraç:
// boşluk ve noktalama; '_' '-' '.' '/' ':' tanımlayıcının parçası sayılır.
func opTokens(msg string) []string {
	parts := strings.FieldsFunc(msg, func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		switch r {
		case '_', '-', '.', '/', ':':
			return false
		}
		return true
	})
	out := []string{}
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.Trim(p, "-.:/")
		// v0.10.1343 — HTTP fiili belirteç değil: "POST" identifierLike sayılmasa da
		// katalog adaylarını (OR, LIMIT 60, çağrı sırası) her POST operasyonuyla doldururdu.
		if len([]rune(p)) < opTokenMin || seen[p] || isHTTPVerb(p) {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) == opTokenMax {
			break
		}
	}
	return out
}

// identifierLike — SAF: bulanık eşleşmeye aday mı (alt çizgi / rakam /
// nokta / eğik çizgi ya da iç büyük harf taşıyan belirteç).
func identifierLike(t string) bool {
	if strings.ContainsAny(t, "_./:0123456789") {
		return true
	}
	upper := 0
	for i, r := range t {
		if i > 0 && unicode.IsUpper(r) {
			upper++
		}
	}
	return upper > 0
}

// opMatch — eşleyici sonucu.
type opMatch struct {
	Kind string   // exact | case_insensitive | fuzzy | ""
	Ops  []string // tekil operasyon adları, çağrı sayısına göre
}

// matchOperations — SAF: belirteçler × katalog adayları. İlk dolu aşama kazanır.
func matchOperations(tokens []string, cands []chstore.CatalogOperation) opMatch {
	calls := map[string]uint64{}
	for _, c := range cands {
		calls[c.Operation] += c.CallCount
	}
	stage := func(pred func(tok, op string) bool, fuzzyOnly bool) []string {
		hit := map[string]bool{}
		for _, t := range tokens {
			if fuzzyOnly && !identifierLike(t) {
				continue
			}
			for op := range calls {
				if pred(t, op) {
					hit[op] = true
				}
			}
		}
		out := make([]string, 0, len(hit))
		for op := range hit {
			out = append(out, op)
		}
		sort.Slice(out, func(i, j int) bool {
			if calls[out[i]] != calls[out[j]] {
				return calls[out[i]] > calls[out[j]]
			}
			return out[i] < out[j]
		})
		return out
	}
	if ops := stage(func(t, op string) bool { return t == op }, false); len(ops) > 0 {
		return opMatch{Kind: "exact", Ops: ops}
	}
	if ops := stage(strings.EqualFold, false); len(ops) > 0 {
		return opMatch{Kind: "case_insensitive", Ops: ops}
	}
	if ops := stage(func(t, op string) bool {
		lt, lo := strings.ToLower(t), strings.ToLower(op)
		return strings.HasPrefix(lo, lt) || strings.Contains(lo, lt)
	}, true); len(ops) > 0 {
		return opMatch{Kind: "fuzzy", Ops: ops}
	}
	return opMatch{}
}

// opTarget — tek eşleşen operasyonun okuma hedefi (katalog satırlarından).
type opTarget struct {
	Name     string
	Services []string // span kaynağı servisleri
	Codes    []string // Oracle fonksiyon kodları
	HasSpan  bool
	HasOra   bool
	// v0.10.1343 — endpoint (http_route) hedefi: katalogdaki `endpoint` satırı
	// ya da endpoint anma aşamasının seçtiği grup (chat_endpoint_prefetch.go).
	Route        string
	Method       string
	HasRoute     bool
	Endpoint     bool // endpoint anma aşamasından geldi
	Alternatives []opAlternative
	// SearchPath — v0.10.1373 (operator-reported): anılan yol katalogda YOK
	// (rota yalnız url.path/http.target'ta ya da katalog okuması düştü) →
	// yol-yalnız hedef: RED ölçülmez, trace'ler yol metniyle aranır
	// (endpointPathOnlyTarget). "" = katalog hedefi (bayt bayt eski yol).
	SearchPath string
	// Envs — v0.10.1374: mesajın andığı ≥2 canlı ortam (endpointEnvSet; yalnız
	// endpoint anma aşaması doldurur) → küme kıyası (chat_cluster_compare.go).
	// nil = kıyas yok (bayt bayt eski yol).
	Envs []string
	// MethodAsked — v0.10.1376: Method sorudaki fiilden mi geldi (span adından
	// türetilen fiil değil). Küme kıyasının VM sorgusu http_request_method
	// süzgecini YALNIZ bu durumda ekler (operatör varsayılanı).
	MethodAsked bool
	// ErrorAsk — v0.10.1399: soru "operasyon neden hata veriyor" şekilli
	// (opErrorWhyAsk). Yalnız sohbet ön-getirmesi doldurur; çekmece endpoint
	// kanıtı (drawerEndpointEvidence) false bırakır → paket bayt bayt eski.
	ErrorAsk bool
}

// opTargetFor — SAF.
func opTargetFor(name string, cands []chstore.CatalogOperation) opTarget {
	t := opTarget{Name: name}
	seenS, seenC := map[string]bool{}, map[string]bool{}
	for _, c := range cands {
		if c.Operation != name {
			continue
		}
		switch c.Source {
		case "span":
			t.HasSpan = true
			if c.Service != "" && !seenS[c.Service] {
				seenS[c.Service] = true
				t.Services = append(t.Services, c.Service)
			}
		case "endpoint": // v0.10.1343 — spanmetrics_1m http_route satırı
			t.HasRoute, t.Route = true, c.Operation
			if c.Service != "" && !seenS[c.Service] {
				seenS[c.Service] = true
				t.Services = append(t.Services, c.Service)
			}
		case "oracle":
			t.HasOra = true
			for _, fc := range c.FunctionCodes {
				if fc != "" && !seenC[fc] && len(t.Codes) < 5 {
					seenC[fc] = true
					t.Codes = append(t.Codes, fc)
				}
			}
		}
	}
	return t
}

// opPrefetch — getirilen paket (modele özet + Faz 3 kartı).
type opPrefetch struct {
	Operation  string                     `json:"operation"`
	MatchKind  string                     `json:"matchKind"`
	Services   []string                   `json:"services,omitempty"`
	Codes      []string                   `json:"functionCodes,omitempty"`
	RangeS     int64                      `json:"rangeS"`
	From       time.Time                  `json:"from"`
	To         time.Time                  `json:"to"`
	Current    chstore.OpRED              `json:"current"`
	Previous   chstore.OpRED              `json:"previous"`
	Traces     []opTraceRow               `json:"traces"`
	Oracle     []chstore.OracleErrorCount `json:"oracleErrors"`
	TracesHref string                     `json:"tracesHref"`
	// Exceptions — v0.10.1331: operasyonun Exceptions grupları (nil = bakılmadı).
	Exceptions *exceptionGroupsEvidence `json:"exceptions,omitempty"`
	// v0.10.1343 — endpoint hedefi (chat_endpoint_prefetch.go). Hepsi omitempty:
	// endpoint dışı operasyonun yükü bayt bayt eski.
	Route                 string                `json:"route,omitempty"`
	Method                string                `json:"method,omitempty"`
	EndpointHref          string                `json:"endpointHref,omitempty"`
	EndpointExceptions    []opEndpointException `json:"endpointExceptions,omitempty"`
	EndpointExceptionsErr string                `json:"endpointExceptionsError,omitempty"`
	// EndpointExceptionsChecked — bakıldı (boş liste omitempty ile düşer; "yok" ≠ "bakılmadı").
	EndpointExceptionsChecked bool            `json:"endpointExceptionsChecked,omitempty"`
	Alternatives              []opAlternative `json:"alternatives,omitempty"`
	// ClusterCompare — v0.10.1374: ≥2 ortam anan endpoint sorusunda ortam başına
	// RED + en yavaş trace şekli (nil = kıyas yok; omitempty → yük bayt bayt eski).
	// Metni (clusterText) summary()'ye GİRMEZ, seed()'in üçüncü sentetik çifti.
	ClusterCompare  *clusterCompareEvidence `json:"clusterCompare,omitempty"`
	clusterText     string
	clusterArgsJSON string
	// Drill — v0.10.1343: en yavaş 1–2 trace'in kritik yolu + logları; summary()'ye
	// GİRMEZ, seed()'in ikinci sentetik tool çifti.
	Drill         string `json:"-"`
	drillArgsJSON string
	// searchText — v0.10.1373: yol-yalnız hedefin trace arama metni
	// (pathSearchText); "" = katalog hedefi.
	searchText string
	// screen — v0.10.1572 (chat_clarify_synth.go): ekrandaki trace; Oracle
	// açıklamasının sentez sorusu önce bunu seçer (sıfır değer = ekran yok).
	screen   opScreenTrace
	endpoint bool // endpoint anma aşamasından geldi
	argsJSON string
	// codeKey — v0.10.1297: operasyon span'lerde yok, köprü fonksiyon kodu
	// (Oracle katalog satırı) → span anahtarının yazımı; "" = span operasyonu
	// ya da kod yok (döngü linkleri yeniden yazılmaz).
	codeKey string
	// errorAsk — v0.10.1399: hedefin ErrorAsk'ı; copilotChat Exceptions grubu
	// varken cevabı sohbet içinde Exceptions açıklamasından üretir
	// (chat_operation_exception_explain.go). JSON'a girmez.
	errorAsk bool
	// explainPool — v0.10.1411: hata-neden sorusunda kapsamın AÇIK grupları
	// (gösterilen 5'in dışındakiler dahil; chat_operation_exception_explain.go
	// collectExplainPool). nil = okunmadı / okunamadı → seçim gösterilenden
	// (bayt bayt eski). JSON'a girmez; gösterilen liste değişmez.
	explainPool []exGroupLine
}

type opTraceRow struct {
	TraceID    string  `json:"traceId"`
	Service    string  `json:"service"`
	DurationMs float64 `json:"durationMs"`
	Error      bool    `json:"error"`
}

// seed — serbest döngünün konuşmasına sentetik tool çağrısı + sonucu
// (nil → mesajlar aynen).
func (p *opPrefetch) seed(msgs []copilot.ChatMessage) []copilot.ChatMessage {
	if p == nil {
		return msgs
	}
	out := append([]copilot.ChatMessage{}, msgs...)
	sum, _ := clampToolResultForModel(p.summary())
	out = append(out,
		copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: opPrefetchToolID, Name: opPrefetchTool, Input: json.RawMessage(p.argsJSON)}}},
		copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: opPrefetchToolID, Name: opPrefetchTool, Content: sum}}},
	)
	if p.Drill != "" { // v0.10.1343 — kırılım ayrı çift (özet bayt bayt eski kalır)
		args := p.drillArgsJSON
		if args == "" {
			args = "{}"
		}
		drill, _ := clampToolResultForModel(p.Drill)
		out = append(out,
			copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: opDrillToolID, Name: opDrillTool, Input: json.RawMessage(args)}}},
			copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: opDrillToolID, Name: opDrillTool, Content: drill}}},
		)
	}
	if p.clusterText != "" { // v0.10.1374 — küme kıyası üçüncü çift (özet bayt bayt eski kalır)
		args := p.clusterArgsJSON
		if args == "" {
			args = "{}"
		}
		cc, _ := clampToolResultForModel(p.clusterText)
		out = append(out,
			copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: opClusterToolID, Name: opClusterTool, Input: json.RawMessage(args)}}},
			copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: opClusterToolID, Name: opClusterTool, Content: cc}}},
		)
	}
	return out
}

func opPctDelta(cur, prev float64) string {
	if prev == 0 {
		if cur == 0 {
			return "değişim yok"
		}
		return "önceki pencerede yoktu"
	}
	return fmt.Sprintf("%+.0f%%", (cur-prev)/prev*100)
}

// summary — SAF: modele giden kompakt özet (ham trace değil).
func (p *opPrefetch) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Operasyon %s otomatik getirildi (eşleşme: %s; pencere son %s, %s – %s UTC).\n",
		p.Operation, p.MatchKind, fmtAgoTR(p.RangeS), p.From.UTC().Format("15:04"), p.To.UTC().Format("15:04"))
	if len(p.Services) > 0 {
		fmt.Fprintf(&b, "Servis: %s\n", strings.Join(p.Services, ", "))
	}
	if len(p.Codes) > 0 {
		fmt.Fprintf(&b, "Oracle fonksiyon kodu: %s\n", strings.Join(p.Codes, ", "))
	}
	if p.endpoint { // v0.10.1343
		route := p.Route
		if route == "" {
			_, route = splitSpanVerb(p.Operation)
		}
		if len(p.Services) == 0 { // v0.10.1373 — yol-yalnız hedef, servis tek adaya çözülmedi
			fmt.Fprintf(&b, "Endpoint: %s · servis belirsiz (tüm servislerde arandı)\n", strings.TrimSpace(p.Method+" "+route))
		} else {
			fmt.Fprintf(&b, "Endpoint: %s · %s\n", strings.TrimSpace(p.Method+" "+route), strings.Join(p.Services, ", "))
		}
		if len(p.Services) > 0 {
			b.WriteString(renderEndpointAlternativesTR(p.Alternatives, p.Services[0]))
		}
	}
	if p.searchText != "" { // v0.10.1373 — yol-yalnız hedef
		fmt.Fprintf(&b, "RED ölçülemedi (rota katalogda yok; url.path üzerinden arandı: %q).\n", p.searchText)
	} else if p.Current.Measured {
		c, pr := p.Current, p.Previous
		fmt.Fprintf(&b, "Çağrı %d (önceki eşit pencere %d, %s) · hata oranı %%%.2f (önce %%%.2f) · p99 %.0f ms (önce %.0f ms, %s)\n",
			c.Calls, pr.Calls, opPctDelta(float64(c.Calls), float64(pr.Calls)), c.ErrorRate()*100, pr.ErrorRate()*100,
			c.P99Ms, pr.P99Ms, opPctDelta(c.P99Ms, pr.P99Ms))
	} else {
		b.WriteString("RED ölçülemedi (bu kaynak için ön-özet yok).\n")
	}
	errN := 0
	for _, t := range p.Traces {
		if t.Error {
			errN++
		}
	}
	fmt.Fprintf(&b, "Trace: %d (%d hatalı). İlk hatalılar:\n", len(p.Traces), errN)
	shown := 0
	for _, t := range p.Traces {
		if !t.Error || shown == 5 {
			continue
		}
		shown++
		fmt.Fprintf(&b, "- %s · %s · %.0f ms\n", t.TraceID, t.Service, t.DurationMs)
	}
	if len(p.Oracle) > 0 {
		b.WriteString("Bu trace'lere bağlı Oracle teknik hataları (TRACEID):\n")
		for _, o := range p.Oracle {
			fmt.Fprintf(&b, "- %s ×%d: %s\n", o.Code, o.Count, clipBytes(o.Sample, 160))
		}
	} else {
		b.WriteString("Bu trace'lere bağlı Oracle hatası yok.\n")
	}
	if p.Exceptions != nil { // v0.10.1331 — v0.10.1324 düşüş özetine de girer
		b.WriteString(renderExceptionGroupsTR(p.Operation+" operasyonu", *p.Exceptions))
	}
	if p.EndpointExceptionsChecked { // v0.10.1343
		b.WriteString(renderEndpointExceptionsTR(p.EndpointExceptions, p.EndpointExceptionsErr))
	}
	return b.String()
}

// exceptionsPromptTR — v0.10.1331: serbest döngü prompt'una ek YALNIZ
// ön-getirme Exceptions grubu bulduysa; yoksa "" (prompt bayt bayt eski).
func (p *opPrefetch) exceptionsPromptTR() string {
	if p == nil || p.Exceptions == nil || p.Exceptions.Err != "" || len(p.Exceptions.Groups) == 0 {
		return ""
	}
	return copilot.OperationExceptionsAddendum() + "\n\n"
}

// exceptionFollowupFacts — v0.10.1331: grubun takip çipi olgusu
// ("<KOD> grubunu açıkla" → grubun Exceptions açıklaması). nil → yok.
//
// v0.10.1399 — en YENİ grup (Groups[0]) değil, en ÖNEMLİ grup önce
// (pickExplainGroup: P1 → son saat → toplam → son görülme; ignored/resolved
// düşer), ardından kalanlar aynı sırayla: çip ile sohbet içi açıklama aynı
// grubu söyler. followupChips tür başına bir çip seçer → ilk (en önemli) kazanır.
func (p *opPrefetch) exceptionFollowupFacts() []followupFact {
	if p == nil || p.Exceptions == nil || p.Exceptions.Err != "" || len(p.Exceptions.Groups) == 0 {
		return nil
	}
	top, others, ok := pickExplainGroup(p.explainCandidates()) // v0.10.1399; v0.10.1411 gösterilen ∪ açık havuz
	if !ok {
		return nil
	}
	out := make([]followupFact, 0, 1+len(others))
	for _, g := range append([]exGroupLine{top}, others...) {
		out = append(out, followupFact{Kind: fuKindExceptionGroup, Code: exGroupLabel(g), Fingerprint: g.Fingerprint})
	}
	return out
}

// followupFacts — v0.10.1388: ön-getirmenin takip olguları — en yeni Exceptions
// grubu (exceptionFollowupFacts) + "<op> örnek trace topolojisi" (op_topology).
// nil → yok.
func (p *opPrefetch) followupFacts() []followupFact {
	if p == nil {
		return nil
	}
	out := p.exceptionFollowupFacts()
	op := strings.TrimSpace(p.Operation)
	if p.Route != "" {
		op = strings.TrimSpace(p.Method + " " + p.Route)
	} else if p.searchText != "" {
		op = "" // yol-yalnız hedef: operasyon adı yok (yol metni çip cümlesine uymaz)
	}
	if op != "" {
		out = append(out, followupFact{Kind: fuKindOpTopology, Operation: op})
	}
	return out
}

// opTracesHref — SAF: filtreli Traces sayfası; Traces'in URL sözleşmesi
// (frontend lib/urlState.ts): range=custom:<fromMs>-<toMs>, op=<ad>,
// service=, filters=<JSON FilterExpr dizisi>, hasError.
func opTracesHref(t opTarget, spanKey string, from, to time.Time) string {
	q := url.Values{}
	q.Set("range", fmt.Sprintf("custom:%d-%d", from.UnixMilli(), to.UnixMilli()))
	switch {
	case t.SearchPath != "": // v0.10.1373 — yol-yalnız: serbest arama
		q.Set("search", pathSearchText(t.SearchPath))
		if len(t.Services) == 1 {
			q.Set("service", t.Services[0])
		}
	case t.HasSpan:
		q.Set("op", t.Name)
		if len(t.Services) == 1 {
			q.Set("service", t.Services[0])
		}
	case t.HasRoute: // v0.10.1343 — yalnız rota: http.route süzgeci
		if len(t.Services) == 1 {
			q.Set("service", t.Services[0])
		}
		f, _ := json.Marshal(opRouteFilter(t.Route))
		q.Set("filters", string(f))
	case len(t.Codes) > 0 && spanKey != "":
		// v0.10.1297 — TÜM kodlar (tek kod "=", çoğu "IN"); eskiden yalnız ilki.
		f, _ := json.Marshal([]chstore.FilterExpr{mcptools.OracleCodeFilter(spanKey, t.Codes)}) // {"k","op","v"} — FE FilterExpr
		q.Set("filters", string(f))
	}
	return "/traces?" + q.Encode()
}

// opTraceFilterSets — SAF (v0.10.1388, opPrefetchForTarget'tan MEKANİK
// çıkarıldı): hedefin trace süzgeç kümeleri. Yol-yalnız → tek süzgeçsiz küme
// (Search taşır), span operasyonu → name, rota → http.route, Oracle kodları →
// kod başına bir küme (spanKey boşsa yok). CoSRE operasyon topolojisi
// (copilot_service_topology.go) örnek trace'i aynı kümelerden seçer.
func opTraceFilterSets(t opTarget, search, spanKey string) [][]chstore.FilterExpr {
	var filters [][]chstore.FilterExpr
	switch {
	case search != "": // v0.10.1373 — süzgeçsiz, yalnız Search (RED yok: Measured=false)
		filters = append(filters, nil)
	case t.HasSpan:
		filters = append(filters, []chstore.FilterExpr{{Key: "name", Op: "=", Values: []string{t.Name}}})
	case t.HasRoute:
		filters = append(filters, opRouteFilter(t.Route))
	case spanKey != "":
		for _, c := range t.Codes {
			filters = append(filters, []chstore.FilterExpr{{Key: spanKey, Op: "=", Values: []string{c}}})
		}
	}
	return filters
}

// opRouteFilter — SAF (v0.10.1343): rota hedefinin trace süzgeci (Traces
// sayfası linki ve GetTraces aynı ifadeyi kullanır).
func opRouteFilter(route string) []chstore.FilterExpr {
	return []chstore.FilterExpr{{Key: "http.route", Op: "=", Values: []string{route}}}
}

// rewriteSearchHref — v0.10.1297: ön-getirme operasyonu fonksiyon koduyla
// çözdüyse, döngünün bu operasyonun ADIYLA kurduğu /traces?search=<ad>
// linkleri (build_link çipi, search_traces deep_link'i → answer.open) kod
// süzgecine çevrilir. nil / span operasyonu → href aynen.
func (p *opPrefetch) rewriteSearchHref(href string) string {
	if p == nil || p.codeKey == "" || len(p.Codes) == 0 {
		return href
	}
	return opSearchHrefToCodeFilter(href, p.Operation, p.codeKey, p.Codes)
}

// opSearchHrefToCodeFilter — SAF (tablo testli): /traces?…search=<op>… →
// search düşer, filters'a kod süzgeci eklenir (aynı anahtarda süzgeç zaten
// varsa eklenmez); diğer paramlar (range, service, sort…) korunur. search
// operasyon adı değilse, href /traces değilse ya da filters çözülemiyorsa
// href aynen döner.
func opSearchHrefToCodeFilter(href, op, key string, codes []string) string {
	if !strings.HasPrefix(href, "/traces?") || strings.TrimSpace(op) == "" || key == "" || len(codes) == 0 {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	q := u.Query()
	if !strings.EqualFold(strings.TrimSpace(q.Get("search")), strings.TrimSpace(op)) {
		return href
	}
	var fs []chstore.FilterExpr
	if raw := q.Get("filters"); raw != "" {
		if json.Unmarshal([]byte(raw), &fs) != nil {
			return href
		}
	}
	has := false
	for _, f := range fs {
		if strings.EqualFold(f.Key, key) {
			has = true
		}
	}
	if !has {
		fs = append(fs, mcptools.OracleCodeFilter(key, codes))
	}
	b, _ := json.Marshal(fs)
	q.Del("search")
	q.Set("filters", string(b))
	return "/traces?" + q.Encode()
}

// mergeErrorsFirst — SAF: tekilleştirilmiş birleşim; önce hatalılar, sonra süre.
func mergeErrorsFirst(sets [][]chstore.TraceRow, limit int) []chstore.TraceRow {
	seen := map[string]bool{}
	var out []chstore.TraceRow
	for _, rows := range sets {
		for _, r := range rows {
			if !seen[r.TraceID] {
				seen[r.TraceID] = true
				out = append(out, r)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HasError != out[j].HasError {
			return out[i].HasError
		}
		return out[i].DurationMs > out[j].DurationMs
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// chatOperationPrefetch — copilotChat'in guided'dan önceki adımı.
// handled=true → seçim çipleri yayınlandı, tur bitti.
// opCatalogSeam — v0.10.1464 TEST DİKİŞİ (prod'da nil; cosreInteractionSeam
// emsali): ön-getirmenin katalog adayı okuması. Yönlendirme regresyon kümesi
// (chat_routing_regression_test.go) store'suz sunucuda yol anan turu gerçek
// ön-getirmeden (endpoint anması → yol-yalnız → dış-yol sondası) geçirir.
var opCatalogSeam func(ctx context.Context, toks []string, limit int) ([]chstore.CatalogOperation, error)

// opCatalogCandidates — katalog adayları (dikiş kuruluysa ondan).
func (s *Server) opCatalogCandidates(ctx context.Context, toks []string, limit int) ([]chstore.CatalogOperation, error) {
	if opCatalogSeam != nil {
		return opCatalogSeam(ctx, toks, limit)
	}
	return s.store.CatalogCandidates(ctx, toks, limit)
}

func (s *Server) chatOperationPrefetch(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, rangeS int64, anchor time.Time) (*opPrefetch, bool) {
	if s == nil || (s.store == nil && opCatalogSeam == nil) {
		return nil, false
	}
	raw := lastUserText(msgs)
	// v0.10.1388 — topoloji sözcüğü taşıyan mesaj ("PAYMENT_CREATE operasyonunun
	// topolojisi", "POST /api/payments topolojisi") ön-getirmeye girmez: guided
	// trace_topology operasyonu kendisi çözer (copilot_service_topology.go).
	if topologyVetoesOpPrefetch(raw) {
		return nil, false
	}
	// v0.10.1416 — yapıştırılan PromQL guided metric_chart'ındır (http_route="/api/x"
	// etiket değeri endpoint anması sanılmasın; copilot_metric_chart_promql.go).
	if promqlVetoesOpPrefetch(raw) {
		return nil, false
	}
	// v0.10.1455 — "dış host <ad> çağrı süreleri" (giden çağrı çipi) guided
	// outbound_call'ındır: tam host adı "POST <host>" client operasyonuna
	// bulanık eşleşmesin (copilot_outbound_call_route.go).
	if outboundVetoesOpPrefetch(raw) {
		return nil, false
	}
	pctx, cancel := context.WithTimeout(ctx, opPrefetchTimeout)
	defer cancel()
	// v0.10.1482 — sarmalayıcısız "/x/**": önekin altındaki tüm rotaların toplamı
	// (chat_endpoint_prefix.go); rota yoksa / okunamadıysa aşağısı bayt bayt.
	if s.endpointPrefixPrefetch(pctx, ctx, emit, raw, rangeS, anchor) {
		return nil, true
	}
	t, kind, handled, ok := s.resolveEndpointMention(pctx, ctx, emit, raw)
	if handled {
		return nil, true
	}
	if outboundSlotFrom(ctx).get() != nil { // v0.10.1454 — giden çağrı: guided outbound_call sahiplenir
		return nil, false
	}
	if !ok { // endpoint anması yok / vetolu / eşleşmedi → bugünkü belirteç yolu
		toks := opTokens(raw)
		if len(toks) == 0 {
			return nil, false
		}
		cands, err := s.opCatalogCandidates(pctx, toks, opCandidateLimit)
		if err != nil { // v0.10.1373 — katalog okuması düştü (3 sn tavanı): sessiz değil, çipte görünür
			emitCatalogStep(emit, toks, 0, err)
		}
		if err != nil || len(cands) == 0 {
			return nil, false
		}
		m := matchOperations(toks, cands)
		switch {
		case len(m.Ops) == 0:
			return nil, false
		case len(m.Ops) > 1:
			ops := m.Ops
			if len(ops) > opChoiceMax {
				ops = ops[:opChoiceMax]
			}
			sugg := make([]string, len(ops))
			for i, op := range ops {
				sugg[i] = op + " operasyonuna bak"
			}
			emit("answer", map[string]any{
				"text":        fmt.Sprintf("Mesajınız %d operasyonla eşleşti; hangisine bakayım?", len(m.Ops)),
				"exchangeId":  copilot.MetaFromContext(ctx).ExchangeID,
				"suggestions": sugg,
			})
			return nil, true
		}
		t, kind = opTargetFor(m.Ops[0], cands), m.Kind
	}
	t.ErrorAsk = opErrorWhyAsk(raw) // v0.10.1399 — "neden hata veriyor" → Exceptions açıklaması sohbet içinde
	if rangeS <= 0 {
		rangeS = opDefaultRangeS
	}
	to := anchor
	if to.IsZero() {
		to = time.Now()
	}
	from := to.Add(-time.Duration(rangeS) * time.Second)
	return s.opPrefetchForTarget(pctx, emit, t, kind, rangeS, from, to), false
}

// opPrefetchForTarget — v0.10.1367: hedef ÇÖZÜLDÜKTEN sonraki ön-getirme
// (RED + önceki eşit pencere, hatalı-önce trace'ler, Oracle, Exceptions,
// kırılım, Faz 3 kartı). chatOperationPrefetch'in kuyruğundan MEKANİK
// çıkarıldı — sohbet yolu bayt bayt aynı (TestOpPrefetch*); çekmece endpoint
// takibi (drawerEndpointEvidence) aynı kurucuyu emit'siz çağırır, ikinci bir
// montaj kopyası yok.
func (s *Server) opPrefetchForTarget(pctx context.Context, emit func(string, any), t opTarget, kind string, rangeS int64, from, to time.Time) *opPrefetch {
	if s == nil || s.store == nil {
		return nil
	}
	prevFrom := from.Add(-time.Duration(rangeS) * time.Second)
	sc := s.oracleOpScope()
	p := &opPrefetch{Operation: t.Name, MatchKind: kind, Services: t.Services, Codes: t.Codes, RangeS: rangeS, From: from, To: to,
		Route: t.Route, Method: t.Method, Alternatives: t.Alternatives, endpoint: t.Endpoint, errorAsk: t.ErrorAsk} // v0.10.1399 errorAsk
	argm := map[string]any{"operation": t.Name, "match": kind, "range_s": rangeS}
	if t.HasRoute {
		argm["route"] = t.Route
	}
	search := "" // v0.10.1373 — yol-yalnız hedefin trace arama metni
	if t.SearchPath != "" {
		search = pathSearchText(t.SearchPath)
		p.searchText = search
		argm["search"] = search
	}
	args, _ := json.Marshal(argm)
	p.argsJSON = string(args)
	n := emitGuidedStep(emit, opPrefetchTool, p.argsJSON)

	svc := ""
	if len(t.Services) == 1 {
		svc = t.Services[0]
	}
	switch {
	case t.HasSpan:
		p.Current, _ = s.store.SpanOperationRED(pctx, svc, t.Name, from, to)
		p.Previous, _ = s.store.SpanOperationRED(pctx, svc, t.Name, prevFrom, from)
	case t.HasRoute: // v0.10.1343 — yalnız http_route: spanmetrics_1m
		p.Current, _ = s.store.RouteRED(pctx, svc, t.Route, from, to)
		p.Previous, _ = s.store.RouteRED(pctx, svc, t.Route, prevFrom, from)
	case len(t.Codes) > 0:
		p.Current, _ = s.store.FunctionCodeRED(pctx, t.Codes, from, to)
		p.Previous, _ = s.store.FunctionCodeRED(pctx, t.Codes, prevFrom, from)
	}
	filters := opTraceFilterSets(t, search, sc.SpanKey) // v0.10.1388 — topoloji demeti de aynı süzgeçleri kullanır
	var sets [][]chstore.TraceRow
	for _, errorsOnly := range []bool{true, false} { // önce hatalılar
		for _, fx := range filters {
			f := traceSearchFilter(svc, "", search, false, from, to)
			f.Filters, f.HasError, f.Limit = fx, errorsOnly, opTraceLimit
			if rows, _, _, terr := s.store.GetTraces(pctx, f); terr == nil {
				sets = append(sets, rows)
			}
		}
	}
	ids := []string{}
	merged := mergeErrorsFirst(sets, opTraceLimit)
	for _, r := range merged {
		p.Traces = append(p.Traces, opTraceRow{TraceID: r.TraceID, Service: r.ServiceName, DurationMs: r.DurationMs, Error: r.HasError})
		ids = append(ids, r.TraceID)
	}
	if sc.Enabled {
		p.Oracle, _ = s.store.OracleErrorsForTraces(pctx, ids, from, to)
	}
	p.TracesHref = opTracesHref(t, sc.SpanKey, from, to)
	if t.HasRoute {
		p.EndpointHref = endpointPageHref(svc, t.Route, from, to)
	}
	if !t.HasSpan && len(t.Codes) > 0 && sc.SpanKey != "" {
		p.codeKey = sc.SpanKey // v0.10.1297 — döngü linkleri kod süzgecine çevrilir
	}
	emitGuidedStepResult(emit, n, opPrefetchTool, p.summary(), nil)
	if opWantsExceptions(t) { // v0.10.1331 — Exceptions'taki son grup + yorum
		eargs, _ := json.Marshal(map[string]any{"operation": t.Name, "range_s": int64(opExceptionsLookback / time.Second)})
		en := emitGuidedStep(emit, opExceptionsTool, string(eargs))
		esc := exScope{Messages: []string{t.Name}, From: to.Add(-opExceptionsLookback), To: to}
		ev := s.exceptionGroupsEvidence(pctx, esc)
		p.Exceptions = &ev
		if opExplainPoolWanted(t.ErrorAsk, ev) { // v0.10.1411 — seçim en yeni 5'ten değil, açık gruplardan
			p.explainPool = s.exceptionExplainPool(pctx, esc)
		}
		var eerr error
		if ev.Err != "" {
			eerr = fmt.Errorf("exceptions okunamadı: %s", ev.Err)
		}
		emitGuidedStepResult(emit, en, opExceptionsTool, renderExceptionGroupsTR(t.Name+" operasyonu", ev), eerr)
	}
	cc := s.startClusterCompare(pctx, emit, t, svc, from, to) // v0.10.1374 — ≥2 ortam yoksa nil; kırılımla paralel okur
	s.endpointDrillPhase(pctx, emit, p, t, svc, merged, from, to)
	cc.finish(emit, p) // v0.10.1374 — nil → no-op
	// Faz 3 — sohbet kartı (metrikler, hatalı trace'ler, Oracle kodları,
	// filtreli Traces linki). seq yüksek: döngünün kendi blokları (b1…) önce.
	emit("block", map[string]any{"id": "op-prefetch", "type": blocks.TypeOperation, "seq": 1000, "final": true, "payload": p})
	return p
}

// resolveEndpointMention — v0.10.1343: endpoint anma aşaması. ok=true → hedef
// seçildi; handled=true → belirsizlik çipleri yayınlandı (tur biter); ikisi de
// false → bugünkü belirteç yolu. Veto: listele/sor şekli (endpointMentionVetoed).
//
// v0.10.1373 (operator-reported) — yol anıldı, veto yok ama katalog adayı yok
// (H1: rota yalnız url.path/http.target'ta, katalog kaynakları
// operation_summary_5m.name + spanmetrics_1m.http_route; H2: katalog okuması 3 sn
// tavanında düştü) ya da hiçbir aday yolu eşlemedi → eskiden sessizce belirteç
// yoluna düşüyordu. Artık yol-yalnız hedef (endpointPathOnlyTarget, kind
// endpoint_path); katalog okuması operation_catalog çipinde görünür.
func (s *Server) resolveEndpointMention(pctx, ctx context.Context, emit func(string, any), raw string) (t opTarget, kind string, handled, ok bool) {
	raw = strings.ReplaceAll(raw, "…", "...") // v0.10.1454 — anma yolu "..." yazımında; Replace(raw, path) eşlesin
	method, path, mentioned := endpointMention(raw)
	if !mentioned {
		return t, "", false, false
	}
	envs := s.guidedEnvNames(pctx) // v0.10.1373 — ortam anan soru analizdir (veto değil)
	if endpointMentionVetoed(raw, envs) {
		return t, "", false, false
	}
	// pathOnly — v0.10.1373: yol-yalnız geri dönüş. Log sorusu ("/var/log/x'te
	// hata", "/yol logları") bugünkü yolunda kalır (log kademesi).
	pathOnly := func() (opTarget, string, bool, bool) {
		if !endpointPathOnlyAllowed(raw) {
			return opTarget{}, "", false, false
		}
		pt := endpointPathOnlyTarget(raw, method, path, s.guidedServiceNames(pctx), envs)
		pt.Envs = endpointEnvSet(raw, path, envs) // v0.10.1374 — küme kıyası
		// v0.10.1454 — yol bir CLIENT span'inin url.path'i olabilir (ESB'ye giden
		// çağrı): dış-yol sondası HIT → yuvaya yaz, ön-getirme nil döner, guided
		// outbound_call cevaplar (copilot_outbound_call.go). MISS/hata → aşağısı bayt bayt.
		if slot := outboundSlotFrom(ctx); slot != nil {
			if hit, hok := s.outboundProbe(pctx, emit, slot, path, firstString(pt.Services), pt.Envs); hok {
				slot.set(hit)
				return opTarget{}, "", false, false
			}
		}
		// v0.10.1380 — kısa adla anılan kümenin canlı değeri çipte ("eu-a → prod-eu-a").
		emitEnvAliasStep(emit, endpointEnvAliasNote(raw, path, envs, pt.Envs))
		return pt, endpointPathKind, false, true
	}
	toks := endpointCatalogTokens(path)
	cands, err := s.opCatalogCandidates(pctx, toks, opCandidateLimit)
	emitCatalogStep(emit, toks, len(cands), err) // v0.10.1373 — gözlemlenebilirlik
	if err != nil || len(cands) == 0 {
		return pathOnly()
	}
	m := matchEndpoint(method, path, cands, endpointServiceHint(raw, path, cands))
	switch {
	case m.Pick != nil:
		t = endpointTargetFor(*m.Pick, method)
		t.Alternatives = opAlternativesFrom(m.Alternatives, method)
		t.Envs = endpointEnvSet(raw, path, envs) // v0.10.1374 — küme kıyası
		// v0.10.1380 — kısa adla anılan kümenin canlı değeri çipte ("eu-a → prod-eu-a").
		emitEnvAliasStep(emit, endpointEnvAliasNote(raw, path, envs, t.Envs))
		return t, m.Kind, false, true
	case len(m.Choices) > 1:
		sugg := make([]string, len(m.Choices))
		for i, g := range m.Choices {
			sugg[i] = endpointChoiceChip(g, method)
		}
		emit("answer", map[string]any{
			"text":        fmt.Sprintf("%s %d serviste eşleşti ve hiçbiri baskın değil; hangisine bakayım?", strings.TrimSpace(method+" "+path), len(m.Choices)),
			"exchangeId":  copilot.MetaFromContext(ctx).ExchangeID,
			"suggestions": sugg,
		})
		return t, "", true, false
	}
	// v0.10.1373 — adaylar var ama hiçbiri yolu eşlemedi: yol katalogda yok.
	return pathOnly()
}

// endpointPathOnlyAllowed — SAF (v0.10.1373): log sinyali taşıyan mesaj
// yol-yalnız hedefe çevrilmez (dosya yolu / log sorusu log kademesine kalır).
func endpointPathOnlyAllowed(raw string) bool {
	return !hasLogSignal(guidedTokens(normalizeGuidedMsg(raw)))
}

// endpointPathKind — v0.10.1373: yol-yalnız hedefin eşleşme türü.
const endpointPathKind = "endpoint_path"

// opCatalogTool — v0.10.1373: katalog okumasının adım çipi.
const opCatalogTool = "operation_catalog"

// emitCatalogStep — v0.10.1373 (A4): katalog okuması çipi {tokens} + sonuç
// ("N aday" / "0 aday" / hata). Davranış değiştirmez; H2 (3 sn tavanı) artık
// sessiz değil.
func emitCatalogStep(emit func(string, any), toks []string, n int, err error) {
	args, _ := json.Marshal(map[string]any{"tokens": toks})
	i := emitGuidedStep(emit, opCatalogTool, string(args))
	emitGuidedStepResult(emit, i, opCatalogTool, fmt.Sprintf("%d aday", n), err)
}

// endpointPathOnlyTarget — SAF (v0.10.1373): katalogda olmayan anılmış yolun
// okuma hedefi. Servis yalnız TEK aday varsa daraltılır: önce yol çıkarılmış
// metinde sınırlı tam ad (extractServiceFullName — tireli ad parça eşine
// oturmaz), sonra serviceCandidates; 2+ aday → servissiz (yanlış servise
// daraltıp 0 trace dönmektense geniş arama).
func endpointPathOnlyTarget(raw, method, path string, services, envs []string) opTarget {
	t := opTarget{Name: path, Method: strings.ToUpper(method), Endpoint: true, SearchPath: path}
	rest := normalizeGuidedMsg(stripMarkdownWrapText(strings.Replace(raw, path, " ", 1)))
	if svc := extractServiceFullName(rest, services); svc != "" {
		t.Services = []string{svc}
	} else if c := serviceCandidates(rest, services, envs, guidedServiceAskMax); len(c) == 1 {
		t.Services = c
	}
	return t
}

// pathSearchText — SAF (v0.10.1373): yol-yalnız hedefin trace arama metni.
// Elips ("..." / "…") ve joker/şablon parçaları ({id}, :id, *, **) url.path'te
// birebir geçmez; yol o parçalarda bölünür ve parçalar boşlukla birleşir —
// TraceFilter.Search TÜM belirteçleri (harf duyarsız alt-dize) arar:
// "/shop/.../detail" → "/shop/ /detail".
func pathSearchText(path string) string {
	segs := strings.Split(path, "/")
	for i, sg := range segs {
		if sg == "..." || sg == "…" || isPlaceholderSegment(sg) {
			segs[i] = "\x00"
		}
	}
	var parts []string
	for _, piece := range strings.Split(strings.Join(segs, "/"), "\x00") {
		if strings.ContainsFunc(piece, func(r rune) bool { return r != '/' }) {
			parts = append(parts, piece)
		}
	}
	if len(parts) == 0 {
		return path
	}
	return strings.Join(parts, " ")
}

// epExResult — endpoint exception okumasının goroutine sonucu.
type epExResult struct {
	rows []chstore.EndpointException
	err  error
}

// endpointDrillPhase — v0.10.1343: trace'ler varsa en yavaş ≤2 trace'in
// kırılımı (operatör varsayılanı: her hedefte, BSA dahil; ≤10 sn) ve endpoint
// hedefinde servisin endpoint span exception'ları (≤4 sn). Exception okuması
// goroutine'de TOPLANIR; emit yalnız bu (ön-getirme) goroutine'inden.
func (s *Server) endpointDrillPhase(pctx context.Context, emit func(string, any), p *opPrefetch, t opTarget, svc string, merged []chstore.TraceRow, from, to time.Time) {
	var epCh chan epExResult
	en := 0
	// v0.10.1373 — yol-yalnız hedefte rota exception okuması yok (http_route
	// eşleşmez; "exception yok" yanlış olurdu).
	if (t.Endpoint || t.HasRoute) && svc != "" && !opWantsExceptions(t) && t.SearchPath == "" {
		path := t.Route
		if path == "" {
			path = t.Name
		}
		eargs, _ := json.Marshal(map[string]any{"service": svc, "path": path})
		en = emitGuidedStep(emit, opEndpointExTool, string(eargs))
		epCh = make(chan epExResult, 1)
		go func() {
			ectx, ecancel := context.WithTimeout(pctx, opEndpointExTimeout)
			defer ecancel()
			rows, err := s.store.EndpointTopExceptions(ectx, chstore.EndpointDetailQuery{Service: svc, Path: path, From: from, To: to}, opEndpointExLimit)
			epCh <- epExResult{rows: rows, err: err}
		}()
	}
	// v0.10.1399 (operator-reported) — "neden hata veriyor" sorusunda Exceptions
	// grubu varsa cevap grubun Exceptions açıklamasından gelir
	// (opExceptionExplainInChat); örnek trace kırılımı (en yavaş hatalının
	// tesadüfi yan hatası, ör. log yayınının PRODUCER timeout'u) okunmaz.
	if picks := pickDrillTraces(merged); len(picks) > 0 && !opDrillSkippedForErrorAsk(t, p) {
		dids := make([]string, len(picks))
		for i, c := range picks {
			dids[i] = c.Row.TraceID
		}
		dargs, _ := json.Marshal(map[string]any{"operation": t.Name, "trace_ids": dids})
		p.drillArgsJSON = string(dargs)
		p.Drill = s.traceDrillEvidenceBody(pctx, emit, picks, svc, from, to, opDrillLogBodyRunes)
	}
	if epCh == nil {
		return
	}
	r := <-epCh
	p.EndpointExceptionsChecked = true
	if r.err != nil {
		p.EndpointExceptionsErr = string(sourcestate.Classify(r.err))
	} else {
		p.EndpointExceptions = opEndpointExceptionsFrom(r.rows)
	}
	emitGuidedStepResult(emit, en, opEndpointExTool, renderEndpointExceptionsTR(p.EndpointExceptions, p.EndpointExceptionsErr), r.err)
}
