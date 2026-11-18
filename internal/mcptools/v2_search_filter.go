package mcptools

// v2_search_filter.go — v0.10.2349 (docs/cosre/investigation-design.md S2,
// kararlar 3–9, §4.1 "Arama süzgeci"): find_traces'in `endpoint`, `client_ip`,
// `filters[≤3]` argümanları ve v2 search_logs'un `client_ip`'i için SAF plan
// parçaları + adlandırılmış hata sözleşmesi.
//
//   - filters: kapalı model — en çok 3, AND; op eq | ne | in | exists; değer
//     ≤160 rune, `values` ≤10; kontrol karakteri ve boşluklu anahtar reddedilir.
//     Regex / içerir / serbest metin YOK (search_traces bu yüzden gizli).
//   - anahtar doğrulama (validateFilterKeys): tipli / terfi kolon
//     (chstore.AttrKeyColumn) ya da kapsamın keşfedilmiş kataloğunda
//     (GetScopedAttrs, 60 sn memo) ya da eşitlikte kesin prob > 0 → kabul;
//     değilse `unknown_attribute {key, near ≤5}`.
//   - client_ip: kapalı aday anahtar listesinde TEK OR grubu (ClientIPKeys);
//     tam IPv4 / IPv6 `=`, IPv4 öneki yalnız sekizli sınırında (/8 /16 /24 ya
//     da "192.0.2.") çapalı `=~`, tek servis, ≤1 sa. x-forwarded-for listede
//     YOK (değeri liste, tam eşleşme tutmaz). Logda yalnız tam adres.
//   - endpoint: sunucu Deps.ResolveEndpoint (api matchEndpoint) ile tam
//     operasyona çözer; `endpoint_ambiguous {candidates ≤4}` /
//     `endpoint_not_found {near ≤3}`.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// ClientIPKeys — istemci adresini taşıyabilen span anahtarları (karar 6;
// kapalı liste, model anahtar seçmez). Kod yalnız ilkini bilir; diğerleri
// eski SDK adlarıdır (attribute'lar aynen saklanır).
var ClientIPKeys = []string{"client.address", "http.client_ip", "net.sock.peer.addr", "network.peer.address", "net.peer.ip"}

// Sınırlar (§4.1).
const (
	sfMaxFilters    = 3
	sfMaxValueRunes = 160
	sfMaxValues     = 10
	sfMaxKeyRunes   = 128
	sfNearMax       = 5
	epCandidatesMax = 4
	epNearMax       = 3
	// sfCatalogWindowS — katalog örnekleminin penceresi (son 1 sa).
	sfCatalogWindowS = 3600
	// sfCatalogServices — çok servisli kapsamda kataloğu okunan en çok servis.
	sfCatalogServices = 3
)

// sfCatalogTTL — kapsamın anahtar kataloğu memo'su (karar 4: 60 sn).
const sfCatalogTTL = 60 * time.Second

// Adlandırılmış arama hata sınıfları (mcp.ToolError.Error).
const (
	ErrUnknownAttribute  = "unknown_attribute"
	ErrEndpointAmbiguous = "endpoint_ambiguous"
	ErrEndpointNotFound  = "endpoint_not_found"
	ErrBadClientIP       = "bad_client_ip"
)

// SearchError — modele giden adlandırılmış hata (mcp.ToolErrorer).
type SearchError struct {
	Class      string
	Hint       string
	Key        string
	Near       []string
	Candidates []string
}

func (e *SearchError) Error() string {
	var b strings.Builder
	b.WriteString(e.Class)
	if e.Key != "" {
		b.WriteString(" " + e.Key)
	}
	if len(e.Near) > 0 {
		b.WriteString(" near=" + strings.Join(e.Near, ","))
	}
	if len(e.Candidates) > 0 {
		b.WriteString(" candidates=" + strings.Join(e.Candidates, ","))
	}
	return b.String()
}

