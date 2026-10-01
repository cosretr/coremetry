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

// v0.10.1022 — yukarıdaki touch ÖLÜ KODDU: faz 1 "none"u applyOutcome'a hiç
// göndermiyordu (operatör: "anomali alarmları çok geliyor"; bantta kalan açık
// anomali 3 dk'da "kaynak sustu" diye süpürülüp eşiği yeniden geçince yeni
// kimlikle + yeni bildirimle açılıyordu). Karar saf işleve çıktı.
func TestReachesApply(t *testing.T) {
	cases := []struct {
		action  string
		hasOpen bool
		want    bool
	}{
		{"none", true, true},   // histerezis bandı + açık satır → touch'a ulaşmalı
		{"none", false, false}, // satır yok → yapılacak iş yok
		{"skip", true, false},  // veri yok: touch YOK (sustu ≠ düzeldi; süpürme kapatır)
		{"skip", false, false},
		{"open", false, true},
		{"open", true, true},
		{"resolve", true, true},
		{"resolve", false, true},
	}
	for _, c := range cases {
		if got := reachesApply(c.action, c.hasOpen); got != c.want {
			t.Errorf("reachesApply(%q, %v) = %v, beklenen %v", c.action, c.hasOpen, got, c.want)
		}
	}
}

// Kablolama: scan() faz 1 süzgeci saf işlevi kullanır; çıplak "none" elemesi
// scan gövdesine geri gelmemeli. Uygulama döngüsünde kümeye KATILAN satır
// (merged) touch'tan ÖNCE atlanmalı — touch onu tam-satır replace ile yeniden
// "open" yazardı.
func TestScanSendsBandRowsToApply(t *testing.T) {
	src := detectorSrc(t)
	i := strings.Index(src, "func (d *Detector) scan(")
	if i < 0 {
		t.Fatal("scan yok")
	}
	body := src[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "if !reachesApply(oc.Action, hasOpen) {") {
		t.Error("faz 1 süzgeci reachesApply kullanmalı")
	}
	if strings.Contains(body, `oc.Action == "skip" || oc.Action == "none"`) {
		t.Error("faz 1 \"none\"u çıplak eliyor — histerezis touch'ı yine ölü kod olur")
	}
	iMerged := strings.Index(body, "if hasEx && merged[ex.ID] {")
	iApply := strings.Index(body, "d.applyOutcome(ctx, pa.service, pa.metric, pa.oc, snap, sens)")
	if iMerged < 0 || iApply < 0 || iMerged > iApply {
		t.Errorf("merged satır kontrolü applyOutcome'dan ÖNCE olmalı (merged=%d apply=%d)", iMerged, iApply)
	}
}
