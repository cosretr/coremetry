package mcptools

// evidence_scale_test.go — v0.10.2384: araç alan tavanlarının kanıt ölçeği.
// Her ölçeklenen tavan için: ölçek yok / 1 → bugünkü çıktıyla bayt bayt aynı;
// ölçek 8 → eskiden kesilen uzun fikstür TAM taşınır; hâlâ uzun girdi kesilir
// ve kesim gerçek sayılarla söylenir. Saat ve dilimden bağımsız (sabit damga).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func esJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func esRunes(s string) int { return utf8.RuneCountInString(s) }

// Ölçek ctx'te: yoksa 1, ≤1 yazılmaz, üst sınır 8.
func TestKanitOlcegiCtxVarsayilanVeKelepce(t *testing.T) {
	ctx := context.Background()
	if EvidenceScale(ctx) != 1 || EvidenceScale(nil) != 1 { //nolint:staticcheck // nil ctx: savunma dalı
		t.Fatal("ölçek yokken 1 olmalı")
	}
	for _, n := range []int{-3, 0, 1} {
		if c := WithEvidenceScale(ctx, n); c != ctx || EvidenceScale(c) != 1 {
			t.Fatalf("ölçek %d ctx'i değiştirmemeli", n)
		}
	}
	if got := EvidenceScale(WithEvidenceScale(ctx, 4)); got != 4 {
		t.Fatalf("ölçek 4 → %d", got)
	}
	if got := EvidenceScale(WithEvidenceScale(ctx, 99)); got != EvidenceScaleMax {
		t.Fatalf("ölçek 99 → %d, tavan %d", got, EvidenceScaleMax)
	}
	if scaleCap(500, 0) != 500 || scaleCap(500, 1) != 500 || scaleCap(500, 8) != 4000 || scaleCap(500, 50) != 4000 {
		t.Fatal("scaleCap: ≤1 taban, 8 → ×8, üstü kelepçeli")
	}
	if scaleCacheKey("k", 1) != "k" || scaleCacheKey("k", 0) != "k" || scaleCacheKey("k", 8) != "k:es8" {
		t.Fatalf("önbellek anahtarı: %q %q", scaleCacheKey("k", 1), scaleCacheKey("k", 8))
	}
}

// search_logs (v1) / get_logs_for_trace satırı: gövde, öznitelik değeri ve sayısı.
func TestKanitOlcegiLogSatiri(t *testing.T) {
	attrs := map[string]string{"error.message": strings.Repeat("ğ", 1000)}
	for i := 0; i < 29; i++ {
		attrs[fmt.Sprintf("custom.key%02d", i)] = "v"
	}
	recs := []*logstore.LogRecord{
		{Timestamp: 1_700_000_000_000_000_000, Severity: 17, ServiceName: "orders-api", Body: strings.Repeat("ş", 3000), Attributes: attrs},
		{Timestamp: 1_700_000_001_000_000_000, Severity: 17, ServiceName: "orders-api", Body: strings.Repeat("ç", 5000)},
	}
	m := logstore.FieldMapping{}
	if a, b := esJSON(t, logRows(recs, m)), esJSON(t, logRowsScaled(recs, m, 1)); a != b {
		t.Fatal("ölçek 1 satırları bugünkünden farklı")
	}
	base := logRows(recs, m)
	if esRunes(base[0].Body) != logBodyMaxRunes+1 || !base[0].BodyTruncated || len(base[0].Attrs) != logAttrsMax || base[0].AttrsOmitted != 30-logAttrsMax {
		t.Fatalf("taban: gövde %d rune, attr %d (+%d)", esRunes(base[0].Body), len(base[0].Attrs), base[0].AttrsOmitted)
	}
	if esRunes(base[0].Attrs["error.message"]) != logAttrValueMaxRunes+1 {
		t.Fatalf("taban öznitelik değeri %d rune", esRunes(base[0].Attrs["error.message"]))
	}
	big := logRowsScaled(recs, m, 8)
	if big[0].Body != recs[0].Body || big[0].BodyTruncated {
		t.Fatalf("ölçek 8: 3000 rune'luk gövde tam taşınmalı (%d rune, truncated=%v)", esRunes(big[0].Body), big[0].BodyTruncated)
	}
	if len(big[0].Attrs) != 30 || big[0].AttrsOmitted != 0 || big[0].Attrs["error.message"] != attrs["error.message"] {
		t.Fatalf("ölçek 8: 30 öznitelik ve 1000 rune'luk değer tam (%d, +%d)", len(big[0].Attrs), big[0].AttrsOmitted)
	}
	// Hâlâ uzun: 5000 > 4000 → kesilir, bayrak + "…".
	if esRunes(big[1].Body) != logBodyMaxRunes*8+1 || !big[1].BodyTruncated || !strings.HasSuffix(big[1].Body, "…") {
		t.Fatalf("ölçek 8: 5000 rune 4000'de kesilmeli (%d, truncated=%v)", esRunes(big[1].Body), big[1].BodyTruncated)
	}
}

