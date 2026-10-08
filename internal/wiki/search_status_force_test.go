package wiki

// v0.10.1126 — operatör: "Aramayı test et" "Canlı arama: 5 sonuç (AND
// sorgusu) · api-version 7.0" dedi, durum kartı "henüz denenmedi"de kaldı.
// Kök neden (iki parça): (1) kart durumu yalnız sayfa açılışında okunuyordu
// (frontend; test yanıtı artık tazelenmiş durumu taşır), (2) recordSearch'ün
// kısma imzası yalnız BU pod'un son yazdığıyla kıyaslanıyordu — arada başka
// pod farklı sonuç yazdıysa aynı imzalı başarı 10 dk blobu güncellemiyordu.
// Açık test (force) her zaman paylaşılan blobu yazar.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDiagnoseForceAlwaysRecordsSharedSearchStatus(t *testing.T) {
	f := runbookWiki()
	f.search = "present"
	dv, _ := newFakeDevOps(t, f)
	st := newMemStore()
	pod := New(st, func() API { return dv }, nil)
	pod.Configure(Config{Enabled: true})
	ctx := context.Background()
	if _, err := pod.Search(ctx, "ledger-oncall", "", 5, 1); err != nil {
		t.Fatal(err)
	}
	// Başka bir pod araya "error" yazdı (bu pod'un bellekteki imzası değişmedi).
	stale, _ := json.Marshal(SearchStatus{State: "error", At: 1, Class: "server_error", HTTPStatus: 503})
	_ = st.PutSetting(ctx, SearchStatusKey, stale)

	// Aynı imzalı kendiliğinden arama kısılır (10 dk) — eski davranış korunur.
	if _, err := pod.Search(ctx, "ledger-oncall", "", 5, 1); err != nil {
		t.Fatal(err)
	}
	if got := readSearchStatus(t, st); got.State != "error" {
		t.Fatalf("kısma: kendiliğinden arama blobu her seferinde yazmamalı: %+v", got)
	}
	// Yöneticinin testi (force) HER ZAMAN yazar.
	if _, err := pod.Diagnose(ctx, "ledger-oncall", 5); err != nil {
		t.Fatal(err)
	}
	got := readSearchStatus(t, st)
	if got.State != SearchAvailable || got.APIVersion == "" || got.Hits == 0 {
		t.Fatalf("test araması paylaşılan durumu tazelemeli: %+v", got)
	}
	// Başka pod (kart hangi pod'a düşerse) aynı durumu görür.
	other := New(st, func() API { return dv }, nil)
	other.Configure(Config{Enabled: true})
	if s := other.Status(ctx, true); s.Search != SearchAvailable || s.SearchLast == nil || s.SearchLast.Hits == 0 {
		t.Fatalf("diğer pod kartı: %s %+v", s.Search, s.SearchLast)
	}
}

func readSearchStatus(t *testing.T, st *memStore) SearchStatus {
	t.Helper()
	raw, _ := st.GetSetting(context.Background(), SearchStatusKey)
	var ss SearchStatus
	if err := json.Unmarshal(raw, &ss); err != nil {
		t.Fatalf("blob: %v %s", err, raw)
	}
	return ss
}
