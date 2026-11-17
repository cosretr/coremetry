package api

// exception_list_reason_msg_test.go — v0.10.2303: Exceptions listesinin
// öncelik gerekçesi anahtarı yalnız liste ucunun satırında. Grup başka
// yollarda BÜTÜN serileşir (deploy raporu newErrors, tekil GET, araç /
// istem yolları, bildirimci aynı merdivenin 2-dönüşlü sarmalayıcısını
// çağırır): oralarda bayt aynı kalmalı.

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func reasonMsgGroup() chstore.ExceptionGroup {
	return chstore.ExceptionGroup{Fingerprint: "fp-1", Type: "java.lang.IllegalStateException", Message: "boom",
		Service: "svc-a", State: chstore.ExStateNew, Occurrences: 600,
		FirstSeen: prioGoldenNow.Add(-48 * time.Hour).UnixNano(), LastSeen: prioGoldenNow.Add(-time.Hour).UnixNano()}
}

// Grup JSON'unda anahtar yok: anahtar dolu / boş bayt eşit.
func TestExceptionGroupJSONCarriesNoReasonMsg(t *testing.T) {
	g := reasonMsgGroup()
	g.Priority, g.PriorityReason, g.PriorityReasonMsg = exceptionPriorityAtMsg(g, chstore.DefaultExceptionTriage(), prioGoldenNow)
	if g.PriorityReasonMsg == nil {
		t.Fatal("merdiven anahtar koymadı (test girdisi)")
	}
	withMsg, _ := json.Marshal(g)
	g.PriorityReasonMsg = nil
	without, _ := json.Marshal(g)
	if !bytes.Equal(withMsg, without) || bytes.Contains(withMsg, []byte("priorityReasonMsg")) || bytes.Contains(withMsg, []byte("prio.")) {
		t.Fatalf("grup JSON'una anahtar sızdı:\n%s", withMsg)
	}
	// Deploy raporu grubu dilim içinde serileştirir — aynı sonuç.
	a, _ := json.Marshal(ServiceReportSection{NewErrors: []chstore.ExceptionGroup{g}})
	g.PriorityReasonMsg = chstore.NewStdMsg("prio.ex.steady")
	b, _ := json.Marshal(ServiceReportSection{NewErrors: []chstore.ExceptionGroup{g}})
	if !bytes.Equal(a, b) {
		t.Fatalf("deploy raporu baytı değişti:\n%s\n%s", a, b)
	}
}

// Liste satırı = grubun baytı + yalnız priorityReasonMsg; anahtar boşken bayt eşit.
func TestExceptionListRowAddsOnlyReasonMsg(t *testing.T) {
	g := reasonMsgGroup()
	g.Priority, g.PriorityReason, g.PriorityReasonMsg = exceptionPriorityMsgAt(g)
	plain := g
	plain.PriorityReasonMsg = nil
	groupJSON, _ := json.Marshal(plain)
	rows := exceptionListRows([]chstore.ExceptionGroup{plain})
	rowJSON, _ := json.Marshal(rows[0])
	if !bytes.Equal(groupJSON, rowJSON) {
		t.Fatalf("anahtarsız satır gruptan farklı:\n%s\n%s", groupJSON, rowJSON)
	}
	rowJSON, _ = json.Marshal(exceptionListRows([]chstore.ExceptionGroup{g})[0])
	var got, want map[string]any
	_ = json.Unmarshal(rowJSON, &got)
	_ = json.Unmarshal(groupJSON, &want)
	m, ok := got["priorityReasonMsg"].(map[string]any)
	if !ok || m["k"] != g.PriorityReasonMsg.K {
		t.Fatalf("satırda anahtar yok: %s", rowJSON)
	}
	delete(got, "priorityReasonMsg")
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if !bytes.Equal(gb, wb) {
		t.Fatalf("satır anahtar dışında gruptan farklı:\n%s\n%s", gb, wb)
	}
	// Gerekçe metni 2-dönüşlü sarmalayıcıyla (bildirimci, arama, replay) aynı.
	_, r2 := exceptionPriorityAt(plain, chstore.DefaultExceptionTriage(), prioGoldenNow)
	if r2 != g.PriorityReason {
		t.Fatalf("gerekçe ayrıştı: %q / %q", r2, g.PriorityReason)
	}
	if out := exceptionListRows(nil); out == nil || len(out) != 0 {
		t.Fatal("nil girdi boş dizi olmalı")
	}
	b, _ := json.Marshal((&exceptionPage{}).body(nil, 0)["items"])
	if string(b) != "[]" {
		t.Fatalf("boş sayfa items = %s", b)
	}
}

func exceptionPriorityMsgAt(g chstore.ExceptionGroup) (string, string, *chstore.StdMsg) {
	return exceptionPriorityAtMsg(g, chstore.DefaultExceptionTriage(), prioGoldenNow)
}
