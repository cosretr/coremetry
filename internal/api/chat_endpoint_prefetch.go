package api

// chat_endpoint_prefetch.go — v0.10.1343 (operator-reported: "POST
// /ACMWEB/application/expressOrderPreValidate neden yavaş?" → CoSRE endpoint'i
// bulamadı). Operasyon ön-getirmesinin (chat_operation_prefetch.go) ENDPOINT
// anma aşaması; saf yarı burada, kablolama chatOperationPrefetch'te.
//
//   1. endpointMention — mesajda isteğe bağlı HTTP fiili + yol (tam URL → yalnız
//      yol; {id} / :id / * parçaları; sondaki noktalama ve Türkçe ek kırpılır;
//      yolun harfi korunur).
//   2. Katalog adayları YALNIZ yol belirteciyle (fiil yok — "post" ORDER BY
//      call_count LIMIT 60'ı doldurmasın); somut kimlik parçası varsa şablon
//      adayları için kimlikten önceki önek de.
//   3. matchEndpoint — puan: span adı == "FİİL yol" 100, == yol 90, harf
//      duyarsız 80/70; rota == yol 95, harf duyarsız 75; şablon eşitliği 65
//      (span) / 60 (rota); içerir 20. En iyi kademe kazanır (içerir yalnız ≥60
//      yoksa). Gruplama servis × yol; mesajdaki katalog servisi önce daraltır.
//      BELİRSİZLİK (operatör varsayılanı): çağrısı ikinciden ≥3× olan servis
//      seçilir ve diğerleri söylenir; aksi "<svc> <FİİL yol> endpointine bak"
//      çipleriyle sorulur (yeniden giriş servis ipucuyla çözülür).
//   4. Veto: "X isteklerini getir" / "/yol trace'lerini getir" (endpoint_traces.go
//      extractEndpointRequest) anma aşamasınca SAHİPLENİLMEZ — listele/sor akışı
//      korunur.

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	opDrillToolID    = "cosre-endpoint-drill"
	opDrillTool      = "trace_drill"
	opEndpointExTool = "endpoint_exceptions"
	// opDrillLogBodyRunes — kırılım log gövdesi tavanı (guided trace_drill 200):
	// 2 kırılım + 16 log satırı ikinci sentetik tool sonucunda model tavanının
	// (6000 rune) altında kalsın (TestOpPrefetchDrillUnderModelClamp).
	opDrillLogBodyRunes = 120
	opEndpointExTimeout = 4 * time.Second
	opEndpointExLimit   = 5
	epExRenderMax       = 5
	epExMsgRunes        = 160
	epDominanceFactor   = 3
)

var httpVerbSet = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}

// isHTTPVerb — SAF, harf duyarsız.
func isHTTPVerb(s string) bool { return httpVerbSet[strings.ToUpper(s)] }

// cleanEndpointPath — SAF: Türkçe ek (kesme işaretinden sonrası), sorgu /
// fragman, sondaki noktalama ve sondaki "/" atılır. v0.10.1373 — sondaki
// Markdown vurgu sarmalayıcısı (`*…*`, `_…_`) da atılır (trimWrapSuffix).
//
// v0.10.1454 (operator-reported) — "**/esb/external/…/customer-kvkk/**": tek
// karakterlik elips (U+2026) ASCII değil, validEndpointPath belirteci anma
// olmaktan çıkarıyordu (yol tetiği hiç ateşlemiyordu). Kırpmalardan SONRA
// "..." yazımına çevrilir (önce çevrilse sondaki "…" noktalama diye kırpılırdı);
// pathSearchText / OutboundPathPattern iki yazımı da bilir.
func cleanEndpointPath(p string) string {
	if i := strings.IndexAny(p, "'’"); i >= 0 {
		p = p[:i]
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimRight(p, ".,;:!)]>\"`")
	p = strings.TrimRight(trimWrapSuffix(p), ".,;:!)]>\"`") // v0.10.1373 — "*/x/y.*" → "/x/y"
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return strings.ReplaceAll(p, "…", "...") // v0.10.1454
}

// validEndpointPath — SAF: "/" ile başlar ("//" değil), yalnız ASCII yol
// karakterleri, en az bir parçası ≥2 karakter.
func validEndpointPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("/-._~%{}:*@+=$,!", r):
		default:
			return false
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if len(seg) >= 2 {
			return true
		}
	}
	return false
}

