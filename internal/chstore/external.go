package chstore

// v0.8.446 — External API monitoring read layer (SigNoz/Uptrace
// gap-closure Wave 3 / A1). Answers "which 3rd-party APIs do my
// services depend on, and how are those dependencies behaving?"
// — the Datadog API-catalog shape.
//
// MV-first by construction: every read here hits topology_edges_5m
// (node_kind='external'), which the 5-min topology aggregator already
// fills with calls / errors / sum_duration_ns / p99_ms / top_labels
// per (caller service, ext:<host>) edge. No new MV, no raw-spans
// scan; the p99 merge is max(p99_ms) across buckets — the same
// conservative roll-up the flow graph reads use.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ExternalHost is one row of the /external overview — a distinct
// third-party destination aggregated across every calling service
// in the window. Display/Category come from the static vendor
// catalogue (external_catalogue.go) and stay empty for unrecognised
// hosts — the frontend then shows the raw host without a badge.
type ExternalHost struct {
	Host        string   `json:"host"`
	Display     string   `json:"display,omitempty"`
	Category    string   `json:"category,omitempty"`
	Callers     uint64   `json:"callers"`
	CallerNames []string `json:"callerNames"`
	Calls       uint64   `json:"calls"`
	Errors      uint64   `json:"errors"`
	ErrorRate   float64  `json:"errorRate"`
	AvgMs       float64  `json:"avgMs"`
	P99Ms       float64  `json:"p99Ms"`
	TopLabels   []string `json:"topLabels"`
}

// ExternalCaller is the per-service breakdown inside one host's
// detail drawer — who depends on this API and how hard.
type ExternalCaller struct {
	Service   string   `json:"service"`
	Calls     uint64   `json:"calls"`
	Errors    uint64   `json:"errors"`
	ErrorRate float64  `json:"errorRate"`
	AvgMs     float64  `json:"avgMs"`
	P99Ms     float64  `json:"p99Ms"`
	TopLabels []string `json:"topLabels"`
}

// ExternalTrendPoint is one 5-minute bucket of the host's RED trend.
// Bucket is unix seconds (the MV's native grain).
type ExternalTrendPoint struct {
	Bucket int64   `json:"bucket"`
	Calls  uint64  `json:"calls"`
	Errors uint64  `json:"errors"`
	AvgMs  float64 `json:"avgMs"`
	P99Ms  float64 `json:"p99Ms"`
}

// ExternalHostDetail is the drawer payload for one host.
//
// v0.9.1255 — Paths: "en çok çağrılan yollar". Host-keyed düğümün
// İÇİNİ açan kırılım (external_paths.go). Üç alan birlikte anlamlı:
//   - Paths boş + PathsError boş = bu pencerede URL taşıyan istemci
//     span'i yok (dürüst boş, hata değil)
//   - PathsError dolu = okuma DENENDİ ve başarısız oldu; çekmece bunu
//     "yol yok" diye göstermez (v0.9.363'ün dersi: timeout ile gerçek
//     boşluk aynı tuvali üretmemeli)
//   - PathsWindowS = yol kırılımının GERÇEKTEN kapsadığı saniye. Ham
//     spans okuması externalPathsMaxWindow ile kırpılıyor; kırpma
//     gizlenirse operatör 7 günlük bir sayı sanır.
type ExternalHostDetail struct {
	Host         string               `json:"host"`
	Display      string               `json:"display,omitempty"`
	Category     string               `json:"category,omitempty"`
	Callers      []ExternalCaller     `json:"callers"`
	Trend        []ExternalTrendPoint `json:"trend"`
	Paths        []ExternalPathRow    `json:"paths"`
	PathsWindowS int64                `json:"pathsWindowS,omitempty"`
	PathsError   string               `json:"pathsError,omitempty"`
}

