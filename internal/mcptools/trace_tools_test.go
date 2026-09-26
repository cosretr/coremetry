package mcptools

// trace_tools_test.go — v0.10.944 get_trace yükseltmesi: kimlik doğrulama
// (bad_args, "geçersiz"), analiz (kritik yol TOPLANMAZ, öz süre aralık
// birleşimi, CPU/profiling notu), servis bağlamı (resource anahtar zinciri),
// liste tavanı + kaynak durumu.
// İnceleme turu: boş Distributed okumasında stub → TTL → tüm-replika yolu
// (replica_miss / aged_out / kurtarılan span'ler).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

const ttMs = int64(1_000_000)

func ttSpan(id, parent, svc, name string, startMs, endMs int64, status string) chstore.SpanRow {
	return chstore.SpanRow{
		TraceID: "t", SpanID: id, ParentSpanID: parent, ServiceName: svc, Name: name,
		StartTime: 1_790_000_000_000*ttMs + startMs*ttMs, EndTime: 1_790_000_000_000*ttMs + endMs*ttMs,
		DurationMs: float64(endMs - startMs), StatusCode: status,
	}
}

func TestTraceToolTraceIDValidation(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"0123456789ABCDEF0123456789abcdef", "0123456789abcdef0123456789abcdef", false},
		{"  0123456789abcdef0123456789abcdef ", "0123456789abcdef0123456789abcdef", false},
		{"", "", true},
		{"abc", "", true},
		{"0123456789abcdef0123456789abcdef0", "", true},
		{"0123456789abcdef0123456789abcdeg", "", true},
	}
	for _, c := range cases {
		got, err := traceToolTraceID(c.in)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%q: got %q err %v", c.in, got, err)
			continue
		}
		if err != nil {
			if cls := mcp.ClassifyToolError(err).Error; cls != mcp.ToolErrBadArgs {
				t.Errorf("%q: sınıf %s, istenen bad_args", c.in, cls)
			}
			if c.in != "" && !strings.Contains(err.Error(), "geçersiz") {
				t.Errorf("%q: hata 'geçersiz' demeli: %v", c.in, err)
			}
		}
	}
}

func TestGetTraceAnalyzedToolHandler(t *testing.T) {
	tool := getTraceAnalyzedTool(Deps{})
	if tool.Name != "get_trace" || tool.MinRole != "" {
		t.Fatalf("ad/rol: %s %q", tool.Name, tool.MinRole)
	}
	if _, ok := tool.InputSchema["properties"].(map[string]any)["env"]; ok {
		t.Error("get_trace env'siz kalır (kimlik-çapalı; tools_test envBlind listesi)")
	}
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"trace_id":"nope"}`)); err == nil {
		t.Fatal("geçersiz id store'a gitmeden reddedilmeli")
	}
	// Store yok → Go hatası değil, not_configured durumu (panik yok).
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"trace_id":"0123456789abcdef0123456789abcdef"}`))
	if err != nil {
		t.Fatal(err)
	}
	st := out.(traceToolEnvelope)["source"].(sourcestate.Status)
	if st.State != sourcestate.NotConfigured {
		t.Errorf("store yok: %+v", st)
	}
	n := len(tool.ShortDescription)
	if n < 60 || n > 400 || n >= len(tool.Description) {
		t.Errorf("kompakt açıklama boyu %d", n)
	}
	for _, want := range []string{"NEVER add span durations", "NOT CPU time", "wall_ms", "self_on_path_ms"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("açıklama %q sözleşmesini taşımalı", want)
		}
	}
}