// trimWrapSuffix — SAF (v0.10.1373): sondaki `*` / `_` dizisi yalnız ÖNÜNDEKİ
// karakter "/" DEĞİLSE sarmalayıcıdır ve atılır: "/x/y*" → "/x/y", ama
// "/users/:id/orders/*" ve "/x/**" joker parçası olarak kalır.
func trimWrapSuffix(tok string) string {
	end := len(tok)
	for end > 0 && (tok[end-1] == '*' || tok[end-1] == '_') {
		end--
	}
	if end == len(tok) || end == 0 || tok[end-1] == '/' {
		return tok
	}
	return tok[:end]
}

// stripMarkdownWrap — SAF (v0.10.1373, operator-reported): Markdown vurgu
// sarmalayıcısı (`*/yol*`, `**/yol**`, `_/yol_`) belirteçten atılır. Baştaki
// `*`/`_` her zaman, sondaki yalnız trimWrapSuffix kuralıyla. Hem anma
// (endpointMention) hem veto (endpointMentionVetoed) aynı kırpmayı görür.
//
// v0.10.1447 (operator-reported): "**/esb/.../customer-kvkk/** için grafikleri
// göster" — yol "/" ile bitince sondaki `**` joker sayılıp kalıyor, belirteç
// "/esb/.../customer-kvkk/**" oluyor ve katalogda hiçbir aday eşlemiyordu
// (yol-yalnız hedef → "veri bulunamadı"). BAŞTA sarmalayıcı varsa sondaki
// eşi (sondaki noktalamadan önce, en çok baştaki kadar) "/"den sonra da
// sarmalayıcıdır (trimWrapPair); başta yoksa eski joker kuralı aynen.
func stripMarkdownWrap(tok string) string {
	lead := len(tok) - len(strings.TrimLeft(tok, "*_"))
	tok = tok[lead:]
	if lead > 0 {
		return trimWrapPair(tok, lead)
	}
	return trimWrapSuffix(tok)
}

// trimWrapPair — SAF (v0.10.1447): baştaki sarmalayıcının sondaki eşini atar;
// sondaki noktalama ("**/x/**)." → ")." ) korunur, cleanEndpointPath kırpar.
// Eşten artan `*` (örn. "*/x/y**") trimWrapSuffix'e kalır.
func trimWrapPair(tok string, lead int) string {
	body := strings.TrimRight(tok, ".,;:!)]>\"`")
	tail := tok[len(body):]
	n := 0
	for n < lead && n < len(body) && (body[len(body)-1-n] == '*' || body[len(body)-1-n] == '_') {
		n++
	}
	return trimWrapSuffix(body[:len(body)-n]) + tail
}

// stripMarkdownWrapText — SAF (v0.10.1373): metnin her belirtecine
// stripMarkdownWrap; ayraç tek boşluk.
func stripMarkdownWrapText(raw string) string {
	fields := strings.Fields(raw)
	for i, f := range fields {
		fields[i] = stripMarkdownWrap(f)
	}
	return strings.Join(fields, " ")
}

// endpointMention — SAF: mesajdaki ilk endpoint anması. method büyük harfe
// çevrilir ("" = fiil yok), path ham metnin harfiyle. v0.10.1373 — `*…*` /
// `_…_` ile sarılı yol da anmadır (eskiden "*/yol*" belirteci atlanıyordu).
func endpointMention(raw string) (method, path string, ok bool) {
	method, path, _, ok = endpointMentionWrap(raw)
	return method, path, ok
}

