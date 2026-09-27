package argocd

// mapper.go — v0.10.985 — ROLLOUTS v2 P3.2: eşleyici adımı, argocd-metrics
// işçisinin İÇİNDE (docs/rollouts/v2-audit.md §10.4: "status diff → mapper
// every 10 min"; lider "argocd-metrics", ayrı kilit yok). Saf çekirdek
// mapping.go'da; bu dosya I/O'yu yalnız enjekte edilen MapperStore ile yapar
// (üretimde *chstore.Store, main.go SetMapperStore; testte sahte).
//
// Bayrak P3.1'inkiyle AYNI: metricsWorker.enabled (+ enabled + ≥1 hub)
// kapalıyken Tick hiç koşmaz, eşleyici de. §11 H/N doğrulanmadı: doğrulanmamış
// varsayıma dayanan her yer fail-safe — yazmak yerine atla + koşu teşhisinde
// say (`mapper_*` sayaçları rollout_worker_runs.error'da):
//
//   - Tur yalnız HAZIR instance'ları uzlaştırır: parça bu tik atlanmadı,
//     bellek CH'den kurulu, son 3 × inventoryMin içinde DOLU bir tam envanter
//     alındı, liderlik yazımdan önce korunuyor. Hazır olmayan instance'ın
//     kenarı ne eklenir ne kaldırılır (last_verified_at'i eskir; okuyucu
//     MappingFreshness'tan eskisini canlı saymaz).
//   - İş yükü listesi (workload_revision_activity_1m, son 24 sa, TÜM filo)
//     tavanda kesilirse tur ATLANIR: kısmi listeyle ad kenarları yanlışlıkla
//     kaldırılırdı. Span cluster değeri Remote Cluster'a eşlenmezse o iş yükü
//     atlanır ve sayılır (mapper_workload_cluster_unmapped).
//   - Kenar sayısı mapperEdgeCap'i aşarsa tur atlanır (yazım yok).
//   - v0.10.985 inceleme: parçası bu tik TAMAM olduğu hâlde hazır olmayan
//     instance (envanter geri çekilmesi, bellek yeni kuruldu) her tik — tur
//     vadesi beklenmeden — koşuyu partial + not yapar. GitOps sekmesi
//     tabloyu yalnız son koşu 'ok' iken kullandığından (service_gitops_
//     mapper.go) kenarı eskiyen instance sekmeyi canlı yola düşürür; atlanan
//     / hatalı parça zaten partial'dır.
//   - Tur hatası parça durumlarını bozmaz; koşu partial + "eşleyici: …" notu.
//     Denenen tur (hatalı da olsa) bir sonraki denemeyi mapperMin sonraya
//     atar — her tik ağır filo okuması yapılmasın.

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/rollout"
)

// WorkloadObs — MV'den gözlenen iş yükü; SpanCluster span'in cluster değeri
// (Remote Cluster'a eşleme işçide, Registry.BySpan).
type WorkloadObs struct {
	SpanCluster, Namespace, Kind, Workload string
}

// MapperStore — eşleyicinin CH kapısı (*chstore.Store karşılar).
type MapperStore interface {
	// ArgoCDMapperWorkloads — [from, to] içinde span üreten tekil (cluster,
	// ns, workload) + tür; capped: tavandan fazlası vardı (liste kesik).
	ArgoCDMapperWorkloads(ctx context.Context, from, to time.Time) ([]WorkloadObs, bool, error)
	// ArgoCDLiveMappings — verilen instance'ların CANLI (removed_at = 0)
	// kenarları (FINAL, keyset); tavan aşımı HATA.
	ArgoCDLiveMappings(ctx context.Context, instanceIDs []string) ([]MappingRow, error)
	ArgoCDWriteMappings(ctx context.Context, rows []MappingRow) error
}

// SetMapperStore — v0.10.985 — eşleyiciyi bağlar (nil = eşleyici yok).
func (w *MetricsWorker) SetMapperStore(ms MapperStore) { w.mapper = ms }

const (
	// MapperWorkloadWindow — iş yükü gözlem penceresi = GitOps sekmesinin
	// iş yükü penceresi (serviceGitOpsWorkloadWindow): sekmenin göremediği iş
	// yükü için kenar tutmanın okuyucusu yok.
	MapperWorkloadWindow = 24 * time.Hour
	// mapperEdgeCap — tur başına en çok kenar (~40k uygulama × zayıf + ad
	// kenarları için bol pay); aşılırsa yazım yok.
	mapperEdgeCap = 400_000
	// mapperReadyInventories — hazır sayılmak için son dolu tam envanterin en
	// çok kaç inventoryMin önce alındığı.
	mapperReadyInventories = 3
)

