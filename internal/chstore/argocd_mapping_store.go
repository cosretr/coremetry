package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
)

// argocd_mapping_store.go — v0.10.985 — ROLLOUTS v2 P3.2 CH kapısı:
// argocd_app_mapping (docs/rollouts/v2-audit.md §10.3.5, §10.6 "Service Argo
// card"; şema rollout_v2_schema.go ≡ migrations/0015) + servis kartının
// argocd_app_status okumaları. Tek yazıcı: argocd-metrics lideri içindeki
// eşleyici (argocd/mapper.go). Hepsi state tablosu → ANA bağlantı (s.conn;
// stateTables "FROM argocd_app_mapping" ve "FROM argocd_app_status" pinli).
// MV iş yükü okuması (ArgoCDMapperWorkloads) telemetri dosyasında:
// rollout_problem_telemetry.go (okuma havuzu, state tablosu yok).
//
// Okumalar FINAL + LIMIT + max_execution_time:
//   - eşleyicinin canlı kenarları: instance_id IN (…) AND removed_at = 0 +
//     tam anahtar keyset (ORDER BY'ın kendisi) + sayfa LIMIT; tavan aşımı
//     HATA — kesik kümeyle uzlaştırma, okunmayan kenarı "yok" sayıp yeniden
//     yazardı (yanlış satır değil ama gereksiz; asıl tehlike tersi: tavanı
//     aşan tablo zaten eşleyicinin mapperEdgeCap'ini aşmıştır).
//   - servis kartı: (cluster_id, namespace) IN (…) — ORDER BY öneki, ≤ 50
//     çift — AND removed_at = 0 AND last_verified_at ≥ tazelik sınırı
//     (argocd.MappingFreshness: hazır olmayan instance'ın eskiyen kenarı
//     gelmez); tavan+1 LIMIT, çağıran kesikliği bilir. v0.10.985 inceleme:
//     İKİ okuma, ayrı bütçe — iş yükü kenarları yalnız servisin (cluster,
//     ns, workload) üçlüleri için, zayıf kenarlar (workload_kind = '' —
//     ORDER BY'da her iş yükü kenarından önce sıralanır) ayrıca: tek
//     sorguda paylaşılan LIMIT'i kalabalık namespace'lerin zayıf kenarları
//     doldurur, sonraki kümelerin eşleşen kenarları kesilirdi.
//   - son durum: (instance_id, app_namespace, app_name) IN (…) — ORDER BY
//     öneki, nokta okuma — ORDER BY …, changed_at DESC LIMIT 1 BY uygulama.
//     Zaman sınırı YOK: değişmeyen uygulamanın son satırı 150 güne dek eski
//     olabilir (StatusRefreshAge); nokta öneki okumayı sınırlar.
//   - 24 sa senkron sayısı: aynı nokta öneki + change_kind='sync' + changed_at
//     ≥ ?; faz başına count(). İşçi tik başına uygulama başına TEK satır yazar
//     (aynı tikte iki senkron tek 'sync'e katlanır) ve sayaç tabanı yokken
//     görülen artışı yazmaz → alt sınırdır; FE "Senkron (24 sa)" aynı sütun.
//     v0.10.985 inceleme: lockDegraded altında aynı senkron iki pod'dan en
//     fazla bir tik arayla İKİ satır düşebilir (DDL: "okuyucu ardışık özdeş
//     satırları katlar"); sayım, uygulamanın bir önceki 'sync' satırıyla aynı
//     fazda ve ≤ bir tik sonraki satırı saymaz (lagInFrame). Bitişik tikteki
//     gerçek ikinci senkron da katlanır — sütun yine alt sınır.
//
// Yazım: tam satır (RMT(version), invariant #4), açık istemci version'ı,
// asyncInsertCtx; her satır argocd.ValidateMappingRow'dan geçer, TEK geçersiz
// satır bütün batch'i reddeder.

const (
	argocdMappingPage    = 20_000
	argocdMappingHardCap = 500_000
	// ArgoCDServiceEdgeMax — servis kartının okuduğu en çok kenar (50 iş
	// yükünün namespace'leri × zayıf kenarlar); aşılırsa kesik (not düşülür).
	ArgoCDServiceEdgeMax = 5_000
	// ArgoCDServiceAppMax — servis kartında durumu okunan en çok uygulama.
	ArgoCDServiceAppMax = 500
)

var argocdMappingCols = strings.Join(argocd.MappingColumns(), ", ")

const argocdMappingOrder = "cluster_id, namespace, workload_kind, workload, instance_id, app_namespace, app_name"

func placeholders(n int, one string) string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = one
	}
	return strings.Join(ps, ", ")
}