// endpointMentionWrap — SAF (v0.10.1482): endpointMention + anmanın baştaki
// Markdown sarmalayıcısı (`*` / `_`) taşıyıp taşımadığı. Önek kipi
// (chat_endpoint_prefix.go) yalnız sarmalayıcısız `/x/**` anmasında açılır.
func endpointMentionWrap(raw string) (method, path string, wrapped, ok bool) {
	fields := strings.Fields(raw)
	for i, f := range fields {
		tok0 := strings.TrimLeft(f, "\"'`(<[")
		wrapped = strings.HasPrefix(tok0, "*") || strings.HasPrefix(tok0, "_")
		tok := stripMarkdownWrap(tok0) // v0.10.1373 — "*" ve "_" sarmalayıcı
		lower := strings.ToLower(tok)
		var p string
		switch {
		case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
			if j := strings.IndexAny(tok, "'’"); j >= 0 {
				tok = tok[:j]
			}
			u, err := url.Parse(strings.TrimRight(tok, ".,;:!)]>\"`"))
			if err != nil {
				continue
			}
			p = u.Path
		case strings.HasPrefix(tok, "/"):
			p = tok
		default:
			continue
		}
		p = cleanEndpointPath(p)
		if !validEndpointPath(p) {
			continue
		}
		if i > 0 && isHTTPVerb(fields[i-1]) {
			method = strings.ToUpper(fields[i-1])
		}
		return method, p, wrapped, true
	}
	return "", "", false, false
}

// endpointChipShape — SAF: belirsizlik çipinin kendi cümlesi ("… endpointine bak").
func endpointChipShape(norm string) bool {
	return strings.HasSuffix(strings.TrimSpace(norm), "endpointine bak")
}

// endpointMentionVetoed — SAF: mesaj endpoint_candidates / endpoint_traces
// şeklindeyse (listele/sor akışı) anma aşaması sahiplenmez. Belirsizlik çipi
// muaf (kendi "endpoint" sözcüğü taşır ama hedefi tektir).
//
// v0.10.1373 (operator-reported) — "*/…/yol* … çağrılıyor, tekrar eden DB
// çağrılarına bak" soruyu ANALİZ istiyordu ama "çağrı" kökü istek sinyali
// sayılıp veto ediliyordu. Veto artık yalnız SAF liste/getir şeklinde:
// onaylı trace isteği (yol + trace kökü) ya da getir/listele/göster/bul/aç
// fiili, VE analiz ipucu yok (hasAnalysisCue; envs canlı ortam adları).
// Yol Markdown sarmalayıcısı atılmış metin üzerinde aranır.
func endpointMentionVetoed(raw string, envs []string) bool {
	norm := normalizeGuidedMsg(raw)
	if endpointChipShape(norm) {
		return false
	}
	clean := stripMarkdownWrapText(raw)
	cnorm := normalizeGuidedMsg(clean)
	toks := guidedTokens(cnorm)
	_, confirmed, ok := extractEndpointRequest(clean, cnorm, toks, nil, nil, "")
	if !ok || hasAnalysisCue(toks, envs) {
		return false
	}
	return confirmed || hasFetchVerb(toks)
}

// epFetchStems / epFetchWords — v0.10.1373: listele/getir fiilleri. "bul" ve
// "aç" yalnız TAM sözcük ("bulunamadı", "açıkla" fiil değil).
var (
	epFetchStems = []string{"getir", "listele", "göster", "goster"}
	epFetchWords = []string{"bul", "aç", "ac"}
)

// hasFetchVerb — SAF (v0.10.1373).
func hasFetchVerb(toks []string) bool {
	return tokenHasPrefix(toks, epFetchStems...) || anyToken(toks, epFetchWords...)
}

