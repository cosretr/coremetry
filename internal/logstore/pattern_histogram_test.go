package logstore

// pattern_histogram_test.go — v0.10.1060 (log deseni anomalisi: zaman içinde
// sayım grafiği). Çivilenen: (1) CH grafiği dedektörle AYNI yüklemle sayar,
// (2) CH sorgusu zaman-sınırlı + LIMIT + max_execution_time taşır, (3) ES
// gövdesi v0.8.3/v0.8.164 maliyet korumalarını ve pencereyi SORGUDA taşır,
// (4) ES cevabı ms → ns doğru çevrilir, kısmi cevap işaretlenir.

import (
	"os"
	"strings"
	"testing"
)

func TestChPatternMatchSQL(t *testing.T) {
	cases := []struct {
		name, tokens, want string
	}{
		{"token ön süzgeçli", "['ora-']", "multiSearchAnyCaseInsensitive(body, ['ora-']) AND match(body, ?)"},
		{"tokensız yalnız regex", "", "match(body, ?)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := chPatternMatchSQL(c.tokens); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Dedektör (countOnePattern) ve grafik tek yüklemden okur — biri değişip
// öteki kalırsa "781 katı" cümlesinin altındaki barlar başka bir şeyi sayardı.
func TestCountOnePattern_UsesSharedPredicate(t *testing.T) {
	src, err := os.ReadFile("clickhouse.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "func (s *CHStore) countOnePattern(")
	if i < 0 {
		t.Fatal("countOnePattern bulunamadı")
	}
	body := s[i:]
	body = body[:strings.Index(body, "\n}\n")]
	if n := strings.Count(body, "chPatternMatchSQL(tokensSQL)"); n != 2 {
		t.Fatalf("countOnePattern'ın iki sorgusu da ortak yüklemi kullanmalı, %d", n)
	}
	if strings.Contains(body, `"multiSearchAnyCaseInsensitive(body, "`) {
		t.Fatal("countOnePattern yüklemi yeniden satır içi yazmış")
	}
}

func TestChPatternHistogramSQL_Bounded(t *testing.T) {
	sql := chPatternHistogramSQL("['ora-']", 300)
	for _, want := range []string{
		"intDiv(toUnixTimestamp(time), 300) * 300",
		"WHERE time >= ? AND time < ? AND multiSearchAnyCaseInsensitive(body, ['ora-']) AND match(body, ?)",
		"GROUP BY bucket",
		"ORDER BY bucket",
		"LIMIT 500",
		"SETTINGS max_execution_time = 10",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q içermiyor:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "coremetry.") {
		t.Error("tablo nitelikli yazılmış (UNQUALIFIED kuralı)")
	}
	if strings.Count(sql, "?") != 3 {
		t.Errorf("bağ sayısı 3 olmalı (from, to, regex): %s", sql)
	}
}

func TestPatternHistogramBody_Guards(t *testing.T) {
	body := patternHistogramBody(`message:"ora-"`, "message", "ts_custom",
		"2026-10-02T10:00:00Z", "2026-10-02T12:00:00Z", 60, "10s", nil)
	// v0.10.1062 — servis alanı yoksa agg kümesi 1060'takiyle aynı: yalnız kovalar.
	if aggs := body["aggs"].(map[string]any); len(aggs) != 1 {
		t.Fatalf("servis alanı yokken ek agg eklenmemeli: %v", aggs)
	}
	if body["size"] != 0 || body["track_total_hits"] != false || body["timeout"] != "10s" {
		t.Fatalf("maliyet korumaları eksik: size=%v tth=%v timeout=%v", body["size"], body["track_total_hits"], body["timeout"])
	}
	filters := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
	if len(filters) != 2 {
		t.Fatalf("filter 2 üye (range + query_string) olmalı: %v", filters)
	}
	rng, ok := filters[0].(map[string]any)["range"].(map[string]any)["ts_custom"].(map[string]any)
	if !ok || rng["gte"] != "2026-10-02T10:00:00Z" || rng["lt"] != "2026-10-02T12:00:00Z" {
		t.Fatalf("pencere sorguda değil / yanlış alan: %v", filters[0])
	}
	qs := filters[1].(map[string]any)["query_string"].(map[string]any)
	if qs["query"] != `message:"ora-"` || qs["default_operator"] != "OR" || qs["allow_leading_wildcard"] != false {
		t.Fatalf("query_string CountPatterns'tan ayrışmış: %v", qs)
	}
	dh := body["aggs"].(map[string]any)["buckets"].(map[string]any)["date_histogram"].(map[string]any)
	if dh["fixed_interval"] != "60s" || dh["field"] != "ts_custom" || dh["min_doc_count"] != 1 {
		t.Fatalf("date_histogram yanlış: %v", dh)
	}
}

func TestDecodePatternHistogram(t *testing.T) {
	var raw esPatternHistogramResponse
	raw.Aggregations.Buckets.Buckets = append(raw.Aggregations.Buckets.Buckets,
		struct {
			Key      float64 `json:"key"`
			DocCount float64 `json:"doc_count"`
		}{Key: 1_759_400_000_000, DocCount: 12},
		struct {
			Key      float64 `json:"key"`
			DocCount float64 `json:"doc_count"`
		}{Key: 1_759_400_060_000, DocCount: 9400},
	)
	got := decodePatternHistogram(raw)
	if got.Partial {
		t.Fatal("tam cevap kısmi işaretlendi")
	}
	if len(got.Points) != 2 || got.Points[0].T != 1_759_400_000_000_000_000 || got.Points[1].V != 9400 {
		t.Fatalf("noktalar: %+v", got.Points)
	}
	raw.TimedOut = true
	if !decodePatternHistogram(raw).Partial {
		t.Fatal("yumuşak zaman aşımı kısmi sayılmadı")
	}
	empty := decodePatternHistogram(esPatternHistogramResponse{})
	if empty.Points == nil || len(empty.Points) != 0 {
		t.Fatalf("boş cevap boş (nil olmayan) dilim olmalı: %#v", empty.Points)
	}
}