// argocdLiveMappingsPageSQL — bağlar: n instance_id, 7 parçalı anahtar kursörü.
func argocdLiveMappingsPageSQL(n int) string {
	return fmt.Sprintf(`SELECT %s
		FROM argocd_app_mapping FINAL
		WHERE instance_id IN (%s)
		  AND removed_at = toDateTime64(0, 3)
		  AND (%s) > (?, ?, ?, ?, ?, ?, ?)
		ORDER BY %s
		LIMIT %d
		SETTINGS max_execution_time = 15`, argocdMappingCols, placeholders(n, "?"), argocdMappingOrder, argocdMappingOrder, argocdMappingPage)
}

func scanMappingRow(rows interface{ Scan(dest ...any) error }) (argocd.MappingRow, error) {
	var r argocd.MappingRow
	err := rows.Scan(&r.ClusterID, &r.Namespace, &r.WorkloadKind, &r.Workload, &r.InstanceID, &r.AppNamespace, &r.AppName,
		&r.MatchMethod, &r.MatchClass, &r.Confidence, &r.Candidates, &r.FirstMatchedAt, &r.LastVerifiedAt, &r.RemovedAt, &r.Version)
	// removed_at DEFAULT 0 okumada 1970 döner; model sıfır = canlı (zeroIfEpoch).
	r.RemovedAt = zeroIfEpoch(r.RemovedAt)
	return r, err
}

