package api

// v0.10.992 — dış skill denetimi V1 dilim 1: CoSRE kök-neden demetinin
// BubbleUp adımı. Kıyas şekli + pencere (planGuidedBubbleUp) ve prompt metni
// (renderBubbleUpTR) saf ve tablo testli; demetin adımı çağırdığı ve kıyasın
// tek yerde kurulduğu kaynak pinli (s.store somut tip — okuma sahtelenemez).

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rca"
)

func TestPlanGuidedBubbleUp(t *testing.T) {
	to := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	prob := func(metric string, ago time.Duration) chstore.Problem {
		return chstore.Problem{ID: "p", Metric: metric, StartedAt: to.Add(-ago).UnixNano()}
	}
	cases := []struct {
		name      string
		probs     []chstore.Problem
		curErrors uint64
		wantErr   bool
		wantStart time.Time
		anchored  bool
	}{
		{"problem (hata) 20 dk önce → hata alt kümesi, açılışı izleyen 10 dk",
			[]chstore.Problem{prob("error_rate", 20*time.Minute)}, 0, true, to.Add(-20 * time.Minute), true},
		{"problem (gecikme) 3 sa önce → zaman kıyası, yine AÇILIŞIN 10 dakikası (son 10 dk değil)",
			[]chstore.Problem{prob("p99_ms", 3*time.Hour)}, 500, false, to.Add(-3 * time.Hour), true},
		{"problem 2 dk önce → pencere geleceğe taşabilir, boyu yine 10 dk",
			[]chstore.Problem{prob("error_rate", 2*time.Minute)}, 0, true, to.Add(-2 * time.Minute), true},
		{"ilk problem çıpadır (öncelik sıralı liste)",
			[]chstore.Problem{prob("p95_ms", 30*time.Minute), prob("error_rate", 5*time.Minute)}, 9, false, to.Add(-30 * time.Minute), true},
		{"problem yok, hata var → son 10 dk, hata alt kümesi", nil, 12, true, to.Add(-10 * time.Minute), false},
		{"problem yok, hata yok → son 10 dk, zaman kıyası", nil, 0, false, to.Add(-10 * time.Minute), false},
	}
	for _, c := range cases {
		p := planGuidedBubbleUp(c.probs, c.curErrors, to)
		if p.ErrorFamily != c.wantErr || !p.Started.Equal(c.wantStart) || p.Anchored != c.anchored {
			t.Errorf("%s: %+v", c.name, p)
		}
		// Maliyet sözleşmesi: ham spans taraması HEP 10 dakika (rca.ExtrasWindow).
		if d := p.End.Sub(p.Started); d != rca.ExtrasWindow {
			t.Errorf("%s: pencere 10 dk olmalı: %s", c.name, d)
		}
	}
	if rca.ExtrasWindow != 10*time.Minute {
		t.Errorf("BubbleUp penceresi 10 dk kalmalı (maliyet tavanı): %s", rca.ExtrasWindow)
	}
	if (guidedBubbleUpPlan{ErrorFamily: true}).compare() != "errors_vs_all" || (guidedBubbleUpPlan{}).compare() != "window_vs_previous" {
		t.Error("çip argümanı kıyas şeklini söylemeli")
	}
}

// buTestVal — chstore'un gerçek birimi: pay = sayım / toplam (oran).
func buTestVal(value string, sel, selTotal, base, baseTotal int64) chstore.BubbleUpValue {
	sp, bp := float64(sel)/float64(selTotal), float64(base)/float64(baseTotal)
	return chstore.BubbleUpValue{Value: value, SelectionCount: sel, BaselineCount: base, SelectionPct: sp, BaselinePct: bp, Score: sp - bp}
}

