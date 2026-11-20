package anomaly

// exception_evidence_scale_test.go — v0.10.2384: exception açıklama girdisinin
// kendi kanıt tavanları ölçeklenir. BuildExceptionExplainInput gerçek
// *chstore.Store ister; burada kurucunun KANIT AŞAMALARI (örnek trace + log,
// örnek trace logları, montaj) sahte okuyucularla, kurucuyla aynı sırayla
// koşar; kurucunun bu aşamalara bağı kaynak piniyle tutulur.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

type exScaleFix struct {
	g      *chstore.ExceptionGroup
	spans  []chstore.SpanRow
	stacks []string
	logs   func() *logstore.Page
	sample func(logstore.Filter) *logstore.Page
}

// exScaleBuild — BuildExceptionExplainInputScaled'ın kanıt aşamaları (aynı
// çağrılar, aynı sıra); legacy=true bu dilimden önceki sarmalayıcıları koşar.
func exScaleBuild(fx exScaleFix, scale int, legacy bool) (user string, st sampleTraceOut, tl, sl *fakeTraceLogs) {
	ctx := context.Background()
	ld := &fakeSpanLoader{spans: map[string][]chstore.SpanRow{teTrace: fx.spans}}
	tl, sl = &fakeTraceLogs{mk: fx.logs}, &fakeTraceLogs{mkF: fx.sample}
	refs := []sampleLogRef{slRef(slA, 0, sampleLogSrcCH), slRef(slB, 1, sampleLogSrcCH)}
	if legacy {
		st = sampleTraceEvidence(ctx, ld, tl, teTrace, fx.stacks)
		s := sampleTraceLogsEvidence(ctx, sl, refs, teTrace)
		return assembleExceptionPromptChain(fx.g, time.UTC, "", "", st.StackForPrompt, st.TraceBlock, st.LogsBlock+s.Block, "", ""), st, tl, sl
	}
	lim := exLimitsFor(scale)
	st = sampleTraceEvidenceLim(ctx, ld, tl, teTrace, fx.stacks, lim)
	s := sampleTraceLogsEvidenceLim(ctx, sl, refs, teTrace, lim)
	return assembleExceptionPromptChainN(fx.g, time.UTC, "", "", st.StackForPrompt, st.TraceBlock, st.LogsBlock+s.Block, "", "", lim.msgRunes), st, tl, sl
}

func exScaleFixture(stackRunes, msgRunes, spans, logN, logBodyRunes int) exScaleFix {
	sp := teSpans(spans, 3)
	for i := range sp {
		if sp[i].DBStatement != "" {
			sp[i].DBStatement = "SELECT " + strings.Repeat("c", 3000) + fmt.Sprintf(" FROM orders_%d", i%4)
		}
	}
	return exScaleFix{
		g: &chstore.ExceptionGroup{Fingerprint: "fp1", Type: "java.lang.IllegalStateException", Service: "svc-orders",
			Message:   strings.Repeat("m", msgRunes),
			FirstSeen: teBase, LastSeen: teBase + int64(time.Hour)},
		spans:  sp,
		stacks: []string{"", strings.Repeat("ş", stackRunes)},
		logs: func() *logstore.Page {
			var lgs []*logstore.LogRecord
			for i := 0; i < logN; i++ {
				lgs = append(lgs, &logstore.LogRecord{Severity: 17, SeverityText: "ERROR", ServiceName: "svc-orders",
					Body: fmt.Sprintf("%03d ", i) + strings.Repeat("g", logBodyRunes-4)})
			}
			return &logstore.Page{Logs: lgs, Total: len(lgs)}
		},
		sample: func(logstore.Filter) *logstore.Page {
			var lgs []*logstore.LogRecord
			for i := 0; i < 40; i++ {
				id := slA
				if i%2 == 1 {
					id = slB
				}
				lgs = append(lgs, &logstore.LogRecord{TraceID: id, Severity: 17, SeverityText: "ERROR", ServiceName: "svc-ledger",
					Timestamp: slT0.UnixNano() + int64(i), Body: fmt.Sprintf("%03d ", i) + strings.Repeat("ö", 996)})
			}
			return &logstore.Page{Logs: lgs}
		},
	}
}

