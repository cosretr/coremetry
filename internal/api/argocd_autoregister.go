package api

// argocd_autoregister.go — v0.10.1013 — keşfedilen Argo CD instance'larının
// KENDİLİĞİNDEN kaydı (operatör: "Argocd entegrasyonu da autodiscover etse
// daha iyi olacak, şu anda tek tek ekle diyorum"). Kural ve sınırlar:
// internal/argocd/autoregister.go.
//
// Yeni uç YOK: bu bir arka plan turudur (StartRAGSync deseni — api rolündeki
// pod'larda main.go başlatır, lider kilidiyle tek pod koşar). Anahtar
// system_settings["argocd"].autoRegister.enabled; varsayılan KAPALI — kapalıyken
// tur hiçbir sorgu atmaz, Redis kilidi de alınmaz (kilit ilk etkin turda kurulur).
//
// Tur:
//
//  1. Her ETKİN hub için elle keşifle AYNI probe (runArgoCDDiscovery: job
//     süzgeçli label-values, aynı çağrı bütçesi). Pasif hub (Remote Cluster
//     kaydı devre dışı) ve tokenRef'i çözülemeyen hub atlanır. Hub'lar SIRAYLA
//     koşar ve bir hub'ın adayları sonrakinin "mevcut" listesine girer —
//     iki hub'da aynı namespace aynı turda çakışan kimlik almaz.
//  2. Uygun aday yoksa yazım YOK.
//  3. Varsa argocdPutMu altında: taze blob yüklenir, adaylar ONA eklenir
//     (argocd.MergeAutoRegistered — yalnız ekler), sonuç PUT ile aynı
//     doğrulamadan geçer (argocd.Validate) ve yazılır. Doğrulama düşerse
//     hiçbir şey yazılmaz.
//  4. Yazım denetim kaydına "settings.argocd.autoregister" olarak düşer
//     (aktör: system) ve peer'lara config-reload yayınlanır.
//
// Elle keşifle aynı meşguliyet bayrağını paylaşır (argocdDiscoverBusy): biri
// koşarken diğeri başlamaz. Yazım updatedAt'i ilerletir — o sırada ayar
// ekranında açık bir taslak Kaydet'te 409 alır ve yeniden yükler (v0.10.978
// bayat yazma koruması); bu yalnız gerçekten yeni instance bulunduğunda olur.

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
)

const (
	argocdAutoRegisterInterval = 30 * time.Minute // tur aralığı
	argocdAutoRegisterPoll     = 2 * time.Minute  // anahtarın açıldığını fark etme / tur zamanı denetimi
	argocdAutoRegisterAction   = "settings.argocd.autoregister"
	argocdAutoRegisterLock     = "coremetry:lock:argocd-autoregister"
	// argocdAutoRegisterAuditIDs — denetim satırında adı yazılan en çok instance.
	argocdAutoRegisterAuditIDs = 50
)

// argocdAutoRegisterActor — otomatik yazımın denetim aktörü.
var argocdAutoRegisterActor = &auth.Claims{UserID: "system", Email: "system:argocd-autoregister", Role: auth.RoleAdmin}

// argocdAutoRegisterDue — SAF: tur zamanı geldi mi (ilk tur hemen).
func argocdAutoRegisterDue(last, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= argocdAutoRegisterInterval
}

// StartArgoCDAutoRegister — lider kapılı arka plan döngüsü. Anahtar kapalıyken
// kilit kurulmaz ve hiçbir sorgu atılmaz.
func (s *Server) StartArgoCDAutoRegister(ctx context.Context, lock cache.Lock) {
	var leader *cache.LeaderHolder
	var last time.Time
	t := time.NewTicker(argocdAutoRegisterPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		svc := argocdSettingsSvc.Load()
		if svc == nil || !argocd.AutoRegisterActive(svc.Current()) {
			continue
		}
		if leader == nil {
			leader = cache.NewLeaderHolder(lock, argocdAutoRegisterLock, cache.LeaderTTL(argocdAutoRegisterPoll))
			leader.Start(ctx)
			continue // liderlik bir sonraki yoklamada belli olur
		}
		if !leader.IsLeader() || !argocdAutoRegisterDue(last, time.Now()) {
			continue
		}
		last = time.Now()
		s.ArgoCDAutoRegisterTick(ctx)
	}
}

// argocdAutoRegisterDetails — SAF: denetim satırının gövdesi.
func argocdAutoRegisterDetails(res argocd.AutoRegisterResult, hubsProbed, hubsFailed int, updatedAt int64) string {
	ids := make([]string, 0, len(res.Added))
	for i, inst := range res.Added {
		if i >= argocdAutoRegisterAuditIDs {
			break
		}
		ids = append(ids, inst.ID)
	}
	b, _ := json.Marshal(map[string]any{
		"added": len(res.Added), "ids": ids, "idsTruncated": len(res.Added) > len(ids),
		"skippedTaken": res.SkippedTaken, "skippedFull": res.SkippedFull,
		"hubsProbed": hubsProbed, "hubsFailed": hubsFailed, "updatedAt": updatedAt,
	})
	return string(b)
}

