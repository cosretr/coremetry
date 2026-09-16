package chstore

import (
	"strings"
	"testing"
)

// v0.10.733 — kök tanımı yüklemleri iki tanımı da gezer (birim-karışımı
// dersi: değer+tanım alan her şablon her tanımı test eder). entry, strict'i
// KAPSAR (OR); entry kolonu yokken MV entry strict'e düşer.
func TestTraceRootDefPredicates(t *testing.T) {
	cases := []struct {
		def      TraceRootDef
		entryCol bool
		mvHas    []string
		mvNot    []string
		rawHas   []string
		rawNot   []string
		predHas  []string
		predNot  []string
	}{
		{TraceRootDefStrict, true,
			[]string{"root_service_state"}, []string{"entry_service_state", " OR "},
			[]string{"parent_id = ''", "name != ''"}, []string{"kind = 'server'"},
			[]string{"parent_id = ''"}, []string{"kind IN"}},
		{TraceRootDefEntry, true,
			[]string{"root_service_state", " OR ", "entry_service_state"}, nil,
			[]string{"parent_id = ''", " OR ", "kind = 'server'", "kind = 'consumer'", "service_name != 'unknown'"}, nil,
			[]string{"parent_id = ''", " OR ", "kind IN ('server', 'consumer')"}, nil},
		// Kolon yokken entry MV strict'e DÜŞER (yanlış SQL yerine dar tanım); ham yol etkilenmez.
		{TraceRootDefEntry, false,
			[]string{"root_service_state"}, []string{"entry_service_state"},
			[]string{" OR ", "kind = 'server'"}, nil,
			[]string{" OR "}, nil},
	}
	check := func(t *testing.T, what, got string, has, not []string) {
		t.Helper()
		for _, h := range has {
			if !strings.Contains(got, h) {
				t.Errorf("%s: %q içermeli: %s", what, h, got)
			}
		}
		for _, n := range not {
			if strings.Contains(got, n) {
				t.Errorf("%s: %q içermemeli: %s", what, n, got)
			}
		}
	}
	for _, c := range cases {
		check(t, string(c.def)+" mv", rootHavingMV(c.def, c.entryCol), c.mvHas, c.mvNot)
		check(t, string(c.def)+" raw", rootHavingRaw(c.def), c.rawHas, c.rawNot)
		check(t, string(c.def)+" pred", rootSpanPredicateFor(c.def), c.predHas, c.predNot)
	}
}

func TestTraceRootDefParseAndStore(t *testing.T) {
	if ParseTraceRootDef("") != TraceRootDefStrict || ParseTraceRootDef("x") != TraceRootDefStrict {
		t.Fatal("bilinmeyen/boş → strict")
	}
	if ParseTraceRootDef("entry") != TraceRootDefEntry {
		t.Fatal("entry tanınmalı")
	}
	var s Store
	if s.TraceRootDef() != TraceRootDefStrict {
		t.Fatal("sıfır değer strict")
	}
	s.SetTraceRootDef(TraceRootDefEntry)
	if s.TraceRootDef() != TraceRootDefEntry {
		t.Fatal("entry yayınlanmalı")
	}
	s.SetTraceRootDef("bozuk")
	if s.TraceRootDef() != TraceRootDefStrict {
		t.Fatal("bozuk değer strict'e döner")
	}
	// Strip yüklemi tablo ile aynı tanımı okur (entry'de giriş span'i kök).
	if !strings.Contains(rootSpanPredicateFor(s.TraceRootDef()), "parent_id") {
		t.Fatal("strict yüklem parent_id taşımalı")
	}
}
