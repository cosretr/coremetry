package ml

// explain_metric_summary.go — v0.10.2032 (operatör: detay cümlesi "Consumer
// lag normalde hiç görülmez, 12 dakikadır" — değeri söylemiyordu). Metrik
// olayının özeti ölçülen değeri ve normalini birimiyle söyler:
//
//	"Consumer lag 162'ye çıktı; normalde hiç görülmez. 16:00'dan beri sürüyor (12 dk)."
//
// Değer = olayın gözlenen değeri, yani dedektörün kova ölçüsü (ör. 5
// dakikalık kova). Birimli değerde "… seviyesine". Cluster'lı (Thanos) olayda
// "prod-east cluster'ında …". Liste ve detay aynı cümleyi gösterir (tek kaynak).

import (
	"fmt"
	"time"
)

func metricSummary(in CardInput, sp detSpec, open bool, end time.Time) string {
	loc := in.loc()
	st := in.StartedAt.In(loc)
	metricS := sp.unit == unitMetric && in.MetricUnit == "s"
	f := newValueFmt(sp, in, in.Observed)
	subj := capFirst(fill(sp.fact, in))
	if _, _, cl := splitThanosPart(in.Part); cl != "" {
		subj = fmt.Sprintf("%s %s cluster'ında", subj, cl)
	}
	verb := "çıktı"
	if in.Direction == "down" {
		verb = "düştü"
	}
	val := f.one(in.Observed, metricS)
	var head string
	if f.suffix() == "" && f.unit != unitRatio {
		head = fmt.Sprintf("%s %s %s", subj, trNumWithSfx(val, sfxDat), verb)
	} else {
		head = fmt.Sprintf("%s %s seviyesine %s", subj, val, verb)
	}
	if in.Service != "" {
		head = in.Service + ": " + head // tek kaynak: Problem başlığı / bildirim de bu cümle; liste öneki kırpar
	}
	normal := "normalde hiç görülmez"
	if in.Baseline > 0 {
		normal = "normalde yaklaşık " + f.one(in.Baseline, metricS)
	}
	plain, _ := durationTR(end.Sub(in.StartedAt))
	day := ""
	switch w := dayWordTR(st, in.Now.In(loc)); w {
	case "Bugün":
	case "Dün":
		day = "Dün "
	default:
		day = w + " "
	}
	if open {
		return fmt.Sprintf("%s; %s. %s%s beri sürüyor (%s).", head, normal, day, clockSfx(st, sfxAbl), plain)
	}
	return fmt.Sprintf("%s; %s. %s%s başladı, %s bitti (%s sürdü).", head, normal, day, clockSfx(st, sfxLoc), clockSfx(end.In(loc), sfxLoc), plain) // v0.10.2034
}