// epAnalysisStems — v0.10.1373: analiz ipucu kökleri (tekrar / DB / yavaşlık /
// neden / kıyas / küme / süre / analiz). "çağrı" BURADA DEĞİL — o istek
// sinyalidir; "X çağrılarını getir" listesi korunur.
var epAnalysisStems = []string{
	"tekrar", "db", "sql", "veritaban", "yavaş", "yavas", "neden", "niye", "kıyas", "kiyas",
	"karşılaştır", "karsilastir", "cluster", "küme", "kume", "uzama", "uzun", "süre", "sure",
	"analiz", "incele",
}

// hasAnalysisCue — SAF (v0.10.1373): analiz kökü ya da canlı bir ortam adı
// (ortam anan soru kıyas/analizdir, liste değil).
func hasAnalysisCue(toks []string, envs []string) bool {
	if tokenHasPrefix(toks, epAnalysisStems...) {
		return true
	}
	// v0.10.1380 — öneksiz kısa ortam biçimi de ortam adıdır ("eu-a", canlı
	// "prod-eu-a"); envNameTokens tam adları (≥2) da içerir.
	envTok := envNameTokens(envs)
	for _, t := range toks {
		if envTok[t] {
			return true
		}
	}
	return false
}

var (
	epUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	epHexRe  = regexp.MustCompile(`^[0-9a-fA-F]{8,}$`)
	epNumRe  = regexp.MustCompile(`^[0-9]+$`)
)

// isPlaceholderSegment — SAF: {id} / :id / * / **.
func isPlaceholderSegment(s string) bool {
	return (len(s) > 2 && strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(len(s) > 1 && strings.HasPrefix(s, ":")) || s == "*" || s == "**"
}

// isIDSegment — SAF: sayı / UUID / rakam taşıyan ≥8 hex.
func isIDSegment(s string) bool {
	return epNumRe.MatchString(s) || epUUIDRe.MatchString(s) ||
		(epHexRe.MatchString(s) && strings.ContainsAny(s, "0123456789"))
}

// routeTemplateEqual — SAF: parça sayısı aynı, her parça harf duyarsız eşit
// ya da bir yanı şablon (diğeri boş değil) ya da iki yanı kimlik; en az bir
// şablon/kimlik eşleşmesi şart (düz eşitlik birebir aşamanın işi).
func routeTemplateEqual(a, b string) bool {
	as := strings.Split(strings.TrimRight(a, "/"), "/")
	bs := strings.Split(strings.TrimRight(b, "/"), "/")
	if len(as) != len(bs) {
		return false
	}
	wild := false
	for i := range as {
		x, y := as[i], bs[i]
		switch {
		case strings.EqualFold(x, y) && !isPlaceholderSegment(x):
		case x == "" || y == "":
			return false
		case isPlaceholderSegment(x) || isPlaceholderSegment(y), isIDSegment(x) && isIDSegment(y):
			wild = true
		default:
			return false
		}
	}
	return wild
}

// endpointCatalogTokens — SAF: CatalogCandidates belirteçleri. Yol tek başına;
// ilk şablon/kimlik parçasından önceki önek de (somut "/users/42" katalogdaki
// "/users/{id}"yi alt-dize olarak İÇERMEZ).
func endpointCatalogTokens(path string) []string {
	out := []string{path}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if i == 0 || !(isPlaceholderSegment(s) || isIDSegment(s)) {
			continue
		}
		prefix := strings.Join(segs[:i], "/") + "/"
		long := false
		for _, ps := range segs[:i] {
			if len(ps) >= 2 {
				long = true
			}
		}
		if long && prefix != path {
			out = append(out, prefix)
		}
		break
	}
	return out
}

// splitSpanVerb — SAF: "POST /x" → ("POST", "/x"); fiilsiz ad → ("", ad).
func splitSpanVerb(name string) (string, string) {
	if i := strings.IndexByte(name, ' '); i > 0 && isHTTPVerb(name[:i]) {
		if rest := strings.TrimSpace(name[i+1:]); strings.HasPrefix(rest, "/") {
			return strings.ToUpper(name[:i]), rest
		}
	}
	return "", name
}