// search_logs handler'ı ölçeği ctx'ten okur; ctx'siz çağrı bugünkü gövde.
func TestKanitOlcegiSearchLogsHandler(t *testing.T) {
	recs := []*logstore.LogRecord{{Timestamp: 1_700_000_000_000_000_000, Severity: 17, ServiceName: "orders-api", Body: strings.Repeat("ş", 3000)}}
	tool := searchLogsV2Tool(Deps{LogStore: &stubLogStore{page: &logstore.Page{Logs: recs, Total: 1}}})
	args := `{"limit":5,"from_iso":"2026-09-26T10:00:00Z","to_iso":"2026-09-26T11:00:00Z"}`
	bodyOf := func(ctx context.Context) string {
		m, err := runLogsTool(t, ctx, tool, args)
		if err != nil {
			t.Fatal(err)
		}
		return m["logs"].([]any)[0].(map[string]any)["body"].(string)
	}
	if n := esRunes(bodyOf(context.Background())); n != logBodyMaxRunes+1 {
		t.Fatalf("ölçeksiz çağrı %d rune — bugünkü tavan %d+…", n, logBodyMaxRunes)
	}
	if n := esRunes(bodyOf(WithEvidenceScale(context.Background(), 1))); n != logBodyMaxRunes+1 {
		t.Fatalf("ölçek 1 çağrı %d rune", n)
	}
	if got := bodyOf(WithEvidenceScale(context.Background(), 8)); got != recs[0].Body {
		t.Fatalf("ölçek 8: gövde tam taşınmalı (%d rune)", esRunes(got))
	}
}

// query_logs gövdesi.
func TestKanitOlcegiQueryLogs(t *testing.T) {
	recs := []*logstore.LogRecord{
		{Timestamp: 1_700_000_000_000_000_000, Severity: 17, ServiceName: "orders-api", Body: strings.Repeat("ö", 2000)},
		{Timestamp: 1_700_000_001_000_000_000, Severity: 17, ServiceName: "orders-api", Body: strings.Repeat("ü", 3000)},
	}
	st := &stubLogStore{page: &logstore.Page{Logs: recs}}
	run := func(ctx context.Context) (string, []qlRow) {
		res, err := runQueryLogs(ctx, st, json.RawMessage(`{"service":"orders-api","limit":5}`))
		if err != nil || !res.OK {
			t.Fatalf("query_logs: %v %+v", err, res.Error)
		}
		return esJSON(t, res.Data), res.Data.(map[string]any)["logs"].([]qlRow)
	}
	baseJSON, base := run(context.Background())
	if one, _ := run(WithEvidenceScale(context.Background(), 1)); one != baseJSON {
		t.Fatal("ölçek 1 gövdesi ölçeksiz gövdeden farklı")
	}
	if esRunes(base[0].Body) != qlBodyMaxRunes+1 {
		t.Fatalf("taban gövde %d rune", esRunes(base[0].Body))
	}
	_, big := run(WithEvidenceScale(context.Background(), 8))
	if big[0].Body != recs[0].Body {
		t.Fatalf("ölçek 8: 2000 rune'luk gövde tam (%d)", esRunes(big[0].Body))
	}
	if esRunes(big[1].Body) != qlBodyMaxRunes*8+1 || !strings.HasSuffix(big[1].Body, "…") {
		t.Fatalf("ölçek 8: 3000 rune 2400'de kesilmeli (%d)", esRunes(big[1].Body))
	}
}

// get_exception_samples: mesaj + stack; kesim notu gerçek sayılarla.
func TestKanitOlcegiExceptionOrnekleri(t *testing.T) {
	in := []chstore.ExceptionSample{
		{TraceID: "t1", Time: 1_700_000_000_000_000_000, Message: strings.Repeat("ö", 900), Stacktrace: strings.Repeat("çerçeve\n", 500)}, // 4000 rune
		{TraceID: "t2", Time: 1_700_000_000_000_000_000, Message: "kısa", Stacktrace: strings.Repeat("x", 20000)},
	}
	b0, cut0 := exSampleRows("payments-api", "java.lang.IllegalStateException", in)
	b1, cut1 := exSampleRowsScaled("payments-api", "java.lang.IllegalStateException", in, 1)
	if esJSON(t, b0) != esJSON(t, b1) || cut0 != cut1 || !cut0 {
		t.Fatal("ölçek 1 satırları bugünkünden farklı")
	}
	if !strings.Contains(b0[0].Stacktrace, fmt.Sprintf("first %d of 4000 characters", exSampleStackMaxRunes)) ||
		!strings.Contains(b0[0].Message, fmt.Sprintf("first %d of 900 characters", exSampleMsgMaxRunes)) {
		t.Fatalf("taban kesim notu sayıları: %q", b0[0].Message[len(b0[0].Message)-60:])
	}
	big, cut := exSampleRowsScaled("payments-api", "java.lang.IllegalStateException", in, 8)
	if big[0].Stacktrace != in[0].Stacktrace || big[0].Message != in[0].Message {
		t.Fatal("ölçek 8: 4000 rune'luk stack ve 900 rune'luk mesaj tam taşınmalı")
	}
	want := fmt.Sprintf("\n…[truncated: first %d of 20000 characters]", exSampleStackMaxRunes*8)
	if !cut || !strings.HasSuffix(big[1].Stacktrace, want) || esRunes(big[1].Stacktrace) != exSampleStackMaxRunes*8+esRunes(want) {
		t.Fatalf("ölçek 8: 20000 rune 16000'de kesilmeli ve not gerçek sayıları yazmalı: …%q", big[1].Stacktrace[len(big[1].Stacktrace)-70:])
	}
}

