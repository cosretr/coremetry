package api

// Log Search Faz 6 dilim 6.3 — "Bu logları açıkla" kanıt paketinin saf
// parçaları + ortak deterministik katman (logs_ai_digest.go).

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogDigestPrevWindow(t *testing.T) {
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		from, to time.Time
		wantFrom time.Time
		wantZero bool
	}{
		{"15 dk", t0, t0.Add(15 * time.Minute), t0.Add(-15 * time.Minute), false},
		{"6 sa", t0, t0.Add(6 * time.Hour), t0.Add(-6 * time.Hour), false},
		{"ters", t0.Add(time.Hour), t0, time.Time{}, true},
		{"boş", time.Time{}, t0, time.Time{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, to := logDigestPrevWindow(c.from, c.to)
			if c.wantZero {
				if !f.IsZero() || !to.IsZero() {
					t.Fatalf("sıfır bekleniyordu: %v %v", f, to)
				}
				return
			}
			if !f.Equal(c.wantFrom) || !to.Equal(c.from) {
				t.Fatalf("got %v→%v, want %v→%v", f, to, c.wantFrom, c.from)
			}
		})
	}
}

func digestFixture() logDigest {
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	cur := &logstore.PatternsResult{Sampled: 500, Distinct: 2, Groups: []logstore.SignatureGroup{
		{Hash: "a", Template: "pool timeout <n>", Count: 180, Severity: 17, SeverityText: "ERROR",
			FirstSeen: t0.Add(6 * time.Minute).UnixNano(), LastSeen: t0.Add(14 * time.Minute).UnixNano(),
			Services: []string{"checkout"}, ServiceCount: 1, Sample: "pool timeout 30000"},
		{Hash: "b", Template: "order done <n>", Count: 320, Severity: 9, Services: []string{"checkout"}, ServiceCount: 1, Sample: "order done 12"},
	}}
	return logDigest{From: t0, To: t0.Add(15 * time.Minute), PrevFrom: t0.Add(-15 * time.Minute), PrevTo: t0,
		Cur: cur, PrevOK: true,
		New: []patternsCompareRow{{Template: "pool timeout <n>", Query: `"pool timeout"`, CountA: 180}},
	}
}

func TestRenderLogDigestTR(t *testing.T) {
	d := digestFixture()
	cases := []struct {
		name    string
		mut     func(*logDigest)
		want    []string
		notWant []string
	}{
		{"tam", func(*logDigest) {}, []string{"DESENLER (500 örnek satırdan 2 farklı desen", "pool timeout <n>", "örnekte ilk 10:06:00",
			"ÖNCEKİ PENCEREYLE FARK (09:45 → 10:00 UTC", "YENİ (önceki örneklemde yok):", "ARTAN: yok", "KAYBOLAN: yok"}, []string{"OKUNAMADI"}},
		{"okunamadı", func(d *logDigest) { d.Cur, d.CurErr = nil, errors.New("x") }, []string{"DESENLER: OKUNAMADI"}, []string{"ÖNCEKİ PENCERE"}},
		{"boş", func(d *logDigest) { d.Cur = &logstore.PatternsResult{} }, []string{"örneklenen satır yok"}, []string{"ÖNCEKİ PENCERE"}},
		{"önceki yok", func(d *logDigest) { d.PrevOK = false }, []string{"ÖNCEKİ PENCERE: okunamadı"}, []string{"YENİ ("}},
		{"tavan", func(d *logDigest) {
			c := *d.Cur
			c.Truncated, c.CoveredFromNs, c.CoveredToNs = true, d.From.Add(10*time.Minute).UnixNano(), d.To.UnixNano()
			d.Cur = &c
		}, []string{"Örnekleme tavanı doldu", "başlangıç daha önce olabilir"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dd := d
			c.mut(&dd)
			out := renderLogDigestTR(dd)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("eksik %q\n%s", w, out)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(out, w) {
					t.Errorf("olmamalı %q\n%s", w, out)
				}
			}
		})
	}
}

