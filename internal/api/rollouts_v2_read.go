package api

// rollouts_v2_read.go — v0.10.984 — Rollouts v2 P2.3: okuma yolu anahtarı
// (docs/rollouts/v2-audit.md §12.1 P2.3, §2.2, §2.4, §10.6; karar 4 ve 14).
//
// Rota YOK (api.go ve route_registry değişmez): mevcut /api/rollouts*
// uçları, SSE tail, GitOps sekmesi ve pod-churn tüketicileri kaynağı
// burada seçer. system_settings["rollouts"].source = "v2" (ve enabled) iken
// rollout_events okunur, aksi hâlde v1 yolu BAYT BAYT bugünkü gibi
// (varsayılan v1 → deploy davranış değiştirmez; bayrak §11 sonuçları +
// 0015 sihirbazıyla birlikte açılır, DECISIONS 2026-09-27 P2.3 kaydı).
//
// Neden ayrı rota değil: FE kaynağı göremez (/api/settings/rollouts admin
// ucu) ve aynı cevap şekli korunuyor (lib/types.ts WorkloadRollout /
// RolloutsResult — yalnız opsiyonel alanlar eklendi); ikinci bir rota her
// tüketiciye "hangisini çağırayım" sorusunu taşırdı. Servis kapsamlı okuma
// (§2.2 "service-scoped read") /api/services/{name}/rollouts'un İÇİNDE:
// DeployHistoryPanel, sürüm çipi, ServiceCharts işaretleri, annotation
// şeridi ve CoSRE grafik anlatımı tek yardımcıdan (serviceRolloutsFor)
// beslenir; pod-churn yolu P6'da emekli olur.
//
// Bağlantılar (karar 14): 6 parçalı ?rollout= (cluster|namespace|kind|
// workload|incarnationAtMs|generation) → rollout_events; 5 parçalı eski
// bağlantı workload_rollouts'tan çözülmeye DEVAM eder (TTL'e dek) — v2
// açıkken de. source=v1'de 6 parçalı istek bugünkü gibi 400 (revizyon yok).
//
// Önbellek anahtarlarının hepsi kaynağı taşır (rollout_keys.go): bayrak
// çevrilince eski kaynağın cevabı 15–60 s daha servis edilmesin (PUT zaten
// "rollouts:" önekini süpürür; öteki pod'lar config reload'la gelir).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

// rolloutReader — rollout uçlarının depo dikişi (handler testleri sahte
// depo verir; üretimde *chstore.Store). rolloutSettingsStoreOf emsali.
type rolloutReader interface {
	RolloutList(ctx context.Context, f chstore.RolloutFilter, from, to time.Time, limit int) ([]chstore.RolloutRow, error)
	RolloutV2List(ctx context.Context, f chstore.RolloutFilter, from, to time.Time, limit int) ([]chstore.RolloutRow, error)
	RolloutByID(ctx context.Context, id chstore.RolloutID) (*chstore.RolloutRow, error)
	RolloutV2ByID(ctx context.Context, id chstore.RolloutV2ID) (*chstore.RolloutRow, error)
	RolloutStats(ctx context.Context, f chstore.RolloutFilter, from, to time.Time, topN int) (*chstore.RolloutStats, error)
	RolloutV2Stats(ctx context.Context, f chstore.RolloutFilter, from, to time.Time, topN int) (*chstore.RolloutStats, error)
	RolloutRuns(ctx context.Context, limit int) ([]rollout.Run, error)
	RolloutWorkerRuns(ctx context.Context, worker string, limit int) ([]rollout.WorkerRun, error)
	RolloutLastRun(ctx context.Context) (*rollout.Run, error)
	RolloutWorkerLastRun(ctx context.Context, worker string) (*rollout.WorkerRun, error)
	RolloutTail(ctx context.Context, cursor chstore.RolloutCursor, wm time.Duration, limit int) ([]chstore.RolloutRow, chstore.RolloutCursor, error)
	RolloutV2Tail(ctx context.Context, cursor chstore.RolloutV2Cursor, wm time.Duration, limit int) ([]chstore.RolloutRow, chstore.RolloutV2Cursor, error)
	RolloutsForWorkloads(ctx context.Context, keys []rollout.Key, from, to time.Time) ([]chstore.RolloutRow, error)
	RolloutV2ForWorkloads(ctx context.Context, keys []rollout.Key, from, to time.Time) ([]chstore.RolloutRow, error)
}

var rolloutReaderOf = func(s *Server) rolloutReader { return s.store }