func esErrorTrace(n, statusRunes int) []chstore.SpanRow {
	id := strings.Repeat("a", 32)
	spans := []chstore.SpanRow{{TraceID: id, SpanID: "root000000000000", ServiceName: "orders-api", Name: "POST /api/orders",
		StartTime: 1_700_000_000_000_000_000, EndTime: 1_700_000_001_000_000_000}}
	for i := 0; i < n; i++ {
		spans = append(spans, chstore.SpanRow{TraceID: id, SpanID: fmt.Sprintf("e%015d", i), ParentSpanID: "root000000000000",
			ServiceName: "payments-api", Name: "charge", StatusCode: "error", StatusMessage: strings.Repeat("ı", statusRunes),
			StartTime: 1_700_000_000_000_000_000 + int64(i+1)*1e6, EndTime: 1_700_000_000_000_000_000 + int64(i+2)*1e6})
	}
	return spans
}

// get_trace analizi: hata span'i sayısı ve durum metni.
func TestKanitOlcegiTraceAnalizi(t *testing.T) {
	spans := esErrorTrace(30, 1000)
	id := spans[0].TraceID
	if a, b := esJSON(t, traceToolPayload(id, spans)), esJSON(t, traceToolPayloadScaled(id, spans, 1)); a != b {
		t.Fatal("ölçek 1 zarfı bugünkünden farklı")
	}
	base := buildTraceToolAnalysis(spans, false)
	if len(base.ErrorSpans) != traceToolErrorSpans || base.ErrorSpanCount != 30 || esRunes(base.ErrorSpans[0].StatusMessage) != traceToolStatusRunes+1 {
		t.Fatalf("taban: %d hata span'i, durum %d rune", len(base.ErrorSpans), esRunes(base.ErrorSpans[0].StatusMessage))
	}
	big := buildTraceToolAnalysisScaled(spans, false, 8)
	if len(big.ErrorSpans) != 30 || big.ErrorSpans[0].StatusMessage != spans[1].StatusMessage {
		t.Fatalf("ölçek 8: 30 hata span'i ve 1000 rune'luk durum tam (%d, %d rune)", len(big.ErrorSpans), esRunes(big.ErrorSpans[0].StatusMessage))
	}
	long := buildTraceToolAnalysisScaled(esErrorTrace(100, 3000), false, 8)
	if len(long.ErrorSpans) != traceToolErrorSpans*8 || long.ErrorSpanCount != 100 ||
		esRunes(long.ErrorSpans[0].StatusMessage) != traceToolStatusRunes*8+1 || !strings.HasSuffix(long.ErrorSpans[0].StatusMessage, "…") {
		t.Fatalf("ölçek 8 tavanı: %d span (toplam %d), durum %d rune", len(long.ErrorSpans), long.ErrorSpanCount, esRunes(long.ErrorSpans[0].StatusMessage))
	}
	// Handler ölçeği ctx'ten okur; span listesi tavanı ölçeklenmez.
	if getTraceSpanCap != 200 {
		t.Fatalf("span listesi tavanı değişti: %d", getTraceSpanCap)
	}
}