// Kritik yol: iç içe zincirde adımların yoldaki süreleri kökün süresini
// PAYLAŞIR (toplamları = wall), toplam alanı YOK; öz süre aralık birleşimi.
func TestBuildTraceToolAnalysisNoSummedCriticalPath(t *testing.T) {
	spans := []chstore.SpanRow{
		ttSpan("a", "", "api", "GET /checkout", 0, 100, "ok"),
		ttSpan("b", "a", "orders", "POST /orders", 10, 90, "ok"),
		ttSpan("c", "b", "db", "SELECT", 20, 50, "ok"),
	}
	an := buildTraceToolAnalysis(spans, false)
	if an.WallMs != 100 || an.Root.SpanID != "a" {
		t.Fatalf("wall_ms kök süresi olmalı: %+v", an.Root)
	}
	got := map[string]float64{}
	for _, s := range an.CriticalPath {
		got[s.SpanID] = s.SelfOnPathMs
	}
	if got["a"] != 20 || got["b"] != 50 || got["c"] != 30 || len(an.CriticalPath) != 3 {
		t.Errorf("yoldaki süreler a=20 b=50 c=30 olmalı (eski toplam 210 yerine): %+v", an.CriticalPath)
	}
	b, _ := json.Marshal(an)
	for _, bad := range []string{"critical_ms", "critical_total", "criticalNs", "path_total"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("toplanmış kritik yol alanı sızdı: %s", bad)
		}
	}
	// paralel çocuklar: a[0,100] → b[10,50], c[20,60] → a'nın öz süresi 50 (birleşim)
	par := buildTraceToolAnalysis([]chstore.SpanRow{
		ttSpan("a", "", "api", "root", 0, 100, "ok"),
		ttSpan("b", "a", "x", "b", 10, 50, "ok"),
		ttSpan("c", "a", "y", "c", 20, 60, "ok"),
	}, false)
	for _, sv := range par.Services {
		if sv.Service == "api" && sv.SelfMs != 50 {
			t.Errorf("api öz süresi aralık birleşimiyle 50 ms olmalı, %v (naif toplam 20 verirdi)", sv.SelfMs)
		}
	}
	if par.TopSelfSpans[0].SpanID != "a" || par.TopSelfSpans[0].SelfMs != 50 {
		t.Errorf("en büyük öz span: %+v", par.TopSelfSpans)
	}
}

func TestBuildTraceToolAnalysisNotes(t *testing.T) {
	an := buildTraceToolAnalysis([]chstore.SpanRow{
		ttSpan("p", "", "producer", "send", 0, 50, "ok"),
		ttSpan("c", "p", "consumer", "consume", 40, 400, "ok"), // ebeveyni aşar → async kuyruk
		ttSpan("o", "missing", "svc", "orphan", 10, 20, "ok"),  // yetim
	}, true)
	if an.Notes[0] != traceToolNoteCPU {
		t.Errorf("CPU/profiling notu ilk ve birebir olmalı: %q", an.Notes[0])
	}
	joined := strings.Join(an.Notes, "\n")
	for _, want := range []string{"TOPLANMAZ", "ARALIK BİRLEŞİMİ", "yetim", "extent_ms", "tavanına takıldı"} {
		if !strings.Contains(joined, want) {
			t.Errorf("not eksik %q:\n%s", want, joined)
		}
	}
	// kök p (en uzun kök; yetim o daha kısa): wall 50, kapsam async kuyrukla 400
	if an.WallMs != 50 || an.ExtentMs != 400 || an.OrphanCount != 1 {
		t.Errorf("wall/extent/yetim: %v %v %d", an.WallMs, an.ExtentMs, an.OrphanCount)
	}
	if !strings.HasSuffix(an.StartISO, "Z") || an.StartUnixNs == 0 {
		t.Errorf("zaman UTC ISO + unix ns: %s %d", an.StartISO, an.StartUnixNs)
	}
}

func TestTraceToolContextUsesColumnKeyChains(t *testing.T) {
	a := ttSpan("a", "", "checkout", "GET /cart", 0, 100, "ok")
	a.ResourceAttributes = map[string]string{
		"deployment.environment": "uat", "k8s.cluster.name": "cluster-a", "k8s.namespace.name": "shop",
		"k8s.pod.name": "checkout-7d9-abc", "service.version": "2.1.0", "container.image.tag": "release.20260926.1",
	}
	b := ttSpan("b", "a", "checkout", "SELECT", 10, 20, "ok")
	b.ResourceAttributes = map[string]string{"deployment.environment.name": "uat", "k8s.namespace.name": "shop"}
	b.HostName = "checkout-7d9-xyz" // k8s.pod.name yoksa host_name (k8s_pod kolonunun yedeği)
	b.Attributes = map[string]string{"cluster": "cluster-a"}
	c := ttSpan("c", "a", "payments", "charge", 30, 60, "error")
	c.StatusMessage = strings.Repeat("ö", 400)
	an := buildTraceToolAnalysis([]chstore.SpanRow{a, b, c}, false)
	var ck *traceToolContext
	for i := range an.Context {
		if an.Context[i].Service == "checkout" {
			ck = &an.Context[i]
		}
	}
	if ck == nil {
		t.Fatalf("checkout bağlamı yok: %+v", an.Context)
	}
	if ck.Env != "uat" || ck.Cluster != "cluster-a" || ck.Namespace != "shop" || ck.PodsTotal != 2 {
		t.Errorf("bağlam: %+v", ck)
	}
	if len(ck.Versions) != 1 || ck.Versions[0] != "release.20260926.1" {
		t.Errorf("sürüm zinciri image tag önce: %v", ck.Versions)
	}
	if an.ErrorSpanCount != 1 || len([]rune(an.ErrorSpans[0].StatusMessage)) != traceToolStatusRunes+1 {
		t.Errorf("hata mesajı tavanlı (…): %d rune", len([]rune(an.ErrorSpans[0].StatusMessage)))
	}
}