// ToolError — sözleşme nesnesi (retryable false: aynı argümanla tekrar etme).
func (e *SearchError) ToolError() mcp.ToolError {
	return mcp.ToolError{Error: e.Class, Retryable: false, Hint: e.Hint, Key: e.Key, Near: e.Near, Candidates: e.Candidates}
}

func badSearchArgs(format string, a ...any) *SearchError {
	return &SearchError{Class: mcp.ToolErrBadArgs, Hint: fmt.Sprintf(format, a...) + " — argümanı düzelt; AYNI argümanla tekrar deneme"}
}

// ── filters ─────────────────────────────────────────────────────────

// v2FilterOps — kapalı işleç kümesi → FilterExpr işleci.
var v2FilterOps = map[string]string{"eq": "=", "ne": "!=", "in": "IN", "exists": "EXISTS"}

func hasControlOrSpace(s string, space bool) bool {
	for _, r := range s {
		if unicode.IsControl(r) || (space && unicode.IsSpace(r)) {
			return true
		}
	}
	return false
}

// planSearchFilters — SAF: modelin süzgeçleri → AND yaprakları.
func planSearchFilters(in []SearchFilter) ([]chstore.FilterExpr, error) {
	if len(in) > sfMaxFilters {
		return nil, badSearchArgs("filters en çok %d süzgeç alır (%d verildi)", sfMaxFilters, len(in))
	}
	out := make([]chstore.FilterExpr, 0, len(in))
	for i, f := range in {
		key := strings.TrimSpace(f.Key)
		if key == "" || utf8.RuneCountInString(key) > sfMaxKeyRunes || hasControlOrSpace(key, true) {
			return nil, badSearchArgs("filters[%d].key boş, çok uzun ya da boşluk / kontrol karakteri içeriyor", i)
		}
		opName := strings.ToLower(strings.TrimSpace(f.Op))
		if opName == "" {
			opName = "eq"
		}
		op, ok := v2FilterOps[opName]
		if !ok {
			return nil, badSearchArgs("filters[%d].op %q geçersiz: yalnız eq | ne | in | exists", i, f.Op)
		}
		vals := f.Values
		if len(vals) == 0 && f.Value != "" {
			vals = []string{f.Value}
		}
		if op == "EXISTS" {
			out = append(out, chstore.FilterExpr{Key: key, Op: op})
			continue
		}
		if len(vals) == 0 || len(vals) > sfMaxValues {
			return nil, badSearchArgs("filters[%d] (%s) 1-%d değer ister", i, opName, sfMaxValues)
		}
		for _, v := range vals {
			if strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > sfMaxValueRunes || hasControlOrSpace(v, false) {
				return nil, badSearchArgs("filters[%d] değeri boş, %d runeden uzun ya da kontrol karakteri içeriyor", i, sfMaxValueRunes)
			}
		}
		if op != "IN" {
			vals = vals[:1]
		}
		out = append(out, chstore.FilterExpr{Key: key, Op: op, Values: append([]string(nil), vals...)})
	}
	return out, nil
}

// ── client_ip ───────────────────────────────────────────────────────

// clientIPMatch — çözülmüş istemci adresi. Prefix: IPv4 sekizli öneki ("192.0.2.").
type clientIPMatch struct {
	Value  string // tam adres (kanonik) ya da önek
	Prefix bool
}

var dottedPrefixRe = regexp.MustCompile(`^(\d{1,3}\.){1,3}$`)

func errBadClientIP(raw string, allowPrefix bool) error {
	h := "client_ip tam bir IPv4 / IPv6 adresi olmalı"
	if allowPrefix {
		h += " ya da IPv4 sekizli öneki (\"192.0.2.\" ya da /8, /16, /24)"
	}
	return &SearchError{Class: ErrBadClientIP, Key: truncateRunes(raw, 64), Hint: h + "; başka biçim (aralık, /20, joker, liste) yok"}
}