// ArgoCDLiveMappings — v0.10.985 — eşleyicinin uzlaştırma girdisi: verilen
// instance'ların canlı kenarları.
func (s *Store) ArgoCDLiveMappings(ctx context.Context, instanceIDs []string) ([]argocd.MappingRow, error) {
	out := []argocd.MappingRow{}
	if len(instanceIDs) == 0 {
		return out, nil
	}
	cur := make([]any, 7)
	for i := range cur {
		cur[i] = ""
	}
	for {
		args := make([]any, 0, len(instanceIDs)+7)
		for _, id := range instanceIDs {
			args = append(args, id)
		}
		args = append(args, cur...)
		rows, err := s.conn.Query(ctx, argocdLiveMappingsPageSQL(len(instanceIDs)), args...)
		if err != nil {
			return nil, fmt.Errorf("argocd_app_mapping: %w", err)
		}
		n := 0
		for rows.Next() {
			r, err := scanMappingRow(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("argocd_app_mapping scan: %w", err)
			}
			out = append(out, r)
			k := r.Key()
			cur = []any{k.ClusterID, k.Namespace, k.WorkloadKind, k.Workload, k.InstanceID, k.AppNamespace, k.AppName}
			n++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if len(out) > argocdMappingHardCap {
			return nil, fmt.Errorf("argocd_app_mapping: %d kenar tavanı aşıldı — kesik kümeyle uzlaştırılmaz", argocdMappingHardCap)
		}
		if n < argocdMappingPage {
			return out, nil
		}
	}
}

// argocdMappingArgs — SAF: tam satır bağları (kolon sırası argocdMappingCols).
func argocdMappingArgs(r argocd.MappingRow) []any {
	removed := epochIfZero(r.RemovedAt) // sıfır (canlı) → 1970 = DEFAULT 0; yıl 1 DateTime64 aralığı dışı
	return []any{r.ClusterID, r.Namespace, r.WorkloadKind, r.Workload, r.InstanceID, r.AppNamespace, r.AppName,
		r.MatchMethod, r.MatchClass, r.Confidence, r.Candidates, r.FirstMatchedAt.UTC(), r.LastVerifiedAt.UTC(), removed.UTC(), r.Version}
}

// validateArgoCDMappings — SAF: batch'in HEPSİ yazıcı sözleşmesinden geçmeli.
func validateArgoCDMappings(rows []argocd.MappingRow) error {
	for _, r := range rows {
		if err := argocd.ValidateMappingRow(r); err != nil {
			return fmt.Errorf("argocd_app_mapping %s/%s/%s ← %s/%s/%s: %s", r.ClusterID, r.Namespace, r.Workload,
				r.InstanceID, r.AppNamespace, r.AppName, strings.ReplaceAll(err.Error(), "\n", "; "))
		}
	}
	return nil
}

// ArgoCDWriteMappings — v0.10.985 — argocd_app_mapping tam satır batch'i.
func (s *Store) ArgoCDWriteMappings(ctx context.Context, rows []argocd.MappingRow) error {
	if len(rows) == 0 {
		return nil
	}
	if err := validateArgoCDMappings(rows); err != nil {
		return err
	}
	b, err := s.conn.PrepareBatch(asyncInsertCtx(ctx), `INSERT INTO argocd_app_mapping (`+argocdMappingCols+`)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := b.Append(argocdMappingArgs(r)...); err != nil {
			return err
		}
	}
	return b.Send()
}

// ── Servis kartı (§10.6) ─────────────────────────────────────────────────

// argocdServiceWorkloadEdgesSQL — bağlar: n × (cluster_id, namespace,
// workload), tazelik sınırı. Yalnız servisin iş yüklerine İŞ YÜKÜ kenarları.
func argocdServiceWorkloadEdgesSQL(n int) string {
	return fmt.Sprintf(`SELECT %s
		FROM argocd_app_mapping FINAL
		WHERE (cluster_id, namespace, workload) IN (%s)
		  AND workload != ''
		  AND removed_at = toDateTime64(0, 3)
		  AND last_verified_at >= toDateTime64(?, 3, 'UTC')
		ORDER BY %s
		LIMIT %d
		SETTINGS max_execution_time = 10`, argocdMappingCols, placeholders(n, "(?, ?, ?)"), argocdMappingOrder, ArgoCDServiceEdgeMax+1)
}

// argocdServiceWeakEdgesSQL — bağlar: n × (cluster_id, namespace), tazelik
// sınırı. Zayıf (namespace) kenarlar: yalnız "aynı namespace'te eşleşmeyen"
// sayısının girdisi; kesilirse sayı alt sınır, liste etkilenmez.
func argocdServiceWeakEdgesSQL(n int) string {
	return fmt.Sprintf(`SELECT %s
		FROM argocd_app_mapping FINAL
		WHERE (cluster_id, namespace) IN (%s)
		  AND workload_kind = '' AND workload = ''
		  AND removed_at = toDateTime64(0, 3)
		  AND last_verified_at >= toDateTime64(?, 3, 'UTC')
		ORDER BY %s
		LIMIT %d
		SETTINGS max_execution_time = 10`, argocdMappingCols, placeholders(n, "(?, ?)"), argocdMappingOrder, ArgoCDServiceEdgeMax+1)
}

// ArgoCDServiceEdges — servis kartının kenarları. WorkloadCapped: iş yükü
// kenarları tavanı aştı (liste eksik olabilir); WeakCapped: zayıf kenarlar
// tavanı aştı (yalnız sayı alt sınır).
type ArgoCDServiceEdges struct {
	Edges                      []argocd.MappingRow
	WorkloadCapped, WeakCapped bool
}

// ArgoCDServiceEdgesFor — v0.10.985 — servisin iş yüklerinin canlı ve taze
// iş yükü kenarları + (cluster_id, namespace) çiftlerinin zayıf kenarları,
// ayrı bütçeyle (dosya başı).
func (s *Store) ArgoCDServiceEdgesFor(ctx context.Context, workloads []argocd.ServiceWorkload, pairs [][2]string, since time.Time) (ArgoCDServiceEdges, error) {
	out := ArgoCDServiceEdges{Edges: []argocd.MappingRow{}}
	if len(workloads) == 0 || len(pairs) == 0 {
		return out, nil
	}
	sinceArg := chDateTime64Arg(since)
	wargs := make([]any, 0, 3*len(workloads)+1)
	for _, w := range workloads {
		wargs = append(wargs, w.ClusterID, w.Namespace, w.Workload)
	}
	wl, wcap, err := s.argocdServiceEdgeRead(ctx, argocdServiceWorkloadEdgesSQL(len(workloads)), append(wargs, sinceArg))
	if err != nil {
		return out, err
	}
	pargs := make([]any, 0, 2*len(pairs)+1)
	for _, p := range pairs {
		pargs = append(pargs, p[0], p[1])
	}
	weak, kcap, err := s.argocdServiceEdgeRead(ctx, argocdServiceWeakEdgesSQL(len(pairs)), append(pargs, sinceArg))
	if err != nil {
		return out, err
	}
	out.Edges = append(append(out.Edges, wl...), weak...)
	out.WorkloadCapped, out.WeakCapped = wcap, kcap
	return out, nil
}

// argocdServiceEdgeRead — tavan+1 LIMIT'li kenar okuması; capped: tavan aşıldı.
func (s *Store) argocdServiceEdgeRead(ctx context.Context, q string, args []any) ([]argocd.MappingRow, bool, error) {
	out := []argocd.MappingRow{}
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, false, fmt.Errorf("argocd_app_mapping servis: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanMappingRow(rows)
		if err != nil {
			return nil, false, fmt.Errorf("argocd_app_mapping servis scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	capped := len(out) > ArgoCDServiceEdgeMax
	if capped {
		out = out[:ArgoCDServiceEdgeMax]
	}
	return out, capped, nil
}

// argocdStatusForAppsSQL — bağlar: n × (instance_id, app_namespace, app_name).
func argocdStatusForAppsSQL(n int) string {
	return fmt.Sprintf(`SELECT %s
		FROM argocd_app_status FINAL
		WHERE (instance_id, app_namespace, app_name) IN (%s)
		ORDER BY instance_id, app_namespace, app_name, changed_at DESC
		LIMIT 1 BY instance_id, app_namespace, app_name
		LIMIT %d
		SETTINGS max_execution_time = 10`, argocdStatusCols, placeholders(n, "(?, ?, ?)"), n)
}

func appKeyArgs(keys []argocd.AppKey) []any {
	args := make([]any, 0, 3*len(keys))
	for _, k := range keys {
		args = append(args, k.InstanceID, k.AppNamespace, k.Name)
	}
	return args
}

func capAppKeys(keys []argocd.AppKey) []argocd.AppKey {
	if len(keys) > ArgoCDServiceAppMax {
		return keys[:ArgoCDServiceAppMax]
	}
	return keys
}

// ArgoCDLatestStatusFor — v0.10.985 — uygulama başına son argocd_app_status
// satırı ('deleted' dahil: çağıran gösterip göstermeyeceğine karar verir).
func (s *Store) ArgoCDLatestStatusFor(ctx context.Context, keys []argocd.AppKey) (map[argocd.AppKey]argocd.StatusRow, error) {
	out := map[argocd.AppKey]argocd.StatusRow{}
	keys = capAppKeys(keys)
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, argocdStatusForAppsSQL(len(keys)), appKeyArgs(keys)...)
	if err != nil {
		return nil, fmt.Errorf("argocd_app_status servis: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r argocd.StatusRow
		if err := rows.Scan(&r.InstanceID, &r.AppNamespace, &r.AppName, &r.ChangedAt, &r.ChangeKind, &r.SyncStatus,
			&r.HealthStatus, &r.Operation, &r.SyncPhase, &r.AutoSync, &r.Project, &r.Repo, &r.DestServer,
			&r.DestNamespace, &r.ClusterID, &r.Version); err != nil {
			return nil, fmt.Errorf("argocd_app_status servis scan: %w", err)
		}
		out[r.Key()] = r
	}
	return out, rows.Err()
}

// argocdSyncCountsSQL — bağlar: n × uygulama anahtarı, pencere başı, katlama
// aralığı (ms). Uygulamanın önceki 'sync' satırıyla aynı fazda ve ≤ bir tik
// sonraki satır sayılmaz (dosya başı; ilk satırın lagInFrame'i boş faz + 1970).
func argocdSyncCountsSQL(n int) string {
	return fmt.Sprintf(`SELECT instance_id, app_namespace, app_name, sync_phase, count()
		FROM (
			SELECT instance_id, app_namespace, app_name, sync_phase, changed_at,
			       lagInFrame(sync_phase) OVER w AS prev_phase,
			       lagInFrame(changed_at) OVER w AS prev_at
			FROM argocd_app_status FINAL
			WHERE (instance_id, app_namespace, app_name) IN (%s)
			  AND change_kind = 'sync'
			  AND changed_at >= toDateTime64(?, 3, 'UTC')
			WINDOW w AS (PARTITION BY instance_id, app_namespace, app_name ORDER BY changed_at ASC
			             ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
		)
		WHERE NOT (sync_phase = prev_phase AND dateDiff('millisecond', prev_at, changed_at) <= ?)
		GROUP BY instance_id, app_namespace, app_name, sync_phase
		ORDER BY instance_id, app_namespace, app_name, sync_phase
		LIMIT %d
		SETTINGS max_execution_time = 10`, placeholders(n, "(?, ?, ?)"), n*8)
}

// ArgoCDSyncCounts — v0.10.985 — son pencerede metrikle görülen tamamlanan
// senkron satırları, uygulama → faz → adet (dosya başı: alt sınır). fold:
// işçinin tik aralığı (metricsS) — bu kadar içindeki aynı fazlı ardışık
// satır tek senkron sayılır.
func (s *Store) ArgoCDSyncCounts(ctx context.Context, keys []argocd.AppKey, since time.Time, fold time.Duration) (map[argocd.AppKey]map[string]int, error) {
	out := map[argocd.AppKey]map[string]int{}
	keys = capAppKeys(keys)
	if len(keys) == 0 {
		return out, nil
	}
	args := append(appKeyArgs(keys), chDateTime64Arg(since), fold.Milliseconds())
	rows, err := s.conn.Query(ctx, argocdSyncCountsSQL(len(keys)), args...)
	if err != nil {
		return nil, fmt.Errorf("argocd_app_status senkron: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k argocd.AppKey
		var phase string
		var n uint64
		if err := rows.Scan(&k.InstanceID, &k.AppNamespace, &k.Name, &phase, &n); err != nil {
			return nil, fmt.Errorf("argocd_app_status senkron scan: %w", err)
		}
		if out[k] == nil {
			out[k] = map[string]int{}
		}
		out[k][phase] += int(n)
	}
	return out, rows.Err()
}