// rolloutSource — uygulanan okuma kaynağı ("v1" | "v2"). Bayrak kapalıyken
// de source okunur; uçlar zaten önce rolloutEnabled'a bakar.
func (s *Server) rolloutSource() string {
	if s.rolloutCfg == nil {
		return rollout.SourceV1
	}
	return s.rolloutCfg.ResolvedV2().Source
}

// rolloutV2Read — bayraksız tüketiciler (tail, GitOps, servis okuması) için:
// v2 yolu yalnız enabled=true VE source=v2 iken (dedektörün koşma koşulu).
func (s *Server) rolloutV2Read() bool {
	return s.rolloutCfg != nil && s.rolloutCfg.Resolved().Enabled && s.rolloutSource() == rollout.SourceV2
}

// parseRolloutV2ID — SAF: 6 parçalı bağlantının sorgu biçimi
// (?cluster&namespace&kind&workload&incarnationAt=<ms>&generation). present
// = istek v2 anahtarı taşıyor (generation ya da incarnationAt var); err =
// taşıyor ama eksik/bozuk.
func parseRolloutV2ID(q url.Values) (chstore.RolloutV2ID, bool, error) {
	if !q.Has("generation") && !q.Has("incarnationAt") {
		return chstore.RolloutV2ID{}, false, nil
	}
	id := chstore.RolloutV2ID{
		ClusterID: strings.TrimSpace(q.Get("cluster")), Namespace: strings.TrimSpace(q.Get("namespace")),
		Kind: strings.TrimSpace(q.Get("kind")), Workload: strings.TrimSpace(q.Get("workload")),
	}
	incMs, _ := strconv.ParseInt(q.Get("incarnationAt"), 10, 64)
	gen, _ := strconv.ParseUint(q.Get("generation"), 10, 64)
	if id.ClusterID == "" || id.Namespace == "" || id.Kind == "" || id.Workload == "" || incMs <= 0 || gen == 0 {
		return id, true, errors.New("cluster, namespace, kind, workload, incarnationAt (ms) ve generation zorunlu")
	}
	id.IncarnationAt = time.UnixMilli(incMs).UTC()
	id.Generation = gen
	return id, true, nil
}

// rolloutV2LayerNote — v2 degrade ilanı: dedektörün son rollout_worker_runs
// satırı (worker="rollout-detector"). run.Error viewer'a AKMAZ (teşhis
// sayaçları + ham CH dizesi); ayrıntı /api/rollouts/runs (admin).
func (s *Server) rolloutV2LayerNote(ctx context.Context) string {
	run, err := rolloutReaderOf(s).RolloutWorkerLastRun(ctx, rollout.WorkerRolloutDetector)
	if err != nil {
		return "dedektör koşu kaydı okunamadı (geçici CH hatası olabilir; ayrıntı: sunucu logları)"
	}
	if run == nil {
		return `KSM dedektörü (rollout-detector) son 24 saatte koşmadı — lider worker, bayrak açık ve kaynak v2 olmalı; dış Distributed'da Admin → ClickHouse → 0015`
	}
	switch run.Status {
	case rollout.RunFailed:
		return "son dedektör koşusu hata verdi — ayrıntı: /api/rollouts/runs (admin)"
	case rollout.RunPartial:
		return "son dedektör koşusu kısmi (okunamayan küme / kesik sonuç / toplu yokluk koruması) — ayrıntı: /api/rollouts/runs (admin)"
	}
	return ""
}

// rolloutRunJSON — v2 koşu satırı, v1 RolloutRun şekli + EKLEMELİ alanlar
// (lib/types.ts RolloutRun). clusters = kapsam, rolloutsWritten = yazılan
// satır (olay + durum); spanMs/ksmMs v2'de yok (0), süre durationMs'te.
type rolloutRunJSON struct {
	StartedAt       int64  `json:"startedAt"`
	FinishedAt      int64  `json:"finishedAt"`
	Host            string `json:"host"`
	Status          string `json:"status"`
	Clusters        int    `json:"clusters"`
	RolloutsWritten int    `json:"rolloutsWritten"`
	SpanMs          int    `json:"spanMs"`
	KSMMs           int    `json:"ksmMs"`
	Error           string `json:"error,omitempty"`
	Worker          string `json:"worker"`
	ScopesOK        int    `json:"scopesOk"`
	SeriesRead      int    `json:"seriesRead"`
	Truncated       bool   `json:"truncated"`
	PartialResponse bool   `json:"partialResponse"`
	Unmapped        int    `json:"unmapped"`
	APICalls        int    `json:"apiCalls"`
	DurationMs      int    `json:"durationMs"`
}