// registryDigest — SAF: eşleyicinin kullandığı kayıt alanlarının sırasız özeti
// (dest_server, span değeri, suffix); değişirse tur hemen koşar.
func registryDigest(reg Registry) string {
	var parts []string
	for k, v := range reg.ByServer {
		parts = append(parts, "u\x00"+k+"\x00"+v)
	}
	for k, v := range reg.BySpan {
		parts = append(parts, "s\x00"+k+"\x00"+v)
	}
	for k, v := range reg.Suffix {
		parts = append(parts, "x\x00"+k+"\x00"+v)
	}
	for k := range reg.Hubs {
		parts = append(parts, "h\x00"+k)
	}
	sort.Strings(parts)
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{1})
	}
	return fmt.Sprintf("%x", h.Sum64())
}

// mapperFingerprint — SAF: ayar sürümü (her admin kaydı; pin, envList,
// güvenler, instance'lar) + kayıt özeti.
func mapperFingerprint(set Settings, reg Registry) string {
	return fmt.Sprintf("%d|%s", set.UpdatedAt, registryDigest(reg))
}

// readyInstances — SAF: bu turda uzlaştırılacak instance'lar (dosya başı).
func readyInstances(plans []shardPlan, results []shardResult, mem map[string]*shardMem, now time.Time, invMin time.Duration) map[string]bool {
	out := map[string]bool{}
	for i, p := range plans {
		m := mem[p.Inst.ID]
		if p.Skip != "" || m == nil || !m.loaded || !m.invDone || m.lastInventory.IsZero() {
			continue
		}
		if i < len(results) && results[i].lostLeader {
			continue
		}
		if now.Sub(m.lastInventory) > mapperReadyInventories*invMin {
			continue
		}
		out[p.Inst.ID] = true
	}
	return out
}

// mapperUnreadyNote — v0.10.985 inceleme — SAF: hazır olmayan planlı
// instance sayısı (teşhis) + parçası bu tik tamam olduğu hâlde hazır olmayanlar
// için koşu notu (boş = yok). Atlanan / hatalı parça kendi notunu taşır.
func mapperUnreadyNote(plans []shardPlan, results []shardResult, ready map[string]bool) (string, int) {
	n := 0
	var ids []string
	for i, p := range plans {
		if ready[p.Inst.ID] {
			continue
		}
		n++
		if i < len(results) && results[i].ok {
			ids = append(ids, p.Inst.ID)
		}
	}
	if len(ids) == 0 {
		return "", n
	}
	sort.Strings(ids)
	return fmt.Sprintf("%d instance hazır değil (%s: son %d × inventoryMin içinde dolu tam envanter yok) — kenarları güncellenmiyor, GitOps sekmesi canlı yolda",
		len(ids), strings.Join(ids, ", "), mapperReadyInventories), n
}

// joinMapperNotes — boş olmayan notları "; " ile birleştirir.
func joinMapperNotes(notes ...string) string {
	var out []string
	for _, n := range notes {
		if n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, "; ")
}

// mapperWorkloads — SAF: MV gözlemleri → EffectiveID'li iş yükleri + Remote
// Cluster'a eşlenmeyen tekil span cluster değeri sayısı. bySpan boşsa değer
// olduğu gibi geçer (serviceGitOpsWorkloads kuralı: küme tanımsız / tek küme).
func mapperWorkloads(obs []WorkloadObs, bySpan map[string]string) ([]MapperWorkload, int) {
	unm := map[string]bool{}
	out := make([]MapperWorkload, 0, len(obs))
	for _, o := range obs {
		id := o.SpanCluster
		if len(bySpan) > 0 {
			v, ok := bySpan[o.SpanCluster]
			if !ok {
				unm[o.SpanCluster] = true
				continue
			}
			id = v
		}
		out = append(out, MapperWorkload{ClusterID: id, Namespace: o.Namespace, Kind: o.Kind, Workload: o.Workload})
	}
	return out, len(unm)
}

// mapperStep — tikin sonunda eşleyici turu (due ise). Dönen metin boş değilse
// koşu notu ("eşleyici: …") ve koşu partial.
func (w *MetricsWorker) mapperStep(ctx context.Context, set Settings, reg Registry, plans []shardPlan, results []shardResult, now time.Time, run *rollout.WorkerRun, diag map[string]int) string {
	if w.mapper == nil {
		return ""
	}
	// Hazırlık her tik (saf, G/Ç yok): tur vadesi gelmemiş tikte de sekme
	// kapısı (son koşu 'ok') hazır olmayan instance'ı görsün.
	invMin := time.Duration(set.Intervals.InventoryMin) * time.Minute
	ready := readyInstances(plans, results, w.mem, now, invMin)
	unready, unreadyN := mapperUnreadyNote(plans, results, ready)
	if unreadyN > 0 {
		diag["mapper_unready_instances"] += unreadyN
	}
	fp := mapperFingerprint(set, reg)
	every := time.Duration(set.Intervals.MapperMin) * time.Minute
	if !w.mapperLast.IsZero() && now.Sub(w.mapperLast) < every && fp == w.mapperFP {
		return joinMapperNotes(unready, w.mapperFailNote(diag))
	}
	if len(ready) == 0 {
		// Ucuz: sorgu yok; sonraki tik yeniden bakar (ilk envanter bekleniyor).
		diag["mapper_no_ready_instance"]++
		return joinMapperNotes(unready, w.mapperFailNote(diag))
	}
	note := w.mapperRound(ctx, set, reg, plans, ready, now, run, diag)
	// Başarısız turun notu başarılı tura dek her tik koşuya taşınır (tur
	// arası tikler 'ok' yazsaydı sekme kapısı son koşuya bakıp ≤26 sa eski
	// kenarlarla açılırdı); ağır okumanın mapperMin temposu aynı kalır.
	w.mapperErr, w.mapperErrAt = note, now
	return joinMapperNotes(unready, note)
}

