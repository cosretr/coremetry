package ml

// explain_play.go — v0.10.1846, öğrenme döngüsü Faz 2 (audit §15 "olayı
// oynat" = olayın kendi gösterimi, rapor DEĞİL; §17 play kararları). Olay
// anı kaydının serisi + bandı → oynatma adımları ve kova başına canlı
// cümle. Cümle kartın oran merdiveninden (RatioPhrase) ve değer biçiminden
// (valueFmt) kurulur; FE yalnız seçer, cümle KURMAZ (mlSingleSource testi).
// Bant grafikteki "beklenen aralık" çizgileriyle AYNI kaynak (kayıt
// thresholds.band q25–q75; yoksa taban medyanı "normalde yaklaşık").

import (
	"fmt"
	"time"
)

// PlayStep — adım düğmesi (ilk sapma, tespit, tepe, bitiş | sürüyor).
type PlayStep struct {
	Key   string `json:"key"` // first | detect | peak | end | open
	Label string `json:"label"`
	T     int64  `json:"t"` // unix sn — seri noktasına hizalı
}

// PlayFrame — kova başına canlı cümle (HH:MM operatör diliminde).
type PlayFrame struct {
	T     int64  `json:"t"`
	Clock string `json:"clock"`
	Text  string `json:"text"`
}

// ExplainPlay — oynatma verisi; seri yoksa kart Play taşımaz.
type ExplainPlay struct {
	Steps  []PlayStep  `json:"steps"`
	Frames []PlayFrame `json:"frames"`
	// From — "geçen kısım" dolgusunun başı (ilk sapma, yoksa tespit).
	From int64 `json:"from"`
}

type playMarks struct {
	first, detect, peak, end int64
	open                     bool
}

// playMarksOf — SAF: işaretler, seri noktalarına hizalı (son nokta ≤ an).
func playMarksOf(in CardInput, open bool) (playMarks, bool) {
	pts := in.Series
	if len(pts) == 0 {
		return playMarks{}, false
	}
	align := func(t int64) int64 {
		best := pts[0].T
		for _, p := range pts {
			if p.T <= t {
				best = p.T
			}
		}
		return best
	}
	m := playMarks{detect: align(in.StartedAt.Unix()), open: open}
	m.first = m.detect
	if len(in.Dwell) > 0 && in.Dwell[0] < m.first {
		m.first = align(in.Dwell[0])
	}
	end := in.LastSeen.Unix()
	if open {
		end = pts[len(pts)-1].T
	}
	m.end = align(end)
	down := in.Direction == "down"
	peakV := 0.0
	for _, p := range pts {
		if p.T < m.first || p.T > m.end {
			continue
		}
		if m.peak == 0 || (!down && p.V > peakV) || (down && p.V < peakV) {
			m.peak, peakV = p.T, p.V
		}
	}
	if m.peak == 0 {
		m.peak = m.detect
	}
	return m, true
}

func (in CardInput) bandOrBase(t int64) (lo, mid, hi float64, ok bool) {
	if b, has := in.bandAt(t); has {
		return b.Lo, b.Mid, b.Hi, true
	}
	return 0, in.Baseline, 0, false
}

// playStageSentence — SAF: kovanın olay içindeki yeri (mockup cümleleri).
func playStageSentence(in CardInput, m playMarks, t int64, ro *CardRollout) string {
	loc := in.loc()
	switch {
	case t < m.first:
		if ro != nil && ro.At.Unix() <= t {
			return fmt.Sprintf(" Yeni sürüm %s yayında; henüz sapma yok.", clockSfx(ro.At.In(loc), sfxLoc))
		}
		return ""
	case t < m.detect:
		return " Sapma başladı, henüz kalıcı olduğu belli değil."
	case t == m.peak:
		return " Şimdiye kadarki en yüksek değer."
	case t < m.peak:
		return " Sapma kalıcı; anomali olarak işaretlendi."
	case m.open:
		return " Hâlâ sürüyor."
	case t < m.end:
		return " Sapma sürüyor."
	}
	return " Olay burada bitti."
}

// BuildPlay — SAF: kart girdisi (seri + bant + işaretler) → oynatma.
func BuildPlay(in CardInput) *ExplainPlay {
	open := cardOpen(in)
	m, ok := playMarksOf(in, open)
	if !ok {
		return nil
	}
	loc := in.loc()
	sp := specFor(in)
	metricS := sp.unit == unitMetric && in.MetricUnit == "s"
	f := newValueFmt(sp, in, in.Observed)
	ro := rolloutInWindow(in, cardEnd(in, open))
	clock := func(t int64) string { return time.Unix(t, 0).In(loc).Format("15:04") }
	p := &ExplainPlay{From: m.first, Steps: []PlayStep{
		{Key: "first", Label: "İlk sapma · " + clock(m.first), T: m.first},
		{Key: "detect", Label: "Tespit · " + clock(m.detect), T: m.detect},
		{Key: "peak", Label: "Tepe · " + clock(m.peak), T: m.peak},
	}}
	// Açık olayın kaydı tespitte yazılır, epizot kapanınca tamamlanır: son
	// nokta şimdiden 2 kovadan eskiyse adım "Kayıt sonu" der ("sürüyor"
	// zaman damgası şimdiyi göstermesin).
	stale := m.open && in.Now.Unix()-m.end > 2*int64(in.bucket().Seconds())
	switch {
	case stale:
		p.Steps = append(p.Steps, PlayStep{Key: "open", Label: "Kayıt sonu · " + clock(m.end), T: m.end})
	case m.open:
		p.Steps = append(p.Steps, PlayStep{Key: "open", Label: "Sürüyor · " + clock(m.end), T: m.end})
	default:
		p.Steps = append(p.Steps, PlayStep{Key: "end", Label: "Bitiş · " + clock(m.end), T: m.end})
	}
	if m.first == m.detect { // dwell kaydı yok: ilk sapma = tespit, tek düğme
		p.Steps = p.Steps[1:]
	}
	for _, pt := range in.Series {
		lo, mid, hi, banded := in.bandOrBase(pt.T)
		var normal, phrase string
		if banded {
			normal = "bu saatte beklenen " + f.rng(lo, hi, metricS)
			if pt.V >= lo && pt.V <= hi {
				phrase = "normal aralıkta"
			}
		} else {
			normal = "bu saatte normalde yaklaşık " + f.one(mid, metricS)
		}
		if phrase == "" {
			phrase = RatioPhrase(pt.V, mid, in.Direction, RatioShort, ratioRef(in))
		}
		val := "değer " + f.one(pt.V, metricS)
		if sp.additive && sp.noun != "" {
			val = f.one(pt.V, false) + " " + sp.noun
		}
		stage := playStageSentence(in, m, pt.T, ro)
		if stale && pt.T == m.end {
			stage = " Kayıt burada bitiyor; olay sürüyor."
		}
		p.Frames = append(p.Frames, PlayFrame{T: pt.T, Clock: clock(pt.T),
			Text: fmt.Sprintf("%s; %s; %s.%s", val, normal, phrase, stage)})
	}
	return p
}