// rolloutRunsFromWorker — SAF: WorkerRun → koşu satırı.
func rolloutRunsFromWorker(runs []rollout.WorkerRun) []rolloutRunJSON {
	out := make([]rolloutRunJSON, 0, len(runs))
	for _, r := range runs {
		out = append(out, rolloutRunJSON{
			StartedAt: r.StartedAt.UnixMilli(), FinishedAt: r.FinishedAt.UnixMilli(), Host: r.Host, Status: r.Status,
			Clusters: r.ScopesTotal, RolloutsWritten: r.RowsWritten, Error: r.Error, Worker: r.Worker, ScopesOK: r.ScopesOK,
			SeriesRead: r.SeriesRead, Truncated: r.Truncated, PartialResponse: r.PartialResponse, Unmapped: r.Unmapped,
			APICalls: r.APICalls, DurationMs: r.DurationMs,
		})
	}
	return out
}

// rolloutStatsResp — v0.10.984 — istatistik gövdesi: RolloutStats alanları
// + eklemeli "v2": true (yalnız v2'de; RolloutStats'a alan eklemek v1
// cevabını değiştirirdi). v0.10.1262 — + platform namespace dışlaması
// (excludeNamespaces: uygulanan desenler, excludeApplied: bu cevapta
// uygulandı mı — açık ?namespace= süzgecinde false).
type rolloutStatsResp struct {
	*chstore.RolloutStats
	V2                bool     `json:"v2,omitempty"`
	ExcludeNamespaces []string `json:"excludeNamespaces"`
	ExcludeApplied    bool     `json:"excludeApplied"`
}

// rolloutV2Services — çekmece: v2 satırının servisleri (v0.10.2232, S2):
// kanonik wl: id → serves kenarları, rollout başlangıcı anında geçerli
// olanlar (yoksa resolver'ın Paths yedeği, aynı an). capped = 20'de kesildi.
func (s *Server) rolloutV2Services(ctx context.Context, cid string, row chstore.RolloutRow) ([]string, bool, error) {
	id := rolloutWorkloadID(cid, row)
	if id == "" {
		return []string{}, false, nil
	}
	m, err := s.entityResolver().WorkloadServices(ctx, []string{id}, row.StartedAt)
	if err != nil && len(m) == 0 {
		return nil, false, err
	}
	all := m[id]
	out := capServices(all)
	if out == nil {
		out = []string{}
	}
	return out, len(all) > len(out), nil
}

// ── servis kapsamlı okuma (pod-churn tüketicileri, §2.2) ───────────────────

// serviceRolloutsWorkloadWindow — MV (workload_revision_activity_1m) 7 gün
// tutar (rollout_schema.go workloadRevisionActivityDays): iş yükü listesi
// en çok bu kadar geriye bakar.
const serviceRolloutsWorkloadWindow = 7 * 24 * time.Hour

// serviceRolloutsSrc — pod-churn tüketicilerinin önbellek anahtarı parçası
// (/api/services/{name}/rollouts, /api/annotations): "ksm:cl=<özet>" (v2
// okuması) | "churn". v2 cevabı Remote Cluster eşlemesine bağlı
// (serviceRolloutsFor: bySpan → rollout_events anahtarları, satırın Cluster
// alanı, eşlenmeyen span notu) — özet anahtarda, yoksa kayıt düzenlemesi
// TTL boyunca eski listeyi servis ederdi (/gitops anahtarı gibi). v1 yolu
// eşlemeye bakmaz, anahtarı aynı.
func (s *Server) serviceRolloutsSrc() string {
	if s.rolloutV2Read() {
		return "ksm:cl=" + s.serviceGitOpsClusterMaps().digest()
	}
	return "churn"
}