// Ölçek 1: girdi bu dilimden önceki kurucuyla bayt bayt aynı; okuma boyları aynı.
func TestExceptionGirdisiOlcek1BaytAyni(t *testing.T) {
	for _, fx := range []exScaleFix{
		exScaleFixture(5000, 900, 90, 40, 900),
		exScaleFixture(200, 50, 4, 3, 40),
	} {
		legacy, lst, ltl, lsl := exScaleBuild(fx, 1, true)
		got, st, tl, sl := exScaleBuild(fx, 1, false)
		if got != legacy {
			t.Fatalf("ölçek 1 istemi farklı:\n got %d bayt\nwant %d bayt", len(got), len(legacy))
		}
		if st.StackRaw != lst.StackRaw || fmt.Sprint(st.Trace.DBStatements) != fmt.Sprint(lst.Trace.DBStatements) || fmt.Sprint(st.Trace.EvSpans) != fmt.Sprint(lst.Trace.EvSpans) {
			t.Fatal("ölçek 1 yapısal alanları farklı")
		}
		if tl.got.Limit != ltl.got.Limit || sl.got.Limit != lsl.got.Limit || sl.got.Limit != sampleLogFetch {
			t.Fatalf("ölçek 1 okuma boyları değişti: %d/%d", tl.got.Limit, sl.got.Limit)
		}
		if strings.Contains(got, "bütçe nedeniyle") {
			t.Fatal("ölçek 1'de yazılmayan satır notu olmamalı (bayt bayt eski)")
		}
	}
	if l := exLimitsFor(1); l != exLimitsFor(0) || l.stackRunes != 1800 || l.msgRunes != 400 || l.logFetch != traceLogsFetch ||
		l.logLines != traceLogsLines || l.logBodyRunes != 500 || l.logStackRunes != 900 || l.traceSpans != traceEvidenceMaxSpans ||
		l.sampleBodyRunes != sampleLogBodyRunes || l.logsBlockBytes != 0 || l.sampleBlockBytes != 0 {
		t.Fatalf("ölçek 1 tavanları bugünkü sabitler olmalı: %+v", l)
	}
	if exLimitsFor(99) != exLimitsFor(8) {
		t.Fatal("ölçek 8'de kelepçelenmeli")
	}
}

// Ölçek 8: eskiden kesilen stack / mesaj / span / log tam taşınır.
func TestExceptionGirdisiOlcek8TamKanit(t *testing.T) {
	fx := exScaleFixture(5000, 900, 90, 40, 900)
	base, bst, btl, bsl := exScaleBuild(fx, 1, false)
	big, st, tl, sl := exScaleBuild(fx, 8, false)
	if strings.Contains(base, fx.stacks[1]) || !strings.Contains(big, fx.stacks[1]) {
		t.Fatal("5000 rune'luk stack tabanda kesik, ölçek 8'de tam olmalı")
	}
	if strings.Contains(base, fx.g.Message) || !strings.Contains(big, fx.g.Message) {
		t.Fatal("900 rune'luk grup mesajı tabanda kesik, ölçek 8'de tam olmalı")
	}
	if len(bst.Trace.Compact) != traceEvidenceMaxSpans || len(st.Trace.Compact) != 90 {
		t.Fatalf("kompakt span: taban %d, ölçek 8 %d (90 beklenir)", len(bst.Trace.Compact), len(st.Trace.Compact))
	}
	if !strings.Contains(big, strings.Repeat("c", 3000)) || strings.Contains(base, strings.Repeat("c", 601)) {
		t.Fatal("3000 rune'luk SQL ifadesi tabanda 600'de kesik, ölçek 8'de tam olmalı")
	}
	// Yapısal alanlar ölçekten bağımsız.
	if fmt.Sprint(st.Trace.DBStatements) != fmt.Sprint(bst.Trace.DBStatements) || fmt.Sprint(st.Trace.EvSpans) != fmt.Sprint(bst.Trace.EvSpans) || st.StackRaw != bst.StackRaw {
		t.Fatal("DBStatements / EvSpans / ham stack ölçekle değişmemeli")
	}
	// Arka uç okuması: yalnız LIMIT büyür; pencere aynı.
	if btl.got.Limit != traceLogsFetch || tl.got.Limit != traceLogsFetch*8 || bsl.got.Limit != sampleLogFetch || sl.got.Limit != sampleLogFetch*8 {
		t.Fatalf("LIMIT: trace log %d→%d, örnek log %d→%d", btl.got.Limit, tl.got.Limit, bsl.got.Limit, sl.got.Limit)
	}
	if !tl.got.From.Equal(btl.got.From) || !tl.got.To.Equal(btl.got.To) || !sl.got.From.Equal(bsl.got.From) || !sl.got.To.Equal(bsl.got.To) {
		t.Fatal("log pencereleri ölçekle değişmemeli")
	}
	// Loglar: 40 satırın hepsi, 900 rune'luk gövde tam (taban 12 satır / 500 rune).
	if len(bst.Logs.Lines) != traceLogsLines || len(st.Logs.Lines) != 40 || utf8.RuneCountInString(st.Logs.Lines[0].Body) != 900 {
		t.Fatalf("trace logları: taban %d satır, ölçek 8 %d satır / %d rune", len(bst.Logs.Lines), len(st.Logs.Lines), utf8.RuneCountInString(st.Logs.Lines[0].Body))
	}
	if strings.Contains(big, "bütçe nedeniyle") {
		t.Fatal("bütçeye sığan kanıtta yazılmayan satır notu olmamalı")
	}
	// Örnek trace logları: trace başına 20 satırın hepsi, 1000 rune'luk gövde tam.
	if n := strings.Count(big, strings.Repeat("ö", 996)); n != 40 {
		t.Fatalf("örnek trace logları: 40 tam gövde beklenir, %d", n)
	}
	if n := strings.Count(base, strings.Repeat("ö", sampleLogBodyRunes-4)+"…"); n != 2*sampleLogPerTrace {
		t.Fatalf("taban örnek logları: %d kesik satır beklenir, %d", 2*sampleLogPerTrace, n)
	}
}

