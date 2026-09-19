package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.808 — dış skill denetimi 2026-09-19 V4 (Honeycomb production-
// investigation: "oryantasyon SLO ve tetiklerle başlar"). Guided kök-neden ve
// servis-sağlık paketleri SLO durumunu OKUMUYORDU; SLO yalnız izole
// explain-slo yüzeyindeydi. Bu adım paketlerin İLK kanıtı: hedef, canlı SLI,
// kalan bütçe, 1 sa yanma ve DETERMİNİSTİK tükenme (sunucu matematiği —
// model hesaplamaz, aktarır). MCP karşılığı list_slo_status (v0.9.1089).

// guidedSLOCap — bir servis için okunan SLO tavanı; fazlası sayıyla ifşa
// (her SLO 30 güne kadar sınırlı tarama; kesme sessiz olmaz).
const guidedSLOCap = 5

// guidedSLOTTL — Redis önbelleği; guidedServiceNames deseniyle aynı.
const guidedSLOTTL = 30 * time.Second

type guidedSLORow struct {
	SLO      chstore.SLO          `json:"slo"`
	Status   *chstore.SLOStatus   `json:"status,omitempty"`
	Forecast *chstore.SLOForecast `json:"forecast,omitempty"`
	Err      string               `json:"err,omitempty"`
}

type guidedSLOSet struct {
	Rows  []guidedSLORow `json:"rows"`
	Total int            `json:"total"` // servisin toplam SLO sayısı (tavan öncesi)
}

// guidedSLOSet — servisin SLO'ları + durum/tahmin (tek durum taraması,
// ComputeSLOOutlook); 30 sn Redis. Soft-fail: liste hatası boş küme, tek
// SLO'nun okuma hatası satırda Err (kanıt "okunamadı" der, uydurmaz).
func (s *Server) guidedSLOSet(ctx context.Context, service string) guidedSLOSet {
	key := "copilot:guided:slo:" + service
	if b, ok, _ := s.cache.Get(ctx, key); ok && len(b) > 0 {
		var set guidedSLOSet
		if json.Unmarshal(b, &set) == nil {
			return set
		}
	}
	var set guidedSLOSet
	slos, err := s.store.ListSLOs(ctx)
	if err != nil {
		return set
	}
	for _, o := range slos {
		if o.Service != service {
			continue
		}
		set.Total++
		if len(set.Rows) >= guidedSLOCap {
			continue
		}
		row := guidedSLORow{SLO: o}
		st, fc, err := s.store.ComputeSLOOutlook(ctx, o, time.Hour)
		if err != nil {
			row.Err = err.Error()
		}
		row.Status, row.Forecast = st, fc
		set.Rows = append(set.Rows, row)
	}
	if b, merr := json.Marshal(set); merr == nil {
		_ = s.cache.Set(ctx, key, b, guidedSLOTTL)
	}
	return set
}

// guidedSLOStep — adım çipi + kanıt + prompt bloğu; paketlerin ilk adımı.
func (s *Server) guidedSLOStep(ctx context.Context, emit func(string, any), b *strings.Builder, service string) guidedSLOSet {
	n := emitGuidedStep(emit, "list_slo_status", `{"service":"`+service+`"}`)
	set := s.guidedSLOSet(ctx, service)
	text := renderSLOStatusTR(set, service)
	emitGuidedStepResult(emit, n, "list_slo_status", text, nil)
	b.WriteString(text)
	b.WriteString("\n")
	return set
}

// renderSLOStatusTR — SAF: modelin okuduğu SLO bloğu. Yüzdeler sunucudan,
// tükenme projectBurnHours'tan; metin "hesapla" değil "aktar" der.
func renderSLOStatusTR(set guidedSLOSet, service string) string {
	var b strings.Builder
	if set.Total == 0 {
		fmt.Fprintf(&b, "SLO DURUMU (%s): tanımlı SLO yok — hedef/bütçe kanıtı yok; RED değişimi ve problemlerle devam. "+
			"Operatör sorarsa \"bu servis için SLO tanımlı değil\" de, hedef UYDURMA.\n", service)
		return b.String()
	}
	fmt.Fprintf(&b, "SLO DURUMU (%s, %d tanım):\n", service, set.Total)
	for _, r := range set.Rows {
		o := r.SLO
		def := fmt.Sprintf("hedef %%%s / %d g", pctTR(o.Target), o.WindowDays)
		if o.SLIType == "latency" && o.ThresholdMs > 0 {
			def += fmt.Sprintf(", p≤%.0f ms", o.ThresholdMs)
		}
		if o.Operation != "" {
			def += ", op " + o.Operation
		}
		fmt.Fprintf(&b, "- %s (%s): ", o.Name, def)
		switch {
		case r.Err != "" && r.Status == nil:
			b.WriteString("durum OKUNAMADI (sorgu hatası) — bilinmiyor de, tahmin etme")
		case r.Status == nil:
			b.WriteString("durum yok")
		case r.Status.NoData:
			b.WriteString("OLAY YOK — pencerede hiç olay; servis/operasyon adı ve pencereyi kontrol et")
		default:
			st := r.Status
			verdict := "sağlıklı"
			if !st.Healthy {
				verdict = "İHLAL"
			}
			fmt.Fprintf(&b, "SLI %%%s · bütçe %%%d kaldı · %s", pctTR(st.SLI), int(st.BudgetRemaining*100+0.5), verdict)
			if fc := r.Forecast; fc != nil {
				fmt.Fprintf(&b, " · yanma %.1f× (1 sa)", fc.BurnRate)
				switch {
				case fc.WillBreachWithin24h:
					fmt.Fprintf(&b, " · tükenme ~%.0f sa — 24 SA İÇİNDE", fc.HoursToExhaust)
				case !fc.SafeBurn:
					fmt.Fprintf(&b, " · tükenme ~%.0f sa", fc.HoursToExhaust)
				default:
					b.WriteString(" · yanma güvenli")
				}
			}
			if st.Hint != "" {
				b.WriteString(" · ipucu: " + st.Hint)
			}
		}
		b.WriteString("\n")
	}
	if set.Total > len(set.Rows) {
		fmt.Fprintf(&b, "(+%d SLO daha; ilk %d gösterildi)\n", set.Total-len(set.Rows), len(set.Rows))
	}
	b.WriteString("KURAL: SLO sayıları sunucunun hesabıdır; tükenme süresini kendin hesaplama, yukarıdaki değeri aktar.\n")
	return b.String()
}

// pctTR — 0.999 → "99.9", 0.9 → "90", 0.99951 → "99.951" (en çok 3 ondalık).
func pctTR(v float64) string {
	s := fmt.Sprintf("%.3f", v*100)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
