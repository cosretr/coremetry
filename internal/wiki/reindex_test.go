package wiki

// v0.10.1127 — inceleme F1: yeniden jetonlamada yazılamayan sayfa kalıcı
// olarak ATLANMAZ (yeniden deneme listesi, en çok 3 deneme); sürüm ancak
// liste boşken ya da kalanlar tükenmişken kaydedilir. Ayrıca: parça sayısı
// aynıyken sayfa satırı yazılmaz; yarım iş SyncDue ile art arda geçiş ister.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReindexRetriesFailedPageUntilSuccess(t *testing.T) {
	s, st := sboxCorpus(t)
	oldTokenize(st)
	fails := 2
	st.failWrite = func(p PageRecord) error {
		if p.Path == "/Redis/Kurulum" && fails > 0 {
			fails--
			return errors.New("geçici yazım hatası")
		}
		return nil
	}
	ctx := context.Background()
	upserts := st.upserts

	res, _ := s.Sync(ctx)
	if res.TokenizerVersion == tokenizerVersion || len(res.ReindexRetry) != 1 || res.ReindexRetry[0].Attempts != 1 || !res.ReindexScanned {
		t.Fatalf("ilk geçiş: başarısız sayfa listede, sürüm kaydedilmemeli: %+v", res)
	}
	if !SyncDue(Config{Enabled: true}, res, time.UnixMilli(res.LastStartedAt).Add(reindexPassGap)) ||
		SyncDue(Config{Enabled: true}, res, time.UnixMilli(res.LastStartedAt).Add(time.Minute)) {
		t.Error("yarım yeniden jetonlama reindexPassGap sonra art arda geçiş istemeli (daha erken değil)")
	}
	res, _ = s.Sync(ctx)
	if res.TokenizerVersion == tokenizerVersion || len(res.ReindexRetry) != 1 || res.ReindexRetry[0].Attempts != 2 {
		t.Fatalf("ikinci geçiş: yalnız bekleyen sayfa yeniden denenir: %+v", res)
	}
	res, _ = s.Sync(ctx)
	if res.TokenizerVersion != tokenizerVersion || len(res.ReindexRetry) != 0 || res.ReindexScanned || res.Reindexed != 1 {
		t.Fatalf("üçüncü geçiş başarılı: sürüm kaydedilmeli: %+v", res)
	}
	if !chunkHas(st, "/Redis/Kurulum", "sunucu") {
		t.Error("yeniden denenen sayfa yeni jetonlarla yazılmalı")
	}
	if st.upserts != upserts {
		t.Errorf("parça sayısı aynıyken sayfa satırı yazılmamalı (yalnız parçalar): %d upsert", st.upserts-upserts)
	}
	if SyncDue(Config{Enabled: true}, res, time.UnixMilli(res.LastStartedAt).Add(reindexPassGap)) {
		t.Error("iş bitince art arda geçiş istenmemeli")
	}
}

func TestReindexExhaustedPageRecordedAsError(t *testing.T) {
	s, st := sboxCorpus(t)
	oldTokenize(st)
	st.failWrite = func(p PageRecord) error {
		if p.Path == "/Kafka/Kümeler" {
			return errors.New("kalıcı yazım hatası")
		}
		return nil
	}
	ctx := context.Background()
	var res Status
	for i := 0; i < reindexMaxAttempts; i++ {
		res, _ = s.Sync(ctx)
		if i < reindexMaxAttempts-1 && res.TokenizerVersion == tokenizerVersion {
			t.Fatalf("geçiş %d: deneme hakkı varken sürüm kaydedilmemeli", i)
		}
	}
	if res.TokenizerVersion != tokenizerVersion || len(res.ReindexRetry) != 0 {
		t.Fatalf("tükenen sayfa sürümü bekletmemeli: %+v", res)
	}
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e, "/Kafka/Kümeler") && strings.Contains(e, "3 denemede") {
			found = true
		}
	}
	if !found {
		t.Errorf("tükenen sayfa durum hatası olmalı: %q", res.Errors)
	}
}

func TestReindexRetryListCapped(t *testing.T) {
	if reindexRetryMax > 500 || reindexMaxAttempts < 2 {
		t.Fatal("yeniden deneme listesi sınırlı, en az iki deneme")
	}
	s, st := sboxCorpus(t)
	oldTokenize(st)
	st.failWrite = func(PageRecord) error { return errors.New("x") }
	res, _ := s.Sync(context.Background())
	if len(res.ReindexRetry) != 4 {
		t.Fatalf("her başarısız sayfa listede: %d", len(res.ReindexRetry))
	}
}