// scoreEndpoint — SAF: tek katalog satırının puanı (0 = eşleşmez). Fiil
// sorulup span'inki farklıysa başka endpoint'tir.
func scoreEndpoint(method, path string, c chstore.CatalogOperation) int {
	contains := strings.Contains(strings.ToLower(c.Operation), strings.ToLower(path))
	switch c.Source {
	case "span":
		verb, sp := splitSpanVerb(c.Operation)
		if method != "" && verb != "" && verb != method {
			return 0
		}
		withVerb := verb != "" && method != ""
		switch {
		case sp == path && withVerb:
			return 100
		case sp == path:
			return 90
		case strings.EqualFold(sp, path) && withVerb:
			return 80
		case strings.EqualFold(sp, path):
			return 70
		case routeTemplateEqual(sp, path):
			return 65
		case contains:
			return 20
		}
	case "endpoint":
		switch {
		case c.Operation == path:
			return 95
		case strings.EqualFold(c.Operation, path):
			return 75
		case routeTemplateEqual(c.Operation, path):
			return 60
		case contains:
			return 20
		}
	}
	return 0
}

func endpointTier(score int) int {
	switch {
	case score >= 90:
		return 3
	case score >= 70:
		return 2
	case score >= 60:
		return 1
	case score > 0:
		return 0
	}
	return -1
}

var endpointTierKind = map[int]string{3: "endpoint_exact", 2: "endpoint_case_insensitive", 1: "endpoint_template", 0: "endpoint_contains"}

// endpointGroup — bir servisteki eşleşen endpoint (span adı ve/veya rota).
type endpointGroup struct {
	Service   string
	SpanOp    string // span kaynağı operasyon adı ("POST /x"), yoksa ""
	RouteOp   string // endpoint kaynağı http_route, yoksa ""
	Score     int
	Calls     uint64
	spanScore int
	routeScr  int
	spanCalls uint64
	routeCall uint64
}

// Op — grubun operasyon adı (span önce).
func (g endpointGroup) Op() string {
	if g.SpanOp != "" {
		return g.SpanOp
	}
	return g.RouteOp
}

// endpointMatch — matchEndpoint sonucu. Pick nil + Choices dolu = sor.
type endpointMatch struct {
	Kind         string
	Pick         *endpointGroup
	Choices      []endpointGroup
	Alternatives []endpointGroup
}