// argocdAutoRegisterProbe — turun OKUMA yarısı: etkin hub'ları sırayla keşfeder,
// uygun adayları döndürür. ran=false: elle keşif koşuyordu (tur atlandı).
// Meşguliyet bayrağı her çıkışta bırakılır (panik dahil).
func (s *Server) argocdAutoRegisterProbe(ctx context.Context, cur argocd.Settings) (cands []argocd.Candidate, probed, failed int, ran bool) {
	if !argocdDiscoverBusy.CompareAndSwap(false, true) {
		return nil, 0, 0, false
	}
	defer argocdDiscoverBusy.Store(false)
	tokenBad := map[string]bool{}
	for _, c := range s.thanos.Snapshot().Clusters {
		if c.TokenRef != "" && !c.TokenResolved {
			tokenBad[c.ID] = true
		}
	}
	existing := append([]argocd.Instance(nil), cur.Instances...)
	for _, h := range cur.Hubs {
		hub, ok := s.thanos.ClusterByID(h.ClusterID)
		if !ok || tokenBad[h.ClusterID] {
			continue // pasif / silinmiş hub ya da çözülemeyen tokenRef: istek gönderilmez
		}
		if !h.Inject() {
			hub.ThanosLabelName, hub.ThanosLabelValue = "", ""
		}
		hctx, cancel := context.WithTimeout(ctx, argocdDiscoverBudget)
		res, err := s.runArgoCDDiscovery(hctx, hub, h.Inject(), existing)
		cancel()
		probed++
		if err != nil {
			failed++
			log.Printf("[argocd/autoregister] hub %s keşfi düştü: %s", hub.Name, argocdProbeErrText(err))
			continue
		}
		for _, c := range res.Candidates {
			if !argocd.AutoRegisterable(c) {
				continue
			}
			cands = append(cands, c)
			// Sonraki hub'ın keşfi bu adayı "mevcut" görsün (kimlik çakışmasın).
			existing = append(existing, argocd.Instance{ID: c.ID, HubClusterID: c.HubClusterID, HubNamespace: c.HubNamespace, MetricsJob: c.MetricsJob})
		}
	}
	return cands, probed, failed, true
}

// ArgoCDAutoRegisterTick — tek tur (dosya başı). Dönüş: eklenen instance sayısı.
func (s *Server) ArgoCDAutoRegisterTick(ctx context.Context) int {
	svc := argocdSettingsSvc.Load()
	if svc == nil || s.thanos == nil {
		return 0
	}
	cur := svc.Current()
	if !argocd.AutoRegisterActive(cur) {
		return 0
	}
	cands, probed, failed, ran := s.argocdAutoRegisterProbe(ctx, cur)
	if !ran || len(cands) == 0 {
		return 0
	}
	st := argocdSettingsStoreOf(s)
	if st == nil {
		return 0
	}
	argocdPutMu.Lock()
	defer argocdPutMu.Unlock()
	if err := svc.LoadPersisted(ctx, st); err != nil {
		log.Printf("[argocd/autoregister] kalıcı blob yüklenemedi, tur atlandı: %v", err)
		return 0
	}
	fresh := svc.Current()
	if !argocd.AutoRegisterActive(fresh) {
		return 0 // tur sırasında kapatıldı
	}
	next, res := argocd.MergeAutoRegistered(fresh, cands)
	if len(res.Added) == 0 {
		return 0
	}
	cfg, err := argocd.Validate(next, s.argocdClusterRefs())
	if err != nil {
		log.Printf("[argocd/autoregister] %d aday doğrulamadan geçmedi, hiçbir şey yazılmadı: %v", len(res.Added), err)
		return 0
	}
	if err := svc.SavePersisted(context.WithoutCancel(ctx), st, cfg); err != nil {
		log.Printf("[argocd/autoregister] yazılamadı: %v", err)
		return 0
	}
	s.publishConfigReload(ctx, "argocd")
	s.auditAs(argocdAutoRegisterActor, "", argocdAutoRegisterAction, "settings", argocd.SettingsKey,
		argocdAutoRegisterDetails(res, probed, failed, svc.Current().UpdatedAt))
	log.Printf("[argocd/autoregister] %d instance eklendi (dolu yuva %d, tavan dışı %d; %d hub tarandı, %d düştü)",
		len(res.Added), res.SkippedTaken, res.SkippedFull, probed, failed)
	return len(res.Added)
}
