package mcptools

// business_code.go — resolve_business_code: serbest metin (kanal kodu,
// fonksiyon kodu, operasyon adı, endpoint) → hangi servis(ler), kapsam
// GEREKMEDEN. Operatör (2026-10-15): "CoSRE channel/function code,
// operasyon adı ve endpoint'i scope istemeden servislere çözsün."
//
// Kaynaklar (sohbet anında ham span YOK):
//   - function_code / channel_code → business_code_summary_5m (ORDER BY kodla
//     başlar; servis süzgeci olmadan nokta okuma). function_code biçim kapısı
//     ^[A-Z][A-Z0-9]{4,9}$ (chstore.FunctionCodeShape).
//   - operation / endpoint → cosre_operation_catalog (operation_summary_5m /
//     spanmetrics_1m'den 10 dk'da bir; son 24 sa toplamları).
// Merdiven resolve_entity'nin scoreName'i: exact > prefix > substring >
// tokens > fuzzy. Birden çok aday ya da birden çok servis → ambiguous=true +
// seçenekler, TAHMİN YOK. Boş sonuç hata değil: reasons.

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// Kod türleri (sonuçtaki `kind`).
const (
	BizChannelCode  = "channel_code"
	BizFunctionCode = "function_code"
	BizOperation    = "operation"
	BizEndpoint     = "endpoint"
)

var bizKinds = []string{BizChannelCode, BizFunctionCode, BizOperation, BizEndpoint}

const (
	bizMaxCandidates = 5
	bizMinScore      = 50 // fuzzy ve üstü; zayıf kısmi kelime eşleşmesi aday değil
	bizIndexTTL      = 5 * time.Minute
	bizCatalogWindow = "son 24 sa (katalog)"
)

// BizName — çözümleyicinin aday listesindeki bir ad.
type BizName struct {
	Kind  string
	Value string
}

// BizServiceStat — adayın bir servisteki sayıları.
type BizServiceStat struct {
	Service      string  `json:"service"`
	Calls        uint64  `json:"calls"`
	Errors       uint64  `json:"errors"`
	ErrorRatePct float64 `json:"error_rate_pct"`
}

// BizCandidate — çözümleme adayı.
type BizCandidate struct {
	Kind     string           `json:"kind"`
	Value    string           `json:"value"`
	Match    string           `json:"match"`
	Score    int              `json:"score"`
	Window   string           `json:"window"`
	Services []BizServiceStat `json:"services"`
	Link     string           `json:"link,omitempty"`
}