func TestRenderBubbleUpTR(t *testing.T) {
	to := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	errPlan := guidedBubbleUpPlan{ErrorFamily: true, Started: to.Add(-10 * time.Minute), End: to}
	shiftPlan := guidedBubbleUpPlan{Started: to.Add(-40 * time.Minute), End: to.Add(-30 * time.Minute), Anchored: true}
	diverging := &chstore.BubbleUpResult{SelectionTotal: 40, BaselineTotal: 900, Attributes: []chstore.BubbleUpAttribute{
		{Key: "http.route", Values: []chstore.BubbleUpValue{buTestVal("/v1/pay-now", 32, 40, 99, 900)}},
		{Key: "k8s.pod.name", Values: []chstore.BubbleUpValue{buTestVal("api-gw-7f", 24, 40, 270, 900)}},
		{Key: "noise", Values: []chstore.BubbleUpValue{buTestVal("x", 5, 40, 90, 900)}}, // 2,5 puan → eşiğin altında
		{Key: "service.version", Values: []chstore.BubbleUpValue{buTestVal("2.4.1", 20, 40, 90, 900)}},
		{Key: "fourth", Values: []chstore.BubbleUpValue{buTestVal("y", 20, 40, 90, 900)}}, // tavan 3
	}}
	flat := &chstore.BubbleUpResult{SelectionTotal: 40, BaselineTotal: 900, Attributes: []chstore.BubbleUpAttribute{
		{Key: "pod", Values: []chstore.BubbleUpValue{buTestVal("a", 4, 40, 90, 900)}},
	}}
	long := &chstore.BubbleUpResult{SelectionTotal: 10, BaselineTotal: 100, Attributes: []chstore.BubbleUpAttribute{
		{Key: "db.statement", Values: []chstore.BubbleUpValue{buTestVal(strings.Repeat("ş", 200), 9, 10, 10, 100)}},
	}}
	cases := []struct {
		name    string
		bu      *chstore.BubbleUpResult
		plan    guidedBubbleUpPlan
		err     error
		want    []string
		mustNot []string
	}{
		{"hata ailesi: yüzdeler 0–100, tavan 3, eşik altı ve dördüncü yok", diverging, errPlan, nil,
			[]string{
				"hatalı span'ler aynı penceredeki TÜM span'lerle kıyaslandı; son 10 dk, hatalı 40 / tüm 900 span",
				"  - http.route=/v1/pay-now: hatalı kümede %80, tüm span'lerde %11\n",
				"  - k8s.pod.name=api-gw-7f: hatalı kümede %60, tüm span'lerde %30\n",
				"  - service.version=2.4.1: hatalı kümede %50, tüm span'lerde %10\n",
				"tek başına sebep kanıtı değildir",
			},
			[]string{"noise", "fourth", "%1,", "OKUNAMADI", "YOK"}},
		{"zaman kıyası: sözcükler pencereye göre", diverging, shiftPlan, nil,
			[]string{"bu pencere ÖNCEKİ eş-boy pencereyle kıyaslandı; problemin açılışını izleyen 10 dk, bu pencere 40 / önceki 900 span", "bu pencerede %80, önceki pencerede %11"},
			[]string{"hatalı kümede"}},
		{"ayrışma yok → açıkça YOK (boş blok değil)", flat, errPlan, nil,
			[]string{"belirgin ayrışma YOK", "tek bir rota / pod / sürüme özgü görünmüyor"}, []string{"  - ", "OKUNAMADI"}},
		{"hatalı span yok → kıyas kurulamadı", &chstore.BubbleUpResult{BaselineTotal: 900}, errPlan, nil,
			[]string{"bu pencerede (son 10 dk) hatalı span yok — kıyas kurulamadı"}, []string{"YOK (", "OKUNAMADI"}},
		{"zaman kıyasında pencere boş", &chstore.BubbleUpResult{BaselineTotal: 900}, shiftPlan, nil,
			[]string{"bu pencerede (problemin açılışını izleyen 10 dk) span yok"}, []string{"hatalı span yok"}},
		{"önceki pencere boş", &chstore.BubbleUpResult{SelectionTotal: 40}, shiftPlan, nil,
			[]string{"önceki pencerede span yok — kıyas kurulamadı"}, nil},
		{"zaman aşımı → OKUNAMADI (timeout), 'yok' DEĞİL", nil, errPlan, context.DeadlineExceeded,
			[]string{"OKUNAMADI (timeout)", "BİLİNMİYOR"}, []string{"ayrışma YOK", "span yok"}},
		{"erişilemedi", nil, errPlan, errors.New("dial tcp 10.0.0.9:9000: connect: connection refused"),
			[]string{"OKUNAMADI (unreachable)"}, []string{"ayrışma YOK"}},
		{"uzun değer kırpılır, UTF-8 bozulmaz", long, errPlan, nil,
			[]string{"db.statement=", "…: hatalı kümede %90"}, []string{strings.Repeat("ş", 60)}},
	}
	for _, c := range cases {
		got := renderBubbleUpTR(c.bu, c.plan, c.err)
		if !strings.HasPrefix(got, "Ayrışan boyutlar (BubbleUp)") || !strings.HasSuffix(got, "\n") {
			t.Errorf("%s: başlık / satır sonu: %q", c.name, got)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q içermeli:\n%s", c.name, w, got)
			}
		}
		for _, w := range c.mustNot {
			if strings.Contains(got, w) {
				t.Errorf("%s: %q içermemeli:\n%s", c.name, w, got)
			}
		}
		if strings.ContainsRune(got, '�') {
			t.Errorf("%s: bozuk UTF-8", c.name)
		}
	}
}