// GetExternalHosts returns every external destination seen in the
// window, busiest first. Capped at 500 rows — beyond that the page
// is a search problem, not a list problem.
func (s *Store) GetExternalHosts(ctx context.Context, from, to time.Time) ([]ExternalHost, error) {
	// Bucket-align the lower bound (same idiom as the other *_5m
	// readers) so a 13:03 winStart catches the 13:00 bucket.
	bucketStart := from.Truncate(5 * time.Minute)

	// Instrumented services are excluded: SDKs that set peer.service
	// (or a k8s DNS name / gRPC target) on *internal* calls produce
	// ext: edges whose "host" is really another monitored service.
	// Datadog semantics — if it emits spans, it is not a third party.
	// The exclusion runs IN SQL, before the LIMIT (v0.10.1266 — see
	// externalHostsSQL), so the page is never short-filled by rows
	// that would be dropped afterwards.
	rows, err := s.conn.Query(ctx, externalHostsSQL(),
		bucketStart, to, bucketStart, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ExternalHost{}
	for rows.Next() {
		var h ExternalHost
		var avgMs *float64
		if err := rows.Scan(&h.Host, &h.Callers, &h.CallerNames,
			&h.Calls, &h.Errors, &avgMs, &h.P99Ms, &h.TopLabels); err != nil {
			return nil, err
		}
		h.AvgMs = safeF(avgMs)
		if h.Calls > 0 {
			h.ErrorRate = float64(h.Errors) * 100 / float64(h.Calls)
		}
		if display, kind, ok := classifyExternal(h.Host); ok {
			h.Display, h.Category = display, kind
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetExternalHostDetail returns the drawer payload for one host:
// per-caller breakdown plus the 5-minute RED trend. Two bounded MV
// reads; either could legitimately be empty (host aged out of the
// window) — the caller renders the empty state, not an error.
func (s *Store) GetExternalHostDetail(ctx context.Context, host string, from, to time.Time) (*ExternalHostDetail, error) {
	bucketStart := from.Truncate(5 * time.Minute)
	d := &ExternalHostDetail{Host: host}
	if host == "" {
		d.Callers, d.Trend, d.Paths = []ExternalCaller{}, []ExternalTrendPoint{}, []ExternalPathRow{}
		return d, nil
	}
	if display, kind, ok := classifyExternal(host); ok {
		d.Display, d.Category = display, kind
	}

	// Per-caller breakdown, heaviest dependency first. The exclusion
	// mirrors GetExternalHosts' instrumented-service exclusion — without
	// it a hand-edited ?host=<service> deep link (or a k8s DNS spelling
	// of one, v0.10.1266) would render a monitored service as a third
	// party; with it the drawer's empty state renders, matching list
	// semantics. The host's candidates are computed in Go by the very
	// function the topology merge uses (ExtPeerServiceCandidates).
	cands := externalHostExclusionCandidates(host)
	rows, err := s.conn.Query(ctx, externalCallersSQL(),
		bucketStart, to, host, bucketStart, to, cands)
	if err != nil {
		return nil, err
	}
	d.Callers = []ExternalCaller{}
	for rows.Next() {
		var c ExternalCaller
		var avgMs *float64
		if err := rows.Scan(&c.Service, &c.Calls, &c.Errors,
			&avgMs, &c.P99Ms, &c.TopLabels); err != nil {
			rows.Close()
			return nil, err
		}
		c.AvgMs = safeF(avgMs)
		if c.Calls > 0 {
			c.ErrorRate = float64(c.Errors) * 100 / float64(c.Calls)
		}
		d.Callers = append(d.Callers, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 5-minute trend across all callers. 14d TTL × 5m grain caps
	// this at ~4k buckets; LIMIT guards a TTL change from silently
	// unbounding the read.
	trows, err := s.conn.Query(ctx, externalTrendSQL(),
		bucketStart, to, host, bucketStart, to, cands)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	d.Trend = []ExternalTrendPoint{}
	for trows.Next() {
		var p ExternalTrendPoint
		var bucket uint32
		var avgMs *float64
		if err := trows.Scan(&bucket, &p.Calls, &p.Errors, &avgMs, &p.P99Ms); err != nil {
			return nil, err
		}
		p.Bucket = int64(bucket)
		p.AvgMs = safeF(avgMs)
		d.Trend = append(d.Trend, p)
	}
	if err := trows.Err(); err != nil {
		return nil, err
	}

	// v0.9.1255 — yol kırılımı. Çağıran listesi ham okumanın birincil
	// anahtar ön eki (service_name IN ?) olarak GİRİYOR, o yüzden bu
	// üçüncü okuma ancak MV yarısı bir şey döndürdüyse anlamlı — ve
	// çağıran yoksa (host penceresinden düştü / elle kurulmuş bir
	// ?host=<servis> derin linki) hiç koşmuyor. Aynı hamle, bogus-host
	// kapısını yukarıdaki servis dışlamasından bedavaya devralıyor.
	//
	// Hata YUTULMUYOR ama ÖLDÜRMÜYOR da: yol okuması ham spans'e
	// gidiyor ve timeout'u mümkün; onun 500'ü çağıranları ve trendi de
	// götürseydi çekmece bir ek kırılım yüzünden tamamen kaybolurdu.
	// Hata satırı payload'a binip UI'da söyleniyor.
	svcNames := make([]string, 0, len(d.Callers))
	for _, c := range d.Callers {
		svcNames = append(svcNames, c.Service)
	}
	pFrom, pTo := clampExternalPathsWindow(from, to)
	d.PathsWindowS = int64(pTo.Sub(pFrom) / time.Second)
	paths, perr := s.GetExternalHostPaths(ctx, host, svcNames, from, to, externalPathsDefaultLimit)
	if perr != nil {
		d.Paths = []ExternalPathRow{}
		d.PathsError = perr.Error()
	} else {
		d.Paths = paths
	}
	return d, nil
}

// ─── v0.10.1266 — enstrümante servis dışlaması, k8s DNS dahil ────────
//
// Operatör raporu (2026-10-17): /external,
// `ext:demo-sales-application-query-prod.sales.svc.cluster.local`'ı
// üçüncü parti diye listeliyordu; `demo-sales-application-query-prod`
// ise enstrümante bir servis. v0.10.1260 topoloji grafiğini
// ExtPeerServiceCandidates/ResolveExtPeer ile düzeltti, ama bu sayfanın
// dışlaması `substring(child_node, 5) GLOBAL NOT IN (service_summary_5m)`
// BİREBİR eşleşmeydi.
//
// Şimdi dışlama ResolveExtPeer'in anlamını taşıyor:
//   - adaylar: ham ad → host (şema/dns:, yol, tek-iki-noktalı sayısal
//     port, sondaki nokta soyulmuş) → yalnız k8s soneklerinde
//     (.svc.cluster.local / .cluster.local / .svc, önünde ≥1 etiket) ilk
//     DNS etiketi. `@`'li geçit biçimi yalnız ham ad.
//   - bir aday ya bir service.name'e BİREBİR, ya da harf duyarsız olarak
//     TEK bir service.name'e eşitse (iki farklı yazımı aynı küçük harfe
//     düşen adlar belirsiz → harf duyarsız yol kapalı) host bir servistir.
//
// SQL'de, LIMIT'ten ÖNCE: Go'da sonradan süzmek sayfayı eksik doldururdu.
// Servis adı kümesi TEK bir sınırlı skaler alt-sorgu (WITH … AS
// svc_names): tuple(birebir adlar, belirsiz-olmayan küçük harfli adlar).
// Skaler alt-sorgu dağıtıkta başlatıcıda bir kez koşar ve sabit olarak
// shard'lara gider — GLOBAL IN'in yaptığı işin aynısı (CHECK 5'in
// koruduğu shard-yerel tuzak burada yok). Pencere dış sorguyla AYNI
// (TestExternalCatalogueBoundsAgreeWithinQuery her okumada çift yüklem
// arar: alt-sorgu + kenarlar).
//
// Kilit adım: host normalizasyonu RE2 adım listesi (extPeerHostSteps)
// olarak TEK yerde tanımlı; SQL ifadesi bu listeden kurulur, test aynı
// listeyi Go'da koşturup ExtPeerServiceCandidates ile aynı vaka
// tablosunda karşılaştırır (external_ext_peer_sql_test.go) ve
// `clickhouse` ikilisi varsa ifadeyi canlı motorda da koşturur.

// extPeerHostStep — host normalizasyonunun bir adımı. Regex adımları RE2
// (Go regexp ve CH replaceRegexp* aynı motor); Replace CH sözdiziminde
// (`\1`), Go aynası `${1}`'e çevirir. Scheme=true adımı regex değil:
// "ilk `://`'ye kadar" RE2'de ancak tembel niceleyici (`.*?`) ile
// yazılır, ve SQL metninde HİÇBİR `?` olmamalı — clickhouse-go'nun
// konumsal bind'i dizgi değişmezinin İÇİNDEKİ `?`'yi de yer tutucu
// sayıyor (bind.go bindPositional). Aynı sebeple desenlerde `(?i)`,
// `(?:…)`, `(?s)` ve `?` niceleyicisi yok: harf duyarsızlık lowerUTF8
// ile (Go tarafı da strings.ToLower kullanıyor), `.` yerine `[\s\S]`.
// Süslü parantez de yok ('{…:…}' sunucu tarafı parametre moduna geçirir,
// gatewayPathRegex notu): opsiyonel grup `(…)*` ile yazılır — "'.' ile
// biten parça" dilinde `*` ile `{0,1}` aynı kümeyi tanır.
type extPeerHostStep struct {
	Pattern string
	Replace string
	All     bool // replaceRegexpAll; aksi hâlde replaceRegexpOne
	Scheme  bool // regex değil: ilk "://"'ye kadar, yoksa harf duyarsız "dns:"
	Why     string
}

// extPeerHostSteps — ExtPeerServiceCandidates'in host türetmesinin
// adım adım ikizi. SIRA ÖNEMLİ (Go fonksiyonundaki sırayla aynı).
var extPeerHostSteps = []extPeerHostStep{
	{Pattern: `^[\t\n\v\f\r ]+|[\t\n\v\f\r ]+$`, All: true, Why: "strings.TrimSpace (ASCII boşluk)"},
	{Scheme: true, Why: "ilk `://`'ye kadar şema, yoksa `dns:` öneki (harf duyarsız)"},
	{Pattern: `^/+`, Why: "dns:/// artığı: baştaki '/'ler"},
	{Pattern: `/[\s\S]*$`, Why: "ilk '/'den sonrası (yol)"},
	{Pattern: `^([^:]+):[0-9]+$`, Replace: `\1`, Why: "tek ':' + sayısal port (IPv6 dokunulmaz)"},
	{Pattern: `\.$`, Why: "sondaki tek nokta"},
}

// extPeerK8sHostPattern — KÜÇÜK HARFE indirilmiş host bir k8s küme-içi
// adı mı (önünde ≥1 etiket + sonek). Aday o zaman ilk DNS etiketidir
// (ilk '.'den önce, özgün harfle). extPeerK8sBareSuffix: Go'nun sonek
// önceliği (.svc.cluster.local önce) `svc.cluster.local`'ın kendisini
// etiketsiz bırakır; tek-desen biçimi onu ".cluster.local önünde 'svc'"
// diye okurdu — bu yüzden açıkça reddedilir.
const (
	extPeerK8sHostPattern = `^[^.]+\.([\s\S]*\.)*(svc\.cluster\.local|cluster\.local|svc)$`
	extPeerK8sBareSuffix  = `svc.cluster.local`
)

// extPeerSQLLit — CH dizgi değişmezi. Desenlerdeki ters bölüler CH
// değişmezinde kaçış sayılır (`'\.'` → `.`), o yüzden ikilenir.
func extPeerSQLLit(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// extPeerHostSQL — extPeerHostSteps'in CH ifadesi. Her adım kendi
// lambda'sında (`arrayMap(s -> …, [önceki])[1]`): girdiyi birden çok kez
// anan adımlar metni üstel büyütmesin; lambda'lar iç içe değil, yalnız
// kendi parametresini görür.
func extPeerHostSQL(raw string) string {
	cur := raw
	for _, st := range extPeerHostSteps {
		switch {
		case st.Scheme:
			cur = "arrayMap(s -> if(position(s, '://') > 0, substring(s, position(s, '://') + 3), " +
				"if(startsWith(lowerUTF8(s), 'dns:'), substring(s, 5), s)), [" + cur + "])[1]"
		case st.All:
			cur = fmt.Sprintf("replaceRegexpAll(%s, %s, %s)", cur, extPeerSQLLit(st.Pattern), extPeerSQLLit(st.Replace))
		default:
			cur = fmt.Sprintf("replaceRegexpOne(%s, %s, %s)", cur, extPeerSQLLit(st.Pattern), extPeerSQLLit(st.Replace))
		}
	}
	return cur
}

// extPeerCandidatesSQL — ExtPeerServiceCandidates'in CH ikizi:
// Array(String), boş adaylar elenmiş (sıra/tekrar dışlama için önemsiz).
// `raw` öneksiz ad ifadesi (ör. `substring(child_node, 5)`).
func extPeerCandidatesSQL(raw string) string {
	return fmt.Sprintf("arrayFilter(c -> c != '', if(position(%[1]s, '@') > 0, [%[1]s], "+
		"arrayMap(h -> [%[1]s, h, if(match(lowerUTF8(h), %[3]s) AND lowerUTF8(h) != %[4]s, splitByChar('.', h)[1], '')], [%[2]s])[1]))",
		raw, extPeerHostSQL(raw), extPeerSQLLit(extPeerK8sHostPattern), extPeerSQLLit(extPeerK8sBareSuffix))
}

// ─── v0.10.1344 — etiket-önek aşamasının SQL ikizi ───────────────────
//
// ResolveExtPeer'in 2. aşaması (extPeerLabelStage, identity.go) liste
// okumasında da LIMIT'ten önce koşmalı; aksi hâlde
// `<svc>-prod-grpc.<ns>.svc…` / `<svc>-prod-kon.<kurum>` host'ları
// topolojide servise birleşip /external'da üçüncü parti kalırdı. Aynı
// kurallar, aynı sırayla:
//   - geçit biçimi (`@`) yok; host = extPeerHostSQL (1. aşamayla aynı);
//   - etiket: host ':' / '[' / ']' içermez, ilk '.' 1. konumdan sonra ve
//     sonda değil, ilk etiket tamamen rakam değil (IPv4);
//   - kapı: k8s host'u (1. aşamanın deseni) YA DA ilk etiket '-' içerir ve
//     ham ad dış katalogda değildir (multiSearchAny, externalCatalogue'tan);
//   - önekler: etiket + en çok iki `-<belirteç>` atımı; belirteç
//     `[A-Za-z0-9]+` ve ≤16 bayt, kalan önek boş değil, '-' ile bitmez,
//     '-' içerir (R3). '{n,m}' niceleyicisi yerine length() (bind kuralı).
// Her adım kendi lambda'sında (girdi metni üstel büyümesin). Canlı motor
// eşliği: TestExtPeerIsServiceSQLMirrorMatchesResolve.

// extPeerLabelStripSQL — tek `-<belirteç>` atımı; geçersizse boş dizgi.
func extPeerLabelStripSQL(x string) string {
	const tail = `'-[A-Za-z0-9]+$'`
	return fmt.Sprintf("if(match(%[1]s, '[^-]-[A-Za-z0-9]+$') AND length(extract(%[1]s, '-([A-Za-z0-9]+)$')) <= %[2]d "+
		"AND position(replaceRegexpOne(%[1]s, %[3]s, ''), '-') > 0, replaceRegexpOne(%[1]s, %[3]s, ''), '')",
		x, extPeerLabelMaxToken, tail)
}

// extPeerLabelPrefixesSQL — ExtPeerLabelPrefixes'in ikizi (2 atım).
func extPeerLabelPrefixesSQL(label string) string {
	return "arrayMap(l -> arrayMap(p -> arrayFilter(x -> x != '', [l, p, " + extPeerLabelStripSQL("p") + "]), [" +
		extPeerLabelStripSQL("l") + "])[1], [" + label + "])[1]"
}

// extPeerCatalogueSQL — classifyExternal'ın boolean ikizi.
func extPeerCatalogueSQL(raw string) string {
	subs := make([]string, 0, len(externalCatalogue))
	for _, e := range externalCatalogue {
		subs = append(subs, extPeerSQLLit(e.substring))
	}
	return "multiSearchAny(lowerUTF8(" + raw + "), [" + strings.Join(subs, ", ") + "])"
}

// extPeerLabelStageSQL — extPeerLabelStage'in CH ikizi: Array(String).
func extPeerLabelStageSQL(raw string) string {
	const label = "splitByChar('.', h)[1]"
	cond := "position(" + raw + ", '@') = 0 AND position(h, ':') = 0 AND position(h, '[') = 0 AND position(h, ']') = 0 " +
		"AND position(h, '.') > 1 AND position(h, '.') < length(h) AND NOT match(" + label + ", '^[0-9]+$') " +
		"AND ((match(lowerUTF8(h), " + extPeerSQLLit(extPeerK8sHostPattern) + ") AND lowerUTF8(h) != " + extPeerSQLLit(extPeerK8sBareSuffix) + ") " +
		"OR (position(" + label + ", '-') > 0 AND NOT " + extPeerCatalogueSQL(raw) + "))"
	return "arrayMap(h -> if(" + cond + ", " + extPeerLabelPrefixesSQL(label) + ", emptyArrayString()), [" + extPeerHostSQL(raw) + "])[1]"
}

// extPeerResolveCandidatesSQL — ExtPeerResolveCandidates'in ikizi (sıra
// ve tekrar dışlama için önemsiz): 1. aşama + 2. aşama.
func extPeerResolveCandidatesSQL(raw string) string {
	return "arrayConcat(" + extPeerCandidatesSQL(raw) + ", " + extPeerLabelStageSQL(raw) + ")"
}

// extPeerIsServiceSQL — adaylardan biri bilinen bir servis mi
// (ResolveExtPeer anlamı). svc_names WITH skalerine bağlı.
func extPeerIsServiceSQL(cands string) string {
	return "arrayExists(x -> has(tupleElement(svc_names, 1), x) OR " +
		"has(tupleElement(svc_names, 2), lowerUTF8(x)), " + cands + ")"
}

// externalHostExclusionCandidates — çekmece okumaları için adaylar,
// Go'da: topoloji birleştirmesinin kullandığı fonksiyonun KENDİSİ.
// "ext:" öneki eklenir ki liste tarafındaki substring(child_node, 5)
// ile birebir aynı ham adı görsün.
func externalHostExclusionCandidates(host string) []string {
	out := []string{}
	for _, c := range ExtPeerResolveCandidates("ext:" + host) { // v0.10.1344: + etiket önekleri
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// GetExternalHosts / GetExternalHostDetail sorgu metinleri — SAF
// kurucular (canlı motor testi gerçekten koşan metni koşturur). Her
// metin İKİ kova yüklemi taşır: svc_names alt-sorgusu + kenarlar, aynı
// argümanlarla (bucketStart, to).

func externalHostsSQL() string {
	return `
		WITH (
			SELECT tuple(groupArrayArray(ns), groupArrayIf(l, length(ns) = 1))
			FROM (
				SELECT lowerUTF8(toString(service_name)) AS l,
				       groupUniqArray(toString(service_name)) AS ns
				FROM service_summary_5m
				WHERE time_bucket >= ? AND time_bucket < ?
				GROUP BY l
			)
		) AS svc_names
		SELECT substring(child_node, 5)          AS host,
		       uniqExact(parent_service)         AS callers,
		       arraySort(groupUniqArray(8)(parent_service)) AS caller_names,
		       sum(calls)                        AS total_calls,
		       sum(errors)                       AS total_errors,
		       sum(sum_duration_ns) / nullIf(sum(calls), 0) / 1e6 AS avg_ms,
		       max(p99_ms)                       AS p99_ms,
		       arraySlice(arrayDistinct(arrayFlatten(groupArray(top_labels))), 1, 8) AS labels
		FROM topology_edges_5m FINAL
		WHERE node_kind = 'external'
		  AND time_bucket >= ? AND time_bucket < ?
		GROUP BY child_node
		HAVING NOT ` + extPeerIsServiceSQL(extPeerResolveCandidatesSQL("substring(child_node, 5)")) + `
		ORDER BY total_calls DESC
		LIMIT 500
		SETTINGS max_execution_time = 10`
}

// externalCallersSQL — argümanlar: bucketStart, to, host, bucketStart,
// to, adaylar ([]string, externalHostExclusionCandidates).
func externalCallersSQL() string {
	return `
		WITH (
			SELECT tuple(groupArrayArray(ns), groupArrayIf(l, length(ns) = 1))
			FROM (
				SELECT lowerUTF8(toString(service_name)) AS l,
				       groupUniqArray(toString(service_name)) AS ns
				FROM service_summary_5m
				WHERE time_bucket >= ? AND time_bucket < ?
				GROUP BY l
			)
		) AS svc_names
		SELECT parent_service,
		       sum(calls)  AS total_calls,
		       sum(errors) AS total_errors,
		       sum(sum_duration_ns) / nullIf(sum(calls), 0) / 1e6 AS avg_ms,
		       max(p99_ms) AS p99_ms,
		       arraySlice(arrayDistinct(arrayFlatten(groupArray(top_labels))), 1, 5) AS labels
		FROM topology_edges_5m FINAL
		WHERE node_kind = 'external'
		  AND child_node = concat('ext:', ?)
		  AND time_bucket >= ? AND time_bucket < ?
		  AND NOT ` + extPeerIsServiceSQL("?") + `
		GROUP BY parent_service
		ORDER BY total_calls DESC
		LIMIT 100
		SETTINGS max_execution_time = 10`
}

// externalTrendSQL — argümanlar externalCallersSQL ile aynı.
func externalTrendSQL() string {
	return `
		WITH (
			SELECT tuple(groupArrayArray(ns), groupArrayIf(l, length(ns) = 1))
			FROM (
				SELECT lowerUTF8(toString(service_name)) AS l,
				       groupUniqArray(toString(service_name)) AS ns
				FROM service_summary_5m
				WHERE time_bucket >= ? AND time_bucket < ?
				GROUP BY l
			)
		) AS svc_names
		SELECT toUnixTimestamp(time_bucket) AS bucket,
		       sum(calls)  AS total_calls,
		       sum(errors) AS total_errors,
		       sum(sum_duration_ns) / nullIf(sum(calls), 0) / 1e6 AS avg_ms,
		       max(p99_ms) AS p99_ms
		FROM topology_edges_5m FINAL
		WHERE node_kind = 'external'
		  AND child_node = concat('ext:', ?)
		  AND time_bucket >= ? AND time_bucket < ?
		  AND NOT ` + extPeerIsServiceSQL("?") + `
		GROUP BY time_bucket
		ORDER BY time_bucket ASC
		LIMIT 5000
		SETTINGS max_execution_time = 10`
}