// v2 search_logs: gövde, bayt bütçesi, satır tavanı.
func TestKanitOlcegiV2SearchLogs(t *testing.T) {
	from, to := time.UnixMilli(1_700_000_000_000), time.UnixMilli(1_700_003_600_000)
	p := searchLogsPlan{Services: []string{"orders-api", "payments-api"}, Team: "acme-orders", WindowS: 3600, Limit: 10, Words: "timeout"}
	page := &logstore.Page{Total: 500}
	for i := 0; i < 10; i++ {
		page.Logs = append(page.Logs, &logstore.LogRecord{Timestamp: int64(i) * 1e9, ServiceName: "orders-api", Body: strings.Repeat("connection reset ", 30), TraceID: strings.Repeat("c", 32)})
	}
	baseOut := searchLogsPayload("es", p, page, []int64{1, 2, 3}, nil, from, to)
	base := esJSON(t, baseOut)
	if one := esJSON(t, searchLogsPayloadScaled("es", p, page, []int64{1, 2, 3}, nil, from, to, 1)); one != base {
		t.Fatal("ölçek 1 gövdesi bugünkünden farklı")
	}
	if len(base) > V2ToolMaxBytes || baseOut["count"].(int) >= 10 || baseOut["truncated"] != true || esRunes(baseOut["rows"].([]slRow)[0].Body) != slBodyRunes+1 {
		t.Fatalf("taban: %d B, %v satır (fikstür tabanda kırpılmalıydı)", len(base), baseOut["count"])
	}
	bigOut := searchLogsPayloadScaled("es", p, page, []int64{1, 2, 3}, nil, from, to, 8)
	rows := bigOut["rows"].([]slRow)
	if len(rows) != 10 || rows[0].Body != page.Logs[0].Body || bigOut["truncated"] != nil || len(esJSON(t, bigOut)) > V2ToolMaxBytes*8 {
		t.Fatalf("ölçek 8: 10 satır tam gövdeyle, kırpmasız (%d satır, %d B)", len(rows), len(esJSON(t, bigOut)))
	}
	// Hâlâ uzun: 80 satır × 1500 rune → gövde 1280'de kesilir, sığmayan satır
	// sondan düşer ve `truncated` yazılır; gövde ölçekli bütçeyi aşmaz.
	p80 := p
	p80.Limit = slScaledLimit(p.Limit, 8)
	long := &logstore.Page{Total: 500}
	for i := 0; i < 80; i++ {
		long.Logs = append(long.Logs, &logstore.LogRecord{Timestamp: int64(i) * 1e9, ServiceName: "orders-api", Body: strings.Repeat("z", 1500)})
	}
	cutOut := searchLogsPayloadScaled("es", p80, long, nil, nil, from, to, 8)
	cut := cutOut["rows"].([]slRow)
	if n := len(esJSON(t, cutOut)); n > V2ToolMaxBytes*8 || cutOut["truncated"] != true || len(cut) == 0 || len(cut) >= 80 ||
		cutOut["count"].(int) != len(cut) || esRunes(cut[0].Body) != slBodyRunes*8+1 {
		t.Fatalf("ölçek 8 tavanı: %d B, %d satır, truncated=%v, gövde %d rune", n, len(cut), cutOut["truncated"], esRunes(cut[0].Body))
	}
	// Satır tavanı: yalnız tavandaki limit büyür; açık küçük limit ve ölçek 1 aynen.
	for _, c := range []struct{ limit, scale, want int }{{10, 1, 10}, {10, 0, 10}, {10, 8, 80}, {3, 8, 3}, {10, 2, 20}} {
		if got := slScaledLimit(c.limit, c.scale); got != c.want {
			t.Errorf("slScaledLimit(%d, %d) = %d, beklenen %d", c.limit, c.scale, got, c.want)
		}
	}
}

// v2 search_logs handler'ı: ölçek arka uç LIMIT'ini ve memo anahtarını değiştirir;
// ölçeksiz çağrı bugünkü LIMIT ve bugünkü anahtar.
func TestKanitOlcegiV2SearchLogsOkumaVeAnahtar(t *testing.T) {
	recs := []*logstore.LogRecord{{Timestamp: 1_700_000_000_000_000_000, Severity: 17, ServiceName: "orders-api", Body: strings.Repeat("k", 1000)}}
	st := &stubLogStore{page: &logstore.Page{Logs: recs, Total: 1}}
	var keys []string
	memo := &fakeMemo{}
	d := Deps{LogStore: st, Memo: func(ctx context.Context, key string, ttl time.Duration, fn func(context.Context) (any, bool, error)) ([]byte, error) {
		keys = append(keys, key)
		// Kapanışa ölçeksiz ctx verilir: gövde ölçeği handler'ın yakaladığı
		// değerden gelmeli (memo uygulaması ctx'i taşımak zorunda değil).
		return memo.memo(context.Background(), key, ttl, fn)
	}}
	tool := cosreSearchLogsTool(d)
	call := func(ctx context.Context) string {
		out, err := tool.Handler(WithAnchor(ctx, time.Unix(1_700_000_000, 0)), json.RawMessage(`{"service":"orders-api"}`))
		if err != nil {
			t.Fatal(err)
		}
		return esJSON(t, out)
	}
	base := call(context.Background())
	if st.gotFilter.Limit != slMaxRows || strings.Contains(keys[0], ":es") {
		t.Fatalf("ölçeksiz: LIMIT %d, anahtar %q", st.gotFilter.Limit, keys[0])
	}
	big := call(WithEvidenceScale(context.Background(), 8))
	if st.gotFilter.Limit != slMaxRows*8 || !strings.HasSuffix(keys[1], ":es8") || keys[1] == keys[0] {
		t.Fatalf("ölçek 8: LIMIT %d, anahtar %q", st.gotFilter.Limit, keys[1])
	}
	if !strings.Contains(big, strings.Repeat("k", 1000)) || strings.Contains(base, strings.Repeat("k", slBodyRunes+1)) {
		t.Fatal("ölçek 8 gövdeyi tam taşımalı; ölçeksiz gövde 160'ta kesik kalmalı")
	}
	// Ölçeksiz çağrı ölçekli girdiden SONRA da bugünkü gövdeyi alır (ayrı anahtar).
	if again := call(context.Background()); again != base || memo.calls != 2 {
		t.Fatalf("ölçekli gövde ölçeksiz çağrıya sızdı (okuma=%d)", memo.calls)
	}
}

