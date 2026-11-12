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
	"math"
	"strings"
	"time"
)

// ExplainValue — v0.10.2044 (operatör: cümle 1,34 bin derken grafik 2,8 bin
// gösteriyordu): metrik özetinin DEĞER parçası. Text cümlede birebir geçer;
// detay sayfası panel verisi gelince yalnız bu parçayı panelin olay
// aralığındaki tepesiyle yeniden yazar (FE mlValueText.withPanelPeak).
// Measured "Teknik ayrıntı"nın "Dedektörün ölçtüğü" satırı.
type ExplainValue struct {
	Text      string `json:"text"`
	Measured  string `json:"measured"`
	Unit      string `json:"unit,omitempty"`
	Direction string `json:"direction,omitempty"`
}

// metricMeasureName — SAF: dedektör değerinin adı. Pencereli ifade (rate /
// increase, `{win}`) kova ortalaması; değilse kova sonundaki ölçüm.
func metricMeasureName(in CardInput) string {
	m := int(in.bucket().Minutes())
	if m <= 0 {
		m = 5
	}
	if in.MetricWindowed {
		return fmt.Sprintf("%d dakikalık ortalama", m)
	}
	return fmt.Sprintf("%d dakikalık ölçüm", m)
}

// metricValue — SAF: değer parçası ("162'ye çıktı (5 dakikalık ölçüm)").
func metricValue(in CardInput, sp detSpec) ExplainValue {
	metricS := sp.unit == unitMetric && in.MetricUnit == "s"
	f := newValueFmt(sp, in, in.Observed)
	verb := "çıktı"
	if in.Direction == "down" {
		verb = "düştü"
	}
	val := f.one(in.Observed, metricS)
	measure := metricMeasureName(in)
	var text string
	if f.suffix() == "" && f.unit != unitRatio {
		text = fmt.Sprintf("%s %s (%s)", trNumWithSfx(val, sfxDat), verb, measure)
	} else {
		text = fmt.Sprintf("%s seviyesine %s (%s)", val, verb, measure)
	}
	return ExplainValue{Text: text, Measured: val + " (" + measure + ")", Unit: in.MetricUnit, Direction: in.Direction}
}

func metricSummary(in CardInput, sp detSpec, open bool, end time.Time) string {
	loc := in.loc()
	st := in.StartedAt.In(loc)
	metricS := sp.unit == unitMetric && in.MetricUnit == "s"
	f := newValueFmt(sp, in, in.Observed)
	subj := capFirst(fill(sp.fact, in))
	if _, _, cl := splitThanosPart(in.Part); cl != "" {
		subj = fmt.Sprintf("%s %s cluster'ında", subj, cl)
	}
	head := subj + " " + metricValue(in, sp).Text
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
	if cp := contribSentence(in.Contrib); cp != "" { // v0.10.2037 — suçlu seri
		normal += ". " + cp
	}
	if open {
		return fmt.Sprintf("%s; %s. %s%s beri sürüyor (%s).", head, normal, day, clockSfx(st, sfxAbl), plain)
	}
	return fmt.Sprintf("%s; %s. %s%s başladı, %s bitti (%s sürdü).", head, normal, day, clockSfx(st, sfxLoc), clockSfx(end.In(loc), sfxLoc), plain) // v0.10.2034
}

// contribSentence — SAF (v0.10.2037): suçlu seri cümlesi (nokta yok).
//
//	tek, pay ≥ %90: "Neredeyse tamamı orders-collector-consumer-v1 istemcisinde"
//	tek, pay < %90: "Çoğu (%73) orders-batch-7d9-a pod'unda"
//	ilk 3:          "En çok orders-a, orders-b ve orders-c istemcilerinde"
//	yayılmış:       "12 istemciye yayılmış; öne çıkan yok"
func contribSentence(c *Contrib) string {
	if c == nil {
		return ""
	}
	one, many, dat := "pod'unda", "pod'larında", "pod'a"
	if c.Label == "client_id" {
		one, many, dat = "istemcisinde", "istemcilerinde", "istemciye"
	}
	switch c.Kind {
	case "single":
		if len(c.Items) == 0 {
			return ""
		}
		it := c.Items[0]
		if it.Share >= 0.9 {
			return fmt.Sprintf("Neredeyse tamamı %s %s", it.Name, one)
		}
		return fmt.Sprintf("Çoğu (%%%d) %s %s", int(math.Round(it.Share*100)), it.Name, one)
	case "top":
		if len(c.Items) == 0 {
			return ""
		}
		names := make([]string, len(c.Items))
		for i, it := range c.Items {
			names[i] = it.Name
		}
		list := names[0]
		if len(names) > 1 {
			list = strings.Join(names[:len(names)-1], ", ") + " ve " + names[len(names)-1]
		}
		return fmt.Sprintf("En çok %s %s", list, many)
	case "spread":
		if c.Active < 2 {
			return ""
		}
		return fmt.Sprintf("%d %s yayılmış; öne çıkan yok", c.Active, dat)
	}
	return ""
}