// mapperFailNote — son tur başarısızsa (w.mapperErr) tur arası tikin notu.
// Boş → son tur başarılı (ya da edinimden beri tur yok).
func (w *MetricsWorker) mapperFailNote(diag map[string]int) string {
	if w.mapperErr == "" {
		return ""
	}
	diag["mapper_last_round_failed"]++
	return "son tur (" + w.mapperErrAt.UTC().Format("15:04:05") + ") başarısız, başarılı tura dek kenarlar bayat olabilir: " + w.mapperErr
}

// mapperRound — vadesi gelmiş tur (≥1 hazır instance). Dönen metin boş
// değilse koşu notu.
func (w *MetricsWorker) mapperRound(ctx context.Context, set Settings, reg Registry, plans []shardPlan, ready map[string]bool, now time.Time, run *rollout.WorkerRun, diag map[string]int) string {
	fp := mapperFingerprint(set, reg)
	w.mapperLast, w.mapperFP = now, fp
	diag["mapper_runs"]++
	diag["mapper_ready_instances"] += len(ready)

	obs, capped, err := w.mapper.ArgoCDMapperWorkloads(ctx, now.Add(-MapperWorkloadWindow), now)
	if err != nil {
		diag["mapper_error"]++
		return "iş yükü okuması: " + err.Error()
	}
	if capped {
		diag["mapper_workloads_capped"]++
		return fmt.Sprintf("iş yükü listesi %d satırda kesildi — kısmi listeyle kenar kaldırılabilirdi, tur atlandı", len(obs))
	}
	wls, unm := mapperWorkloads(obs, reg.BySpan)
	if unm > 0 {
		diag["mapper_workload_cluster_unmapped"] += unm
	}
	var apps []MapperApp
	for _, p := range plans {
		if !ready[p.Inst.ID] {
			continue
		}
		for k, m := range w.mem[p.Inst.ID].apps {
			if m.Deleted {
				continue
			}
			apps = append(apps, MapperApp{InstanceID: k.InstanceID, HubClusterID: p.Hub.ID, AppNamespace: k.AppNamespace, Name: k.Name,
				DestServer: m.Row.DestServer, DestNamespace: m.Row.DestNamespace})
		}
	}
	res := BuildMapping(MapperInput{Apps: apps, Workloads: wls, Settings: set, Ready: ready,
		ByServer: reg.ByServer, Normalize: reg.Normalize, SuffixByCluster: reg.Suffix})
	for k, v := range res.Diag {
		diag["mapper_"+k] += v
	}
	if len(res.Edges) > mapperEdgeCap {
		diag["mapper_edges_capped"]++
		return fmt.Sprintf("%d kenar tavanı (%d) aştı — tur atlandı, tablo değişmedi", len(res.Edges), mapperEdgeCap)
	}
	ids := make([]string, 0, len(ready))
	for id := range ready {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	existing, err := w.mapper.ArgoCDLiveMappings(ctx, ids)
	if err != nil {
		diag["mapper_error"]++
		return "canlı kenar okuması: " + err.Error()
	}
	rows, rdiag := ReconcileMapping(ReconcileInput{Desired: res.Edges, Existing: existing, Ready: ready, Now: now,
		TouchEvery: MappingTouchEvery, NextVersion: w.nextVersion})
	for k, v := range rdiag {
		diag["mapper_"+k] += v
	}
	run.Unmapped += res.UnmappedApps
	if len(rows) == 0 {
		return ""
	}
	if !w.leader() || w.resetPending.Load() {
		w.mapperLast = time.Time{}
		diag["mapper_write_skipped_leader"]++
		return "liderlik tur sırasında kaybedildi/yeniden edinildi — eşleme yazılmadı"
	}
	if err := w.mapper.ArgoCDWriteMappings(ctx, rows); err != nil {
		diag["mapper_error"]++
		return "argocd_app_mapping yazılamadı: " + err.Error()
	}
	run.RowsWritten += len(rows)
	diag["mapper_rows_written"] += len(rows)
	return ""
}