// find_traces + explain_trace + endpoint_metrics: gövde bütçesi.
func TestKanitOlcegiV2GovdeButcesi(t *testing.T) {
	from, to := time.UnixMilli(1_700_000_000_000), time.UnixMilli(1_700_003_600_000)
	p := findTracesPlan{Services: []string{"orders-api", "payments-api"}, WindowS: 3600, Limit: 10}
	var rows []chstore.TraceRow
	for i := 0; i < 10; i++ {
		rows = append(rows, chstore.TraceRow{TraceID: strings.Repeat(fmt.Sprint(i%10), 32), RootName: "POST /api/orders/" + strings.Repeat("x", 120), ServiceName: "orders-api", DurationMs: 12.5, SpanCount: 40, HasError: true})
	}
	f := p.traceFilter(from, to)
	baseOut := findTracesPayload(p, f, rows, false, from, to)
	if esJSON(t, baseOut) != esJSON(t, findTracesPayloadScaled(p, f, rows, false, from, to, 1)) {
		t.Fatal("find_traces ölçek 1 gövdesi bugünkünden farklı")
	}
	if baseOut["count"].(int) >= 10 || baseOut["truncated"] != true {
		t.Fatalf("fikstür tabanda kırpılmalıydı: count=%v", baseOut["count"])
	}
	bigOut := findTracesPayloadScaled(p, f, rows, false, from, to, 8)
	if bigOut["count"].(int) != 10 || bigOut["truncated"] != nil || len(esJSON(t, bigOut)) > V2ToolMaxBytes*8 {
		t.Fatalf("find_traces ölçek 8: 10 satır kırpmasız (count=%v, %d B)", bigOut["count"], len(esJSON(t, bigOut)))
	}

	an := traceToolAnalysis{WallMs: 1200, ExtentMs: 1300, SpanCount: 400, ErrorSpanCount: 12,
		Root: traceToolSpanRef{Service: "orders-api", Name: "POST /api/orders", DurationMs: 1200}}
	for i := 0; i < 12; i++ {
		an.CriticalPath = append(an.CriticalPath, traceToolPathStep{Service: "orders-api", Name: fmt.Sprintf("step %02d", i), SelfOnPathMs: float64(100 - i), SharePct: 8, Top: i == 0})
		an.ErrorSpans = append(an.ErrorSpans, traceToolErrorSpan{Service: "payments-api", Name: "charge", StatusMessage: strings.Repeat("declined ", 40), ExceptionMessage: strings.Repeat("e", 300)})
	}
	id := strings.Repeat("d", 32)
	etBase := explainTracePayload(id, an, nil, false)
	if esJSON(t, etBase) != esJSON(t, explainTracePayloadScaled(id, an, nil, false, 1)) {
		t.Fatal("explain_trace ölçek 1 gövdesi bugünkünden farklı")
	}
	if n := len(etBase["error_spans"].([]etErr)); n > etMaxErrorSpans || len(etBase["critical_path"].([]etStep)) > etMaxPathSteps {
		t.Fatalf("taban: %d hata span'i", n)
	}
	etBig := explainTracePayloadScaled(id, an, nil, false, 8)
	errs := etBig["error_spans"].([]etErr)
	if len(errs) != 12 || len(etBig["critical_path"].([]etStep)) != 12 || errs[0].Status != an.ErrorSpans[0].StatusMessage ||
		errs[0].ExMsg != an.ErrorSpans[0].ExceptionMessage || etBig["truncated"] != nil || len(esJSON(t, etBig)) > V2ToolMaxBytes*8 {
		t.Fatalf("explain_trace ölçek 8: 12 adım + 12 hata span'i tam metinle (%d, %d B)", len(errs), len(esJSON(t, etBig)))
	}
	// Hâlâ uzun: 60 hata span'i × 2000 rune → metin 1280'de kesilir, satır düşer, `truncated`.
	an.ErrorSpans = nil
	for i := 0; i < 60; i++ {
		an.ErrorSpans = append(an.ErrorSpans, traceToolErrorSpan{Service: "payments-api", Name: "charge", StatusMessage: strings.Repeat("s", 2000)})
	}
	etCut := explainTracePayloadScaled(id, an, nil, false, 8)
	cutErrs := etCut["error_spans"].([]etErr)
	if n := len(esJSON(t, etCut)); n > V2ToolMaxBytes*8 || etCut["truncated"] != true || len(cutErrs) == 0 || len(cutErrs) > etMaxErrorSpans*8 ||
		esRunes(cutErrs[0].Status) != etMsgRunes*8+1 {
		t.Fatalf("explain_trace ölçek 8 tavanı: %d B, %d satır, truncated=%v", n, len(cutErrs), etCut["truncated"])
	}
}