func TestTraceToolErrorSpansEarliestFirstAndCapped(t *testing.T) {
	spans := []chstore.SpanRow{ttSpan("root", "", "api", "root", 0, 1000, "ok")}
	for i := 14; i >= 0; i-- {
		spans = append(spans, ttSpan(fmt.Sprintf("e%02d", i), "root", "svc", "op", int64(10+i), int64(20+i), "error"))
	}
	an := buildTraceToolAnalysis(spans, false)
	if an.ErrorSpanCount != 15 || len(an.ErrorSpans) != traceToolErrorSpans || an.ErrorSpans[0].SpanID != "e00" {
		t.Errorf("hata span'leri: count=%d listed=%d first=%s", an.ErrorSpanCount, len(an.ErrorSpans), an.ErrorSpans[0].SpanID)
	}
}

func TestTrimPathStepsKeepsPathOrder(t *testing.T) {
	var steps []traceToolPathStep
	for i := 0; i < 30; i++ {
		steps = append(steps, traceToolPathStep{SpanID: fmt.Sprintf("s%02d", i), SelfOnPathMs: float64(i % 7)})
	}
	got := ttTrimPathSteps(steps, 5)
	if len(got) != 5 {
		t.Fatalf("kırpma: %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].SpanID >= got[i].SpanID {
			t.Errorf("yol sırası bozuldu: %v", got)
		}
	}
	for _, s := range got {
		if s.SelfOnPathMs < 5 {
			t.Errorf("en uzun adımlar kalmalı: %+v", got)
		}
	}
}

func TestTraceToolPayloadSourceStates(t *testing.T) {
	empty := traceToolPayload("t", nil)
	if st := empty["source"].(sourcestate.Status); st.State != sourcestate.Empty || len(st.Notes) == 0 || !strings.Contains(st.Notes[0], "'hata yok' demek DEĞİLDİR") {
		t.Errorf("boş trace: %+v", st)
	}
	if _, ok := empty["analysis"]; ok {
		t.Error("boş trace'e analiz eklenmemeli")
	}
	spans := []chstore.SpanRow{ttSpan("root", "", "api", "root", 0, 5000, "ok")}
	for i := 1; i < 250; i++ {
		spans = append(spans, ttSpan(fmt.Sprintf("s%03d", i), "root", "svc", "op", int64(i), int64(i+10), "ok"))
	}
	p := traceToolPayload("t", spans)
	st := p["source"].(sourcestate.Status)
	if st.State != sourcestate.Truncated || st.Returned != getTraceSpanCap || st.FromISO == "" {
		t.Errorf("200 tavanı: %+v", st)
	}
	an := p["analysis"].(traceToolAnalysis)
	if an.SpanCount != 250 || p["span_count"].(int) != getTraceSpanCap || !p["truncated"].(bool) {
		t.Errorf("analiz tüm span'ler üzerinden, liste tavanlı: analysis=%d listed=%v", an.SpanCount, p["span_count"])
	}
	small := traceToolPayload("t", spans[:3])
	if st := small["source"].(sourcestate.Status); st.State != sourcestate.OK {
		t.Errorf("küçük trace ok: %+v", st)
	}
}

