package wiki

// v0.10.1127 — inceleme F3: >300 "sunucusu" tuzağı + tek tanımlayıcı parçası.
// Eski aday sırası (eşleşen FARKLI jeton sayısı) tuzakları (sunucusu / sunucu /
// sunuc … 3-4 biçim) tanımlayıcı parçasının (1 biçim) önüne koyup 300'lük
// tavanın dışına iterdi. Bellek-içi depo CandidatePriority ile CH ORDER BY'ının
// aynısını uygular (SQL şekli chstore/wiki_sql_test.go'da pinli; ifade
// `clickhouse local` ile de doğrulandı). Adlar sentetik.

import (
	"context"
	"fmt"
	"testing"
)

func TestCandidateOrderKeepsRareIdentifierPastCommonStem(t *testing.T) {
	st := newMemStore()
	s := New(st, func() API { return nil }, nil)
	s.Configure(Config{Enabled: true, Mode: ModeSync})
	ctx := context.Background()
	for i := 0; i < 320; i++ {
		p := PageRecord{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki",
			Path: fmt.Sprintf("/A-Notlar/%03d", i), Title: "Not"}
		p.Content = "# Bakım\nUygulama sunucusu bakım penceresinde sunucuları sırayla yeniden başlatın."
		if err := st.UpsertWikiPage(ctx, p, BuildChunks(p.Title, p.Content), 0); err != nil {
			t.Fatal(err)
		}
	}
	target := PageRecord{Project: "Platform", WikiID: "w1", WikiName: "Platform.wiki", Path: "/Z-Envanter", Title: "Envanter"}
	target.Content = "# Makineler\n| Ad | IP |\n|---|---|\n| WSBXAKFP01 | 10.0.0.11 |\n| WSBXAKFP02 | 10.0.0.12 |\n"
	if err := st.UpsertWikiPage(ctx, target, BuildChunks(target.Title, target.Content), 0); err != nil {
		t.Fatal(err)
	}

	terms := QueryTerms("WSBXAKFP01 sunucusu")
	stats, _ := st.WikiTermStats(ctx, ExpandTerms(terms), "")
	cands, err := st.WikiCandidates(ctx, NewCandidateQuery(terms, stats), "", candidateLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != candidateLimit || cands[0].Path != "/Z-Envanter" {
		t.Fatalf("tanımlayıcı parçası aday tavanının içinde (önde) olmalı: %d aday, ilk %q", len(cands), cands[0].Path)
	}
	res, err := s.SearchWith(ctx, "WSBXAKFP01 sunucusu", "", SearchOptions{Limit: 3, PerPage: 1, Live: LiveOff})
	if err != nil || len(res.Hits) == 0 || res.Hits[0].Path != "/Z-Envanter" {
		t.Fatalf("tanımlayıcı sayfası ilk sırada olmalı: %+v %v", res.Hits, err)
	}
}

func TestCandidatePrioritySurfaceStemNone(t *testing.T) {
	terms := []string{"sunuculari"}
	q := NewCandidateQuery(terms, Stats{N: 10, DF: make([]uint64, len(ExpandTerms(terms)))})
	surface := CandidatePriority([]string{"sunuculari", "sunucu"}, q)
	stem := CandidatePriority([]string{"sunucu"}, q)
	if !(surface > stem && stem > 0) || CandidatePriority([]string{"kafka"}, q) != 0 {
		t.Errorf("CandidatePriority: yüzey > kök > hiç: %v %v", surface, stem)
	}
	// Nadir terim (düşük df) yaygın terimden ağır basar.
	q2 := NewCandidateQuery([]string{"wsbxakfp01", "sunucu"}, Stats{N: 1000, DF: []uint64{1, 900, 900}})
	if CandidatePriority([]string{"wsbxakfp01"}, q2) <= CandidatePriority([]string{"sunucu", "sunuc"}, q2) {
		t.Error("nadir tanımlayıcı yaygın kökten önce gelmeli")
	}
}
