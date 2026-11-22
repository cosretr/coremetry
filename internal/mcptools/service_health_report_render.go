package mcptools

// service_health_report_render.go — v0.10.2454: get_service_health raporunun
// Türkçe kanıt metni (guided sağlık demeti bunu kanıtın başına koyar; döngü
// JSON'u görür). SAF; değerler rapordan AYNEN (yeniden hesap yok), bölüm
// sırası HRSectionOrder. Eksik bölüm adıyla söylenir, değer yazılmaz.

import (
	"fmt"
	"strconv"
	"strings"
)

// HealthReportMarker — kanıttaki rapor başlığı (anlatım istemi buna bakar).
const HealthReportMarker = "SAĞLIK RAPORU"

func hrNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func hrSigned(v float64) string {
	if v > 0 {
		return "+" + hrNum(v)
	}
	return hrNum(v)
}

// hrStatusTR — ok dışı durumun cümlesi ("" = ok).
func hrStatusTR(status, note string) string {
	switch status {
	case HRStatusMissing:
		return "ölçülmüyor / kaynak yok — " + note
	case HRStatusUnread:
		return "OKUNAMADI (yok demek değil) — " + note
	case HRStatusNone:
		return note
	}
	return ""
}

func hrCountsTR(label string, cs []HRCount) string {
	if len(cs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s %d (%%%s)", c.Value, c.Errors, hrNum(c.SharePct)))
	}
	return label + ": " + strings.Join(parts, ", ")
}