// Hâlâ uzun girdi: ölçekli tavanda kesilir ve kesim / yazılmayan satır söylenir.
func TestExceptionGirdisiOlcek8Tavani(t *testing.T) {
	fx := exScaleFixture(20000, 5000, 600, traceLogsFetch*8, 6000) // log deposu ölçekli LIMIT kadar satır döndürür
	big, st, _, _ := exScaleBuild(fx, 8, false)
	if !strings.Contains(big, strings.Repeat("ş", exStackPromptRunes*8)+"…") || strings.Contains(big, strings.Repeat("ş", exStackPromptRunes*8+1)) {
		t.Fatal("20000 rune'luk stack 14400'de kesilmeli ve … ile söylenmeli")
	}
	if !strings.Contains(big, strings.Repeat("m", exGroupMsgRunes*8)+"…") || strings.Contains(big, strings.Repeat("m", exGroupMsgRunes*8+1)) {
		t.Fatal("5000 rune'luk mesaj 3200'de kesilmeli")
	}
	if len(st.Trace.Compact) != traceEvidenceMaxSpans*8 {
		t.Fatalf("kompakt span tavanı %d olmalı, %d", traceEvidenceMaxSpans*8, len(st.Trace.Compact))
	}
	// Log bloğu: gövde 4000'de kesilir; toplam bayt bütçesi (24000 × 8) satır
	// sayısını sınırlar ve not okunan / verilen satırı gerçek sayılarla yazar.
	n := len(st.Logs.Lines)
	if utf8.RuneCountInString(st.Logs.Lines[0].Body) != exLogBodyRunes*8+1 || n >= traceLogsLines*8 || n == 0 {
		t.Fatalf("log satırı: %d satır, gövde %d rune", n, utf8.RuneCountInString(st.Logs.Lines[0].Body))
	}
	total := 0
	for _, l := range st.Logs.Lines {
		total += len(l.Body)
	}
	if total > exLogsBlockBytes*8+exLogBodyRunes*8*4 {
		t.Fatalf("log bloğu bütçeyi aştı: %d bayt", total)
	}
	if want := fmt.Sprintf(exLogsOmittedFmt, traceLogsFetch*8, n); !strings.Contains(big, want) {
		t.Fatalf("yazılmayan satır notu gerçek sayılarla olmalı: %q", want)
	}
}

// Kurucu bu aşamalara bağlı: ölçeksiz kurucu ölçek 1'i, ölçekli kurucu
// tavanları aşamalara ve montaja verir (tested-but-unreachable pini).
func TestExceptionKurucusuOlcegiAsamalaraVerir(t *testing.T) {
	b, err := os.ReadFile("exception_context.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Join(strings.Fields(string(b)), " ")
	for _, want := range []string{
		"return BuildExceptionExplainInputScaled(ctx, store, logs, g, loc, 1)",
		"lim := exLimitsFor(scale)",
		"st := sampleTraceEvidenceLim(ctx, store, logs, traceID, sampleStacks, lim)",
		"sl := sampleTraceLogsEvidenceLim(ctx, logs, spanSampleLogRefs(samples), sampleLogsExclude, lim)",
		"renderPodConcentration(pods), lim.msgRunes),",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("exception_context.go %q bağını kaybetti", want)
		}
	}
}