// list_my_services / open_problems / ML araçları: yalnız bayt bütçesi.
func TestKanitOlcegiBaytButceliAraclar(t *testing.T) {
	var inbox []InboxRow
	for i := 0; i < 25; i++ {
		inbox = append(inbox, InboxRow{ID: fmt.Sprintf("problem:%030d", i), Kind: "problem", Service: "orders-api", Priority: "P2",
			Reason: strings.Repeat("timeout ", 15), SinceUnix: 1_800_000_000, PageURL: "/problems?problem=p"})
	}
	res := InboxOpenResult{Rows: inbox, Total: 25}
	opBase := openProblemsPayload("acme", "prod", []string{"orders-api"}, 0, 0, res, 25)
	if esJSON(t, opBase) != esJSON(t, openProblemsPayloadScaled("acme", "prod", []string{"orders-api"}, 0, 0, res, 25, 1)) {
		t.Fatal("open_problems ölçek 1 gövdesi bugünkünden farklı")
	}
	opBig := openProblemsPayloadScaled("acme", "prod", []string{"orders-api"}, 0, 0, res, 25, 8)
	if opBase["count"].(int) >= 25 || opBig["count"].(int) != 25 || len(esJSON(t, opBig)) > OpenProblemsMaxBytes*8 {
		t.Fatalf("open_problems: taban %v satır, ölçek 8 %v satır", opBase["count"], opBig["count"])
	}

	var svcs []chstore.ServiceSummary
	var names []string
	for i := 0; i < 20; i++ {
		n := fmt.Sprintf("orders-api-%s-%02d", strings.Repeat("s", 90), i)
		names = append(names, n)
		svcs = append(svcs, chstore.ServiceSummary{Name: n, SpanCount: 500, ErrorCount: uint64(20 - i)})
	}
	msBase := myServicesPayload("acme", names, 0, svcs, 3600, 20, "", false, nil)
	if esJSON(t, msBase) != esJSON(t, myServicesPayloadScaled("acme", names, 0, svcs, 3600, 20, "", false, nil, 1)) {
		t.Fatal("list_my_services ölçek 1 gövdesi bugünkünden farklı")
	}
	msBig := myServicesPayloadScaled("acme", names, 0, svcs, 3600, 20, "", false, nil, 8)
	if msBase["count"].(int) >= 20 || msBase["truncated"] != true || msBig["count"].(int) != 20 || msBig["truncated"] != nil ||
		len(esJSON(t, msBig)) > MyServicesMaxBytes*8 {
		t.Fatalf("list_my_services: taban %v satır, ölçek 8 %v satır", msBase["count"], msBig["count"])
	}

	snap := chstore.MLSnapshot{EventID: "e1", Signal: "log", Detector: "error_rate", Service: "orders-api",
		Evidence: `{"bubbleUp":"` + strings.Repeat("b", 3000) + `","exceptions":"` + strings.Repeat("x", 200) + `"}`}
	mlBase := explainMLPayload(snap, nil, nil)
	if esJSON(t, mlBase) != esJSON(t, explainMLPayloadScaled(snap, nil, nil, 1)) {
		t.Fatal("explain_ml_event ölçek 1 gövdesi bugünkünden farklı")
	}
	mlBig := explainMLPayloadScaled(snap, nil, nil, 8)
	if _, kept := mlBase["bubbleUp"]; kept || mlBig["bubbleUp"] == nil || len(esJSON(t, mlBig)) > mlToolMaxBytes*8 {
		t.Fatalf("explain_ml_event: tabanda düşen kanıt ölçek 8'de kalmalı (taban=%v)", kept)
	}
}

// read_source_code: zarf tavanı (api tavanının altında kalır).
func TestKanitOlcegiKaynakKod(t *testing.T) {
	r := &scReader{out: scOK(scWindow(60, 180, 120, 120), 60, 180, 120)}
	tool := scTool(t, Deps{SourceCode: r.read})
	args := json.RawMessage(`{"service":"payments-api","file":"ChargeHandler.java","line":120,"context_lines":60}`)
	call := func(ctx context.Context) (sourceCodeResult, string) {
		out, err := tool.Handler(ctx, args)
		if err != nil {
			t.Fatal(err)
		}
		raw := esJSON(t, out)
		var res sourceCodeResult
		if err := json.Unmarshal([]byte(raw), &res); err != nil {
			t.Fatal(err)
		}
		return res, raw
	}
	base, baseRaw := call(context.Background())
	if _, oneRaw := call(WithEvidenceScale(context.Background(), 1)); oneRaw != baseRaw {
		t.Fatal("ölçek 1 zarfı ölçeksiz zarftan farklı")
	}
	if esRunes(baseRaw) > SourceResultMaxRunes || base.Source.State != "truncated" {
		t.Fatalf("taban: %d rune, state=%s", esRunes(baseRaw), base.Source.State)
	}
	big, bigRaw := call(WithEvidenceScale(context.Background(), 8))
	if big.FromLine != 60 || big.ToLine != 180 || big.Source.State == "truncated" || esRunes(bigRaw) > SourceResultMaxRunes*8 ||
		strings.Contains(strings.Join(big.Notes, " "), "daraltıldı") {
		t.Fatalf("ölçek 8: pencere 60–180 daraltılmadan taşınmalı (%d–%d, state=%s, %d rune)", big.FromLine, big.ToLine, big.Source.State, esRunes(bigRaw))
	}
	// Hâlâ uzun: ölçekli tavana da sığmayan pencere daraltılır ve not söyler.
	r.out = scOK(scWindow(1, 400, 200, 300), 1, 400, 200)
	cut, cutRaw := call(WithEvidenceScale(context.Background(), 8))
	if esRunes(cutRaw) > SourceResultMaxRunes*8 || cut.Source.State != "truncated" || cut.FromLine > 200 || cut.ToLine < 200 ||
		!strings.Contains(strings.Join(cut.Notes, " "), fmt.Sprintf("%d–%d satırlarına daraltıldı", cut.FromLine, cut.ToLine)) {
		t.Fatalf("ölçek 8 tavanı: %d rune, %d–%d, notes=%v", esRunes(cutRaw), cut.FromLine, cut.ToLine, cut.Notes)
	}
}

