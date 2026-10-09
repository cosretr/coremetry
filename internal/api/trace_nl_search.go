package api

// trace_nl_search.go — v0.10.436 (CoSRE router boşlukları D2): iki doğal
// dil trace sorusu.
//
// (a) pair_requests — "A'dan B'ye giden istekler(in tamamını göster)":
//     yönlü A→B sayısı topology_edges_5m'den (MV-first: A'nın çocuk
//     kenarları, B servis ya da dış/DB düğüm — "login external'dan osbprod'a"
//     gibi host hedefleri de kapsar), örnek trace'ler A ve B'yi BİRLİKTE
//     içeren trace'ler (RequireServices) ya da B düğümse A'nın içinde B
//     parçası geçen trace'ler (Search). Link /traces?services=A,B — eş-
//     görünüm: anlatım "birlikte içeren, doğrudan kenar garantisi değil"
//     der (FocusedNeighborhood v0.9.381 etiketi ile aynı dürüstlük).
//     Spec 2026-09-06 "200 trace'lik örnekten yönlü sayım" demişti; MV
//     kenarı hem tam hem ucuz (ham span üzerinden agregat = bug ilkesi),
//     örnek yalnız trace LİSTESİ için kullanılır.
// (b) trace_search — "X servisinden içinde <host/route/sorgu> geçen
//     trace'ler": servis (bulanık, D1 adayları) + serbest parça →
//     GetTraces Search (name + http_method + http_route + attr_values,
//     büyük/küçük harf duyarsız, TÜM jetonlar); parça SQL görünümlüyse
//     db.statement LIKE (db_statement haystack'te değil). Kimlik-önce
//     arama (v0.10.342) Search üzerinden zaten koşar.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const guidedTraceSearchLimit = 10

var (
	// pairAblativeRe — "A'dan" / "A'den" / "A'tan" / "A’den" (ek apostroflu).
	pairAblativeRe = regexp.MustCompile(`^(.+?)['‘’](?:dan|den|tan|ten)$`)
	// pairDativeRe — "B'ye" / "B'ya" / "B'e" / "B'a".
	pairDativeRe = regexp.MustCompile(`^(.+?)['‘’](?:ye|ya|e|a)$`)
	pairArrowRe  = regexp.MustCompile(`^(.+?)\s*(?:->|→|=>)\s*(.+?)$`)
	sqlLikeRe    = regexp.MustCompile(`(?i)^\s*(select|insert|update|delete|with|merge)\b|\s(from|where|join)\s`)
)

func stripPairSuffix(tok string, re *regexp.Regexp) (string, bool) {
	if m := re.FindStringSubmatch(tok); m != nil {
		return m[1], true
	}
	return tok, false
}

// hasPairRequestSignal — istek/çağrı/trace sözcüğü ya da giden/gelen.
func hasPairRequestSignal(toks []string) bool {
	return tokenHasPrefix(toks, "istek", "çağrı", "cagri", "request", "call", "trace", "giden", "gelen", "atılan", "atilan", "yapılan", "yapilan")
}

// splitPairFragments — SAF: ham mesajı (kaynak parça, hedef parça) olarak
// böler. Şekiller: "A'dan B'ye …", "A servisinden B servisine …",
// "A servisinden B giden …", "from A to B", "A -> B". ok=false: çift yok.
func splitPairFragments(raw string) (from, to string, ok bool) {
	q := strings.TrimSpace(raw)
	if m := pairArrowRe.FindStringSubmatch(q); m != nil {
		return cleanPairFragment(m[1]), cleanPairFragment(strings.Fields(m[2])[0]), true
	}
	words := strings.Fields(q)
	fromEnd, toStart, toEnd := -1, -1, -1
	for i, w := range words {
		lw := strings.ToLower(w)
		if fromEnd < 0 {
			if lw == "servisinden" || lw == "servisten" || lw == "from" {
				if lw == "from" {
					// "from A to B requests": kaynak from'dan SONRA başlar, hedef
					// to'dan sonra ilk istek/çağrı sözcüğüne dek.
					for j := i + 1; j < len(words); j++ {
						if strings.ToLower(words[j]) == "to" {
							return cleanPairFragment(strings.Join(words[i+1:j], " ")), cleanPairFragment(strings.Join(cutAtRequestWord(words[j+1:]), " ")), j > i+1 && j+1 < len(words)
						}
					}
					return "", "", false
				}
				fromEnd = i // parça words[:i]
				toStart = i + 1
				continue
			}
			if base, hit := stripPairSuffix(w, pairAblativeRe); hit {
				words[i] = base
				fromEnd = i + 1
				toStart = i + 1
				continue
			}
			continue
		}
		if lw == "servisine" || lw == "servise" || lw == "to" {
			toEnd = i
			break
		}
		if base, hit := stripPairSuffix(w, pairDativeRe); hit {
			words[i] = base
			toEnd = i + 1
			break
		}
		if strings.HasPrefix(lw, "giden") || strings.HasPrefix(lw, "gelen") || strings.HasPrefix(lw, "atılan") || strings.HasPrefix(lw, "yapılan") || strings.HasPrefix(lw, "olan") {
			toEnd = i
			break
		}
	}
	if fromEnd <= 0 || toStart < 0 || toEnd <= toStart {
		return "", "", false
	}
	return cleanPairFragment(strings.Join(words[:fromEnd], " ")), cleanPairFragment(strings.Join(words[toStart:toEnd], " ")), true
}

