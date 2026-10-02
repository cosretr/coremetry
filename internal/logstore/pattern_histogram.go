package logstore

// pattern_histogram.go — v0.10.1060 (operatör, prod: "Bunu doğru yakalamış
// ama artışın ne zaman başladığını göstermiyor. Elastic'e gidip bakınca
// barlardan net görüyorum.").
//
// Log deseni anomalisinin detay sayfası yalnız sayılar gösteriyordu; artışın
// BAŞLANGICI ancak Discover'ın kova histogramında görünüyordu. PatternHistogram
// CountPatterns'ın zaman eksenidir: aynı desen, aynı eşleşme yüklemi, kova
// başına sayım. Yeni bir eşleşme dili DEĞİL — grafiğin dedektörden başka bir
// şey sayması "781 katı" cümlesinin altına uyumsuz barlar koyardı.
//
//   CH: chPatternMatchSQL (token ön süzgeci tokenbf_v1'i kullanır + regex),
//       countOnePattern ile ORTAK yüklem; zaman-sınırlı WHERE + LIMIT +
//       max_execution_time.
//   ES: buildPatternTokenQuery (token-OR query_string; regex yok sayılır —
//       CountPatterns ES yolu da böyle) + date_histogram; size:0,
//       track_total_hits:false, yumuşak timeout, request_cache.
//
// Kova sayısını ÇAĞIRAN sınırlar (API katmanı ≤120 kova); burada yalnız
// savunma tavanı (patternHistogramMaxRows).
//
// v0.10.1062 (operatör, prod ES: servissiz log deseni anomalisinde "Ne
// yapabilirim" yalnız "servis adı yok" diyordu, operatör Kibana'ya elle
// gidiyordu) — iki ek, ikisi de BU okumanın üstünde, yeni istek yok:
//   • PatternSearchText: desenin /logs arama metni (token'ların tırnaklı
//     OR'u) — sayfanın "Logları aç" bağlantısı dedektörün token listesinden.
//   • ES servis atfı: aynı _search'e sınırlı terms zinciri (patternServiceAggs).
//     Dedektörün servis atfı tek alana (fields.Service) bakıyor; OpenShift
//     cluster-logging dokümanında iş yükü kimliği kubernetes.container_name'de
//     ve service.name çoğu kayıtta YOK → terms agg boş, olay servissiz yazılır.
//     Zincir servis SÜZGECİNİN aday alanlarını (buildQuery svcFields) sırayla
//     dener: gösterilen ad /logs'ta süzülebilen addır (v0.8.265 sınıfı).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"

	"github.com/cilcenk/coremetry/internal/logql"
)

// PatternSearchText — v0.10.1062, SAF: desenin /logs arama kutusu metni.
// Her token tırnaklı ifade (LİTERAL, joker yok), birden çoksa " OR " ile:
//
//	["service quota", "quota exceeded"] → "service quota" OR "quota exceeded"
//
// İki arka uçta da aynı satırları seçer:
//   - ES: Search bunu query_string'e default_field = gövde ile verir; dedektörün
//     buildPatternTokenQuery'si (`<gövde>:"t1" OR <gövde>:"t2"`) ile AYNI
//     yüklem — ES dedektörü regex'i zaten yok sayar, yani birebir.
//   - CH: logql her ifadeyi multiSearchAnyCaseInsensitive(body, [?]) yapar —
//     dedektörün token ön süzgeci. Regex (match) /logs arama dilinde YOK: CH'de
//     liste token'ların eşleştiği her satırı gösterir, dedektör bunların regex'e
//     de uyanını sayar (üst küme; token'lar regex'in sıfır-yanlış-negatif
//     alt dizeleri olduğundan desenin her satırı listededir).
//
// Metin logql.Expr.String() ile kurulur: kaçış ayrıştırıcının kendisinden.
// Token'sız desen → "" (sadık bir arama ifade edilemez; çağıran bağlantı
// basmaz).
func PatternSearchText(pat PatternSpec) string {
	kids := make([]*logql.Expr, 0, len(pat.Tokens))
	for _, t := range pat.Tokens {
		if strings.TrimSpace(t) == "" {
			continue
		}
		kids = append(kids, &logql.Expr{Kind: logql.KindClause, Op: logql.OpMatch, Value: t, Phrase: true})
	}
	switch len(kids) {
	case 0:
		return ""
	case 1:
		return kids[0].String()
	}
	return (&logql.Expr{Kind: logql.KindOr, Kids: kids}).String()
}

