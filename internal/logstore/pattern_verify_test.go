package logstore

// v0.10.1080 — ES log deseni regex doğrulaması (operatör, prod ES: "Oracle
// TNS error diyor ama loglarda öyle bir şey yok, hatalı desen buluyor.").
// Gövdeler SENTETİK.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var tnsSpec = PatternSpec{Name: "Oracle TNS errors", Regex: `TNS-[0-9]+`, Tokens: []string{"tns-"}}

// Örnek alt sorgusu SINIRLI: size ≤ 50, shard başına terminate_after, _source
// yalnız gövde, _doc sırası, sayım yok, yumuşak timeout; yüklem sayımınkiyle
// aynı (pencere + patternMatchClause).
func TestPatternSampleBody_Bounded(t *testing.T) {
	b := patternSampleBody(patternMatchClause(tnsSpec, "message"), "message", "@timestamp", "F", "T", patternSampleSize, "5s")
	if b["size"] != patternSampleSize || patternSampleSize > 50 {
		t.Fatalf("size = %v, tavan 50", b["size"])
	}
	if b["terminate_after"] != patternSampleSize {
		t.Errorf("terminate_after = %v, want %d", b["terminate_after"], patternSampleSize)
	}
	if b["track_total_hits"] != false || b["timeout"] != "5s" {
		t.Errorf("maliyet bekçileri eksik: %v", b)
	}
	if src, _ := b["_source"].([]string); len(src) != 1 || src[0] != "message" {
		t.Errorf("_source yalnız gövde olmalı: %v", b["_source"])
	}
	raw, _ := json.Marshal(b)
	s := string(raw)
	for _, want := range []string{
		`"sort":["_doc"]`,
		`"range":{"@timestamp":{"gte":"F","lt":"T"}}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("gövde %q taşımıyor: %s", want, s)
		}
	}
	clause, _ := json.Marshal(patternMatchClause(tnsSpec, "message"))
	if !strings.Contains(s, string(clause)) {
		t.Errorf("yüklem CountPatterns'ınkiyle aynı olmalı: %s", s)
	}
	for _, banned := range []string{`"aggs"`, `"highlight"`} {
		if strings.Contains(s, banned) {
			t.Errorf("örnek gövdesinde %s olmamalı: %s", banned, s)
		}
	}
}

// _msearch gövdesi: desen başına başlık+gövde; başlık request_cache taşır;
// token'sız desen match_none ile hizayı korur.
func TestPatternVerifyNDJSON_Shape(t *testing.T) {
	nd := patternVerifyNDJSON([]PatternSpec{tnsSpec, {Name: "boş", Regex: `x`}},
		[]string{"logs-*"}, "message", "@timestamp", "F", "T", "5s")
	lines := strings.Split(strings.TrimSuffix(nd, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("4 satır bekleniyordu, %d: %q", len(lines), nd)
	}
	for _, i := range []int{0, 2} {
		if !strings.Contains(lines[i], `"request_cache":true`) || !strings.Contains(lines[i], `"index":["logs-*"]`) {
			t.Errorf("başlık %d: %s", i, lines[i])
		}
	}
	if !strings.Contains(lines[1], `"size":50`) {
		t.Errorf("örnek gövdesi: %s", lines[1])
	}
	if lines[3] != `{"size":0,"query":{"match_none":{}}}` {
		t.Errorf("token'sız desen match_none olmalı: %s", lines[3])
	}
}

// Regex Go'da CH match() anlamıyla: büyük/küçük harf DUYARLI (desen (?i)
// taşımıyorsa), `.` satır sonunu da eşler.
func TestVerifyPatternBodies_MirrorsCHMatch(t *testing.T) {
	bodies := []string{
		"TNS-12541: TNS:no listener on host db-sentetik-01",
		"cache warmup done, tns alias resolved for demo-db",
		"order 7 shipped via tns route",
		"tns-12541 küçük harf", // CH match duyarlı → uymaz
		"fallback ok",
	}
	v := verifyPatternBodies(`TNS-[0-9]+`, bodies)
	if v.Sampled != 5 || v.Matched != 1 || v.Sample != bodies[0] {
		t.Fatalf("got %+v", v)
	}
	if r, ok := v.Ratio(); !ok || r != 0.2 {
		t.Errorf("ratio = %v %v", r, ok)
	}
	// (?s): CH'de `.` satır sonunu eşler.
	dl := verifyPatternBodies(`Deployment ".*" was rolled back`, []string{"Deployment \"demo\nwar\" was rolled back"})
	if dl.Matched != 1 {
		t.Errorf("çok satırlı gövde CH'de eşleşir, Go'da da eşleşmeli: %+v", dl)
	}
	// (?i) taşıyan desen duyarsız kalır.
	au := verifyPatternBodies(`(?i)401 Unauthorized|access denied`, []string{"ACCESS DENIED for demo-user"})
	if au.Matched != 1 {
		t.Errorf("(?i) desen: %+v", au)
	}
	// Derlenemeyen regex → bilinmiyor (bastırma yok).
	if bad := verifyPatternBodies(`(`, bodies); bad.Sampled != 0 {
		t.Errorf("bozuk regex Sampled 0 olmalı: %+v", bad)
	}
	if _, ok := (PatternVerification{}).Ratio(); ok {
		t.Error("boş örnek oran üretmemeli")
	}
}

func TestVerifiedRatioNote(t *testing.T) {
	for _, c := range []struct {
		r    float64
		want string
	}{
		{0, ""},
		{1, ""},
		{-0.5, ""},
		{0.4, "örneklemde %40 regex doğrulandı"},
		{0.996, "örneklemde %99 regex doğrulandı"},
		{0.004, "örneklemde %1 regex doğrulandı"},
	} {
		if got := VerifiedRatioNote(c.r); got != c.want {
			t.Errorf("VerifiedRatioNote(%v) = %q, want %q", c.r, got, c.want)
		}
	}
}

// CH yolu DOKUNULMADI: VerifyPatterns sorgusuz nil (sayım regex'i içeriyor —
// nil store'a dokunsaydı panik) ve sayım yüklemi bayt bayt aynı.
func TestCHVerifyPatterns_NoQueryAndPredicateUnchanged(t *testing.T) {
	vs, err := (&CHStore{}).VerifyPatterns(context.Background(), []PatternSpec{tnsSpec}, time.Now(), time.Now())
	if vs != nil || err != nil {
		t.Fatalf("CH VerifyPatterns nil, nil döndürmeli: %v %v", vs, err)
	}
	if got := chPatternMatchSQL(chBuildTokenLiteral(tnsSpec.Tokens)); got != "multiSearchAnyCaseInsensitive(body, ['tns-']) AND match(body, ?)" {
		t.Errorf("CH yüklemi değişti: %s", got)
	}
	src, err := os.ReadFile("clickhouse.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "VerifyPatterns") || strings.Contains(string(src), "verifyPatternBodies") {
		t.Error("CH sayım yolu doğrulamaya bağlanmamalı")
	}
}

// ES uçtan uca: TEK _msearch, gövde başına sınırlı örnek; regex Go'da;
// alt sorgu hatası ve gövdesiz doküman "bilinmiyor".
func TestESVerifyPatterns_SingleMsearch(t *testing.T) {
	var mu sync.Mutex
	var msearches int
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/":
			_, _ = w.Write([]byte(`{"name":"n1","cluster_name":"c","version":{"number":"8.13.0"},"tagline":"You Know, for Search"}`))
		case strings.HasSuffix(r.URL.Path, "/_msearch"):
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			msearches++
			sent = string(b)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"responses":[
				{"timed_out":false,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"hits":[
					{"_source":{"message":"TNS-12541: TNS:no listener"}},
					{"_source":{"message":"token tns refreshed for demo-user"}},
					{"_source":{"other":"gövdesiz"}}]}},
				{"error":{"type":"query_shard_exception","reason":"stub"},"status":400}
			]}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"type":"not_found","reason":"stub"},"status":404}`))
		}
	}))
	t.Cleanup(srv.Close)
	es, err := NewES(ESConfig{Addresses: []string{srv.URL}, Fields: ESFieldMap{Body: "message"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ora := PatternSpec{Name: "Oracle errors (ORA-)", Regex: `ORA-[0-9]+`, Tokens: []string{"ora-"}}
	vs, err := es.VerifyPatterns(context.Background(), []PatternSpec{tnsSpec, ora}, now.Add(-5*time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if msearches != 1 {
		t.Fatalf("tek _msearch bekleniyordu, %d", msearches)
	}
	if len(vs) != 2 {
		t.Fatalf("len = %d", len(vs))
	}
	if vs[0].Sampled != 2 || vs[0].Matched != 1 || vs[0].Sample != "TNS-12541: TNS:no listener" {
		t.Errorf("slot 0 = %+v (gövdesiz doküman örneğe girmemeli)", vs[0])
	}
	if vs[1] != (PatternVerification{}) {
		t.Errorf("hatalı alt sorgu bilinmiyor olmalı: %+v", vs[1])
	}
	if c := strings.Count(sent, `"terminate_after":50`); c != 2 {
		t.Errorf("iki sınırlı alt sorgu bekleniyordu (%d): %s", c, sent)
	}
	if !strings.Contains(sent, `"_source":["message"]`) {
		t.Errorf("_source yalnız gövde: %s", sent)
	}
}