// bizQueries — SAF: tam metin + ≥4 karakterli belirteçler ("BQRZ001 hangi
// serviste" → "bqrz001 hangi serviste", "bqrz001", "hangi", "serviste").
func bizQueries(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	out := []string{text}
	seen := map[string]bool{text: true}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == '?' || r == '!' || r == ';' || r == '"' || r == '\''
	}) {
		if len([]rune(f)) >= 4 && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// RankBizNames — SAF: adayları merdivenle skorlar; en güçlü katman kuralı
// resolve_entity ile aynı (tam eş varsa yalnız tam eşler), ≤max.
func RankBizNames(text string, names []BizName, max int) []BizCandidate {
	qs := bizQueries(text)
	if len(qs) == 0 {
		return nil
	}
	qts := make([][]string, len(qs))
	for i, q := range qs {
		qts[i] = NameTokens(q)
	}
	var out []BizCandidate
	for _, n := range names {
		best, match := 0, ""
		for i, q := range qs {
			if sc, m := scoreName(q, qts[i], n.Value); sc > best {
				best, match = sc, m
			}
		}
		if best >= bizMinScore {
			out = append(out, BizCandidate{Kind: n.Kind, Value: n.Value, Score: best, Match: match})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > 0 && out[0].Score == 100 { // tam eş varsa zayıflar düşer
		k := 0
		for k < len(out) && out[k].Score == 100 {
			k++
		}
		out = out[:k]
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// BizDecision — SAF karar: belirsiz mi, seçenekler, gerekçeler.
func BizDecision(cands []BizCandidate, text string, kinds []string) (ambiguous bool, options, reasons []string) {
	if len(cands) == 0 {
		return false, nil, []string{
			fmt.Sprintf("%q için %s ekseninde eşleşme yok", text, strings.Join(kinds, " / ")),
			"function_code yalnız ^[A-Z][A-Z0-9]{4,9}$ biçimindeyse indekslenir (GUID / sayısal ID / LOGIN-123 gibi değerler değil)",
			"operasyon ve endpoint son 24 saatte trafik almış olmalı (katalog 10 dk'da bir yenilenir)",
		}
	}
	top := cands[0].Score
	for _, c := range cands {
		if c.Score == top {
			options = append(options, c.Kind+": "+c.Value)
		}
	}
	if len(options) > 1 {
		return true, options, []string{fmt.Sprintf("%d aday aynı güçte eşleşti — kullanıcıya hangisi olduğunu sor", len(options))}
	}
	options = nil
	if n := len(cands[0].Services); n > 1 {
		for _, s := range cands[0].Services {
			options = append(options, s.Service)
		}
		return true, options, []string{fmt.Sprintf("%s %d serviste görülüyor — tahmin etme, hangi servis olduğunu sor ya da hepsini say", cands[0].Value, n)}
	}
	if len(cands[0].Services) == 0 {
		return false, nil, []string{cands[0].Value + " katalogda var ama pencerede servis sayısı okunmadı"}
	}
	return false, nil, nil
}

// bizStats — SAF: sayılar → hata oranı (yüzde, 2 hane).
func bizStats(in []chstore.CodeServiceStat) []BizServiceStat {
	out := make([]BizServiceStat, 0, len(in))
	for _, s := range in {
		st := BizServiceStat{Service: s.Service, Calls: s.Calls, Errors: s.Errors}
		if s.Calls > 0 {
			st.ErrorRatePct = float64(int(float64(s.Errors)/float64(s.Calls)*10000+0.5)) / 100
		}
		out = append(out, st)
	}
	return out
}

// bizWindowLabel — SAF: "son 24 sa" / "son 30 dk".
func bizWindowLabel(rs int) string {
	if rs >= 3600 && rs%3600 == 0 {
		return fmt.Sprintf("son %d sa", rs/3600)
	}
	return fmt.Sprintf("son %d dk", (rs+59)/60)
}

// bizFilter — SAF: adayın Traces süzgeci.
func bizFilter(kind, value string) chstore.FilterExpr {
	key := map[string]string{BizFunctionCode: "FUNCTION_CODE", BizChannelCode: "CHANNEL_CODE", BizOperation: "name", BizEndpoint: "http.route"}[kind]
	return chstore.FilterExpr{Key: key, Op: "=", Values: []string{value}}
}

// bizIndexKey — SAF: ad listesi önbellek anahtarı; TÜM girdiler (tür +
// pencere uçları 5 dk'ya oturmuş) FNV ile — uzunluk özeti değil (v0.5.187).
func bizIndexKey(kind string, from, to time.Time) string {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%d|%d", kind, from.Unix()/300, to.Unix()/300)
	return fmt.Sprintf("biz:%s:%x", kind, h.Sum64())
}

type bizCacheEntry struct {
	at    time.Time
	names []BizName
}

var (
	bizCacheMu sync.Mutex
	bizCache   = map[string]bizCacheEntry{}
	// bizClock — önbellek damgası (pencere DEĞİL; pencere rangeWindow'dan).
	bizClock = time.Now
)

// bizIndex — tür başına ad listesi (5 dk önbellek; ~17 K kod, ≤300 K ad).
func bizIndex(ctx context.Context, st *chstore.Store, kind string, from, to time.Time) ([]BizName, error) {
	key := bizIndexKey(kind, from, to)
	bizCacheMu.Lock()
	if e, ok := bizCache[key]; ok && bizClock().Sub(e.at) < bizIndexTTL {
		bizCacheMu.Unlock()
		return e.names, nil
	}
	bizCacheMu.Unlock()
	var names []BizName
	switch kind {
	case BizFunctionCode, BizChannelCode:
		pairs, err := st.BusinessCodeIndex(ctx, from, to)
		if err != nil {
			return nil, err
		}
		for _, p := range pairs {
			if p[0] == kind {
				names = append(names, BizName{Kind: kind, Value: p[1]})
			}
		}
	case BizOperation, BizEndpoint:
		src := "span"
		if kind == BizEndpoint {
			src = "endpoint"
		}
		ns, err := st.CatalogNames(ctx, src)
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			names = append(names, BizName{Kind: kind, Value: n})
		}
	}
	bizCacheMu.Lock()
	if len(bizCache) > 64 {
		bizCache = map[string]bizCacheEntry{}
	}
	bizCache[key] = bizCacheEntry{at: bizClock(), names: names}
	bizCacheMu.Unlock()
	return names, nil
}

type resolveBizArgs struct {
	Text   string `json:"text"`
	Kind   string `json:"kind,omitempty"`
	RangeS int    `json:"range_s,omitempty"`
}

func resolveBusinessCodeTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "resolve_business_code",
		ShortDescription: "Kanal/fonksiyon kodu, operasyon ya da endpoint hangi serviste — kapsamsız çözüm.",
		Description: "Resolve a business identifier the user typed — a CHANNEL_CODE, a FUNCTION_CODE (e.g. BQRZ001), an operation (span) name or an HTTP endpoint route — to the services that carry it, WITHOUT needing a service scope. " +
			"Each candidate has kind, value, match (exact|prefix|substring|tokens|fuzzy), per-service calls / errors / error_rate_pct, the window, and a link to the filtered Traces page. " +
			"ambiguous=true means several candidates or several services matched: ASK the user which one, never guess. An empty result is not an error — read reasons. " +
			"kind narrows the axis (default any). range_s applies to channel/function codes (default 1 day, max 7 days); operations/endpoints use the last 24 h catalog. Read-only.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":    map[string]any{"type": "string", "description": "The code / operation / endpoint as the user wrote it (typos allowed)."},
				"kind":    map[string]any{"type": "string", "enum": []string{"any", BizChannelCode, BizFunctionCode, BizOperation, BizEndpoint}, "description": "Axis to search. Default any."},
				"range_s": map[string]any{"type": "integer", "minimum": 0, "maximum": 604800, "description": "Look-back for channel/function codes. Default 86400."},
			},
			"required": []string{"text"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a resolveBizArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("decode args: %w", err)
			}
			a.Text = strings.TrimSpace(a.Text)
			if a.Text == "" {
				return nil, fmt.Errorf("text required")
			}
			kinds := bizKinds
			if a.Kind != "" && a.Kind != "any" {
				kinds = []string{a.Kind}
			}
			rs := a.RangeS
			if rs <= 0 {
				rs = 86400
			}
			from, to := rangeWindow(ctx, rs)
			var names []BizName
			var reasons []string
			for _, k := range kinds {
				ns, err := bizIndex(ctx, d.Store, k, from, to)
				if err != nil {
					reasons = append(reasons, k+" okunamadı: "+err.Error())
					continue
				}
				names = append(names, ns...)
			}
			cands := RankBizNames(a.Text, names, bizMaxCandidates)
			// Servis kırılımı: tür başına tek okuma.
			byKind := map[string][]string{}
			for _, c := range cands {
				byKind[c.Kind] = append(byKind[c.Kind], c.Value)
			}
			stats := map[string]map[string][]chstore.CodeServiceStat{}
			for k, vals := range byKind {
				var m map[string][]chstore.CodeServiceStat
				var err error
				switch k {
				case BizFunctionCode, BizChannelCode:
					m, err = d.Store.BusinessCodeServices(ctx, k, vals, from, to)
				case BizOperation:
					m, err = d.Store.CatalogServices(ctx, "span", vals)
				case BizEndpoint:
					m, err = d.Store.CatalogServices(ctx, "endpoint", vals)
				}
				if err != nil {
					reasons = append(reasons, k+" servis kırılımı okunamadı: "+err.Error())
					continue
				}
				stats[k] = m
			}
			rangeParam := fmt.Sprintf("custom:%d-%d", from.UnixMilli(), to.UnixMilli())
			for i := range cands {
				c := &cands[i]
				c.Services = bizStats(stats[c.Kind][c.Value])
				c.Window = bizWindowLabel(rs)
				if c.Kind == BizOperation || c.Kind == BizEndpoint {
					c.Window = bizCatalogWindow
				}
				svc := ""
				if len(c.Services) == 1 {
					svc = c.Services[0].Service
				}
				if href, err := buildLink(buildLinkArgs{Page: "traces", Service: svc}, "", []chstore.FilterExpr{bizFilter(c.Kind, c.Value)}, rangeParam); err == nil {
					c.Link = href
				}
			}
			ambiguous, options, why := BizDecision(cands, a.Text, kinds)
			if cands == nil {
				cands = []BizCandidate{}
			}
			return map[string]any{
				"query": a.Text, "ambiguous": ambiguous, "options": options,
				"candidates": cands, "reasons": append(reasons, why...),
			}, nil
		},
	}
}
