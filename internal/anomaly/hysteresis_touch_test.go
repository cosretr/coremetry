package anomaly

import (
	"strings"
	"testing"
)

// v0.10.889 — histerezis bandındaki ("none", açık problem var) anomali
// satırı her tik TOUCH edilir; yoksa evaluator süpürmesi 3 dk sonra "kaynak
// sustu" diye kapatır ve resolveZ histerezisi fiilen çalışmaz. Pin: applyOutcome
// içinde "none" dalı hasOpen'da UpsertProblem çağırır ve erken dönüşün ÜSTÜNDE
// hasOpen hesaplanır; "skip" dalında touch yok.
func TestHysteresisNoneTouchesOpenRow(t *testing.T) {
	src := detectorSrc(t)
	i := strings.Index(src, "func (d *Detector) applyOutcome(")
	if i < 0 {
		t.Fatal("applyOutcome yok")
	}
	body := src[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	iHas := strings.Index(body, "hasOpen := open != nil")
	iNone := strings.Index(body, `if oc.Action == "skip" || oc.Action == "none" {`)
	iTouch := strings.Index(body, `if oc.Action == "none" && hasOpen {`)
	iUpsert := strings.Index(body[iTouch:], "d.store.UpsertProblem(ctx, *open)")
	if iHas < 0 || iNone < 0 || iTouch < 0 || iHas > iNone || iTouch < iNone || iUpsert < 0 || iUpsert > 200 {
		t.Fatalf("histerezis touch sözleşmesi bozuldu: hasOpen=%d none=%d touch=%d upsert=%d", iHas, iNone, iTouch, iUpsert)
	}
	if strings.Contains(body[iNone:iTouch], "return") {
		t.Error("\"none\" dalı touch'tan ÖNCE dönmemeli")
	}
}
