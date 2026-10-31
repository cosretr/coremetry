package api

// explain_trace_log_outcome.go — v0.10.1742 (operatör onayı): "Bu trace'i
// açıkla" trace kimliğiyle ≤30 log okur; depo hata / zaman aşımı verdiğinde ya
// da 0 satır döndüğünde cevap sessizce logsuz kalıyor, Kanıt satırı yalnız
// "3 span · 1 Oracle satırı" diyordu. Log okumasının sonucu üç hâlle Kanıt
// satırına girer: "N log satırı" · "log yok" · "log okunamadı (zaman aşımı |
// hata | log deposu yok)". Prompt DEĞİŞMEZ (yalnız UI künyesi).

import (
	"fmt"

	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// traceLogOutcome — trace açıklamasının log okuması (lib/types.ts TraceLogOutcome).
type traceLogOutcome struct {
	State  string `json:"state"`            // rows | none | failed
	Rows   int    `json:"rows,omitempty"`   // State=rows: okunan satır
	Reason string `json:"reason,omitempty"` // State=failed: timeout | error | nostore
}

const (
	traceLogRows   = "rows"
	traceLogNone   = "none"
	traceLogFailed = "failed"
)

// classifyTraceLogRead — SAF: depo var mı + okuma sonucu → üç hâl. Zaman
// aşımı / yapılandırılmamış ayrımı sohbet trace drill'iyle aynı sınıflayıcı
// (sourcestate.Classify).
func classifyTraceLogRead(storeOK bool, page *logstore.Page, err error) traceLogOutcome {
	if !storeOK {
		return traceLogOutcome{State: traceLogFailed, Reason: "nostore"}
	}
	if err != nil {
		switch sourcestate.Classify(err) {
		case sourcestate.Timeout:
			return traceLogOutcome{State: traceLogFailed, Reason: "timeout"}
		case sourcestate.NotConfigured:
			return traceLogOutcome{State: traceLogFailed, Reason: "nostore"}
		}
		return traceLogOutcome{State: traceLogFailed, Reason: "error"}
	}
	if page == nil || len(page.Logs) == 0 {
		return traceLogOutcome{State: traceLogNone}
	}
	return traceLogOutcome{State: traceLogRows, Rows: len(page.Logs)}
}

// sanitizeTraceLogOutcome — SAF: arşive giren künye (istemciden gelir) —
// yalnız bilinen hâl / neden, satır sayısı tavanlı (okuma ≤30).
func sanitizeTraceLogOutcome(o *traceLogOutcome) *traceLogOutcome {
	if o == nil {
		return nil
	}
	switch o.State {
	case traceLogRows:
		if o.Rows <= 0 {
			return nil
		}
		return &traceLogOutcome{State: traceLogRows, Rows: min(o.Rows, 1000)}
	case traceLogNone:
		return &traceLogOutcome{State: traceLogNone}
	case traceLogFailed:
		r := o.Reason
		if r != "timeout" && r != "nostore" {
			r = "error"
		}
		return &traceLogOutcome{State: traceLogFailed, Reason: r}
	}
	return nil
}

// traceLogEvidenceTR — SAF: Kanıt satırının log parçası (istemci
// lib/explainLogEvidence.ts ile aynı metin; boş hâl → "").
func traceLogEvidenceTR(o traceLogOutcome) string {
	switch o.State {
	case traceLogRows:
		return fmt.Sprintf("%d log satırı", o.Rows)
	case traceLogNone:
		return "log yok"
	case traceLogFailed:
		reason := map[string]string{"timeout": "zaman aşımı", "nostore": "log deposu yok"}[o.Reason]
		if reason == "" {
			reason = "hata"
		}
		return "log okunamadı (" + reason + ")"
	}
	return ""
}