// RenderServiceHealthReportTR — SAF: rapor → kanıt metni.
func RenderServiceHealthReportTR(r ServiceHealthReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (get_service_health; pencere %s, bitiş %s", HealthReportMarker, fmtWindowTR(r.WindowS), r.To)
	if r.Env != "" {
		fmt.Fprintf(&b, ", ortam %s", r.Env)
	}
	b.WriteString("; cevap bu bölüm sırasıyla, her bölüm kaynağıyla):\n")

	m := r.Metrics
	fmt.Fprintf(&b, "1) Metrikler [%s]: ", m.Source)
	if s := hrStatusTR(m.Status, m.Note); s != "" || m.Current == nil {
		b.WriteString(s + "\n")
	} else {
		c := m.Current
		var parts []string
		if c.RPS != nil {
			parts = append(parts, "istek/s "+hrNum(*c.RPS))
		}
		if c.ErrorPct != nil {
			parts = append(parts, "hata %"+hrNum(*c.ErrorPct))
		}
		if c.P95Ms != nil {
			parts = append(parts, "p95 "+hrNum(*c.P95Ms)+" ms")
		}
		if c.P99Ms != nil {
			parts = append(parts, "p99 "+hrNum(*c.P99Ms)+" ms")
		}
		b.WriteString(strings.Join(parts, ", "))
		if d := m.Delta; d != nil {
			var dp []string
			if d.RPSPct != nil {
				dp = append(dp, "istek %"+hrSigned(*d.RPSPct))
			}
			if d.ErrorPP != nil {
				dp = append(dp, "hata "+hrSigned(*d.ErrorPP)+" puan")
			}
			if d.P95Pct != nil {
				dp = append(dp, "p95 %"+hrSigned(*d.P95Pct))
			}
			if d.P99Pct != nil {
				dp = append(dp, "p99 %"+hrSigned(*d.P99Pct))
			}
			if len(dp) > 0 {
				b.WriteString("; önceki eş pencereye göre: " + strings.Join(dp, ", "))
			}
		}
		if m.InfraExcluded {
			b.WriteString("; tesisat rotaları (health / scrape) dışlandı")
		}
		if m.Note != "" {
			b.WriteString(" (" + m.Note + ")")
		}
		b.WriteString("\n")
	}

	t := r.TraceErrors
	fmt.Fprintf(&b, "2) Trace hataları — ÖRNEKLENMİŞ [%s]: ", t.Source)
	if s := hrStatusTR(t.Status, t.Note); s != "" && t.Status != HRStatusNone {
		b.WriteString(s + "\n")
	} else {
		fmt.Fprintf(&b, "saklanan %d span'in %d'i hatalı (pay %%%s)", t.StoredSpans, t.ErrorSpans, hrNum(t.ErrorSharePct))
		if t.PriorErrorSharePct != nil && t.PriorErrorSpans != nil {
			fmt.Fprintf(&b, "; önceki pencere %%%s (%d hatalı span) → eğilim %s", hrNum(*t.PriorErrorSharePct), *t.PriorErrorSpans, t.Trend)
		}
		if t.ErrorTraces != nil {
			fmt.Fprintf(&b, "; hatalı trace %d (son %s)", *t.ErrorTraces, fmtWindowTR(t.ErrorTracesWindowS))
		}
		b.WriteString(". " + t.Note + "\n")
	}

	o := r.ErrorOps
	fmt.Fprintf(&b, "3) En çok hata veren operasyonlar [%s]: ", o.Source)
	if s := hrStatusTR(o.Status, o.Note); s != "" {
		b.WriteString(s + "\n")
	} else {
		if o.Note != "" {
			b.WriteString("(" + o.Note + ")")
		}
		b.WriteString("\n")
		for _, x := range o.Rows {
			fmt.Fprintf(&b, "- %s: %d hata", x.Operation, x.Errors)
			if x.ErrorPct != nil {
				fmt.Fprintf(&b, " (hata %%%s)", hrNum(*x.ErrorPct))
			}
			fmt.Fprintf(&b, ", baskın %s (%%%s)", x.Dominant, hrNum(x.DominantPct))
			if x.SampleTrace != "" {
				b.WriteString(", örnek trace " + x.SampleTrace)
			}
			b.WriteString("\n")
		}
	}

	te := r.Technical
	fmt.Fprintf(&b, "4) Teknik hatalar [%s]: ", te.Source)
	if s := hrStatusTR(te.Status, te.Note); s != "" {
		b.WriteString(s)
	} else {
		var parts []string
		for _, p := range []string{hrCountsTR("error.type", te.ErrorType), hrCountsTR("HTTP", te.HTTPStatus), hrCountsTR("gRPC", te.GRPCStatus), hrCountsTR("DB", te.DB)} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			parts = append(parts, "hatalarda error.type / HTTP / gRPC / DB etiketi yok")
		}
		b.WriteString(strings.Join(parts, "; "))
	}
	b.WriteString("; aşağı hedefler: ")
	if len(te.Downstream) == 0 {
		b.WriteString(te.DownstreamStatus)
	} else {
		parts := make([]string, 0, len(te.Downstream))
		for _, x := range te.Downstream {
			s := fmt.Sprintf("%s hata %%%s (%s)", x.Target, hrNum(x.ErrorPct), x.Source)
			if x.Dominant != "" {
				s += " baskın " + x.Dominant
			}
			parts = append(parts, s)
		}
		b.WriteString(strings.Join(parts, ", ") + " — " + te.DownstreamStatus)
	}
	b.WriteString("\n")

	e := r.Exceptions
	fmt.Fprintf(&b, "5) Exception'lar [%s]: ", e.Source)
	if s := hrStatusTR(e.Status, e.Note); s != "" {
		b.WriteString(s + "\n")
	} else {
		if e.Note != "" {
			b.WriteString("(" + e.Note + ")")
		}
		b.WriteString("\n")
		for _, g := range e.Groups {
			kind := "tekrarlayan"
			if g.Kind == "new" {
				kind = "yeni"
			}
			fmt.Fprintf(&b, "- %s", g.Type)
			if g.Message != "" {
				fmt.Fprintf(&b, ": %q", g.Message)
			}
			fmt.Fprintf(&b, " — %d kez, ilk %s, son %s, %s\n", g.Occurrences, g.FirstSeen, g.LastSeen, kind)
		}
		if e.HasMore {
			b.WriteString("- (daha fazla grup var)\n")
		}
	}

	ms := r.Messaging
	fmt.Fprintf(&b, "6) Messaging [%s]: ", ms.Source)
	if s := hrStatusTR(ms.Status, ms.Note); s != "" {
		b.WriteString(s)
	} else {
		if ms.Family != nil && ms.Family.State == "metric" {
			b.WriteString("Kafka istemci metriği ailesi " + ms.Family.Family + "; ")
		}
		parts := make([]string, 0, len(ms.Topics))
		for _, x := range ms.Topics {
			parts = append(parts, fmt.Sprintf("%s %s (%s): %d span, hata %%%s, p95 %s ms", x.System, x.Destination, x.Role, x.Spans, hrNum(x.ErrorPct), hrNum(x.P95Ms)))
		}
		b.WriteString(strings.Join(parts, "; "))
		if ms.Note != "" {
			b.WriteString(" (" + ms.Note + ")")
		}
	}
	b.WriteString("; consumer lag: ")
	if len(ms.Lag) == 0 {
		b.WriteString(ms.LagStatus)
	} else {
		parts := make([]string, 0, len(ms.Lag))
		for _, l := range ms.Lag {
			parts = append(parts, fmt.Sprintf("%s son %s / en yüksek %s kayıt", l.Topic, hrNum(l.Last), hrNum(l.Max)))
		}
		b.WriteString(strings.Join(parts, ", ") + " — " + ms.LagStatus)
	}
	b.WriteString("\n")

	c := r.Context
	b.WriteString("Bağlam: ")
	var cp []string
	if c.OpenProblems != nil {
		cp = append(cp, fmt.Sprintf("açık problem %d", *c.OpenProblems))
	}
	if c.Anomalies != nil {
		s := fmt.Sprintf("anomali %d", *c.Anomalies)
		if c.AnomaliesHasMore {
			s += "+"
		}
		cp = append(cp, s)
	}
	if c.LastDeploy != nil {
		cp = append(cp, fmt.Sprintf("son deploy %s (%s)", c.LastDeploy.Version, c.LastDeploy.At))
	} else {
		cp = append(cp, "deploy: "+c.DeployStatus)
	}
	if len(c.Unread) > 0 {
		cp = append(cp, "okunamadı: "+strings.Join(c.Unread, ", "))
	}
	if len(c.Missing) > 0 {
		cp = append(cp, "kaynak yok: "+strings.Join(c.Missing, ", "))
	}
	b.WriteString(strings.Join(cp, "; ") + ".\n")
	return b.String()
}