// parseClientIP — SAF. allowPrefix yalnız trace'te (karar 7).
func parseClientIP(raw string, allowPrefix bool) (clientIPMatch, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return clientIPMatch{}, nil
	}
	if a, err := netip.ParseAddr(raw); err == nil && a.Zone() == "" {
		return clientIPMatch{Value: a.Unmap().String()}, nil
	}
	if !allowPrefix {
		return clientIPMatch{}, errBadClientIP(raw, false)
	}
	if p, err := netip.ParsePrefix(raw); err == nil {
		bits := p.Bits()
		if !p.Addr().Is4() || (bits != 8 && bits != 16 && bits != 24) {
			return clientIPMatch{}, errBadClientIP(raw, true)
		}
		oct := p.Masked().Addr().As4()
		var b strings.Builder
		for i := 0; i < bits/8; i++ {
			b.WriteString(strconv.Itoa(int(oct[i])) + ".")
		}
		return clientIPMatch{Value: b.String(), Prefix: true}, nil
	}
	if dottedPrefixRe.MatchString(raw) {
		for _, o := range strings.Split(strings.TrimSuffix(raw, "."), ".") {
			if n, err := strconv.Atoi(o); err != nil || n > 255 || (len(o) > 1 && o[0] == '0') {
				return clientIPMatch{}, errBadClientIP(raw, true)
			}
		}
		return clientIPMatch{Value: raw, Prefix: true}, nil
	}
	return clientIPMatch{}, errBadClientIP(raw, true)
}

// clientIPGroup — SAF: aday anahtarlarda TEK OR grubu (`=` ya da çapalı `=~`).
func clientIPGroup(m clientIPMatch) chstore.FilterGroup {
	g := chstore.FilterGroup{Join: "OR"}
	for _, k := range ClientIPKeys {
		if m.Prefix {
			g.Filters = append(g.Filters, chstore.FilterExpr{Key: k, Op: "=~", Values: []string{regexp.QuoteMeta(m.Value) + ".*"}})
		} else {
			g.Filters = append(g.Filters, chstore.FilterExpr{Key: k, Op: "=", Values: []string{m.Value}})
		}
	}
	return g
}

// ── anahtar doğrulama ───────────────────────────────────────────────

// attrKeyLookup — doğrulamanın G/Ç kancaları (test sahte verir).
type attrKeyLookup struct {
	Catalog func(ctx context.Context) ([]string, error)
	Probe   func(ctx context.Context, key, value string) (uint64, error)
}

func stripAttrScope(k string) string {
	return strings.TrimPrefix(strings.TrimPrefix(k, "span."), "resource.")
}

