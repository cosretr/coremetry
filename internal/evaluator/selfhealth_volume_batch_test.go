package evaluator

// selfhealth_volume_batch_test.go — v0.10.1039: batch servisler
// self-volume-spike açmaz.
//
// Operatör (prod): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
//
// Pinlenen sözleşme: süzgeç saatlik önbellekten SONRA ve her tikte uygulanır
// (ayar değişikliği bir sonraki tikte etkili; önbellek bozulmaz); kural
// kapsanmış (ok=true) döner ki reconcileSelfHealth açık batch satırını
// normal kapatma dalıyla kapatsın; batch olmayan servis aynen.
//
// MUTASYON KANITI (çalıştırıldı, geri alındı): selfVolumeSpike'taki
// dropBatchVolumeRows çağrısı kaldırılınca TestSelfVolumeSpikeBatchFilterAfterCache
// kızarır.

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func volRow(svc string) selfProblem {
	return volumeSpikeProblem(chstore.ServiceVolume{Service: svc, Cur: 1_200_000, Prev: 210_000}, chstore.DefaultSelfHealth())
}

func TestDropBatchVolumeRows(t *testing.T) {
	def := chstore.DefaultAnomalySensitivity()
	off := chstore.DefaultAnomalySensitivity()
	empty := []string{}
	off.BatchServicePatterns = &empty

	in := []selfProblem{volRow("orders-batch"), volRow("payments-api"), volRow("Nightly-BATCH-loader")}
	// Başka bir self-* kuralı (Service boş) asla süzülmez.
	in = append(in, selfProblem{id: selfIngestRuleID, ruleID: selfIngestRuleID})

	got := dropBatchVolumeRows(in, def)
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.id] = true
	}
	if ids[selfVolumeRuleID+":orders-batch"] || ids[selfVolumeRuleID+":Nightly-BATCH-loader"] {
		t.Fatalf("batch servisin hacim satırı süzülmedi: %v", ids)
	}
	if !ids[selfVolumeRuleID+":payments-api"] || !ids[selfIngestRuleID] {
		t.Fatalf("batch olmayan / başka kural satırı düştü: %v", ids)
	}
	if len(in) != 4 || in[0].service != "orders-batch" {
		t.Fatal("girdi dilimi yerinde değiştirildi (önbellek bozulur)")
	}
	if got := dropBatchVolumeRows(in, off); len(got) != 4 {
		t.Fatalf("kural kapalıyken %d/4 satır kaldı", len(got))
	}
}

// TestSelfVolumeSpikeBatchFilterAfterCache — önbellekli tik (CH'ye gitmez):
// süzgeç önbellekten sonra; ayar değişince saatlik yenilemeyi beklemeden
// etkili; önbellek ham ölçümü korur.
func TestSelfVolumeSpikeBatchFilterAfterCache(t *testing.T) {
	store := &chstore.Store{}
	e := &Evaluator{store: store}
	e.volCache = []selfProblem{volRow("orders-batch"), volRow("payments-api")}
	e.volAt = time.Now() // taze ölçüm: bu tik önbellekten sunulur

	// Ayar bu süreçte hiç doğrulanmadı → batch kuralı DEVRE DIŞI (varsayılan
	// liste yalnız tahmin; tahminle açık satır kapatılmaz — v0.10.1039 inceleme).
	got, ok := e.selfVolumeSpike(context.Background(), chstore.DefaultSelfHealth())
	if !ok || len(got) != 2 {
		t.Fatalf("doğrulanmamış ayarda süzgeç uygulandı: %+v", got)
	}

	store.SetAnomalySensitivity(chstore.DefaultAnomalySensitivity()) // doğrulandı: ["-batch"]
	got, ok = e.selfVolumeSpike(context.Background(), chstore.DefaultSelfHealth())
	if !ok {
		t.Fatal("kural kapsanmadı — açık batch satırı reconcile'da KAPANMAZ")
	}
	if len(got) != 1 || got[0].service != "payments-api" {
		t.Fatalf("varsayılan ayarda yalnız batch olmayan satır bekleniyordu: %+v", got)
	}

	// Operatör kuralı kapattı (boş liste) → bir sonraki tik, önbellek
	// yenilenmeden, batch satırı geri gelir.
	empty := []string{}
	off := chstore.DefaultAnomalySensitivity()
	off.BatchServicePatterns = &empty
	store.SetAnomalySensitivity(off)
	got, ok = e.selfVolumeSpike(context.Background(), chstore.DefaultSelfHealth())
	if !ok || len(got) != 2 {
		t.Fatalf("kural kapatılınca iki satır bekleniyordu (önbellek ham ölçümü korumalı): %+v", got)
	}

	// Geri açıldı → yine süzülür; önbellek iki satırı da taşımaya devam eder.
	store.SetAnomalySensitivity(chstore.DefaultAnomalySensitivity())
	got, _ = e.selfVolumeSpike(context.Background(), chstore.DefaultSelfHealth())
	if len(got) != 1 || len(e.volCache) != 2 {
		t.Fatalf("süzgeç önbelleği değiştirdi ya da geri açılınca uygulanmadı: got=%d cache=%d", len(got), len(e.volCache))
	}
}
