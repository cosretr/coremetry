package ml

// explain_play_test.go — v0.10.1846 (audit §15, §17): "olayı oynat" adım ve
// canlı cümle sözleşmesi. Adımlar seri noktalarına hizalı (ilk sapma →
// tespit → tepe → sürüyor | bitiş); cümle kartın oran merdiveninden ve
// grafikteki AYNI banttan; bant yoksa uydurma aralık yok.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func playFixture() CardInput {
	vals := []float64{0.04, 0.04, 0.04, 0.04, 0.04, 0.09, 0.12, 0.15, 0.12}
	var pts []chstore.MLPoint
	for i, v := range vals {
		pts = append(pts, chstore.MLPoint{T: at(14, 0).Add(time.Duration(i) * 5 * time.Minute).Unix(), V: v})
	}
	return CardInput{Detector: "op_latency", Signal: chstore.MLSignalTrace, Service: "notify-gateway", Code: "/notify/order-status", Direction: "up",
		StartedAt: at(14, 30), LastSeen: at(14, 44), Now: at(14, 44), Loc: trLoc, BucketSec: 300, Observed: 0.12, Baseline: 0.04,
		Series: pts, Band: bands(at(14, 0), 9, 0.03, 0.04, 0.05), Dwell: []int64{at(14, 25).Unix(), at(14, 30).Unix()},
		Rollouts: []CardRollout{{At: at(14, 10)}}, RolloutsKnown: true}
}

func TestBuildPlayStepsAndSentences(t *testing.T) {
	p := BuildPlay(playFixture())
	if p == nil {
		t.Fatal("play yok")
	}
	var steps []string
	for _, s := range p.Steps {
		steps = append(steps, s.Key+"="+s.Label)
	}
	eqList(t, "steps", steps, []string{"first=İlk sapma · 14:25", "detect=Tespit · 14:30", "peak=Tepe · 14:35", "open=Sürüyor · 14:40"})
	if p.From != at(14, 25).Unix() {
		t.Errorf("from %d", p.From)
	}
	want := map[string]string{
		"14:00": "değer %4; bu saatte beklenen %3–5; normal aralıkta.",
		"14:15": "değer %4; bu saatte beklenen %3–5; normal aralıkta. Yeni sürüm 14:10'da yayında; henüz sapma yok.",
		"14:25": "değer %9; bu saatte beklenen %3–5; yaklaşık 2 kat. Sapma başladı, henüz kalıcı olduğu belli değil.",
		"14:30": "değer %12; bu saatte beklenen %3–5; yaklaşık 3 kat. Sapma kalıcı; anomali olarak işaretlendi.",
		"14:35": "değer %15; bu saatte beklenen %3–5; yaklaşık 4 kat. Şimdiye kadarki en yüksek değer.",
		"14:40": "değer %12; bu saatte beklenen %3–5; yaklaşık 3 kat. Hâlâ sürüyor.",
	}
	if len(p.Frames) != 9 {
		t.Fatalf("kare sayısı %d", len(p.Frames))
	}
	for _, f := range p.Frames {
		if w, ok := want[f.Clock]; ok {
			eq(t, "frame "+f.Clock, f.Text, w)
		}
	}
}

func TestBuildPlayClosedCountNoBand(t *testing.T) {
	in := playFixture()
	in.Detector, in.Signal, in.Code = "error_rate", chstore.MLSignalLog, ""
	in.Stage, in.LastSeen, in.Now = chstore.MLStageClosed, at(14, 35), at(16, 0)
	in.Band, in.Rollouts, in.Baseline, in.Observed = nil, nil, 5, 50
	for i := range in.Series {
		in.Series[i].V = in.Series[i].V * 1000 / 4 // 10 … 37,5 satır
	}
	p := BuildPlay(in)
	last := p.Steps[len(p.Steps)-1]
	if last.Key != "end" || last.Label != "Bitiş · 14:35" {
		t.Errorf("son adım %+v", last)
	}
	for _, f := range p.Frames {
		switch f.Clock {
		case "14:00":
			eq(t, "14:00", f.Text, "10 hata satırı; bu saatte normalde yaklaşık 5; yaklaşık 2 kat.")
		case "14:40":
			eq(t, "14:40", f.Text, "30 hata satırı; bu saatte normalde yaklaşık 5; yaklaşık 6 kat. Olay burada bitti.")
		}
		if strings.Contains(f.Text, "beklenen") {
			t.Errorf("bant yokken aralık uyduruldu: %q", f.Text)
		}
	}
}

func TestBuildPlayNoSeries(t *testing.T) {
	in := playFixture()
	in.Series = nil
	if BuildPlay(in) != nil {
		t.Error("serisiz olayda play üretildi")
	}
	in = playFixture()
	in.Dwell = nil // ilk sapma = tespit → tek düğme
	if p := BuildPlay(in); p.Steps[0].Key != "detect" {
		t.Errorf("dwell yokken ilk adım %q", p.Steps[0].Key)
	}
	// açık olay, kayıt tespitte donmuş (son nokta 14:40, şimdi 15:30): "Kayıt sonu"
	in = playFixture()
	in.Now, in.LastSeen = at(15, 30), at(15, 30)
	p := BuildPlay(in)
	if l := p.Steps[len(p.Steps)-1].Label; l != "Kayıt sonu · 14:40" {
		t.Errorf("bayat açık kayıt adımı %q", l)
	}
	if f := p.Frames[len(p.Frames)-1].Text; !strings.HasSuffix(f, " Kayıt burada bitiyor; olay sürüyor.") {
		t.Errorf("son kare %q", f)
	}
	if c := BuildExplainCard(playFixture()); c.Play == nil {
		t.Error("kart play taşımıyor")
	}
}
