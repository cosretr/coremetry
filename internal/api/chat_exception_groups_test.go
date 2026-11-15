package api

// v0.10.1331 — CoSRE operasyon → Exceptions: paylaşılan okuma dikişi
// (chat_exception_groups.go) — sahte okuyucuyla toplama (yalnız Oracle,
// karışık, 5 tavanı, OKUNAMADI ≠ grup yok), saf render altını, ön-getirme
// metninin model tavanı altında kalması ve ACM biçim kapısı.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeExGroups struct {
	rows      []chstore.ExceptionGroup
	err       error
	gotFilter chstore.ExceptionGroupFilter
	sampled   []string
	samples   map[string][]chstore.ExceptionSample
}

func (f *fakeExGroups) ListExceptionGroups(_ context.Context, fl chstore.ExceptionGroupFilter) ([]chstore.ExceptionGroup, error) {
	f.gotFilter = fl
	return f.rows, f.err
}

func (f *fakeExGroups) GetExceptionGroupSamples(_ context.Context, fp string, _ int) (chstore.ExceptionSamples, error) {
	f.sampled = append(f.sampled, fp)
	return chstore.ExceptionSamples{Samples: f.samples[fp]}, nil
}

var exNow = time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)

const exOp = "LEND_PERSONAL_WEB_APPLICATIONTRX_EXPRESS_ORDER_PRE_VALIDATE_REST"

func exOraRow(code string, lastAgo time.Duration) chstore.ExceptionGroup {
	return chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("src1", code, exOp), Type: code, Message: exOp,
		Service: "acm-loan", State: "new", Occurrences: 12345,
		FirstSeen: exNow.Add(-26 * time.Hour).UnixNano(), LastSeen: exNow.Add(-lastAgo).UnixNano()}
}

func exHooks() exGroupHooks {
	return exGroupHooks{
		priority: func(g chstore.ExceptionGroup) (string, string) { return "P1", "Teknik hata patlaması" },
		oracleInfo: func(g chstore.ExceptionGroup) *chstore.OracleGroupInfo {
			return &chstore.OracleGroupInfo{Known: true, LastHour: 900, PrevHour: 200, ServiceCount: 2,
				Services: []chstore.OracleNamedNum{{Name: "acm-loan", Count: 800}, {Name: "mobile-bff", Count: 100}}}
		},
	}
}

func TestCollectExceptionGroupsOracleOnly(t *testing.T) {
	yes, no := true, false
	r := exOraRow("COR-30518", 4*time.Minute)
	fk := &fakeExGroups{rows: []chstore.ExceptionGroup{r}, samples: map[string][]chstore.ExceptionSample{r.Fingerprint: {
		{TraceID: "aaaa", Message: "Kredi ön onay\n  zaman aşımı", TraceInCoremetry: &no},
		{TraceID: "bbbb", TraceInCoremetry: &yes}, {TraceID: "bbbb"}, {TraceID: "cccc"}, {TraceID: "dddd"},
	}}}
	ev := collectExceptionGroups(context.Background(), fk, exScope{Messages: []string{exOp}, From: exNow.Add(-24 * time.Hour), To: exNow}, exHooks())
	if ev.Err != "" || len(ev.Groups) != 1 || ev.LookbackS != 86400 {
		t.Fatalf("%+v", ev)
	}
	f := fk.gotFilter
	if !reflect.DeepEqual(f.Messages, []string{exOp}) || f.Sort != "lastSeen" || f.Dir != "desc" || f.Limit != exGroupListLimit ||
		f.ActiveFromNs != exNow.Add(-24*time.Hour).UnixNano() || f.ActiveToNs != exNow.UnixNano() || f.Search != "" {
		t.Fatalf("süzgeç: %+v", f)
	}
	g := ev.Groups[0]
	if !g.Oracle || g.Code != "COR-30518" || g.Operation != exOp || g.Priority != "P1" || !g.HourKnown || g.LastHour != 900 ||
		!reflect.DeepEqual(g.Services, []string{"acm-loan", "mobile-bff"}) || g.Sample != "Kredi ön onay zaman aşımı" ||
		!reflect.DeepEqual(g.SampleTraceIDs, []string{"bbbb", "cccc"}) || g.Href != "/problems?exc="+strings.Replace(r.Fingerprint, ":", "%3A", 1) ||
		g.ExplainHref != g.Href+"&ai=exception" {
		t.Fatalf("satır: %+v", g)
	}
}