// cutAtRequestWord — istek/çağrı/trace sözcüğünde keser ("payment requests" → "payment").
func cutAtRequestWord(words []string) []string {
	for i, w := range words {
		if hasPairRequestSignal([]string{strings.ToLower(w)}) {
			return words[:i]
		}
	}
	return words
}

// cleanPairFragment — parantezli açıklamayı ve noktalamayı atar.
func cleanPairFragment(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "("); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.Trim(s, " ,.;:\"'")
}

// resolvePairSide — parça → canlı servis adayları; 1 aday çözüldü, 2+ sor,
// 0 servis değil (hedef tarafta dış düğüm olabilir).
func resolvePairSide(frag string, services, envs []string) []string {
	if frag == "" {
		return nil
	}
	// Tam (sınırlı) ad önce — "checkout-service" tireli tek jeton olarak
	// aday üreticinin parça eşine oturmaz; sonra bulanık adaylar.
	if exact := extractServiceEntity(normalizeGuidedMsg(frag), services, envs); exact != "" {
		return []string{exact}
	}
	return serviceCandidates(frag, services, envs, guidedServiceAskMax)
}

// nodeFragmentTokens — dış düğüm parçasının aranabilir jetonları (≥3,
// stopword değil): "osbprod" → ["osbprod"]; "osbprod.example.com" aynen.
func nodeFragmentTokens(frag string) []string {
	var out []string
	for _, t := range strings.Fields(strings.ToLower(frag)) {
		t = strings.Trim(t, "()[]{}\"',.;")
		if len(t) >= 3 && !guidedStopwords[t] {
			out = append(out, t)
		}
	}
	return out
}

// extractTraceSearch — "X servisinden içinde <parça> geçen trace'ler",
// "içinde "…" geçen trace'leri getir", "traces containing <parça>".
// Trace kökü ŞART (log'lar D5). Parça: tırnaklı değer ya da "içinde …
// geçen/olan/içeren" arası.
func extractTraceSearch(raw string, toks []string) (frag string, isSQL bool, ok bool) {
	if !tokenHasPrefix(toks, "trace") {
		return "", false, false
	}
	hasCue := tokenHasPrefix(toks, "içinde", "icinde", "geçen", "gecen", "içeren", "iceren", "contain", "with", "including")
	if !hasCue {
		return "", false, false
	}
	quoted := false
	if q, ok := findQuotedValue(raw); ok {
		frag, quoted = q, true
	} else {
		lw := strings.ToLower(raw)
		start := -1
		for _, cue := range []string{"içinde ", "icinde ", "containing ", "with "} {
			if i := strings.Index(lw, cue); i >= 0 {
				start = i + len(cue)
				break
			}
		}
		if start < 0 {
			return "", false, false
		}
		rest := raw[start:]
		end := len(rest)
		for _, stop := range []string{" geçen", " gecen", " olan", " içeren", " iceren", " trace"} {
			if i := strings.Index(strings.ToLower(rest), stop); i >= 0 && i < end {
				end = i
			}
		}
		frag = strings.TrimSpace(rest[:end])
	}
	frag = strings.Trim(frag, "\"'“”‘’` ")
	if frag == "" || len([]rune(frag)) > 256 {
		return "", false, false
	}
	// v0.10.443 — tırnaksız parça değer ŞEKLİNDE olmalı (host/yol/sorgu:
	// nokta, /, :, =, -, _ ya da rakam taşır, ya da SQL); "içinde hata olan
	// trace'ler" gibi düz sözcükler literal arama olmasın.
	if !quoted && !traceSearchValueOK(frag) {
		return "", false, false
	}
	return frag, sqlLikeRe.MatchString(frag), true
}