// validateFilterKeys — karar 4: kolon → katalog → kesin prob; ilk bilinmeyen
// anahtar `unknown_attribute` + ≤5 yakın anahtar. Katalog okunamadıysa ve prob
// teyit etmediyse okuma hatası döner (bilinmeyen sayılmaz).
func validateFilterKeys(ctx context.Context, filters []chstore.FilterExpr, lk attrKeyLookup) error {
	var catalog []string
	var catErr error
	loaded := false
	for _, f := range filters {
		if _, _, ok := chstore.AttrKeyColumn(f.Key); ok {
			continue
		}
		if !loaded && lk.Catalog != nil {
			catalog, catErr = lk.Catalog(ctx)
			loaded = true
		}
		want := stripAttrScope(f.Key)
		found := false
		for _, k := range catalog {
			if k == f.Key || stripAttrScope(k) == want {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if f.Op == "=" && len(f.Values) == 1 && lk.Probe != nil {
			if n, err := lk.Probe(ctx, f.Key, f.Values[0]); err == nil && n > 0 {
				continue
			}
		}
		if catErr != nil {
			return catErr
		}
		return &SearchError{Class: ErrUnknownAttribute, Key: f.Key, Near: nearAttrKeys(f.Key, catalog, sfNearMax),
			Hint: "bu anahtar kapsamın örnekleminde ve tipli kolonlarda yok — `near`daki anahtarlardan birini kullan ya da describe_attributes ile doğrula; anahtar UYDURMA"}
	}
	return nil
}

// nearAttrKeys — SAF: düzenleme uzaklığı (küçük harf, kapsam öneksiz) +
// alt-dize; en çok n, tekil, uzaklık sonra ad sırası.
func nearAttrKeys(key string, catalog []string, n int) []string {
	want := strings.ToLower(stripAttrScope(key))
	type cand struct {
		k string
		d int
	}
	var cs []cand
	seen := map[string]bool{}
	for _, k := range catalog {
		if seen[k] {
			continue
		}
		seen[k] = true
		lk := strings.ToLower(stripAttrScope(k))
		d := levenshtein(want, lk)
		if strings.Contains(lk, want) || strings.Contains(want, lk) {
			d = min(d, 1)
		}
		if d <= max(3, len(want)/2) {
			cs = append(cs, cand{k, d})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].k < cs[j].k
	})
	out := make([]string, 0, n)
	for _, c := range cs {
		if len(out) == n {
			break
		}
		out = append(out, c.k)
	}
	return out
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// ── endpoint ────────────────────────────────────────────────────────

// Endpoint çözüm türleri.
const (
	EndpointResolved  = "ok"
	EndpointAmbiguous = "ambiguous"
	EndpointNotFound  = "not_found"
)

// EndpointResolution — Deps.ResolveEndpoint sonucu (api matchEndpoint).
type EndpointResolution struct {
	Kind       string
	Service    string
	Operation  string   // tam span adı ("" = yalnız rota biliniyor)
	Route      string   // http.route ("" = bilinmiyor)
	Candidates []string // ambiguous: "servis FİİL yol" (≤4)
	Near       []string // not_found: yakın operasyonlar (≤3)
}

// endpointFilter — SAF: çözülen uç noktanın süzgeci (span adı tipli kolon;
// yalnız rota biliniyorsa http.route).
func endpointFilter(r EndpointResolution) chstore.FilterExpr {
	if r.Operation != "" {
		return chstore.FilterExpr{Key: "name", Op: "=", Values: []string{r.Operation}}
	}
	return chstore.FilterExpr{Key: "http.route", Op: "=", Values: []string{r.Route}}
}

// endpointError — SAF: çözülemeyen uç noktanın adlandırılmış hatası (nil = çözüldü).
func endpointError(endpoint string, r EndpointResolution) error {
	switch r.Kind {
	case EndpointResolved:
		if r.Operation == "" && r.Route == "" {
			break
		}
		return nil
	case EndpointAmbiguous:
		c := r.Candidates
		if len(c) > epCandidatesMax {
			c = c[:epCandidatesMax]
		}
		return &SearchError{Class: ErrEndpointAmbiguous, Key: endpoint, Candidates: c,
			Hint: "uç nokta birden çok serviste / operasyonda eşleşti — kullanıcıya `candidates`tan hangisi olduğunu sor ya da service ver"}
	}
	n := r.Near
	if len(n) > epNearMax {
		n = n[:epNearMax]
	}
	return &SearchError{Class: ErrEndpointNotFound, Key: endpoint, Near: n,
		Hint: "uç nokta bu kapsamın operasyon kataloğunda yok — yazımı `near` ile doğrula ya da list_operations kullan; yol UYDURMA"}
}

// scopedAttrLookup — find_traces'in anahtar doğrulama kancaları: kapsamın
// (tek servis ya da kümenin ilk sfCatalogServices servisi) keşfedilmiş anahtar
// kataloğu (GetScopedAttrs, 5.000 span örneklemi, son 1 sa; servis başına
// 60 sn memo, anahtar bütün girdileri — servis + pencere — özetler) ve kesin
// eşitlik probu (AttrValueProbe, kendi 3 sn tavanı).
func scopedAttrLookup(d Deps, p findTracesPlan, to time.Time) attrKeyLookup {
	svcs := p.Services
	if p.Service != "" {
		svcs = []string{p.Service}
	}
	if len(svcs) > sfCatalogServices {
		svcs = svcs[:sfCatalogServices]
	}
	from := to.Add(-sfCatalogWindowS * time.Second)
	return attrKeyLookup{
		Catalog: func(ctx context.Context) ([]string, error) {
			if d.Store == nil {
				return nil, nil
			}
			var keys []string
			var firstErr error
			for _, svc := range svcs {
				ks, err := scopedAttrKeys(ctx, d, svc, from, to)
				if err != nil && firstErr == nil {
					firstErr = err
				}
				keys = append(keys, ks...)
			}
			if len(keys) > 0 {
				return keys, nil
			}
			return nil, firstErr
		},
		Probe: func(ctx context.Context, key, value string) (uint64, error) {
			if d.Store == nil {
				return 0, nil
			}
			for _, svc := range svcs {
				n, _, err := d.Store.AttrValueProbe(ctx, chstore.AttrScope{Service: svc}, key, value, from, to)
				if err == nil && n > 0 {
					return n, nil
				}
			}
			return 0, nil
		},
	}
}

// scopedAttrKeys — tek servisin anahtar adları (span + resource), memo'lu.
func scopedAttrKeys(ctx context.Context, d Deps, svc string, from, to time.Time) ([]string, error) {
	read := func(c context.Context) ([]string, error) {
		rctx, cancel := context.WithTimeout(c, cosreBudget)
		defer cancel()
		rows, err := d.Store.GetScopedAttrs(rctx, chstore.AttrScope{Service: svc}, from, to, 200, 1)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			k := r.Key
			if r.Scope == "resource" {
				k = "resource." + k
			}
			out = append(out, k)
		}
		return out, nil
	}
	if d.Memo == nil {
		return read(ctx)
	}
	body, err := d.Memo(ctx, fmt.Sprintf("mcp:attr_keys:v1:%s:%d", fnvHex(svc), sfCatalogWindowS), sfCatalogTTL,
		func(c context.Context) (any, bool, error) {
			ks, err := read(c)
			return ks, err == nil, err
		})
	if err != nil {
		return nil, err
	}
	var ks []string
	if err := json.Unmarshal(body, &ks); err != nil {
		return nil, err
	}
	return ks, nil
}

// ── konuşma durumu köprüsü (api/chat_state*.go; karar 12) ─────────────

// ParseClientIP — SAF: durum yuvası ve çözücü için (değer kanonik, önek mi, geçerli mi).
func ParseClientIP(raw string, allowPrefix bool) (string, bool, bool) {
	m, err := parseClientIP(raw, allowPrefix)
	if err != nil || m.Value == "" {
		return "", false, false
	}
	return m.Value, m.Prefix, true
}

// V2SearchFilterExprs — SAF: modelin süzgeç argümanı → durumun `filters`
// yuvası (aynı kapalı doğrulama; geçersizse nil).
func V2SearchFilterExprs(in []SearchFilter) []chstore.FilterExpr {
	out, err := planSearchFilters(in)
	if err != nil || len(out) == 0 {
		return nil
	}
	return out
}

// V2SearchFiltersFrom — SAF: durumun süzgeçleri → find_traces `filters`
// argümanı (yalnız kapalı işleçler; başka işleçli yaprak düşer).
func V2SearchFiltersFrom(fs []chstore.FilterExpr) []SearchFilter {
	var out []SearchFilter
	for _, f := range fs {
		var op string
		for name, o := range v2FilterOps {
			if o == f.Op {
				op = name
			}
		}
		if op == "" || len(out) == sfMaxFilters {
			continue
		}
		sf := SearchFilter{Key: f.Key, Op: op}
		switch {
		case op == "in":
			sf.Values = append([]string(nil), f.Values...)
		case op != "exists" && len(f.Values) > 0:
			sf.Value = f.Values[0]
		}
		out = append(out, sf)
	}
	return out
}