// patternTopServices — servis atfı tavanı (dedektörün v0.5.287 top-5'i).
const patternTopServices = 5

// patternHistogramMaxRows — CH LIMIT'i ve ES kova okumasının savunma tavanı.
// API katmanı ≤120 kova ister; bu yalnız elle kurulmuş bir çağrının
// sınırsız satır taşımasını engeller.
const patternHistogramMaxRows = 500

// chPatternMatchSQL — desen eşleşme yüklemi (SAF). tokensSQL boş değilse
// multiSearchAnyCaseInsensitive ön süzgeci (tokenbf_v1 granül budaması) +
// regex; boşsa yalnız regex. Tek `?` bağı = pat.Regex. countOnePattern'ın
// iki sorgusu ve PatternHistogram BU işlevden okur — üçü ayrışamaz.
func chPatternMatchSQL(tokensSQL string) string {
	if tokensSQL != "" {
		return "multiSearchAnyCaseInsensitive(body, " + tokensSQL + ") AND match(body, ?)"
	}
	return "match(body, ?)"
}

// chPatternHistogramSQL — CH desen histogramı sorgusu (SAF, golden testli).
// Kova epoch'a hizalı (intDiv): toStartOfInterval saat/gün birimlerinde
// sunucu saat dilimine göre hizalanır; ES fixed_interval ise epoch'a. İki
// arka uç aynı kova başlarını üretsin.
func chPatternHistogramSQL(tokensSQL string, bucketSec int) string {
	return fmt.Sprintf(`
		SELECT toUInt64(intDiv(toUnixTimestamp(time), %d) * %d) AS bucket,
		       count() AS c
		FROM logs
		WHERE time >= ? AND time < ? AND %s
		GROUP BY bucket
		ORDER BY bucket
		LIMIT %d
		SETTINGS max_execution_time = 10`,
		bucketSec, bucketSec, chPatternMatchSQL(tokensSQL), patternHistogramMaxRows)
}

// CH servis atfı TAŞIMAZ (TopServices boş, v0.10.1062 bilinçli): dedektör
// servisi service_name KOLONUNDAN alır (anyHeavyIf) ve CH olayı pratikte hep
// servisli yazılır; servissiz CH olayı satırların service_name'i boş demektir —
// GROUP BY service_name yine boş dize döndürür. Üstelik ikinci bir regex taramasıdır
// (≤7 gün), bu okumanın maliyetini ikiye katlardı.
func (s *CHStore) PatternHistogram(ctx context.Context, pat PatternSpec, from, to time.Time, bucketSec int) (*PatternHistogramResult, error) {
	if bucketSec <= 0 {
		return nil, fmt.Errorf("pattern histogram: bucketSec must be > 0")
	}
	if !to.After(from) {
		return &PatternHistogramResult{Points: []LogPoint{}}, nil
	}
	sql := chPatternHistogramSQL(chBuildTokenLiteral(pat.Tokens), bucketSec)
	rows, err := s.store.Conn().Query(ctx, sql, from, to, pat.Regex)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &PatternHistogramResult{Points: []LogPoint{}}
	for rows.Next() {
		var sec, c uint64
		if err := rows.Scan(&sec, &c); err != nil {
			return nil, err
		}
		out.Points = append(out.Points, LogPoint{T: int64(sec) * int64(time.Second), V: int64(c)})
	}
	return out, rows.Err()
}

// patternHistogramBody — ES desen histogramı gövdesi (SAF, testli). Yüklem
// patternCountBody'ninkiyle aynı query_string; pencere SORGUDA (v0.10.412
// dersi: yalnız agg'de olsaydı token taraması tüm indeks saklamasını
// gezerdi). Kova agg'i Histogram'ın histogramDateAgg'i (min_doc_count:1 —
// v0.8.3 yoğun ızgara korunması; sıfır doldurma çağıranda).
// v0.10.1062 — svcFields boş değilse aynı gövdeye servis atfı zinciri
// (patternServiceAggs) eklenir; boşsa gövde 1060'takiyle bayt bayt aynı.
func patternHistogramBody(tokenQuery, bodyField, tsField, from, to string, bucketSec int, timeout string, svcFields []string) map[string]any {
	aggs := map[string]any{"buckets": histogramDateAgg(tsField, bucketSec)}
	for k, v := range patternServiceAggs(svcFields) {
		aggs[k] = v
	}
	return map[string]any{
		"size":             0,
		"track_total_hits": false,
		"timeout":          timeout,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"range": map[string]any{tsField: map[string]any{"gte": from, "lt": to}}},
					map[string]any{
						"query_string": map[string]any{
							"query":                  tokenQuery,
							"default_field":          bodyField,
							"default_operator":       "OR",
							"allow_leading_wildcard": false,
							"lenient":                true,
						},
					},
				},
			},
		},
		"aggs": aggs,
	}
}

