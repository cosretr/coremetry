package api

// inbox_team_kinds_test.go — v0.10.2120, Problems sayfası sadeleştirme S0
// (docs/frontend/problems-page-plan.md §6): karakterizasyon. Üretim kodu
// değişmedi; S1'in "Ekip" kolonu bu sözleşmeye dayanır.
//
// Ekip, satıra okuma anında servis kataloğundan (service_metadata) bağlanır
// ve bağlama ANAHTARI satırın Service alanıdır. Pinlenen iki şey:
//   1. üç kaynak türünün dönüştürücüsü (problem / exception / anomali) Service
//      alanını taşır — biri boş bırakırsa o türün satırı "sahipsiz" görünür;
//   2. zenginleştirme döngüsü türe göre DALLANMAZ (yalnız db öznesi kendi
//      sahiplik kuralına gider) — "ekip yalnız alarm satırlarında" gerilemesi
//      bu iddiayı kırar.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestInboxEveryKindCarriesTeamJoinKey(t *testing.T) {
	cases := []struct {
		name string
		it   InboxItem
	}{
		{"problem", problemToInbox(chstore.Problem{ID: "p1", Service: "payments-api", Status: "open"})},
		{"exception", exceptionToInbox(chstore.ExceptionGroup{Fingerprint: "fp1", Service: "checkout", Type: "java.lang.X", Occurrences: 9})},
		{"anomaly", anomalyToInbox(chstore.AnomalyEvent{ID: "a1", Kind: "log_volume", Service: "orders-api", Status: "active"})},
	}
	want := map[string]string{"problem": "payments-api", "exception": "checkout", "anomaly": "orders-api"}
	for _, c := range cases {
		if c.it.Service != want[c.name] {
			t.Errorf("%s: Service = %q, istenen %q (ekip bağlama anahtarı)", c.name, c.it.Service, want[c.name])
		}
	}
}

func TestInboxTeamEnrichmentIsKindAgnostic(t *testing.T) {
	src := readSrc(t, "inbox.go")
	start := strings.Index(src, "// Team enrichment — reuses the catalog read")
	end := strings.Index(src, "// Team filter — applies AFTER enrichment")
	if start < 0 || end < start {
		t.Fatal("ekip zenginleştirme bloğu bulunamadı")
	}
	block := src[start:end]
	for _, frag := range []string{
		"items[i].OwnerTeam = md.OwnerTeam",
		"items[i].SRETeam = md.SRETeam",
		"md, ok := mdMap[items[i].Service]",
	} {
		if !strings.Contains(block, frag) {
			t.Errorf("zenginleştirmede %q yok", frag)
		}
	}
	for _, kindGate := range []string{".Kind ==", ".Kind !=", `"exception"`, `"anomaly"`, `"problem"`} {
		if strings.Contains(block, kindGate) {
			t.Errorf("zenginleştirme türe göre dallanıyor (%q) — ekip her türde dolmalı", kindGate)
		}
	}
}
