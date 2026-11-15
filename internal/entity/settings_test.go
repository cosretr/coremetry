package entity

import (
	"context"
	"strings"
	"testing"
	"time"
)

// v0.10.2214 (S1, K1) — bayrak ve dört vida sabit; blobda yalnız backfill
// durumu. Sabitler plan K1'in değerleri: 60 s, 10 dk, 24 sa, 4.
func TestConstantsPinned(t *testing.T) {
	if SyncInterval != time.Minute || PodGap != 10*time.Minute || StaleAfter != 24*time.Hour || ParallelClusters != 4 {
		t.Fatalf("K1 sabitleri: %v %v %v %d", SyncInterval, PodGap, StaleAfter, ParallelClusters)
	}
	if RefreshEvery != 24*time.Hour {
		t.Fatalf("RefreshEvery 24 saat olmalı (v0.10.2225): %v", RefreshEvery)
	}
}

type rawStore struct{ raw []byte }

func (r *rawStore) GetEntitySettingsRaw(context.Context) ([]byte, error) { return r.raw, nil }
func (r *rawStore) PutEntitySettingsRaw(_ context.Context, raw []byte) error {
	r.raw = raw
	return nil
}

// Eski sürümün yazdığı blob (enabled=false + vidalar) güvenle yüklenir:
// bilinmeyen alanlar yok sayılır, backfill durumu korunur, katman açık kalır.
func TestStaleStoredSettingsIgnored(t *testing.T) {
	st := &rawStore{raw: []byte(`{"enabled":false,"syncInterval":"1s","podGap":"100h","staleAfter":"x","parallelClusters":99,"updatedAt":5,"backfillUntil":1700000000000,"backfillValue":" edge-a "}`)}
	svc := NewSettingsService()
	if err := svc.LoadPersisted(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	r := svc.Resolved()
	if r.BackfillValue != "edge-a" || !r.BackfillUntil.Equal(time.UnixMilli(1700000000000)) {
		t.Fatalf("backfill durumu korunmalı: %+v", r)
	}
	if err := svc.SavePersisted(context.Background(), st, svc.Current()); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"enabled", "syncInterval", "podGap", "staleAfter", "parallelClusters"} {
		if strings.Contains(string(st.raw), `"`+k+`"`) {
			t.Fatalf("yeniden yazımda %q düşmeli: %s", k, st.raw)
		}
	}
}

// v0.10.129 — kendi reload sinyali (ya da 30 s poll) henüz replike olmamış
// ESKİ blobu okuyup bellekteki yeni değeri geri alıyordu. Blob bir
// updatedAt taşır; LoadPersisted daha eski bir blobu yeninin ÜSTÜNE yazmaz.
func TestLoadPersistedDoesNotRegress(t *testing.T) {
	svc := NewSettingsService()
	svc.Configure(Settings{BackfillValue: "new", UpdatedAt: 200})
	if svc.applyLoaded(Settings{BackfillValue: "old", UpdatedAt: 100}) {
		t.Fatal("eski damgalı blob bellekteki yeniyi ezmemeli")
	}
	if svc.Current().BackfillValue != "new" {
		t.Fatal("değer geri alındı")
	}
	if !svc.applyLoaded(Settings{BackfillValue: "newer", UpdatedAt: 300}) || svc.Current().BackfillValue != "newer" {
		t.Fatal("daha yeni blob uygulanmalı")
	}
}