// v0.10.944 — boş Distributed okuması "yok" demeden önce REST ucunun yolunu
// izler (trace_routes.go): stub → TTL → tüm replikalar. Dört sonuç, saf seam.
func TestTraceToolMissPayload(t *testing.T) {
	stub := chstore.TraceAggregateStub{
		RootService: "checkout", RootName: "GET /cart", SpanCount: 42, ErrorCount: 3,
		StartTimeNs: 1_790_000_000_000 * ttMs, EndTimeNs: 1_790_000_000_000*ttMs + 1500*ttMs,
	}
	recovered := []chstore.SpanRow{ttSpan("root", "", "checkout", "GET /cart", 0, 1500, "ok"), ttSpan("c", "root", "payments", "charge", 10, 900, "error")}
	cases := []struct {
		name       string
		agedOut    bool
		rows       []chstore.SpanRow
		rerr       error
		state      sourcestate.State
		reason     string
		readPath   string
		noteHas    []string
		replicaCls sourcestate.State
	}{
		{"replikalardan kurtarıldı", false, recovered, nil, sourcestate.OK, "", "clickhouse_all_replicas",
			[]string{"replika ıraksaması", "clusterAllReplicas", "Replika tutarlılığı"}, ""},
		{"TTL içinde, span yok (tek düğüm nil,nil)", false, nil, nil, sourcestate.Partial, "replica_miss", "",
			[]string{"replica_miss: trace özeti MV'de var (42 span, 3 hata, kök checkout/GET /cart, başlangıç ", "saklama/örnekleme DEĞİL", "tek düğümlü"}, ""},
		{"TTL içinde, yedek zaman aşımı", false, nil, fmt.Errorf("trace all-replica read: %w", context.DeadlineExceeded), sourcestate.Partial, "replica_miss", "",
			[]string{"replica_miss", "yedek okuması başarısız (timeout)"}, sourcestate.Timeout},
		{"TTL dışında", true, nil, nil, sourcestate.Empty, "aged_out", "",
			[]string{"saklama süresi (TTL) dışında", "42 span, 3 hata", "'hata yok' demek DEĞİLDİR"}, ""},
	}
	for _, c := range cases {
		out := traceToolMissPayload("0123456789abcdef0123456789abcdef", stub, c.agedOut, c.rows, c.rerr)
		st := out["source"].(sourcestate.Status)
		if st.State != c.state {
			t.Errorf("%s: durum %s, istenen %s (%+v)", c.name, st.State, c.state, st)
		}
		notes := strings.Join(st.Notes, "\n")
		for _, w := range c.noteHas {
			if !strings.Contains(notes, w) {
				t.Errorf("%s: not %q eksik:\n%s", c.name, w, notes)
			}
		}
		if got, _ := out["stub_reason"].(string); got != c.reason {
			t.Errorf("%s: stub_reason %q, istenen %q", c.name, got, c.reason)
		}
		if got, _ := out["read_path"].(string); got != c.readPath {
			t.Errorf("%s: read_path %q", c.name, got)
		}
		if got, _ := out["replica_read_state"].(sourcestate.State); got != c.replicaCls {
			t.Errorf("%s: replica_read_state %q", c.name, got)
		}
		if c.reason != "" {
			sj, ok := out["stub"].(map[string]any)
			if !ok || sj["span_count"] != uint64(42) || sj["root_service"] != "checkout" || sj["start_unix_ns"] != stub.StartTimeNs ||
				!strings.HasSuffix(sj["end_iso"].(string), "Z") {
				t.Errorf("%s: stub nesnesi: %+v", c.name, out["stub"])
			}
			if st.FromISO == "" || st.ToISO == "" {
				t.Errorf("%s: kaynak stub penceresini taşımalı: %+v", c.name, st)
			}
			if _, has := out["analysis"]; has {
				t.Errorf("%s: span'siz zarfa analiz eklenmemeli", c.name)
			}
		} else {
			if _, has := out["analysis"]; !has || out["span_count"].(int) != 2 {
				t.Errorf("%s: kurtarılan span'ler normal zarfla (analiz dahil) dönmeli: %v", c.name, out["span_count"])
			}
			if _, has := out["stub_reason"]; has {
				t.Errorf("%s: kurtarılan trace'te stub_reason olmamalı", c.name)
			}
		}
		if _, err := json.Marshal(out); err != nil {
			t.Errorf("%s: JSON: %v", c.name, err)
		}
	}
	// Özet de yoksa bugünkü boş zarf + "özette de yok" notu.
	nf := traceToolNotFoundPayload("0123456789abcdef0123456789abcdef")
	if st := nf["source"].(sourcestate.Status); st.State != sourcestate.Empty || !strings.Contains(strings.Join(st.Notes, ";"), "trace_summary_5m özetinde de yok") {
		t.Errorf("özetsiz boş trace: %+v", st)
	}
}

// Açıklama yeni durumları sözleşme olarak taşır.
func TestGetTraceDescriptionDeclaresReplicaMiss(t *testing.T) {
	d := getTraceAnalyzedTool(Deps{}).Description
	for _, want := range []string{"trace_summary_5m", "replica_miss", "aged_out", "clickhouse_all_replicas", "no Tempo fallback"} {
		if !strings.Contains(d, want) {
			t.Errorf("açıklama %q içermeli", want)
		}
	}
}