// matchEndpoint — SAF (tablo testli). serviceHint: mesajda geçen katalog
// servisi ("" = yok); eşleşen satırı yoksa yok sayılır.
func matchEndpoint(method, path string, cands []chstore.CatalogOperation, serviceHint string) endpointMatch {
	method = strings.ToUpper(method)
	type hit struct {
		c     chstore.CatalogOperation
		score int
	}
	var hits []hit
	for _, c := range cands {
		if s := scoreEndpoint(method, path, c); s > 0 {
			hits = append(hits, hit{c, s})
		}
	}
	if serviceHint != "" {
		var narrowed []hit
		for _, h := range hits {
			if strings.EqualFold(h.c.Service, serviceHint) {
				narrowed = append(narrowed, h)
			}
		}
		if len(narrowed) > 0 {
			hits = narrowed
		}
	}
	best := -1
	for _, h := range hits {
		if t := endpointTier(h.score); t > best {
			best = t
		}
	}
	if best < 0 {
		return endpointMatch{}
	}
	groups := map[string]*endpointGroup{}
	var order []string
	for _, h := range hits {
		if endpointTier(h.score) != best {
			continue
		}
		_, p := splitSpanVerb(h.c.Operation)
		key := h.c.Service + "\x00" + strings.ToLower(p)
		g := groups[key]
		if g == nil {
			g = &endpointGroup{Service: h.c.Service}
			groups[key] = g
			order = append(order, key)
		}
		switch h.c.Source {
		case "span":
			if g.SpanOp == "" || h.score > g.spanScore || (h.score == g.spanScore && h.c.CallCount > g.spanCalls) {
				g.SpanOp, g.spanScore, g.spanCalls = h.c.Operation, h.score, h.c.CallCount
			}
		default:
			if g.RouteOp == "" || h.score > g.routeScr || (h.score == g.routeScr && h.c.CallCount > g.routeCall) {
				g.RouteOp, g.routeScr, g.routeCall = h.c.Operation, h.score, h.c.CallCount
			}
		}
		if h.score > g.Score {
			g.Score = h.score
		}
		if h.c.CallCount > g.Calls {
			g.Calls = h.c.CallCount
		}
	}
	gs := make([]endpointGroup, 0, len(order))
	for _, k := range order {
		gs = append(gs, *groups[k])
	}
	sort.SliceStable(gs, func(i, j int) bool {
		if gs[i].Calls != gs[j].Calls {
			return gs[i].Calls > gs[j].Calls
		}
		if gs[i].Score != gs[j].Score {
			return gs[i].Score > gs[j].Score
		}
		if gs[i].Service != gs[j].Service {
			return gs[i].Service < gs[j].Service
		}
		return gs[i].Op() < gs[j].Op()
	})
	m := endpointMatch{Kind: endpointTierKind[best]}
	if len(gs) == 1 || (gs[0].Calls > 0 && gs[0].Calls >= epDominanceFactor*gs[1].Calls) {
		pick := gs[0]
		m.Pick = &pick
		if rest := gs[1:]; len(rest) > 0 {
			if len(rest) > opChoiceMax {
				rest = rest[:opChoiceMax]
			}
			m.Alternatives = append([]endpointGroup(nil), rest...)
		}
		return m
	}
	if len(gs) > opChoiceMax {
		gs = gs[:opChoiceMax]
	}
	m.Choices = gs
	return m
}

// endpointLabel — SAF: "FİİL yol" (sorulan fiil önce, yoksa span adınınki).
func endpointLabel(g endpointGroup, method string) string {
	verb, sp := splitSpanVerb(g.SpanOp)
	path := g.RouteOp
	if path == "" {
		path = sp
	}
	if method == "" {
		method = verb
	}
	return strings.TrimSpace(method + " " + path)
}

// endpointChoiceChip — SAF: belirsizlik çipi; tek başına yönlenen tam cümle.
func endpointChoiceChip(g endpointGroup, method string) string {
	return g.Service + " " + endpointLabel(g, method) + " endpointine bak"
}

// endpointServiceHint — SAF: yol çıkarıldıktan sonra mesajda birebir sözcük
// olarak geçen katalog servisi (en uzun ad kazanır); yoksa "".
func endpointServiceHint(raw, path string, cands []chstore.CatalogOperation) string {
	rest := strings.Replace(raw, path, " ", 1)
	toks := map[string]bool{}
	for _, t := range guidedTokens(normalizeGuidedMsg(rest)) {
		toks[t] = true
	}
	best := ""
	for _, c := range cands {
		if c.Service != "" && toks[strings.ToLower(c.Service)] && len(c.Service) > len(best) {
			best = c.Service
		}
	}
	return best
}

// endpointTargetFor — SAF: seçilen grubun okuma hedefi (yalnız o servis).
func endpointTargetFor(g endpointGroup, method string) opTarget {
	t := opTarget{Name: g.Op(), HasSpan: g.SpanOp != "", Services: []string{g.Service}, Endpoint: true}
	if g.RouteOp != "" {
		t.HasRoute, t.Route = true, g.RouteOp
	}
	verb, _ := splitSpanVerb(g.SpanOp)
	t.Method = strings.ToUpper(method)
	t.MethodAsked = t.Method != "" // v0.10.1376 — sorudaki fiil
	if t.Method == "" {
		t.Method = verb
	}
	return t
}