func TestCollectExceptionGroupsMixedCapAndErrors(t *testing.T) {
	var rows []chstore.ExceptionGroup
	for i := 0; i < exGroupListLimit; i++ {
		rows = append(rows, exOraRow(fmt.Sprintf("COR-%05d", i), time.Duration(i+1)*time.Minute))
	}
	span := chstore.ExceptionGroup{Fingerprint: "0123456789abcdef", Type: "java.net.SocketTimeoutException", Message: exOp,
		Service: "acm-gw", State: "acknowledged", Occurrences: 7, LastSeen: exNow.UnixNano()}
	rows[1] = span
	fk := &fakeExGroups{rows: rows}
	ev := collectExceptionGroups(context.Background(), fk, exScope{Messages: []string{exOp}, From: exNow.Add(-time.Hour), To: exNow}, exHooks())
	if len(ev.Groups) != exGroupRenderMax || ev.More != exGroupListLimit-exGroupRenderMax || !ev.MoreAtMin {
		t.Fatalf("tavan: n=%d more=%d atMin=%v", len(ev.Groups), ev.More, ev.MoreAtMin)
	}
	if len(fk.sampled) != exGroupSampleGroups {
		t.Fatalf("örnek okuması ≤%d grup: %v", exGroupSampleGroups, fk.sampled)
	}
	sg := ev.Groups[1]
	if sg.Oracle || sg.Code != "" || sg.HourKnown || !reflect.DeepEqual(sg.Services, []string{"acm-gw"}) {
		t.Fatalf("span grubu Oracle istatistiği almaz: %+v", sg)
	}
	if !strings.Contains(exGroupTrendTR(sg), "span grubu") {
		t.Fatalf("span trendi: %s", exGroupTrendTR(sg))
	}

	errEv := collectExceptionGroups(context.Background(), &fakeExGroups{err: errors.New("code: 159 timeout")}, exScope{Messages: []string{exOp}, From: exNow.Add(-time.Hour), To: exNow}, exHooks())
	emptyEv := collectExceptionGroups(context.Background(), &fakeExGroups{}, exScope{Messages: []string{exOp}, From: exNow.Add(-time.Hour), To: exNow}, exHooks())
	et, mt := renderExceptionGroupsTR("X operasyonu", errEv), renderExceptionGroupsTR("X operasyonu", emptyEv)
	if errEv.Err == "" || !strings.Contains(et, "OKUNAMADI") || strings.Contains(et, "Grup yok") {
		t.Fatalf("hata metni:\n%s", et)
	}
	if emptyEv.Err != "" || !strings.Contains(mt, "Grup yok") || strings.Contains(mt, "OKUNAMADI") {
		t.Fatalf("boş metni:\n%s", mt)
	}
	if ev := collectExceptionGroups(context.Background(), &fakeExGroups{}, exScope{}, exHooks()); ev.Err == "" {
		t.Fatal("kapsamsız okuma (tüm filo) yapılmamalı")
	}
}

func TestExGroupTrendTR(t *testing.T) {
	cases := []struct {
		last, prev uint64
		want       string
	}{
		{900, 200, "patlama (≥3×)"}, {300, 200, "sürekli akış"}, {50, 200, "azalıyor"},
		{40, 0, "yeni akış"}, {0, 30, "son saatte yok"}, {0, 0, "durmuş"},
	}
	for _, c := range cases {
		got := exGroupTrendTR(exGroupLine{Oracle: true, HourKnown: true, LastHour: c.last, PrevHour: c.prev})
		if !strings.Contains(got, c.want) {
			t.Errorf("%d/%d → %s, want %s", c.last, c.prev, got, c.want)
		}
	}
	if got := exGroupTrendTR(exGroupLine{Oracle: true}); !strings.Contains(got, "bilinmiyor") {
		t.Errorf("istatistiksiz: %s", got)
	}
}