// read_wiki_page: pencere büyür, sayfanın toplam tavanı aynı kalır.
func TestKanitOlcegiWikiPenceresi(t *testing.T) {
	rec := wiki.PageRecord{Project: "P", WikiName: "W", Path: "/p", Title: "p", URL: "https://devops.example.test/p",
		Content: strings.Repeat("a", WikiPageMaxRunes+5000)}
	if esJSON(t, WikiPageWindow(rec, 0)) != esJSON(t, WikiPageWindowScaled(rec, 0, 1)) ||
		esJSON(t, wikiPageWindowWithTableHint(rec, 0)) != esJSON(t, wikiPageWindowWithTableHintScaled(rec, 0, 1)) {
		t.Fatal("ölçek 1 penceresi bugünkünden farklı")
	}
	mid := wiki.PageRecord{Content: strings.Repeat("b", 30000)}
	if w := WikiPageWindow(mid, 0); w["next_offset"] != WikiWindowRunes {
		t.Fatalf("taban: 30000 karakterlik sayfa pencereyle kesilmeli (next=%v)", w["next_offset"])
	}
	w8 := WikiPageWindowScaled(mid, 0, 8)
	if _, more := w8["next_offset"]; more || len(w8["content"].(string)) != 30000 {
		t.Fatalf("ölçek 8: 30000 karakterlik sayfa tek pencerede (next=%v)", w8["next_offset"])
	}
	big := WikiPageWindowScaled(rec, 0, 8)
	if big["next_offset"] != WikiWindowRunes*8 || len(big["content"].(string)) != WikiWindowRunes*8 || big["truncated"] != true ||
		!strings.Contains(big["note"].(string), fmt.Sprintf("Sayfa %d karakter; yalnız ilk %d karakteri", WikiPageMaxRunes+5000, WikiPageMaxRunes)) {
		t.Fatalf("ölçek 8 tavanı: next=%v, not=%v", big["next_offset"], big["note"])
	}
	// Araç bütçesi api tavanının (6000 × ölçek) altında kalır.
	if n := esRunes(esJSON(t, big)); n > 6000*8 {
		t.Fatalf("ölçek 8 pencere zarfı %d rune — sohbet tavanını aşıyor", n)
	}
}

// get_problem / get_runbook / get_log_patterns metinleri.
func TestKanitOlcegiProblemRunbookDesen(t *testing.T) {
	p := chstore.Problem{ID: "p1", Service: "orders-api", Description: strings.Repeat("d", 3000)}
	if esJSON(t, problemView(p)) != esJSON(t, problemViewScaled(p, 1)) {
		t.Fatal("problem ölçek 1 farklı")
	}
	if n := esRunes(problemView(p)["description"].(string)); n != problemDescriptionMax+1 {
		t.Fatalf("taban açıklama %d rune", n)
	}
	if problemViewScaled(p, 8)["description"] != p.Description {
		t.Fatal("ölçek 8: 3000 rune'luk açıklama tam taşınmalı")
	}
	p.Description = strings.Repeat("d", 6000)
	if d := problemViewScaled(p, 8)["description"].(string); esRunes(d) != problemDescriptionMax*8+1 || !strings.HasSuffix(d, "…") {
		t.Fatalf("ölçek 8 tavanı: %d rune", esRunes(d))
	}

	rb := chstore.Runbook{ID: "rb1", Title: "t", Description: strings.Repeat("r", 20000),
		Steps: []chstore.RunbookStep{{Order: 1, Kind: "manual", Title: "s", Instructions: strings.Repeat("i", 3000), Expected: strings.Repeat("e", 6000)}}}
	if esJSON(t, runbookView(rb)) != esJSON(t, runbookViewScaled(rb, 1)) {
		t.Fatal("runbook ölçek 1 farklı")
	}
	v := runbookViewScaled(rb, 8)
	st := v["steps"].([]map[string]any)[0]
	if v["description"] != rb.Description || st["instructions"] != rb.Steps[0].Instructions || esRunes(st["expected"].(string)) != runbookStepMax*8+1 {
		t.Fatalf("runbook ölçek 8: gövde %d, adım %d, beklenen %d rune", esRunes(v["description"].(string)), esRunes(st["instructions"].(string)), esRunes(st["expected"].(string)))
	}

	hits := []anomaly.LogPatternAnomaly{{Pattern: "timeout", Kind: "spike", Service: "orders-api", Ratio: 4, Sample: strings.Repeat("ö", 1000)},
		{Pattern: "refused", Kind: "spike", Service: "orders-api", Ratio: 2, Sample: strings.Repeat("ü", 2000)}}
	b0, _ := filterLogPatterns(hits, "", "", nil, 10)
	b1, _ := filterLogPatternsScaled(hits, "", "", nil, 10, 1)
	if esJSON(t, b0) != esJSON(t, b1) || esRunes(b0[0].Sample) != logPatternSampleMax+1 {
		t.Fatal("log deseni ölçek 1 farklı")
	}
	b8, _ := filterLogPatternsScaled(hits, "", "", nil, 10, 8)
	if b8[0].Sample != hits[0].Sample || esRunes(b8[1].Sample) != logPatternSampleMax*8+1 {
		t.Fatalf("log deseni ölçek 8: %d / %d rune", esRunes(b8[0].Sample), esRunes(b8[1].Sample))
	}
}