// endpointPageHref — SAF: /endpoint sayfası (frontend pages/endpoints/
// endpointParam.ts endpointDetailHref; mcptools build_link "endpoint").
func endpointPageHref(service, route string, from, to time.Time) string {
	if service == "" || route == "" {
		return ""
	}
	q := url.Values{}
	q.Set("service", service)
	q.Set("path", route)
	q.Set("range", fmt.Sprintf("custom:%d-%d", from.UnixMilli(), to.UnixMilli()))
	return "/endpoint?" + q.Encode()
}

// opEndpointException — endpoint span'lerindeki exception tipi (kart + özet).
type opEndpointException struct {
	Type       string `json:"type"`
	Message    string `json:"message,omitempty"`
	Count      uint64 `json:"count"`
	LastSeenNs int64  `json:"lastSeenNs"`
	Href       string `json:"href"`
}

// opAlternative — ≥3× kuralıyla geri planda kalan servis.
type opAlternative struct {
	Service string `json:"service"`
	Op      string `json:"op"`
	Calls   uint64 `json:"calls"`
}

func opEndpointExceptionsFrom(rows []chstore.EndpointException) []opEndpointException {
	out := make([]opEndpointException, 0, len(rows))
	for _, r := range rows {
		e := opEndpointException{Type: r.Type, Message: r.Message, Count: r.Count, LastSeenNs: r.LastSeenNs}
		if r.Fingerprint != "" {
			e.Href = "/problems?exc=" + url.QueryEscape(r.Fingerprint)
		}
		out = append(out, e)
	}
	return out
}

// renderEndpointExceptionsTR — SAF: kısa bölüm (≤5 satır, mesaj ≤160 rune).
// OKUNAMADI ≠ yok.
func renderEndpointExceptionsTR(rows []opEndpointException, errState string) string {
	if errState != "" {
		return fmt.Sprintf("Endpoint teknik hataları OKUNAMADI (%s); exception durumu bilinmiyor, UYDURMA.\n", errState)
	}
	if len(rows) == 0 {
		return "Endpoint teknik hataları: bu pencerede endpoint span'lerinde exception yok.\n"
	}
	var b strings.Builder
	b.WriteString("Endpoint teknik hataları (endpoint span'lerindeki exception'lar, en sık önce):\n")
	for i, r := range rows {
		if i == epExRenderMax {
			fmt.Fprintf(&b, "(+%d tip daha — tümü Exceptions'ta)\n", len(rows)-epExRenderMax)
			break
		}
		fmt.Fprintf(&b, "- %s ×%d", r.Type, r.Count)
		if r.LastSeenNs > 0 {
			fmt.Fprintf(&b, ", son %s UTC", time.Unix(0, r.LastSeenNs).UTC().Format("15:04"))
		}
		if msg := oneLineSample(r.Message, epExMsgRunes); msg != "" {
			b.WriteString(": " + msg)
		}
		if r.Href != "" {
			b.WriteString(" · " + r.Href)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderEndpointAlternativesTR — SAF: ≥3× kuralının söylenişi.
func renderEndpointAlternativesTR(alts []opAlternative, picked string) string {
	if len(alts) == 0 {
		return ""
	}
	parts := make([]string, len(alts))
	for i, a := range alts {
		parts[i] = fmt.Sprintf("%s (%s, %d çağrı)", a.Service, a.Op, a.Calls)
	}
	return fmt.Sprintf("Ayrıca şu servislerde de var: %s — en çok çağrılan %s seçildi (≥%d×).\n", strings.Join(parts, ", "), picked, epDominanceFactor)
}

// opAlternativesFrom — SAF.
func opAlternativesFrom(gs []endpointGroup, method string) []opAlternative {
	if len(gs) == 0 {
		return nil
	}
	out := make([]opAlternative, len(gs))
	for i, g := range gs {
		out[i] = opAlternative{Service: g.Service, Op: endpointLabel(g, method), Calls: g.Calls}
	}
	return out
}
