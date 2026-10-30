package api

// v0.10.1678 — operatör-bildirimi (CoSRE sohbeti, servisin HTTP endpoint'i
// soruldu): cevap "⚠ Kaynak: hiçbir telemetri aracı çağrılmadı" uyarısını
// taşıyordu, oysa koşu 11 araç adımı gösterdi. Kök neden: endpoint/operasyon
// ön-getirmesi (chatOperationPrefetch) verisini konuşmaya tohumluyor, model
// döngüde araç çağırmadan cevaplıyor; künye yalnız döngüde veri döndüren
// araçları (calledTools) sayıyordu. İkinci yanlış dal: döngüde çağrı yapıldı
// ama hiçbiri veri döndürmedi → yine "hiç çağrılmadı".

import (
	"os"
	"strings"
	"testing"
)

func TestLoopSourceNote(t *testing.T) {
	endpointPre := &opPrefetch{Operation: "GET /api/orders", Drill: "kırılım", clusterText: "küme"}
	cases := []struct {
		name       string
		followUp   bool
		prefetched []string
		data       []string
		attempted  int
		wantIn     []string
		wantNotIn  []string
	}{
		{"operatör vakası: ön-getirme verisi, döngüde araç yok",
			false, endpointPre.seededTools(), nil, 0,
			[]string{"Kaynak: operation_prefetch, trace_drill, cluster_compare", "(3 araç)"},
			[]string{"hiçbir telemetri aracı çağrılmadı", "canlı veriye değil"}},
		{"ön-getirme + döngü aracı birlikte, tekil",
			false, (&opPrefetch{}).seededTools(), []string{"search_traces", "operation_prefetch"}, 1,
			[]string{"operation_prefetch, search_traces", "(2 araç)"}, []string{"⚠"}},
		{"döngüde çağrı var, hiçbiri veri döndürmedi",
			false, nil, nil, 4,
			[]string{"4 araç çağrısı yapıldı", "veri döndürmedi"},
			[]string{"hiçbir telemetri aracı çağrılmadı"}},
		{"gerçekten sıfır araç — uyarı aynen",
			false, nil, nil, 0,
			[]string{"hiçbir telemetri aracı çağrılmadı", "canlı veriye değil"}, nil},
		{"takipte sıfır araç — takip künyesi aynen",
			true, nil, nil, 0,
			[]string{"bu turda hiçbir telemetri aracı çağrılmadı"}, nil},
		{"takipte ön-getirme varsa genel künye",
			true, (&opPrefetch{}).seededTools(), nil, 0,
			[]string{"Kaynak: operation_prefetch (1 araç)"}, []string{"hiçbir"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := loopSourceNoteTR(c.followUp, c.prefetched, c.data, c.attempted)
			for _, w := range c.wantIn {
				if !strings.Contains(got, w) {
					t.Errorf("%q yok: %q", w, got)
				}
			}
			for _, w := range c.wantNotIn {
				if strings.Contains(got, w) {
					t.Errorf("%q olmamalı: %q", w, got)
				}
			}
		})
	}
}

func TestOpPrefetchSeededTools(t *testing.T) {
	var nilPre *opPrefetch
	if got := nilPre.seededTools(); got != nil {
		t.Errorf("nil ön-getirme → nil, got %v", got)
	}
	if got := (&opPrefetch{}).seededTools(); len(got) != 1 || got[0] != opPrefetchTool {
		t.Errorf("yalnız özet → [operation_prefetch], got %v", got)
	}
}

// Bağlantı pini: iki döngü çıkışı da künyeyi ön-getirme + deneme sayısıyla
// kurar; eski çağrı (yalnız calledTools) kalmaz.
func TestLoopSourceNoteWired(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if n := strings.Count(src, "loopSourceNoteTR(fuHadExplain, opPre.seededTools(), calledTools, executedN+skippedN)"); n != 2 {
		t.Errorf("iki çıkış da loopSourceNoteTR'den geçmeli (got %d)", n)
	}
	if strings.Contains(src, "chatSourceNoteTR(calledTools)") {
		t.Error("eski künye çağrısı (ön-getirmeyi saymayan) hâlâ duruyor")
	}
}
