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

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

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
func patternHistogramBody(tokenQuery, bodyField, tsField, from, to string, bucketSec int, timeout string) map[string]any {
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
		"aggs": map[string]any{"buckets": histogramDateAgg(tsField, bucketSec)},
	}
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
	bodyMap := patternHistogramBody(
		buildPatternTokenQuery(pat.Tokens, s.fields.Body), s.fields.Body, s.fields.Timestamp,
		from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano),
		bucketSec, esTimeseriesTimeoutFromEnv("10s"))
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
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
		return nil, s.recordQueryError("pattern-histogram", idx, body, 0, fmt.Errorf("ES pattern histogram: %w", err))
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, s.recordQueryError("pattern-histogram", idx, body, res.StatusCode,
			parseESError("pattern-histogram", res, s.cfg.Index))
	}
	var raw esPatternHistogramResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode ES pattern histogram: %w", err)
	}
	if d := raw.describe(); d != "" {
		log.Printf("[logstore-es] PARTIAL pattern histogram (%s) — buckets are a subset", d)
	}
	return decodePatternHistogram(raw), nil
}
