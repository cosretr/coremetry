package sre

// readers_errors.go — v0.10.1494 (CoSRE 2d, plan §2.5): hata izi özeti —
// çıpa penceresinde en çok hata üreten ≤3 operasyon (operation_summary_5m,
// TopErrorOperations; MV-first) + en sık ≤3 istisna grubu ve grubun örnek
// trace'i (GetExceptions type-service gruplaması: argMax(trace_id, time) —
// v0.10.1486 CoSRE exception cevabının örnek trace kaynağıyla AYNI okuma).
// Stack kesiti okunmaz (ek okuma; sohbetin exception cevabı taşır). Temsilci
// trace pivot adımının gerçeğinden gelir (yeniden okuma yok). Yalnız Context
// Pack açıkken; dalga 1.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const errorDigestTopN = 3

// ErrorOp — hatalı operasyon.
type ErrorOp struct {
	Name   string  `json:"name"`
	Errors uint64  `json:"errors"`
	Spans  uint64  `json:"spans"`
	ErrPct float64 `json:"errPct"`
}

// ErrorGroup — istisna grubu (tür + servis) ve örnek trace.
type ErrorGroup struct {
	Type          string `json:"type"`
	Message       string `json:"message,omitempty"`
	Count         int64  `json:"count"`
	SampleTraceID string `json:"sampleTraceId,omitempty"`
}

// ErrorFacts — hata özeti adımının parçası.
type ErrorFacts struct {
	OpsOK    bool         `json:"opsOk"`
	Ops      []ErrorOp    `json:"ops,omitempty"`
	GroupsOK bool         `json:"groupsOk"`
	Groups   []ErrorGroup `json:"groups,omitempty"`
}

// errorsRequest — adımın girdisi.
type errorsRequest struct {
	Service  string
	From, To time.Time
}

// errorOpsOf — SAF: sıralı (okuyucu hata azalan döner) ≤3 satır.
func errorOpsOf(rows []chstore.ErrorRank) []ErrorOp {
	var out []ErrorOp
	for _, r := range rows {
		if len(out) >= errorDigestTopN {
			break
		}
		op := ErrorOp{Name: truncRunes(r.Name, 160), Errors: r.Errors, Spans: r.Spans}
		if r.Spans > 0 {
			op.ErrPct = round2(float64(r.Errors) / float64(r.Spans) * 100)
		}
		out = append(out, op)
	}
	return out
}

// errorGroupsOf — SAF: ≤3 grup (okuyucu sayı azalan döner).
func errorGroupsOf(rows []chstore.ExceptionRow) []ErrorGroup {
	var out []ErrorGroup
	for _, r := range rows {
		if len(out) >= errorDigestTopN {
			break
		}
		out = append(out, ErrorGroup{Type: truncRunes(r.Type, 160), Message: truncRunes(oneLine(r.Message), 160), Count: int64(r.Count), SampleTraceID: r.SampleTraceID})
	}
	return out
}

// runErrors — hata özeti adımı.
func runErrors(ctx context.Context, rd readers, req errorsRequest) stepOutcome {
	st := Step{Name: stepErrors}
	if rd.errorOps == nil && rd.exceptions == nil {
		return skippedOutcome(stepErrors, "hata okuyucusu bağlı değil", "step.skip.errorsReader")
	}
	ef := &ErrorFacts{}
	var errs []string
	if rd.errorOps != nil {
		rows, err := rd.errorOps(ctx, req.Service, req.From, req.To, errorDigestTopN)
		if err != nil {
			errs = append(errs, "operasyon: "+err.Error())
		} else {
			ef.OpsOK, ef.Ops = true, errorOpsOf(rows)
		}
	}
	if rd.exceptions != nil {
		rows, err := rd.exceptions(ctx, chstore.ExceptionFilter{Services: []string{req.Service}, GroupBy: "type-service", From: req.From, To: req.To, Limit: errorDigestTopN})
		if err != nil {
			errs = append(errs, "istisna: "+err.Error())
		} else {
			ef.GroupsOK, ef.Groups = true, errorGroupsOf(rows)
		}
	}
	switch {
	case !ef.OpsOK && !ef.GroupsOK:
		st.State, st.Summary = StepError, truncRunes("hata özeti okunamadı: "+strings.Join(errs, "; "), 400)
		return stepOutcome{step: st}
	case len(errs) > 0:
		st.State = StepPartial
	default:
		st.State = StepOK
	}
	st.Summary = fmt.Sprintf("%d hatalı operasyon · %d istisna grubu", len(ef.Ops), len(ef.Groups))
	if len(errs) > 0 {
		st.Summary += " · " + strings.Join(errs, "; ")
	}
	if len(ef.Ops) == 0 && len(ef.Groups) == 0 && st.State == StepOK {
		st.NoData = true
		st.Summary = "veri yok: pencerede hatalı operasyon ya da istisna grubu yok"
		st.SummaryMsg = chstore.NewStdMsg("step.noData.errors")
	}
	return stepOutcome{step: st, facts: &Facts{Errors: ef}}
}