// Kaynak pini: demet adımı RED'den sonra, deploy'dan önce çağırır; BubbleUp
// kıyası api paketinde TEK yerde kurulur (serviceBubbleUp) — /rootcause'un iki
// fan-out'u ve katalog toplayıcı aynı yardımcıyı kullanır.
func TestBubbleUpSingleChokePoint(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	guided := read("copilot_guided.go")
	i := strings.Index(guided, "func (s *Server) guidedRootCauseBundle(")
	fn := guided[i : i+strings.Index(guided[i:], "\nfunc ")]
	step := strings.Index(fn, "s.guidedBubbleUpStep(ctx, emit, &b, service, probs, cx, to)")
	ctxStep, dep := strings.Index(fn, `emitGuidedStep(emit, "service_context"`), strings.Index(fn, `emitGuidedStep(emit, "recent_deploys"`)
	if step < 0 || ctxStep < 0 || dep < 0 || !(ctxStep < step && step < dep) {
		t.Fatalf("BubbleUp adımı service_context ile recent_deploys arasında olmalı: ctx=%d step=%d dep=%d", ctxStep, step, dep)
	}
	if !strings.Contains(fn, "ayrışan boyutlar (BubbleUp)") {
		t.Error("kaynak satırı BubbleUp'ı anmalı")
	}
	own := read("copilot_bubbleup.go")
	if strings.Count(own, "s.store.ServiceBubbleUp(ctx, service, errorFamily, started, end)") != 1 || !strings.Contains(own, `emitGuidedStep(emit, "bubble_up"`) ||
		!strings.Contains(own, "context.WithTimeout(ctx, rca.BubbleUpTimeout)") {
		t.Error("copilot_bubbleup.go: chstore.ServiceBubbleUp + bubble_up çipi + 8 sn tavanı beklenir")
	}
	for _, f := range []string{"copilot_bubbleup.go", "rootcause.go", "rca_extras.go", "copilot_guided.go"} {
		if strings.Contains(read(f), "s.store.BubbleUp(") {
			t.Errorf("%s BubbleUp'ı doğrudan çağırmamalı — serviceBubbleUp kullan", f)
		}
	}
	// İki kıyas dalı chstore'da, tek fonksiyonda (MCP bubble_up ile ortak).
	ch := read("../chstore/bubbleup.go")
	j := strings.Index(ch, "func (s *Store) ServiceBubbleUp(")
	if j < 0 {
		t.Fatal("chstore.ServiceBubbleUp yok")
	}
	body := ch[j : j+strings.Index(ch[j:], "\n}\n")]
	for _, w := range []string{
		`{Key: "status_code", Op: "=", Values: []string{"error"}}`,
		"return s.BubbleUp(ctx, baseline, selection, started, end, started, end)",
		"priorFrom := started.Add(-end.Sub(started))",
		"return s.BubbleUp(ctx, baseline, nil, priorFrom, started, started, end)",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("ServiceBubbleUp %q taşımalı", w)
		}
	}
	if n := strings.Count(read("rootcause.go"), "s.serviceBubbleUp(ctx, "); n != 2 {
		t.Errorf("rootcause.go iki fan-out'ta serviceBubbleUp çağırmalı: %d", n)
	}
}
