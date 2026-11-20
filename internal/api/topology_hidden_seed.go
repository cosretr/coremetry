package api

// topology_hidden_seed.go — v0.10.2268: varsayılan gizli topoloji deseni
// kaynaktan çıktı. Kod varsayılanı artık BOŞ (topology.go
// defaultTopologyHiddenPatterns). Eski varsayılan gerçek bir eşleşme
// kuralıydı: düz kaldırmak, kaydı hiç yazılmamış mevcut kurulumlarda o
// kuyruk düğümlerini topolojiye geri getirirdi. Davranış değişmesin diye
// TEK SEFERLİK, LİDER-YALNIZ, İDEMPOTENT göç:
//
//	kayıt VAR (operatör ya da önceki koşu yazmış) → DOKUNMA
//	kayıt YOK + mevcut kurulum                   → eski varsayılanı yaz
//	kayıt YOK + taze kurulum                     → boş listeyi yaz
//
// İki yazım da mevcut `topology_hidden` kaydını doldurur; ikinci koşu ilk
// satıra düşer. YENİ ayar anahtarı / işaret YOK: işaret kaydın kendisi.
// Boş liste yazmak bir kez karar verip o kararı kalıcı kılar — taze
// kurulum sonraki bir boot'ta (artık telemetrisi varken) "mevcut" sanılıp
// tohumlanmaz.
//
// "Mevcut kurulum" ölçüsü: system_settings'teki en eski kaydın son yazımı
// existingInstallAge'dan eski. Eski sürümler boot/lider turlarında tek
// seferlik işaretler yazıyor (ör. inbox / ML göç işaretleri) ve bunlar bir
// daha yazılmıyor; taze kurulumda bütün kayıtlar bu binary'nin ilk
// dakikalarından. Ölçü okunamazsa HİÇBİR ŞEY yazılmaz, sonraki tur dener.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// legacyTopologyHiddenSeed — v0.10.2268 öncesi kod varsayılanı. YALNIZ bu
// göçün süreklilik verisidir; başka hiçbir yol okumaz.
var legacyTopologyHiddenSeed = []string{"kafka:log*", "kafka:bsa*"}

// existingInstallAge — en eski ayar kaydı bundan eskiyse kurulum mevcut.
const existingInstallAge = 24 * time.Hour

// Göç sonuçları.
const (
	topologyHiddenSeedPresent = "present" // kayıt vardı, dokunulmadı
	topologyHiddenSeedSeeded  = "seeded"  // mevcut kurulum, eski varsayılan yazıldı
	topologyHiddenSeedFresh   = "fresh"   // taze kurulum, boş liste yazıldı
)

// topologyHiddenSeedStore — göçün dokunduğu yüzey (*chstore.Store karşılar).
type topologyHiddenSeedStore interface {
	GetTopologyHiddenRaw(ctx context.Context) ([]byte, error)
	PutTopologyHiddenRaw(ctx context.Context, raw []byte) error
	OldestSettingWrite(ctx context.Context) (time.Time, bool, error)
	AppendAudit(ctx context.Context, e chstore.AuditEntry) error
}

// seedTopologyHidden — tek koşu. Hata → hiçbir şey yazılmadı (ya da yazım
// düştü); çağıran sonraki turda yeniden dener.
func seedTopologyHidden(ctx context.Context, st topologyHiddenSeedStore, now time.Time) (string, error) {
	raw, err := st.GetTopologyHiddenRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("read topology_hidden: %w", err)
	}
	if len(raw) > 0 {
		return topologyHiddenSeedPresent, nil
	}
	oldest, ok, err := st.OldestSettingWrite(ctx)
	if err != nil {
		return "", fmt.Errorf("install age: %w", err)
	}
	outcome, patterns := topologyHiddenSeedFresh, []string{}
	if ok && now.Sub(oldest) >= existingInstallAge {
		outcome, patterns = topologyHiddenSeedSeeded, legacyTopologyHiddenSeed
	}
	body, _ := json.Marshal(topologyHidden{Patterns: patterns})
	if err := st.PutTopologyHiddenRaw(ctx, body); err != nil {
		return "", fmt.Errorf("write topology_hidden: %w", err)
	}
	if outcome == topologyHiddenSeedSeeded {
		details, _ := json.Marshal(map[string]any{
			"version": "v0.10.2268", "patterns": patterns,
			"reason": "kod varsayılanı kaldırıldı; kaydı olmayan mevcut kurulumda önceki varsayılan korunur",
		})
		if err := st.AppendAudit(ctx, chstore.AuditEntry{
			Time: now.UnixNano(), ActorID: "system", ActorEmail: "system", ActorRole: "system",
			Action: "topology.hidden.seed", TargetKind: "settings", TargetID: "topology_hidden",
			Details: string(details),
		}); err != nil {
			log.Printf("[topology] gizli desen göçü: audit: %v", err)
		}
	}
	return outcome, nil
}

const topologyHiddenSeedRetry = time.Minute

// StartTopologyHiddenSeed — main (worker kipi) çağırır. Lider kilidi
// alındığında koşar; başarıya dek dakikada bir dener, sonra döner.
func (s *Server) StartTopologyHiddenSeed(ctx context.Context, lock cache.Lock) {
	if s.store == nil {
		return
	}
	// İş bitince kilit bırakılsın: holder lctx iptaliyle Release eder.
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	leader := cache.NewLeaderHolder(lock, "coremetry:lock:topology-hidden-seed", cache.LeaderTTL(topologyHiddenSeedRetry))
	leader.Start(lctx)
	t := time.NewTicker(topologyHiddenSeedRetry)
	defer t.Stop()
	for {
		if leader.IsLeader() {
			out, err := seedTopologyHidden(lctx, s.store, time.Now())
			if err == nil {
				if out != topologyHiddenSeedPresent {
					log.Printf("[topology] gizli desen göçü: %s", out)
				}
				return
			}
			log.Printf("[topology] gizli desen göçü (sonraki turda yeniden): %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