var traceSearchValueRe = regexp.MustCompile(`[./:=_\-0-9]`)

func traceSearchValueOK(frag string) bool {
	return traceSearchValueRe.MatchString(frag) || sqlLikeRe.MatchString(frag)
}

// traceSearchFilter — SAF: parça → TraceFilter (SQL parçası db.statement
// LIKE, gerisi haystack Search).
func traceSearchFilter(service, env, frag string, isSQL bool, from, to time.Time) chstore.TraceFilter {
	f := chstore.TraceFilter{Service: service, Env: env, From: from, To: to, Sort: "duration", Order: "desc", Limit: guidedTraceSearchLimit, CountMode: "skip"}
	if isSQL {
		f.Filters = []chstore.FilterExpr{{Key: "db.statement", Op: "LIKE", Values: []string{frag}}}
	} else {
		f.Search = frag
	}
	return f
}

// searchKeyPick — SAF (v0.10.476, F3-5): örneklem eşleşmelerinden aranacak
// anahtarlar — yalnız TAM eşleşenler, tipli/terfi kolonu olanlar önce, ≤3.
// Alt-dize eşleşmeleri (host url.full'un içinde) haystack aramasına kalır.
func searchKeyPick(matches []mcptools.AttrValueMatch) []string {
	var col, arr []string
	for _, m := range matches {
		if m.Match != "exact" {
			continue
		}
		if m.Column != "" {
			col = append(col, m.Key)
		} else {
			arr = append(arr, m.Key)
		}
	}
	out := append(col, arr...)
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// mergeTraceRows — anahtar başına sonuçları trace_id ile tekilleştir, süreye
// göre sırala, limit.
func mergeTraceRows(sets [][]chstore.TraceRow, limit int) []chstore.TraceRow {
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
	sort.SliceStable(out, func(i, j int) bool { return out[i].DurationMs > out[j].DurationMs })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// traceSearchIO — v0.10.1133: guided trace aramasının okumaları. Bundle'ın
// gövdesi depoya değil bu üç fonksiyona bakar; testler sahte okuma verir
// (s.store somut tip). oracleOp nil = etkin Oracle kaynağı yok (adım HİÇ
// yayınlanmaz — Oracle'sız kurulumda akış birebir eskisi).
type traceSearchIO struct {
	scopedAttrs func(ctx context.Context, scope chstore.AttrScope, from, to time.Time, top, sample int) ([]chstore.ServiceAttrRow, error)
	traces      func(ctx context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, error)
	oracleOp    func(ctx context.Context, name string) (mcptools.OracleOpResolution, error)
}

func (s *Server) traceSearchIO() traceSearchIO {
	io := traceSearchIO{
		scopedAttrs: s.store.GetScopedAttrs,
		traces: func(ctx context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, error) {
			rows, _, _, err := s.store.GetTraces(ctx, f)
			return rows, err
		},
	}
	if s.oracleOpsEnabled() {
		io.oracleOp = s.ResolveOracleOperation
	}
	return io
}

func (s *Server) guidedTraceSearchBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64) (string, string, error) {
	return guidedTraceSearchRun(ctx, emit, route, from, to, rangeS, s.traceSearchIO())
}

// oracleOpNameRe — v0.10.1133: Oracle operasyon adı ŞEKLİ (büyük harf, rakam,
// alt çizgi; ≥6). Yalnız şekil kapısı: çözümleyiciye gitmeye değer mi.
var oracleOpNameRe = regexp.MustCompile(`^[A-Z0-9_]{6,}$`)

// looksLikeOracleOperation — SAF: en az bir harf ve bir alt çizgi şart (salt
// rakam bir kimlik ya da süre, "SAMP01" gibi tek kelime bir kod; operasyon
// adı değil).
func looksLikeOracleOperation(s string) bool {
	s = strings.TrimSpace(s)
	return oracleOpNameRe.MatchString(s) && strings.Contains(s, "_") && strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

func guidedTraceSearchRun(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64, io traceSearchIO) (string, string, error) {
	errorsOnly := route.TraceErrorsOnly // v0.10.479 (F4-2) — "sadece hatalı olanlar" takibi
	// v0.10.476 (F3-5; audit kabul 3-4) — ÖNCE anahtar keşfi: değer hangi
	// attribute'ta? Servis kapsamlı örneklem (5000 span) + tam eşleşen
	// anahtarlar → anahtar başına ayrı süzgeçli arama (OR yerine; v0.10.343
	// dersi) → birleşim. Anahtar bulunamazsa eski haystack araması AYNEN.
	var keyNote string
	if !route.SearchSQL && route.Service != "" {
		nk := emitGuidedStep(emit, "find_attribute_by_value", fmt.Sprintf(`{"service":%q,"value":%q}`, route.Service, route.SearchText))
		attrs, aerr := io.scopedAttrs(ctx, chstore.AttrScope{Service: route.Service}, from, to, 100, 20)
		if aerr != nil {
			emitGuidedStepResult(emit, nk, "find_attribute_by_value", "", aerr)
		} else {
			matches := mcptools.MatchSamples(attrs, route.SearchText)
			keys := searchKeyPick(matches)
			var sub []string
			for _, m := range matches {
				if m.Match == "substring" {
					sub = append(sub, m.Key)
				}
			}
			route.SearchKeys = keys
			res := fmt.Sprintf("tam eşleşen anahtar: %s; alt-dize (örneklem): %s", strings.Join(keys, ", "), strings.Join(sub, ", "))
			emitGuidedStepResult(emit, nk, "find_attribute_by_value", res, nil)
			if len(keys) > 0 {
				keyNote = "Anahtar keşfi (servis örneklemi, 5000 span): tam eşleşme " + strings.Join(keys, ", ")
				if len(sub) > 0 {
					keyNote += "; alt-dize olarak " + strings.Join(sub, ", ")
				}
				var sets [][]chstore.TraceRow
				for _, k := range keys {
					f := traceSearchFilter(route.Service, route.Env, route.SearchText, false, from, to)
					f.Search = ""
					f.HasError = errorsOnly
					f.Filters = []chstore.FilterExpr{{Key: k, Op: "=", Values: []string{route.SearchText}}}
					n := emitGuidedStep(emit, "trace_search", fmt.Sprintf(`{"service":%q,"filter":{"key":%q,"op":"=","value":%q},"limit":%d}`, route.Service, k, route.SearchText, guidedTraceSearchLimit))
					rows, err := io.traces(ctx, f)
					if err != nil {
						emitGuidedStepResult(emit, n, "trace_search", "", err)
						return "", "", err
					}
					emitGuidedStepResult(emit, n, "trace_search", fmt.Sprintf("%d trace (%s)", len(rows), k), nil)
					sets = append(sets, rows)
				}
				rows := mergeTraceRows(sets, guidedTraceSearchLimit)
				src := fmt.Sprintf("trace araması %s = %q (son %s%s; anahtar başına süzgeç, birleşim)", strings.Join(keys, " | "), route.SearchText, fmtAgoTR(rangeS), guidedScopeTR(route.Service, route.Env))
				return renderTraceSearchEvidenceTR(rows, *route, rangeS) + keyNote + "\n", src, nil
			}
		}
	}
	// v0.10.1133 — anahtar keşfi boş döndüyse ya da servis yoksa, haystack'ten
	// ÖNCE: metin Oracle operasyon adı şeklindeyse (ad span'lerde YOK, köprü
	// fonksiyon kodu) çözümleyiciye sor. Yalnız TAM ad eşleşmesi aranır; yakın
	// adlar yalnız "bunu mu demek istediniz" olarak cevaba eklenir.
	var nearNames []string
	if !route.SearchSQL && io.oracleOp != nil && looksLikeOracleOperation(route.SearchText) {
		ev, src, near, handled, err := guidedOracleOpTraces(ctx, emit, route, from, to, rangeS, io)
		if handled || err != nil {
			return ev, src, err
		}
		nearNames = near
	}
	f := traceSearchFilter(route.Service, route.Env, route.SearchText, route.SearchSQL, from, to)
	f.HasError = errorsOnly
	args, _ := json.Marshal(map[string]any{"service": route.Service, "search": route.SearchText, "sql": route.SearchSQL, "limit": guidedTraceSearchLimit})
	n := emitGuidedStep(emit, "trace_search", string(args))
	rows, err := io.traces(ctx, f)
	if err != nil {
		emitGuidedStepResult(emit, n, "trace_search", "", err)
		return "", "", err
	}
	emitGuidedStepResult(emit, n, "trace_search", fmt.Sprintf("%d trace", len(rows)), nil)
	src := fmt.Sprintf("trace araması %q (son %s%s; ad + http.route + attribute değerleri, büyük/küçük harf duyarsız)", route.SearchText, fmtAgoTR(rangeS), guidedScopeTR(route.Service, route.Env))
	if route.SearchSQL {
		src = fmt.Sprintf("trace araması db.statement LIKE %q (son %s%s)", route.SearchText, fmtAgoTR(rangeS), guidedScopeTR(route.Service, route.Env))
	}
	return renderTraceSearchEvidenceTR(rows, *route, rangeS) + oracleNearNamesNoteTR(nearNames), src, nil
}

// guidedOracleOpTraces — v0.10.1133: operasyon adı → kodlar → kod başına
// FUNCTION_CODE süzgeçli arama → birleşim. handled=false: tam eşleşme yok
// (ya da çözümleme hatası) — çağıran haystack'e AYNEN düşer; near o durumda
// cevaba eklenecek yakın adlardır. Servis boş olabilir: süzgeç servissiz çalışır.
func guidedOracleOpTraces(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64, io traceSearchIO) (evidence, src string, near []string, handled bool, err error) {
	name := strings.TrimSpace(route.SearchText)
	rn := emitGuidedStep(emit, "resolve_oracle_operation", fmt.Sprintf(`{"name":%q}`, name))
	res, rerr := io.oracleOp(ctx, name)
	if rerr != nil {
		// Çözümleme yan yol: hata çipte görünür, arama haystack ile sürer.
		emitGuidedStepResult(emit, rn, "resolve_oracle_operation", "", rerr)
		return "", "", nil, false, nil
	}
	// Savunma: çözümleyici sözleşmesi ≤5 tekil kod; burada da tekilleştir + kes.
	res.Matches = capOracleMatches(res.Matches)
	codes := res.Codes()
	emitGuidedStepResult(emit, rn, "resolve_oracle_operation", oracleResolveStepTR(res), nil)
	if len(codes) == 0 {
		return "", "", res.NearNames, false, nil
	}
	var sets [][]chstore.TraceRow
	for _, m := range res.Matches {
		f := traceSearchFilter(route.Service, route.Env, "", false, from, to)
		f.Search = ""
		f.HasError = route.TraceErrorsOnly
		f.Filters = []chstore.FilterExpr{{Key: m.SpanKey, Op: "=", Values: []string{m.Code}}}
		n := emitGuidedStep(emit, "trace_search", fmt.Sprintf(`{"service":%q,"filter":{"key":%q,"op":"=","value":%q},"limit":%d}`, route.Service, m.SpanKey, m.Code, guidedTraceSearchLimit))
		rows, terr := io.traces(ctx, f)
		if terr != nil {
			emitGuidedStepResult(emit, n, "trace_search", "", terr)
			return "", "", nil, true, terr
		}
		emitGuidedStepResult(emit, n, "trace_search", fmt.Sprintf("%d trace (%s = %s)", len(rows), m.SpanKey, m.Code), nil)
		sets = append(sets, rows)
	}
	rows := mergeTraceRows(sets, guidedTraceSearchLimit)
	// Link + sohbet bağlamı süzgeci operasyon adıyla değil KODLA kurulur.
	route.SearchKeys = []string{res.SpanKey}
	route.OracleCodes = codes
	src = fmt.Sprintf("%s; trace araması %s = %s (son %s%s; kod başına süzgeç, birleşim)",
		oracleTranslationTR(name, codes, route.TraceErrorsOnly), res.SpanKey, strings.Join(codes, " | "), fmtAgoTR(rangeS), guidedScopeTR(route.Service, route.Env))
	return renderOracleOpTraceEvidenceTR(rows, *route, name, res, rangeS), src, nil, true, nil
}

// capOracleMatches — SAF: kod tekilleştirme + mcptools.OracleOpMaxCodes tavanı.
func capOracleMatches(in []mcptools.OracleOpMatch) []mcptools.OracleOpMatch {
	seen := map[string]bool{}
	out := make([]mcptools.OracleOpMatch, 0, len(in))
	for _, m := range in {
		if m.Code == "" || seen[m.Code] || len(out) >= mcptools.OracleOpMaxCodes {
			continue
		}
		seen[m.Code] = true
		out = append(out, m)
	}
	return out
}

// oracleResolveStepTR — SAF: çözümleme adımının kanıt satırı ("SAMP01 (1 kod)").
func oracleResolveStepTR(res mcptools.OracleOpResolution) string {
	codes := res.Codes()
	if len(codes) > 0 {
		return fmt.Sprintf("%s (%d kod)", strings.Join(codes, ", "), len(codes))
	}
	if len(res.NearNames) > 0 {
		return "tam eşleşme yok; yakın adlar: " + strings.Join(res.NearNames, ", ")
	}
	return "tam eşleşme yok"
}

// oracleTranslationTR — SAF: çevirinin açık cümlesi (kanıt + kaynak satırı).
func oracleTranslationTR(name string, codes []string, errorsOnly bool) string {
	kind := "başarılı + hatalı tüm trace'ler"
	if errorsOnly {
		kind = "yalnız hatalı trace'ler"
	}
	return fmt.Sprintf("Oracle operasyonu %s → fonksiyon kodu %s ile arandı (%s)", name, strings.Join(codes, ", "), kind)
}

// renderOracleOpTraceEvidenceTR — SAF: çevrilmiş aramanın kanıt metni.
func renderOracleOpTraceEvidenceTR(rows []chstore.TraceRow, route guidedRoute, name string, res mcptools.OracleOpResolution, rangeS int64) string {
	var b strings.Builder
	codes := res.Codes()
	b.WriteString(oracleTranslationTR(name, codes, route.TraceErrorsOnly) + ".\n")
	fmt.Fprintf(&b, "Operasyon adı span'lerde yok; köprü Oracle hata tablosundaki fonksiyon kodu (span anahtarı %s). Bu çeviriyi cevapta AÇIKÇA söyle.\n", res.SpanKey)
	fmt.Fprintf(&b, "Trace araması — %s = %s (son %s%s, en yavaş %d):\n", res.SpanKey, strings.Join(codes, " | "), fmtAgoTR(rangeS), guidedScopeTR(route.Service, route.Env), guidedTraceSearchLimit)
	if len(rows) == 0 {
		b.WriteString("Bu pencerede bu fonksiyon kod(lar)ıyla trace yok — dürüstçe söyle; pencereyi genişletmeyi öner.\n")
		if t := res.LatestTraceID(); t != "" {
			fmt.Fprintf(&b, "Oracle hata tablosundaki son hata trace'i: trace=%s (bu pencerenin dışında olabilir; \"son hata trace'i\" olarak sun).\n", t)
		}
		return b.String()
	}
	for _, r := range rows {
		flag := ""
		if r.HasError {
			flag = ", HATA"
		}
		fmt.Fprintf(&b, "- %.0fms — %s / %s (%d span%s) trace=%s\n", r.DurationMs, r.ServiceName, r.RootName, r.SpanCount, flag, r.TraceID)
	}
	b.WriteString("Yorum: kaç trace bulunduğunu, hata olup olmadığını ve ortak kök operasyonu söyle; listede olmayan trace ya da servis uydurma.\n")
	return b.String()
}

// extractOracleOpTraceRequest — v0.10.1133, SAF: "<OPERASYON_ADI> operasyonuna
// ait trace'leri getir". Dar kapı: trace kökü + "operasyon/operation" sözcüğü +
// tam BİR operasyon-adı şekilli (büyük harf, alt çizgi taşıyan) kelime, ve o
// kelime "operasyon…" sözcüğüne BİTİŞİK ("X operasyonuna" / "operation X").
// Bitişiklik "CONNECTION_TIMEOUT hatası veren operasyonların trace'leri"
// gibi hata kodlarını eski rotasında bırakır. known: servis/ortam adları (+
// çözülmüş svc/env) — harf duyarsız eşleşen kelime aday sayılmaz.
func extractOracleOpTraceRequest(raw string, toks []string, known []string) (string, bool) {
	if !tokenHasPrefix(toks, "trace") || !tokenHasPrefix(toks, "operasyon", "operation") {
		return "", false
	}
	isKnown := func(w string) bool {
		for _, k := range known {
			if k != "" && strings.EqualFold(strings.TrimSpace(k), w) {
				return true
			}
		}
		return false
	}
	isOpWord := func(w string) bool {
		lw := strings.ToLower(w)
		return strings.HasPrefix(lw, "operasyon") || strings.HasPrefix(lw, "operation")
	}
	words := strings.Fields(raw)
	clean := make([]string, len(words))
	for i, w := range words {
		if k := strings.IndexAny(w, "'‘’"); k > 0 { // "X'in", "X’e" — Türkçe ek
			w = w[:k]
		}
		clean[i] = strings.Trim(w, "\"“”`.,;:!?()[]")
	}
	found, adjacent := "", false
	for i, w := range clean {
		if !strings.Contains(w, "_") || !looksLikeOracleOperation(w) || isKnown(w) {
			continue
		}
		if found != "" && found != w {
			return "", false // iki aday: tahmin etme
		}
		found = w
		if (i+1 < len(clean) && isOpWord(clean[i+1])) || (i > 0 && isOpWord(clean[i-1])) {
			adjacent = true
		}
	}
	if found == "" || !adjacent {
		return "", false
	}
	return found, true
}

// oracleNearNamesNoteTR — SAF: tam eşleşme yokken Oracle'daki yakın adlar
// ("" = ekleme yok; haystack kanıtı o zaman birebir eskisi).
func oracleNearNamesNoteTR(near []string) string {
	if len(near) == 0 {
		return ""
	}
	return "Oracle hata tablosunda bu adla TAM eşleşen operasyon yok; benzer adlar (otomatik ARANMADI): " + strings.Join(near, ", ") +
		". Cevabın sonunda \"Bunu mu demek istediniz?\" başlığıyla bu adları listele.\n"
}

func renderTraceSearchEvidenceTR(rows []chstore.TraceRow, route guidedRoute, rangeS int64) string {
	var b strings.Builder
	where := "ad/route/attribute değerlerinde"
	if route.SearchSQL {
		where = "db.statement içinde"
	}
	if len(route.SearchKeys) > 0 { // v0.10.476 (F3-5)
		where = strings.Join(route.SearchKeys, " | ") + " anahtarında (tam eşitlik)"
	}
	fmt.Fprintf(&b, "Trace araması — %s %q geçen trace'ler (son %s%s, en yavaş %d):\n", where, route.SearchText, fmtAgoTR(rangeS), guidedScopeTR(route.Service, route.Env), guidedTraceSearchLimit)
	if len(rows) == 0 {
		b.WriteString("Bu pencerede eşleşen trace yok — dürüstçe söyle; parça yazımı ya da servis farklı olabilir.\n")
		return b.String()
	}
	for _, r := range rows {
		flag := ""
		if r.HasError {
			flag = ", HATA"
		}
		fmt.Fprintf(&b, "- %.0fms — %s / %s (%d span%s) trace=%s\n", r.DurationMs, r.ServiceName, r.RootName, r.SpanCount, flag, r.TraceID)
	}
	b.WriteString("Yorum: kaç trace bulunduğunu, hata olup olmadığını ve ortak kök operasyonu söyle; listede olmayan trace ya da servis uydurma.\n")
	return b.String()
}

// guidedPairBundle — A→B: MV kenarları + örnek trace'ler.
func (s *Server) guidedPairBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64) (string, string, error) {
	a, b := route.PairFrom, route.PairTo
	n := emitGuidedStep(emit, "topology_edges", fmt.Sprintf(`{"from":%q,"to":%q}`, a, b))
	edges, err := s.store.ReadServiceTopologyAggForFocus(ctx, from, to, a, 1, 20000)
	if err != nil {
		emitGuidedStepResult(emit, n, "topology_edges", "", err)
		return "", "", err
	}
	matched, others := matchPairEdges(edges, a, b, route.PairToKind == "service")
	emitGuidedStepResult(emit, n, "topology_edges", fmt.Sprintf("%d kenar", len(matched)), nil)
	// Eşleşen düğüm adını rotaya yaz (link + çipler gerçek adı taşısın).
	if route.PairToKind != "service" && len(matched) > 0 {
		route.PairTo = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(matched[0].ChildNode, "ext:"), "db:"), "q:")
	}
	var rows []chstore.TraceRow
	if len(matched) > 0 || route.PairToKind == "service" {
		f := chstore.TraceFilter{Service: a, Env: route.Env, From: from, To: to, Sort: "duration", Order: "desc", Limit: guidedTraceSearchLimit, CountMode: "skip"}
		if route.PairToKind == "service" {
			f.RequireServices = []string{a, b}
		} else {
			f.Search = route.PairTo
		}
		nt := emitGuidedStep(emit, "traces", fmt.Sprintf(`{"service":%q,"with":%q}`, a, route.PairTo))
		rows, _, _, err = s.store.GetTraces(ctx, f)
		if err != nil {
			emitGuidedStepResult(emit, nt, "traces", "", err)
			return "", "", err
		}
		emitGuidedStepResult(emit, nt, "traces", fmt.Sprintf("%d trace", len(rows)), nil)
	}
	src := fmt.Sprintf("topology_edges_5m (%s → %s, son %s) + örnek trace'ler", a, route.PairTo, fmtAgoTR(rangeS))
	return renderPairEvidenceTR(*route, matched, others, rows, rangeS), src, nil
}

