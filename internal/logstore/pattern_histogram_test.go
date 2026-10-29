package logstore

// pattern_histogram_test.go — v0.10.1060 (log deseni anomalisi: zaman içinde
// sayım grafiği). Çivilenen: (1) CH grafiği dedektörle AYNI yüklemle sayar,
// (2) CH sorgusu zaman-sınırlı + LIMIT + max_execution_time taşır, (3) ES
// gövdesi v0.8.3/v0.8.164 maliyet korumalarını ve pencereyi SORGUDA taşır,
// (4) ES cevabı ms → ns doğru çevrilir, kısmi cevap işaretlenir.

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
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
	// v0.10.1586 — ana sayım sorgusu saf chCountPatternSQL'e çıktı; iki
	// sorgunun yüklemi yine ortak (biri orada, top-servis sorgusu burada).
	j := strings.Index(s, "func chCountPatternSQL(")
	if j < 0 {
		t.Fatal("chCountPatternSQL bulunamadı")
	}
	main := s[j:]
	main = main[:strings.Index(main, "\n}\n")]
	if !strings.Contains(body, "chCountPatternSQL(tokensSQL,") {
		t.Fatal("countOnePattern ana sorguyu chCountPatternSQL'den almıyor")
	}
	if n := strings.Count(body, "chPatternMatchSQL(tokensSQL)") + strings.Count(main, "chPatternMatchSQL(tokensSQL)"); n != 2 {
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
	oraClause := patternMatchClause(PatternSpec{Tokens: []string{"ora-"}}, "message")
	body := patternHistogramBody(oraClause, "message", "ts_custom",
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
		t.Fatalf("filter 2 üye (range + desen yan tümcesi) olmalı: %v", filters)
	}
	rng, ok := filters[0].(map[string]any)["range"].(map[string]any)["ts_custom"].(map[string]any)
	if !ok || rng["gte"] != "2026-10-02T10:00:00Z" || rng["lt"] != "2026-10-02T12:00:00Z" {
		t.Fatalf("pencere sorguda değil / yanlış alan: %v", filters[0])
	}
	// v0.10.1087 — `ora-` kısa + tireli: ifade kalır (prefix `ora*` "oracle"ı sayardı).
	if !reflect.DeepEqual(filters[1], oraClause) {
		t.Fatalf("desen yan tümcesi CountPatterns'tan ayrışmış: %v", filters[1])
	}
	want := map[string]any{"bool": map[string]any{"should": []any{
		map[string]any{"match_phrase": map[string]any{"message": "ora-"}}}, "minimum_should_match": 1}}
	if !reflect.DeepEqual(oraClause, want) {
		t.Fatalf("ora- ifade olmalı: %v", oraClause)
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

// v0.10.1586 — sürdürme penceresi yokken ana sayım sorgusu ve argümanları
// bayt bayt öncekiyle aynı (eski satır içi metin buraya sabitlendi).
func TestCHCountPatternSQLLegacyIdentity(t *testing.T) {
	cur := time.Date(2026, 10, 29, 9, 55, 0, 0, time.UTC)
	base, now := cur.Add(-55*time.Minute), cur.Add(5*time.Minute)
	sql, args := chCountPatternSQL("['ora-']", `ORA-[0-9]+`, cur, nil, base, now)
	want := `
		SELECT
		  countIf(time >= ?)                                      AS cur,
		  countIf(time <  ?)                                      AS base,
		  anyHeavyIf(service_name, time >= ?)                     AS svc,
		  anyIf(body, time >= ?)                                  AS sample,
		  toUnixTimestamp64Nano(maxIf(time, time >= ?))           AS last_ns
		FROM logs
		WHERE time >= ? AND time < ? AND ` + chPatternMatchSQL("['ora-']") + `
		SETTINGS max_execution_time = 10`
	if sql != want {
		t.Fatalf("metin değişti:\n%s\n---\n%s", sql, want)
	}
	wantArgs := []any{cur, cur, cur, cur, cur, base, now, `ORA-[0-9]+`}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("argümanlar: %v", args)
	}
	p0, p1 := cur.Add(-5*time.Minute), cur.Add(-10*time.Minute)
	sql, args = chCountPatternSQL("['ora-']", `ORA-[0-9]+`, cur, []time.Time{p0, p1}, base, now)
	if strings.Count(sql, "?") != len(args) || !strings.Contains(sql, "AS prev_0") || !strings.Contains(sql, "AS prev_1") {
		t.Fatalf("sürdürme kolonları: %s %v", sql, args)
	}
	if !args[1].(time.Time).Equal(p1) || !args[5].(time.Time).Equal(p0) || !args[6].(time.Time).Equal(cur) ||
		!args[7].(time.Time).Equal(p1) || !args[8].(time.Time).Equal(p0) {
		t.Fatalf("pencere bağları: taban üstü en eski başlangıç, prev_0=[p0,cur), prev_1=[p1,p0): %v", args)
	}
}