// patternServiceAggs — v0.10.1062, SAF: servis atfı zinciri. Alanlar ÖNCELİK
// sırasıyla (yapılandırılmış servis alanı, sonra süzgecin yedekleri); her
// doküman ilk TAŞIDIĞI alanda sayılır (mapHit'in readPathAny sırası, agg
// düzeyinde):
//
//	svc       terms(alan₀, 5)
//	svc_rest  filter(alan₀ yok) → { svc terms(alan₁, 5), svc_rest filter(alan₁ yok) → … }
//
// Böylece service.name'i olmayan cluster-logging dokümanı container_name ile
// sayılır, ikisini de taşıyan doküman iki kez sayılmaz. Maliyet: eşleşen
// doküman kümesi üzerinde ≤ len(alan) küçük keyword terms agg'i — sorgu ve
// istek aynı. Boş liste → nil (gövdeye hiçbir şey eklenmez).
func patternServiceAggs(fields []string) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	lvl := map[string]any{
		"svc": map[string]any{"terms": map[string]any{"field": fields[0], "size": patternTopServices}},
	}
	if rest := patternServiceAggs(fields[1:]); rest != nil {
		lvl["svc_rest"] = map[string]any{
			"filter": map[string]any{"bool": map[string]any{
				"must_not": []any{map[string]any{"exists": map[string]any{"field": fields[0]}}},
			}},
			"aggs": rest,
		}
	}
	return lvl
}

// esServiceChain — patternServiceAggs cevabının özyinelemeli şekli. Key `any`:
// beklenmedik sayısal bir mapping tüm cevabın çözümünü düşürmesin.
type esServiceChain struct {
	Svc struct {
		Buckets []struct {
			Key      any     `json:"key"`
			DocCount float64 `json:"doc_count"`
		} `json:"buckets"`
	} `json:"svc"`
	Rest *esServiceChain `json:"svc_rest"`
}