func TestRenderExceptionGroupsTRGolden(t *testing.T) {
	ev := exceptionGroupsEvidence{LookbackS: 86400, at: exNow, More: 2, Groups: []exGroupLine{{
		Fingerprint: "ora:0123456789abcdef", Type: "COR-30518", Message: exOp, State: "new", Priority: "P1", PriorityReason: "Teknik hata patlaması: son 1 sa 900",
		Oracle: true, Code: "COR-30518", Operation: exOp, HourKnown: true, LastHour: 900, PrevHour: 200, Occurrences: 12345,
		FirstSeen: exNow.Add(-26 * time.Hour).UnixNano(), LastSeen: exNow.Add(-4 * time.Minute).UnixNano(),
		Services: []string{"acm-loan"}, ServiceCount: 2, SampleTraceIDs: []string{"4bf92f3577b34da6a3ce929d0e0e4736"},
		Sample: "Kredi ön onay zaman aşımı", Href: "/problems?exc=ora%3A0123456789abcdef",
	}, {Fingerprint: "0123456789abcdef", Type: "java.net.SocketTimeoutException", Message: "Read timed out", Service: "acm-gw", State: "resolved",
		Priority: "P3", PriorityReason: "düşük hacim", Occurrences: 7, LastSeen: exNow.Add(-2 * time.Hour).UnixNano(), Services: []string{"acm-gw"}, ServiceCount: 1,
		Href: "/problems?exc=0123456789abcdef"}, {Fingerprint: "ora:fedcba9876543210", Oracle: true, Code: "COR-1", Operation: exOp, State: "new",
		HourKnown: true, LastHour: 50, PrevHour: 60, Occurrences: 110, LastSeen: exNow.Add(-90 * time.Second).UnixNano(), Href: "/problems?exc=ora%3Afedcba9876543210"}}}
	want := "Exceptions teknik hata grupları — " + exOp + " operasyonu (son 1gün, en yeni önce):\n" +
		"1. COR-30518 · " + exOp + " · P1 (Teknik hata patlaması: son 1 sa 900) · durum new · son görülme 4dk önce (10-18 09:56 UTC) · ilk 10-17 08:00 UTC · toplam 12,345\n" +
		"   ne demek: örnek satır mesajı \"Kredi ön onay zaman aşımı\"\n" +
		"   nerede yoğunlaşıyor: başlıktaki operasyon/kod · servis acm-loan (toplam 2 servis)\n" +
		"   patlama mı sürekli akış mı: son 1 sa 900 · önceki 1 sa 200 → patlama (≥3×)\n" +
		"   örnek trace: 4bf92f3577b34da6a3ce929d0e0e4736\n" +
		"   Exceptions: /problems?exc=ora%3A0123456789abcdef\n" +
		"2. java.net.SocketTimeoutException · P3 (düşük hacim) · durum resolved · son görülme 2sa önce (10-18 08:00 UTC) · toplam 7\n" +
		"   ne demek: mesaj \"Read timed out\"\n" +
		"   nerede yoğunlaşıyor: başlıktaki operasyon/kod · servis acm-gw\n" +
		"   patlama mı sürekli akış mı: saatlik sayı yok (span grubu; ömür boyu toplam 7)\n" +
		"   Exceptions: /problems?exc=0123456789abcdef\n" +
		"3. COR-1 · " + exOp + " · durum new · son görülme 1dk önce (10-18 09:58 UTC) · toplam 110 · son 1 sa 50 · önceki 1 sa 60 → sürekli akış · Exceptions: /problems?exc=ora%3Afedcba9876543210\n" +
		"(+2 grup daha gösterilmedi — tümü Exceptions'ta)\n"
	if got := renderExceptionGroupsTR(exOp+" operasyonu", ev); got != want {
		t.Fatalf("altın metin farklı:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// TestOpPrefetchWithExceptionsUnderModelClamp — 5 azami grup + azami trace /
// Oracle bloğu ile bütün ön-getirme metni clampToolResultForModel tavanının
// ALTINDA (kırpılan sentetik araç sonucu son grubu sessizce keserdi).
func TestOpPrefetchWithExceptionsUnderModelClamp(t *testing.T) {
	long := func(n int, c string) string { return strings.Repeat(c, n) }
	p := &opPrefetch{Operation: exOp, MatchKind: "exact", Codes: []string{"SAMP01", "SAMP02", "SAMP03", "SAMP04", "SAMP05"}, RangeS: 3600,
		Services: []string{long(60, "s"), long(60, "t")}, From: exNow.Add(-time.Hour), To: exNow,
		Current: chstore.OpRED{Calls: 1 << 40, Errors: 1 << 39, P99Ms: 99999, Measured: true}, Previous: chstore.OpRED{Calls: 1, Measured: true}}
	for i := 0; i < opTraceLimit; i++ {
		p.Traces = append(p.Traces, opTraceRow{TraceID: long(32, "a"), Service: long(60, "x"), DurationMs: 99999, Error: true})
	}
	for i := 0; i < 10; i++ {
		p.Oracle = append(p.Oracle, chstore.OracleErrorCount{Code: long(20, "C"), Count: 1 << 30, Sample: long(400, "ş")})
	}
	ev := exceptionGroupsEvidence{LookbackS: 86400, at: exNow, More: 15, MoreAtMin: true}
	for i := 0; i < exGroupRenderMax; i++ {
		ev.Groups = append(ev.Groups, exGroupLine{Fingerprint: "ora:0123456789abcdef", Oracle: true, Code: long(200, "K"), Operation: long(200, "O"),
			State: "regressed", Priority: "P1", PriorityReason: long(300, "r"), HourKnown: true, LastHour: 1 << 40, PrevHour: 1 << 30, Occurrences: 1 << 50,
			FirstSeen: 1, LastSeen: exNow.UnixNano(), Services: []string{long(200, "a"), long(200, "b"), long(200, "c")}, ServiceCount: 999,
			Href: "/problems?exc=ora%3A0123456789abcdef"})
		if i < exGroupSampleGroups { // örnek yalnız ilk iki grupta okunur (collectExceptionGroups)
			ev.Groups[i].SampleTraceIDs = []string{long(32, "f"), long(32, "e")}
			ev.Groups[i].Sample = clipRunes(long(400, "ğ"), exGroupSampleRunes)
		}
	}
	p.Exceptions = &ev
	sum := p.summary()
	if n := utf8.RuneCountInString(sum); n >= chatToolResultMaxRunes {
		t.Fatalf("ön-getirme metni %d rune, tavan %d", n, chatToolResultMaxRunes)
	}
	if _, cut := clampToolResultForModel(sum); cut {
		t.Fatal("kırpıldı")
	}
	if !strings.Contains(sum, "\n5. ") || !strings.Contains(sum, "en az 15 grup daha") || strings.Count(sum, "Exceptions: /problems") != exGroupRenderMax {
		t.Fatalf("beşinci grup / kesilme notu yok:\n%s", sum)
	}
}

func TestOpWantsExceptions(t *testing.T) {
	cases := []struct {
		t    opTarget
		want bool
	}{
		{opTarget{Name: exOp, HasSpan: true}, true},
		{opTarget{Name: "ATM_PIN"}, true},
		{opTarget{Name: "GET /orders", HasSpan: true}, false},
		{opTarget{Name: "checkout", HasSpan: true}, false},
		{opTarget{Name: "lowercase_op_name", HasSpan: true}, false},
		{opTarget{Name: "weird op", HasOra: true}, true},
	}
	for _, c := range cases {
		if got := opWantsExceptions(c.t); got != c.want {
			t.Errorf("%+v → %v", c.t, got)
		}
	}
	var nilP *opPrefetch
	if nilP.exceptionsPromptTR() != "" || (&opPrefetch{Exceptions: &exceptionGroupsEvidence{}}).exceptionsPromptTR() != "" {
		t.Fatal("grup yokken prompt eki boş olmalı (bayt bayt eski)")
	}
	withG := &opPrefetch{Exceptions: &exceptionGroupsEvidence{Groups: []exGroupLine{{Type: "X"}}}}
	if !strings.Contains(withG.exceptionsPromptTR(), "EXCEPTIONS GRUPLARI") {
		t.Fatal("grup varken ek")
	}
}