// serviceRolloutsFor — /api/services/{name}/rollouts, annotation şeridi ve
// CoSRE grafik anlatımının ortak kaynağı: v2 okuması açıksa rollout_events,
// değilse bugünkü pod-churn (GetServiceRollouts) — v1 cevabı aynı.
func (s *Server) serviceRolloutsFor(ctx context.Context, service string, from, to time.Time) (*chstore.RolloutsResult, error) {
	if !s.rolloutV2Read() {
		return s.store.GetServiceRollouts(ctx, service, from, to)
	}
	wFrom := from.Add(-time.Hour)
	if to.Sub(wFrom) > serviceRolloutsWorkloadWindow {
		wFrom = to.Add(-serviceRolloutsWorkloadWindow)
	}
	refs, capped, err := s.store.ServiceWorkloads(ctx, service, wFrom, to)
	if err != nil {
		return nil, err
	}
	cm := s.serviceGitOpsClusterMaps()
	workloads, unmapped := serviceGitOpsWorkloads(refs, cm.bySpan)
	keys := make([]rollout.Key, 0, len(workloads))
	for _, w := range workloads {
		keys = append(keys, rollout.Key{ClusterID: w.ClusterID, Namespace: w.Namespace, Workload: w.Workload})
	}
	rows, err := rolloutReaderOf(s).RolloutV2ForWorkloads(ctx, keys, from, to)
	if err != nil {
		return nil, err
	}
	res := serviceRolloutsFromV2(service, rows, spanClusterOf(refs, cm.bySpan))
	var notes []string
	if capped {
		notes = append(notes, fmt.Sprintf("iş yükü listesi %d'de kesildi", chstore.ServiceWorkloadsMax))
	}
	if len(unmapped) > 0 {
		notes = append(notes, "Remote Cluster kaydına eşlenmeyen span cluster değeri: "+strings.Join(unmapped, ", "))
	}
	if len(rows) >= serviceGitOpsRolloutsCap {
		notes = append(notes, fmt.Sprintf("rollout listesi %d satırda kesildi", serviceGitOpsRolloutsCap))
	}
	res.Note = strings.Join(notes, "; ")
	return res, nil
}

// spanClusterOf — SAF: EffectiveID → temsilci span cluster değeri (sözlük
// sırasında ilki). Pod-churn satırının Cluster alanı span değeridir
// (DeployHistoryPanel Topbar kapsamıyla onu karşılaştırır); v2 satırı aynı
// sözleşmeyi taşısın. bySpan boşsa (tek küme) değer kimliğin kendisi.
func spanClusterOf(refs []chstore.WorkloadRevisionRef, bySpan map[string]string) map[string]string {
	out := map[string]string{}
	for _, r := range refs {
		id := r.Cluster
		if len(bySpan) > 0 {
			v, ok := bySpan[r.Cluster]
			if !ok {
				continue
			}
			id = v
		}
		if cur, ok := out[id]; !ok || r.Cluster < cur {
			out[id] = r.Cluster
		}
	}
	return out
}

// serviceRolloutsFromV2 — SAF: rollout_events satırları (started_at DESC) →
// pod-churn şekli (zaman ARTAN — strip deploys[len-1] sözleşmesi).
// Kind: rollout / rollback / initial = "deploy" (yeni kod ya da yeni iş
// yükü), config = "restart" (imaj aynı; sürüm çipi ve "deploy" işaretleri
// onu saymaz). Sürüm: version_tag, yoksa birincil imajın tag'i.
func serviceRolloutsFromV2(service string, rows []chstore.RolloutRow, spanOf map[string]string) *chstore.RolloutsResult {
	res := &chstore.RolloutsResult{Service: service, Rollouts: []chstore.Rollout{}, InstancesTracked: true, Source: "ksm"}
	versions := map[string]bool{}
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		ro := chstore.Rollout{
			TimeUnixNs: r.StartedAt.UnixNano(), Kind: "deploy", VersionBefore: r.PrevImageTag, VersionAfter: r.ImageTag,
			Cluster: r.ClusterID, Source: "ksm", WorkloadKind: r.Kind, Namespace: r.Namespace, Workload: r.Workload, Status: r.Status,
		}
		if c, ok := spanOf[r.ClusterID]; ok {
			ro.Cluster = c
		}
		if v := r.V2; v != nil {
			if v.VersionTag != "" {
				ro.VersionAfter = v.VersionTag
			}
			if v.ChangeType == rollout.V2ChangeConfig {
				ro.Kind = "restart"
			}
			ro.PodsAdded, ro.ActivePods = int(v.UpdatedReplicas), int(v.AvailableReplicas)
			ro.SpecReplicas, ro.UpdatedReplicas = int(v.SpecReplicas), int(v.UpdatedReplicas)
		}
		for _, x := range []string{ro.VersionBefore, ro.VersionAfter} {
			if x != "" {
				versions[x] = true
			}
		}
		res.Rollouts = append(res.Rollouts, ro)
	}
	res.VersionConstant = len(versions) <= 1
	return res
}

// logRolloutTailErr — tail hata günlüğü (kaynak etiketli).
func logRolloutTailErr(src string, err error) {
	log.Printf("[rollout] tail (%s): %v", src, err)
}