// TestTraceToolPayloadStatusBeforeBulk — v0.10.944: sohbet araç sonucunu
// 6000 rune'da kırpar (clampToolResultForModel). Büyük bir trace'te `source`,
// `truncated` ve dürüstlük notları (yetim span / eksik ağaç) kırpmanın çok
// içinde, ham span listesi en sonda olmalı. TestQueryMetricStatusBeforeBulk
// deseni. Context de services ile aynı tavanda.
func TestTraceToolPayloadStatusBeforeBulk(t *testing.T) {
	spans := []chstore.SpanRow{ttSpan("root", "", "svc-00", "GET /checkout", 0, 5000, "ok")}
	msg := strings.Repeat("upstream connect error or disconnect/reset before headers ", 6)[:300]
	for i := 1; i < 249; i++ {
		st := "ok"
		if i <= 10 {
			st = "error"
		}
		sp := ttSpan(fmt.Sprintf("s%04d", i), "root", fmt.Sprintf("svc-%02d", i%25), fmt.Sprintf("op-%03d", i), int64(i), int64(i+20), st)
		if st == "error" {
			sp.StatusMessage = msg
		}
		sp.ResourceAttributes = map[string]string{"k8s.pod.name": fmt.Sprintf("pod-%02d-a", i%25), "deployment.environment": "uat"}
		spans = append(spans, sp)
	}
	spans = append(spans, ttSpan("orphan1", "missing-parent", "svc-07", "late", 30, 60, "ok"))
	p := traceToolPayload("0123456789abcdef0123456789abcdef", spans)
	an := p["analysis"].(traceToolAnalysis)
	if len(an.Context) > traceToolServices || !an.ServicesTruncated {
		t.Errorf("context tavanı: %d satır, truncated=%v", len(an.Context), an.ServicesTruncated)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if len(s) < 20000 {
		t.Fatalf("test sonucu yeterince iri değil (%d B)", len(s))
	}
	if i := strings.Index(s, `"source":`); i < 0 || i >= 1024 {
		t.Fatalf(`"source" %d. baytta (≥1024): %.200s`, i, s)
	}
	for _, w := range []string{`"truncated":true`, "yetim span", traceToolNoteCPU} {
		if i := strings.Index(s, w); i < 0 || i >= 4096 {
			t.Errorf("%q %d. baytta (≥4096; model kırpması 6000 rune)", w, i)
		}
	}
	ia, is := strings.Index(s, `"analysis":`), strings.Index(s, `"spans":`)
	if ia < 0 || is < ia {
		t.Errorf(`"spans" (%d) "analysis"tan (%d) SONRA gelmeli`, is, ia)
	}
	// Tur-dönüşü: sıralı zarf geçerli JSON ve aynı anahtarları taşır.
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil || len(back) != len(p) {
		t.Fatalf("sıralı zarf geçersiz: %v (%d/%d anahtar)", err, len(back), len(p))
	}
}

// TestTraceToolStubUnreadablePayload — v0.10.944: trace_summary_5m özet
// okuması BAŞARISIZ → hata sınıfı + stub_reason=stub_unreadable; "hiç
// ulaşmamış" (yokluk) DENMEZ.
func TestTraceToolStubUnreadablePayload(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		err  error
		want sourcestate.State
	}{
		{"bütçe", fmt.Errorf("stub: %w", context.DeadlineExceeded), sourcestate.Timeout},
		{"CH zaman aşımı", fmt.Errorf("code: 159, DB::Exception: Timeout exceeded: elapsed 5.1 seconds"), sourcestate.Timeout},
		{"bağlantı", fmt.Errorf("dial tcp 10.0.0.9:9000: connect: connection refused"), sourcestate.Unreachable},
	}
	for _, c := range cases {
		out := traceToolStubUnreadablePayload(id, c.err)
		st := out["source"].(sourcestate.Status)
		notes := strings.Join(st.Notes, "\n")
		if st.State == sourcestate.Empty || st.State != c.want {
			t.Errorf("%s: durum %q, istenen %q", c.name, st.State, c.want)
		}
		if strings.Contains(notes, "hiç ulaşmamış") || !strings.Contains(notes, "yokluk kanıtı DEĞİL") {
			t.Errorf("%s: not yokluk iddia etmemeli:\n%s", c.name, notes)
		}
		if out["stub_reason"] != "stub_unreadable" || out["trace_id"] != id {
			t.Errorf("%s: stub_reason=%v trace_id=%v", c.name, out["stub_reason"], out["trace_id"])
		}
		if b, err := json.Marshal(out); err != nil || !strings.HasPrefix(string(b), `{"source":`) {
			t.Errorf("%s: zarf source ile başlamalı: %v %.80s", c.name, err, b)
		}
	}
	if d := getTraceAnalyzedTool(Deps{}).Description; !strings.Contains(d, "stub_unreadable") {
		t.Error("açıklama stub_unreadable sözleşmesini taşımalı")
	}
}
