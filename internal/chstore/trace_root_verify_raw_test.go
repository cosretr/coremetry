package chstore

// trace_root_verify_raw_test.go — v0.10.755: MV-gap gününde kök doğrulaması
// ham spans'tan. Saf SQL şekli (kaynak tablo, id sınırı, zaman sınırı,
// tanım-duyarlı HAVING, bütçe) + ULAŞILABİLİRLİK: iki post-filter çağrısı
// ve tek-geçiş alt sorgusu f.MVGap'e göre dallanır; tuple kontrolü de
// tanım-duyarlı (eskiden sabit strict).

import (
	"os"
	"strings"
	"testing"
)

func TestRootVerifyRawSQLShape(t *testing.T) {
	q := rootVerifyRawSQL(3, TraceRootDefStrict)
	for _, want := range []string{"FROM spans", "trace_id IN (?,?,?)", "time >= toDateTime(?, 'UTC')", "time < toDateTime(?, 'UTC')", "GROUP BY trace_id", "HAVING " + strictRootRaw, "max_execution_time = 10"} {
		if !strings.Contains(q, want) {
			t.Errorf("strict sorgu %q içermeli:\n%s", want, q)
		}
	}
	if strings.Contains(q, "trace_summary_5m") || strings.Contains(q, entrySpanRaw) {
		t.Errorf("strict ham sorgu MV'ye ya da giriş yüklemine dokunmamalı:\n%s", q)
	}
	e := rootVerifyRawSQL(1, TraceRootDefEntry)
	if !strings.Contains(e, strictRootRaw+" OR "+entrySpanRaw) {
		t.Errorf("entry tanımı giriş yüklemini OR'lamalı:\n%s", e)
	}
	sub := rootSubqueryRawSQL(TraceRootDefStrict)
	if !strings.HasPrefix(sub, "trace_id GLOBAL IN (") || !strings.Contains(sub, "FROM spans") || strings.Contains(sub, "trace_id IN (") {
		t.Errorf("tek-geçiş alt sorgusu pencere-sınırlı GLOBAL IN olmalı (id listesi yok):\n%s", sub)
	}
}

func TestRootVerifyRawReachable(t *testing.T) {
	src, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if n := strings.Count(s, "s.filterRootTracesAt(ctx, cands, f.From, f.To, f.MVGap)"); n != 2 {
		t.Errorf("filterRootTracesAt iki çağrıda f.MVGap geçirmeli, %d", n)
	}
	if !strings.Contains(s, "rootSubqueryRawSQL(s.TraceRootDef())") {
		t.Error("tek-geçiş HAVING'i gap gününde ham alt sorguya dönmüyor")
	}
	if !strings.Contains(s, "return s.filterRootTracesRaw(ctx, ids, from, to)") {
		t.Error("filterRootTracesAt gap gününde ham ikize gitmiyor")
	}
	if !strings.Contains(s, "rootCheckSQL(len(buckets), len(ids), s.rootHavingMV())") {
		t.Error("tuple kontrolü tanım-duyarlı HAVING almalı (eskiden sabit strict)")
	}
}