// topServicesFromChain — SAF: zincirin kovaları → en çok ≤5 servis (sayı
// azalan, eşitlikte ad artan). Aynı ad iki seviyede çıkarsa toplanır (farklı
// dokümanlar — zincir dokümanı tek seviyede sayar). Boş ad atılır.
func topServicesFromChain(c *esServiceChain) []PatternServiceHit {
	by := map[string]uint64{}
	for lvl := c; lvl != nil; lvl = lvl.Rest {
		for _, b := range lvl.Svc.Buckets {
			name := strings.TrimSpace(fmt.Sprint(b.Key))
			if b.Key == nil || name == "" || b.DocCount <= 0 {
				continue
			}
			by[name] += uint64(b.DocCount)
		}
	}
	out := make([]PatternServiceHit, 0, len(by))
	for n, cnt := range by {
		out = append(out, PatternServiceHit{Service: n, Count: cnt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Service < out[j].Service
	})
	if len(out) > patternTopServices {
		out = out[:patternTopServices]
	}
	return out
}

// esPatternHistogramResponse — cevabın okunan alt kümesi.
type esPatternHistogramResponse struct {
	esSearchEnvelope
	Aggregations struct {
		Buckets struct {
			Buckets []struct {
				Key      float64 `json:"key"` // epoch ms
				DocCount float64 `json:"doc_count"`
			} `json:"buckets"`
		} `json:"buckets"`
		esServiceChain // v0.10.1062 — svc / svc_rest kökte
	} `json:"aggregations"`
}

// decodePatternHistogram — ES cevabı → seyrek noktalar (SAF, testli).
func decodePatternHistogram(raw esPatternHistogramResponse) *PatternHistogramResult {
	out := &PatternHistogramResult{Points: []LogPoint{}, Partial: raw.partial()}
	for i, b := range raw.Aggregations.Buckets.Buckets {
		if i >= patternHistogramMaxRows {
			break
		}
		out.Points = append(out.Points, LogPoint{T: int64(b.Key) * int64(time.Millisecond), V: int64(b.DocCount)})
	}
	if top := topServicesFromChain(&raw.Aggregations.esServiceChain); len(top) > 0 {
		out.TopServices = top
	}
	return out
}

// patternServiceAggFields — ES servis atfı alanları: yapılandırılmış servis
// alanı + süzgecin yedekleri (esServiceFallbackFields — buildQuery svcFields
// ile pinli), her biri terms agg YAZIMINA çözümlü (termsAggField: cache'li
// field_caps / probe; text alanı asla — 400 değil, unmapped boş). Çözümlü
// yazımda yinelenen alan bir kez.
func (s *ESStore) patternServiceAggFields(ctx context.Context) []string {
	cands := append([]string{s.fields.Service}, esServiceFallbackFields...)
	out := make([]string, 0, len(cands))
	seen := map[string]bool{}
	for _, bare := range cands {
		f := s.termsAggField(ctx, bare)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func (s *ESStore) PatternHistogram(ctx context.Context, pat PatternSpec, from, to time.Time, bucketSec int) (*PatternHistogramResult, error) {
	if bucketSec <= 0 {
		return nil, fmt.Errorf("pattern histogram: bucketSec must be > 0")
	}
	// CountPatterns ES yolu tokensız deseni hiç sorgulamaz (regex ES'te
	// patolojik); grafik de aynı: boş seri, sorgu yok.
	if len(pat.Tokens) == 0 || !to.After(from) {
		return &PatternHistogramResult{Points: []LogPoint{}}, nil
	}
	svcFields := s.patternServiceAggFields(ctx)
	out, status, err := s.patternHistogramOnce(ctx, pat, from, to, bucketSec, svcFields)
	// v0.10.1062 — servis atfı İKİNCİL: zincir ES'te reddedilirse (4xx:
	// beklenmedik mapping) grafik onunla birlikte düşmesin. Tek yeniden
	// deneme, atıfsız (1060 gövdesi); zaman aşımı / 5xx yeniden denenmez.
	if err != nil && len(svcFields) > 0 && status >= 400 && status < 500 {
		log.Printf("[logstore-es] pattern histogram: service aggs rejected (%d), retrying without: %v", status, err)
		out, _, err = s.patternHistogramOnce(ctx, pat, from, to, bucketSec, nil)
	}
	return out, err
}

// patternHistogramOnce — tek _search; dönen int HTTP durumu (taşıma hatasında 0).
func (s *ESStore) patternHistogramOnce(ctx context.Context, pat PatternSpec, from, to time.Time, bucketSec int, svcFields []string) (*PatternHistogramResult, int, error) {
	bodyMap := patternHistogramBody(
		buildPatternTokenQuery(pat.Tokens, s.fields.Body), s.fields.Body, s.fields.Timestamp,
		from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano),
		bucketSec, esTimeseriesTimeoutFromEnv("10s"), svcFields)
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, 0, err
	}
	tru := true
	idx := s.queryIndices(ctx, Filter{From: from, To: to})
	req := esapi.SearchRequest{
		Index:             idx,
		Body:              bytes.NewReader(body),
		AllowNoIndices:    &tru,
		IgnoreUnavailable: &tru,
		// Mutlak pencere: aynı gövde ES'in kendi istek önbelleğinden döner.
		RequestCache: &tru,
	}
	res, err := req.Do(ctx, s.cli)
	if err != nil {
		return nil, 0, s.recordQueryError("pattern-histogram", idx, body, 0, fmt.Errorf("ES pattern histogram: %w", err))
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, res.StatusCode, s.recordQueryError("pattern-histogram", idx, body, res.StatusCode,
			parseESError("pattern-histogram", res, s.cfg.Index))
	}
	var raw esPatternHistogramResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, res.StatusCode, fmt.Errorf("decode ES pattern histogram: %w", err)
	}
	if d := raw.describe(); d != "" {
		log.Printf("[logstore-es] PARTIAL pattern histogram (%s) — buckets are a subset", d)
	}
	return decodePatternHistogram(raw), res.StatusCode, nil
}