func TestCleanExplainLogsRows(t *testing.T) {
	long := strings.Repeat("ş", explainLogsBodyRunes+50)
	in := []explainLogsRow{
		{Body: "  ", Service: "x"},
		{Body: long, Service: "svc", Severity: "error", TraceID: "0AF7651916CD43DD8448EB211C80319C"},
		{Body: "b", TraceID: "not-a-trace"},
	}
	for i := 0; i < explainLogsMaxRows+2; i++ {
		in = append(in, explainLogsRow{Body: "r"})
	}
	out, dropped := cleanExplainLogsRows(in)
	if len(out) != explainLogsMaxRows || dropped != 4 {
		t.Fatalf("len=%d dropped=%d", len(out), dropped)
	}
	if got := len([]rune(out[0].Body)); got != explainLogsBodyRunes+1 {
		t.Errorf("gövde kesimi %d rune", got)
	}
	if out[0].TraceID != "0af7651916cd43dd8448eb211c80319c" || out[0].Severity != "ERROR" {
		t.Errorf("normalize: %+v", out[0])
	}
	if out[1].TraceID != "" {
		t.Errorf("geçersiz trace düşmeli: %q", out[1].TraceID)
	}
}

func TestExplainLogsLinks(t *testing.T) {
	rows := []explainLogsRow{
		{TraceID: "0af7651916cd43dd8448eb211c80319c"}, {TraceID: "0af7651916cd43dd8448eb211c80319c"},
		{TraceID: ""}, {TraceID: "11111111111111111111111111111111"},
	}
	tl := explainLogsTraceLinks(rows, 5)
	if len(tl) != 2 || tl[0].Href != "/trace?id=0af7651916cd43dd8448eb211c80319c" || tl[0].Label != "trace 0af76519" {
		t.Fatalf("trace links: %+v", tl)
	}
	if got := explainLogsTraceLinks(rows, 1); len(got) != 1 {
		t.Fatalf("tavan: %d", len(got))
	}
	d := digestFixture()
	d.Rising = []patternsCompareRow{{Template: "r", Query: "rq"}, {Template: "dup", Query: `"pool timeout"`}, {Template: "empty"}}
	pl := digestPatternLinks(d, 6)
	if len(pl) != 2 || pl[0].Kind != "new" || pl[1].Kind != "rising" || pl[1].Query != "rq" {
		t.Fatalf("pattern links: %+v", pl)
	}
	if got := digestPatternLinks(logDigest{}, 6); got == nil || len(got) != 0 {
		t.Fatalf("boş özet boş dizi döndürmeli: %#v", got)
	}
}

func TestRenderExplainLogsTR(t *testing.T) {
	d := digestFixture()
	f := logstore.Filter{Service: "checkout", Env: "prod", SeverityMin: 17}
	sel := []explainLogsRow{{TsNs: "1762941971204000000", Severity: "ERROR", Service: "checkout", Body: "boom", TraceID: "0af7651916cd43dd8448eb211c80319c"}}
	out := renderExplainLogsTR(f, d, sel, true, 3)
	for _, w := range []string{"KAPSAM: servis=checkout · env=prod · seviye≥17", "SEÇİLEN SATIRLAR (operatörün açtığı 1 satır",
		"trace=0af7651916cd43dd8448eb211c80319c: boom", "(+3 seçili satır daha"} {
		if !strings.Contains(out, w) {
			t.Errorf("eksik %q\n%s", w, out)
		}
	}
	rep := representativeRows(d, 6)
	if len(rep) != 2 || rep[0].Body != "pool timeout 30000" || rep[0].Service != "checkout" {
		t.Fatalf("temsili: %+v", rep)
	}
	if out := renderExplainLogsTR(logstore.Filter{}, d, rep, false, 0); !strings.Contains(out, "TEMSİLİ SATIRLAR (ilk 2 desenin") ||
		!strings.Contains(out, "KAPSAM: süzgeç yok") {
		t.Errorf("temsili paket:\n%s", out)
	}
	// Tavan: çok uzun satırlar paketi 12 KB'ı geçirmez.
	var many []explainLogsRow
	for i := 0; i < explainLogsMaxRows; i++ {
		many = append(many, explainLogsRow{Body: strings.Repeat("x", explainLogsBodyRunes)})
	}
	if out := renderExplainLogsTR(f, d, many, true, 0); len(out) > explainLogsInputMax+8 {
		t.Errorf("paket tavanı aşıldı: %d", len(out))
	}
	if rep := representativeRows(logDigest{CurErr: errors.New("x")}, 6); rep != nil {
		t.Errorf("okunamayan özetten temsili satır çıkmamalı")
	}
}