// esCtxProbe — handler'a ulaşan ctx'in ölçeğini kaydeden log deposu.
type esCtxProbe struct {
	*stubLogStore
	scales []int
}

func (p *esCtxProbe) Search(ctx context.Context, f logstore.Filter) (*logstore.Page, error) {
	p.scales = append(p.scales, EvidenceScale(ctx))
	return p.stubLogStore.Search(ctx, f)
}

// Dış MCP sunucusu ölçek YAZMAZ: tel üzerindeki tools/call sonucu, ölçeksiz
// doğrudan çağrıyla bayt bayt aynıdır (bugünkü tavanlar).
func TestDisMCPSunucusuOlcekYazmaz(t *testing.T) {
	recs := []*logstore.LogRecord{{Timestamp: 1_700_000_000_000_000_000, Severity: 17, ServiceName: "orders-api", Body: strings.Repeat("ş", 3000),
		Attributes: map[string]string{"error.message": strings.Repeat("ğ", 1000)}}}
	probe := &esCtxProbe{stubLogStore: &stubLogStore{page: &logstore.Page{Logs: recs, Total: 1}}}
	d := Deps{LogStore: probe}
	srv := mcp.New("coremetry", "test")
	Register(srv, d)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/mcp", srv.HandleStreamable)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	args := `{"limit":5,"from_iso":"2026-09-26T10:00:00Z","to_iso":"2026-09-26T11:00:00Z"}`
	resp, err := http.Post(ts.URL+"/api/mcp", "application/json",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_logs","arguments":`+args+`}}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil || rpc.Result.IsError || len(rpc.Result.Content) == 0 {
		t.Fatalf("tools/call: %v %+v", err, rpc)
	}
	if len(probe.scales) != 1 || probe.scales[0] != 1 {
		t.Fatalf("dış MCP çağrısının ctx'i ölçek taşıyor: %v", probe.scales)
	}
	var wire, direct map[string]any
	if err := json.Unmarshal([]byte(rpc.Result.Content[0].Text), &wire); err != nil {
		t.Fatal(err)
	}
	out, err := toolByName(t, ToolList(d), "search_logs").Handler(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(esJSON(t, out)), &direct); err != nil {
		t.Fatal(err)
	}
	if esJSON(t, wire["logs"]) != esJSON(t, direct["logs"]) {
		t.Fatal("dış MCP satırları ölçeksiz doğrudan çağrıdan farklı")
	}
	row := wire["logs"].([]any)[0].(map[string]any)
	if esRunes(row["body"].(string)) != logBodyMaxRunes+1 || row["body_truncated"] != true ||
		esRunes(row["attrs"].(map[string]any)["error.message"].(string)) != logAttrValueMaxRunes+1 {
		t.Fatalf("dış MCP bugünkü tavanları uygulamalı: gövde %d rune", esRunes(row["body"].(string)))
	}
	// Protokol katmanı ölçeği hiç bilmez (kaynak pini).
	files, _ := filepath.Glob("../mcp/*.go")
	if len(files) == 0 {
		t.Fatal("internal/mcp kaynakları okunamadı")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "EvidenceScale") {
			t.Fatalf("%s kanıt ölçeğine dokunuyor — dış MCP yolu ölçeksiz kalmalı", f)
		}
	}
}

// Paket model adı ya da copilot bilmez (import döngüsü ve katman pini).
func TestKanitOlcegiPaketiModelBilmez(t *testing.T) {
	b, err := os.ReadFile("evidence_scale.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if i := strings.Index(src, "\nimport ("); i >= 0 {
		src = src[i:] // başlık yorumu hariç: yalnız kod
	}
	for _, bad := range []string{"internal/copilot", "modelcaps", "qwen", "gemma"} {
		if strings.Contains(strings.ToLower(src), bad) {
			t.Fatalf("evidence_scale.go %q içeriyor", bad)
		}
	}
}