// matchPairEdges — SAF: A'nın çocuk kenarlarından B'ye gidenler; B servis
// ise tam ad, düğümse parça eşleşmesi (ext:/db:/q: önekleri atılır).
// others: eşleşme yoksa "şu hedefler var" listesi için en çok çağrılan 8.
func matchPairEdges(edges []chstore.ServiceTopologyEdge, a, b string, bIsService bool) (matched, others []chstore.ServiceTopologyEdge) {
	frag := nodeFragmentTokens(b)
	for _, e := range edges {
		if e.ParentService != a {
			continue
		}
		child := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(e.ChildNode, "ext:"), "db:"), "q:")
		hit := false
		if bIsService {
			hit = e.NodeKind == "service" && child == b
		} else {
			lc := strings.ToLower(child + " " + e.ExtDisplay)
			for _, t := range frag {
				if strings.Contains(lc, t) {
					hit = true
					break
				}
			}
		}
		if hit {
			matched = append(matched, e)
		} else {
			others = append(others, e)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i].Calls > others[j].Calls })
	if len(others) > 8 {
		others = others[:8]
	}
	return matched, others
}

func renderPairEvidenceTR(route guidedRoute, matched, others []chstore.ServiceTopologyEdge, rows []chstore.TraceRow, rangeS int64) string {
	var b strings.Builder
	a, to := route.PairFrom, route.PairTo
	fmt.Fprintf(&b, "%s → %s istekleri (son %s):\n", a, to, fmtAgoTR(rangeS))
	if len(matched) == 0 {
		fmt.Fprintf(&b, "Bu pencerede %s'dan %s'a giden çağrı kenarı YOK (topology_edges_5m).", a, to)
		if len(others) > 0 {
			var names []string
			for _, e := range others {
				child := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(e.ChildNode, "ext:"), "db:"), "q:")
				names = append(names, fmt.Sprintf("%s (%d)", child, e.Calls))
			}
			fmt.Fprintf(&b, " %s'ın hedefleri: %s.", a, strings.Join(names, ", "))
		}
		b.WriteString("\nYorum: kenar bulunmadığını söyle, hedef adı farklı yazılmış olabilir; hedef listesinden uygun olanı öner.\n")
		return b.String()
	}
	var calls, errs uint64
	var p99 float64
	for _, e := range matched {
		calls += e.Calls
		errs += e.Errors
		if e.P99Ms > p99 {
			p99 = e.P99Ms
		}
		proto := e.Protocol
		if proto == "" {
			proto = "-"
		}
		fmt.Fprintf(&b, "- %s → %s [%s/%s]: %d çağrı, %d hata (%.1f%%), ort %.0f ms, p99 %.0f ms\n", a, strings.TrimPrefix(strings.TrimPrefix(e.ChildNode, "ext:"), "db:"), e.NodeKind, proto, e.Calls, e.Errors, e.ErrorRate, e.AvgMs, e.P99Ms)
	}
	rate := 0.0
	if calls > 0 {
		rate = float64(errs) / float64(calls) * 100
	}
	fmt.Fprintf(&b, "Toplam: %d yönlü çağrı, %d hata (%.1f%%), p99 %.0f ms — 5 dk ön-toplamdan, tam sayım.\n", calls, errs, rate, p99)
	if len(rows) > 0 {
		if route.PairToKind == "service" {
			fmt.Fprintf(&b, "Örnek trace'ler (%s ve %s'ı BİRLİKTE içeren, en yavaş %d — doğrudan kenar garantisi değil, eş-görünüm):\n", a, to, len(rows))
		} else {
			fmt.Fprintf(&b, "Örnek trace'ler (%s içinde %q geçen, en yavaş %d):\n", a, to, len(rows))
		}
		for _, r := range rows {
			flag := ""
			if r.HasError {
				flag = ", HATA"
			}
			fmt.Fprintf(&b, "- %.0fms — %s / %s (%d span%s) trace=%s\n", r.DurationMs, r.ServiceName, r.RootName, r.SpanCount, flag, r.TraceID)
		}
	} else {
		b.WriteString("Örnek trace bulunamadı (kenar var ama pencerede eşleşen trace yok — örnekleme ya da saklama).\n")
	}
	b.WriteString("Yorum: sayı, hata oranı ve p99'u söyle; 'isteklerin tamamı' için linkteki listenin eş-görünüm (ikisini birlikte içeren trace'ler) olduğunu, doğrudan kenar garantisi vermediğini belirt; kanıtta olmayan servis/sayı uydurma.\n")
	return b.String()
}

// oracleCodeFilter — v0.10.1133, SAF: çevrilmiş aramanın tek süzgeç çipi
// (link + sohbet bağlamı); tek kod "=", birden çok kod "IN".
func oracleCodeFilter(key string, codes []string) chstore.FilterExpr {
	if len(codes) == 1 {
		return chstore.FilterExpr{Key: key, Op: "=", Values: []string{codes[0]}}
	}
	return chstore.FilterExpr{Key: key, Op: "IN", Values: append([]string(nil), codes...)}
}
