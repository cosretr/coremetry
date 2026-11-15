package api

// rollout_row_services.go — v0.10.1420 (operatör, /rollouts: "Deployment
// Workload basınca service'e girsin, şu an bulamıyor.")
//
// Workload hücresinin BİRİNCİL tıkı o iş yükünün koştuğu servisin sayfasını
// açar. v0.10.2232 (Entity S2): eşleme entity modelinden — satır → kanonik
// wl: id → kalıcı `serves` kenarı (entity.Resolver.WorkloadServices; kenarı
// olmayan iş yükü resolver'ın Paths yedeğine düşer: pod runs > attr > ad).
// Rollouts'un kendi workload_revision_activity_1m iş yükü düzeyi eşlemesi
// silindi.
//
// N+1 YOK: listenin TÜM satırları için TEK resolver çağrısı (id'ler
// tekilleşir), liste cevabıyla birlikte serveCached'te. Hata = alan yok
// (liste düşmez, hücre entity sayfasına düşer).
//
// Seçim deterministik (pickRolloutService): adayların hepsi `serves`
// kenarıdır (yetkili); birden çoksa adı iş yüküne eşit olan, yoksa
// alfabetik ilk. Hepsi `services`te gider (FE title'ında listeler).

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

// rolloutRowServicesMax — satır başına JSON'a giden en çok servis adı.
const rolloutRowServicesMax = 20

// workloadServicesLookup — entity.Resolver.WorkloadServices imzası (test dikişi).
type workloadServicesLookup func(ctx context.Context, ids []string, at time.Time) (map[string][]string, error)

// rolloutWorkloadID — SAF: satırın kanonik wl: id'si (tür yoksa Derived;
// sahipsiz ReplicaSet "<ad>-<hash>" → Deployment). cid "" → "".
func rolloutWorkloadID(cid string, row chstore.RolloutRow) string {
	if cid == "" || row.Namespace == "" || row.Workload == "" {
		return ""
	}
	kind := row.Kind
	if kind == "" {
		kind = entity.DerivedKind
	}
	return entity.CanonicalID(entity.WorkloadID(cid, row.Namespace, kind, row.Workload))
}

// attachRolloutServices — listRollouts yükleyicisi çağırır.
func (s *Server) attachRolloutServices(ctx context.Context, rows []chstore.RolloutRow) {
	if s.store == nil || len(rows) == 0 {
		return
	}
	if err := attachRolloutServicesWith(ctx, rows, s.rolloutCID, s.entityResolver().WorkloadServices); err != nil {
		log.Printf("[rollouts] workload services: %v", err)
	}
}

// rolloutCID — satırın Remote Cluster kaydı → entity cid ("" = çözülmedi).
func (s *Server) rolloutCID(id string) string {
	if c, ok := s.resolveCluster(id); ok {
		return c.EffectiveID()
	}
	return ""
}

// attachRolloutServicesWith — test dikişi: cluster çözümü + TEK toplu çağrı
// ("şimdi": açık serves kenarları).
func attachRolloutServicesWith(ctx context.Context, rows []chstore.RolloutRow, cidOf func(string) string, lookup workloadServicesLookup) error {
	ids := make([]string, len(rows))
	seen := map[string]bool{}
	var uniq []string
	for i, row := range rows {
		ids[i] = rolloutWorkloadID(cidOf(row.ClusterID), row)
		if ids[i] != "" && !seen[ids[i]] {
			seen[ids[i]] = true
			uniq = append(uniq, ids[i])
		}
	}
	if len(uniq) == 0 {
		return nil
	}
	m, err := lookup(ctx, uniq, time.Time{})
	if err != nil && len(m) == 0 {
		return err
	}
	for i := range rows {
		svcs := capServices(m[ids[i]])
		rows[i].Services = svcs
		rows[i].Service = pickRolloutService(rows[i].Workload, svcs)
	}
	return err
}

// capServices — SAF: tekil, alfabetik, rolloutRowServicesMax'ta kesik; boş → nil.
func capServices(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	set := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !set[s] {
			set[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	if len(out) > rolloutRowServicesMax {
		out = out[:rolloutRowServicesMax]
	}
	return out
}

// pickRolloutService — SAF: birincil tıkın servisi. Adı workload'a eşit
// olan (harf duyarsız) kazanır; yoksa alfabetik ilk (svcs sıralı gelir);
// aday yoksa "".
func pickRolloutService(workload string, svcs []string) string {
	for _, svc := range svcs {
		if strings.EqualFold(svc, workload) {
			return svc
		}
	}
	if len(svcs) == 0 {
		return ""
	}
	sorted := append([]string(nil), svcs...)
	sort.Strings(sorted)
	return sorted[0]
}
