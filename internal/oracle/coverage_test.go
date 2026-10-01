package oracle

// v0.10.999 — özne kapsamı raporu (Oracle odak "2"): sınıflama çözücünün
// kalıcı basamaklarını aynalar; çözülmeyen satırlar nedene göre bölünür.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestBuildCoverage(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Hour).UnixNano()
	learned := LearnedMap{V: 1, Entries: map[string]*LearnedEntry{
		"OP_OK":      {Service: "bsa-payments-prod", Hits: 9, Total: 10, LastConfirmed: fresh},
		"OP_DEAD":    {Service: "bsa-retired-prod", Hits: 5, Total: 5, LastConfirmed: fresh},
		"OP_MULTI":   {Service: "bsa-a-prod", Hits: 4, Total: 10, LastConfirmed: fresh}, // %40 < %70
		"OP_FEW":     {Service: "bsa-b-prod", Hits: 2, Total: 2, LastConfirmed: fresh},  // < 3 teyit
		"OP_DEADPOD": {Service: "bsa-retired-prod", Hits: 5, Total: 5, LastConfirmed: fresh},
	}}
	alive := map[string]bool{"bsa-payments-prod": true, "bsa-mobile-login-prod": true}
	obs := []chstore.OracleOpObs{
		{Operation: "OP_OK", Rows: 400, WithTrace: 400},
		{Operation: "OP_POD", Rows: 300, WithTrace: 0, TopInstance: "bsa-mobile-login-prod-7b9949bb74-l4bg5"},
		{Operation: "OP_HOST", Rows: 120, WithTrace: 0, TopInstance: "WMOBAPPP84", TopHost: "WMOBAPPP84"},
		{Operation: "OP_NOTFOUND", Rows: 80, WithTrace: 75, TopInstance: "WMOBAPPP85"},
		{Operation: "OP_MULTI", Rows: 50, WithTrace: 50},
		{Operation: "OP_DEAD", Rows: 30, WithTrace: 30},
		{Operation: "OP_FEW", Rows: 12, WithTrace: 12},
		{Operation: "", Rows: 5},
		{Operation: "OP_DEADPOD", Rows: 3, TopInstance: "bsa-mobile-login-prod-7b9949bb74-zzzzz"},
	}
	rep := BuildCoverage(obs, chstore.OracleOpTotals{Rows: 1100, WithTrace: 567, Ops: 12}, learned, alive, now, 4)

	if rep.RowsTotal != 1100 || rep.OpsTotal != 12 || rep.RowsListed != 1000 || rep.OpsListed != 9 || !rep.AliveKnown {
		t.Fatalf("toplamlar: %+v", rep)
	}
	// Çözülen: OP_OK (öğrenilmiş) 400 + OP_POD 300 + OP_DEADPOD (ölü servis ama pod canlı) 3.
	if rep.RowsResolved != 703 || rep.RowsLearned != 400 || rep.RowsPod != 303 || rep.OpsResolved != 3 {
		t.Errorf("çözülen: %+v", rep)
	}
	want := map[string]uint64{
		ReasonNoTraceID: 120, ReasonTraceNotFound: 80, ReasonMultiService: 50,
		ReasonDeadService: 30, ReasonUnconfirmed: 12, ReasonNoOperation: 5,
	}
	if len(rep.ByReason) != len(want) {
		t.Errorf("neden kümesi: %v", rep.ByReason)
	}
	for k, v := range want {
		if rep.ByReason[k] != v {
			t.Errorf("neden %s: %d, istenen %d", k, rep.ByReason[k], v)
		}
	}
	// Çözülmeyenler satır sayısına göre, topN = 4.
	var ops []string
	for _, u := range rep.Unresolved {
		ops = append(ops, u.Operation)
	}
	if got := len(rep.Unresolved); got != 4 || ops[0] != "OP_HOST" || ops[1] != "OP_NOTFOUND" || ops[2] != "OP_MULTI" || ops[3] != "OP_DEAD" {
		t.Errorf("çözülmeyen sırası: %v", ops)
	}
	host := rep.Unresolved[0]
	if host.Reason != ReasonNoTraceID || host.PodLike || host.Instance != "WMOBAPPP84" || host.Host != "WMOBAPPP84" {
		t.Errorf("host adlı instance pod sayılmaz: %+v", host)
	}
	if m := rep.Unresolved[2]; m.Reason != ReasonMultiService || m.Service != "bsa-a-prod" || m.Votes != "4/10" {
		t.Errorf("çok servisli op: %+v", m)
	}
	if d := rep.Unresolved[3]; d.Reason != ReasonDeadService || d.Service != "bsa-retired-prod" {
		t.Errorf("ölü servis: %+v", d)
	}

	// Canlı liste OKUNAMADI: pod basamağı doğrulanamaz (uydurma yok), öğrenilmiş canlı varsayılır.
	blind := BuildCoverage(obs, chstore.OracleOpTotals{Rows: 1100, Ops: 12}, learned, nil, now, 0)
	if blind.AliveKnown || blind.RowsPod != 0 || blind.RowsLearned != 400+30+3 || blind.ByReason[ReasonDeadService] != 0 {
		t.Errorf("canlı liste yokken: %+v", blind)
	}
	if blind.ByReason[ReasonNoTraceID] != 300+120 {
		t.Errorf("pod doğrulanamayınca op trace'siz çözülmemiş sayılır: %v", blind.ByReason)
	}
	// topN 0 = kesme yok; boş girdi nil değil boş dilim.
	if len(blind.Unresolved) != 6 {
		t.Errorf("topN 0 → tüm çözülmeyenler: %d", len(blind.Unresolved))
	}
	empty := BuildCoverage(nil, chstore.OracleOpTotals{}, LearnedMap{}, alive, now, 10)
	if empty.Unresolved == nil || empty.ByReason == nil || empty.RowsResolved != 0 {
		t.Errorf("boş rapor: %+v", empty)
	}
}

// Süresi geçmiş (30 günden eski teyit) girdi onaylı sayılmaz.
func TestCoverageExpiredLearnedIsUnconfirmed(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour).UnixNano()
	c := classifyCoverageOp(chstore.OracleOpObs{Operation: "OP", Rows: 9, WithTrace: 9},
		&LearnedEntry{Service: "svc", Hits: 9, Total: 9, LastConfirmed: old}, map[string]bool{"svc": true}, now)
	if c.Status != CoverageUnresolved || c.Reason != ReasonUnconfirmed || c.Service != "svc" {
		t.Errorf("süresi geçmiş girdi: %+v", c)
	}
}
