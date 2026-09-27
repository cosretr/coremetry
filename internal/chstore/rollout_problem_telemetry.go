package chstore

// rollout_problem_telemetry.go — v0.10.241 Problem↔Rollout korelasyonu
// (D1, telemetri yarısı). Yalnız MV + spans okur → telemetryReadConn
// (RoundRobin); state tablosu YOK (conn_strategy_test allowlist'i).
//
// Neden MV'nin tersi: RolloutServices (rollout_services.go) "bu rollout
// hangi servisleri taşıdı" sorar; burada "bu servis pencerede hangi
// (cluster, ns, workload, revision)'da span üretti" — aynı MV, ters yön.
// service_name ORDER BY öneğinde DEĞİL (5. kolon) → pencere partition'ı
// taranır; 125 dk × 1-dk kova × birkaç bin iş yükü = küçük, LIMIT + 10 s
// tavanla sınırlı.
//
// Cluster: MV'deki `cluster` SPAN değeridir (k8s.cluster.name…),
// workload_rollouts.cluster_id EffectiveID'dir. Çeviri çağıranda
// (thanos ayarı SpanClusterKeys → EffectiveID); burada ham değer döner.

import (
	"context"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
)

// WorkloadRevisionRef — MV'den/spans'ten gelen ham (span cluster değeri)
// iş yükü + revizyon referansı.
type WorkloadRevisionRef struct {
	Cluster   string // span değeri, EffectiveID DEĞİL
	Namespace string
	Workload  string
	Revision  string
}

const rolloutRefsForServiceMax = 50

// rolloutRefsForServiceSQL — SAF; nClusters > 0 ise cluster IN (…) eklenir.
func rolloutRefsForServiceSQL(nClusters int) string {
	clusterWhere := ""
	if nClusters > 0 {
		clusterWhere = " AND cluster IN (" + chPlaceholders(nClusters) + ")"
	}
	return `SELECT cluster, k8s_namespace, workload, revision
		FROM workload_revision_activity_1m
		WHERE service_name = ?
		  AND bucket >= toDateTime64(?, 3, 'UTC') AND bucket <= toDateTime64(?, 3, 'UTC')
		  AND workload != '' AND revision != ''` + clusterWhere + `
		GROUP BY cluster, k8s_namespace, workload, revision
		ORDER BY cluster, k8s_namespace, workload, revision
		LIMIT ` + fmt.Sprint(rolloutRefsForServiceMax) + ` SETTINGS max_execution_time = 10`
}

// RolloutRefsForService — servisin [from, to] penceresinde span ürettiği
// (cluster, ns, workload, revision) kümesi. clusterValues boşsa cluster
// filtresi yok (tek-cluster kurulum / Clusters zenginleştirmesi boş).
func (s *Store) RolloutRefsForService(ctx context.Context, service string, clusterValues []string, from, to time.Time) ([]WorkloadRevisionRef, error) {
	if service == "" {
		return nil, nil
	}
	args := []any{service, chDateTime64Arg(from), chDateTime64Arg(to)}
	for _, c := range clusterValues {
		args = append(args, c)
	}
	rows, err := s.telemetryReadConn().Query(ctx, rolloutRefsForServiceSQL(len(clusterValues)), args...)
	if err != nil {
		return nil, fmt.Errorf("rollout refs for service: %w", err)
	}
	defer rows.Close()
	var out []WorkloadRevisionRef
	for rows.Next() {
		var r WorkloadRevisionRef
		if err := rows.Scan(&r.Cluster, &r.Namespace, &r.Workload, &r.Revision); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ServiceWorkloadsMax — v0.10.981 — ServiceWorkloads'un döndürdüğü en çok
// iş yükü (GitOps sekmesi; RolloutsForWorkloads da 50'de keser).
const ServiceWorkloadsMax = 50

// serviceWorkloadsSQL — SAF. RolloutRefsForService'in iş yükü düzeyi
// kardeşi: revizyona göre GRUPLAMAZ — çok revizyonlu tek iş yükü LIMIT'i
// yiyip alfabede sonraki iş yüklerini düşürmesin (v0.10.981 inceleme).
// LIMIT tavan+1: çağıran kesikliği bilsin.
func serviceWorkloadsSQL() string {
	return `SELECT cluster, k8s_namespace, workload
		FROM workload_revision_activity_1m
		WHERE service_name = ?
		  AND bucket >= toDateTime64(?, 3, 'UTC') AND bucket <= toDateTime64(?, 3, 'UTC')
		  AND workload != ''
		GROUP BY cluster, k8s_namespace, workload
		ORDER BY cluster, k8s_namespace, workload
		LIMIT ` + fmt.Sprint(ServiceWorkloadsMax+1) + ` SETTINGS max_execution_time = 10`
}

// ServiceWorkloads — v0.10.981 — servisin [from, to] penceresinde span
// ürettiği tekil (cluster span değeri, ns, workload); Revision boş.
// capped: tavandan fazlası vardı (liste ServiceWorkloadsMax'ta kesildi).
func (s *Store) ServiceWorkloads(ctx context.Context, service string, from, to time.Time) ([]WorkloadRevisionRef, bool, error) {
	if service == "" {
		return nil, false, nil
	}
	rows, err := s.telemetryReadConn().Query(ctx, serviceWorkloadsSQL(), service, chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return nil, false, fmt.Errorf("service workloads: %w", err)
	}
	defer rows.Close()
	var out []WorkloadRevisionRef
	for rows.Next() {
		var r WorkloadRevisionRef
		if err := rows.Scan(&r.Cluster, &r.Namespace, &r.Workload); err != nil {
			return nil, false, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	capped := len(out) > ServiceWorkloadsMax
	if capped {
		out = out[:ServiceWorkloadsMax]
	}
	return out, capped, nil
}

// rolloutRefForPodSQL — SAF. service_name PK öneği + zaman sınırı; pod
// filtresi terfi kolonu k8s_pod (promoted_attr.go). Revizyon MV ile AYNI
// ifade (RS yoksa imaj tag'i — v0.10.211 STS/DS vekili).
func rolloutRefForPodSQL() string {
	return `SELECT any(cluster), any(k8s_namespace),
		any(multiIf(k8s_deployment != '', k8s_deployment,
		            k8s_statefulset != '', k8s_statefulset,
		            k8s_daemonset != '', k8s_daemonset, '')),
		any(if(k8s_replicaset != '', k8s_replicaset, container_image_tag))
		FROM spans
		WHERE service_name = ? AND k8s_pod = ?
		  AND time >= toDateTime64(?, 3, 'UTC') AND time <= toDateTime64(?, 3, 'UTC')
		LIMIT 1 SETTINGS max_execution_time = 5`
}

// RolloutRefForPod — problemin pod'unun penceredeki iş yükü + revizyonu.
// ok=false → pod pencerede span üretmemiş ya da iş yükü/revizyon boş.
func (s *Store) RolloutRefForPod(ctx context.Context, service, pod string, from, to time.Time) (WorkloadRevisionRef, bool, error) {
	if service == "" || pod == "" {
		return WorkloadRevisionRef{}, false, nil
	}
	row := s.telemetryReadConn().QueryRow(ctx, rolloutRefForPodSQL(),
		service, pod, chDateTime64Arg(from), chDateTime64Arg(to))
	var r WorkloadRevisionRef
	if err := row.Scan(&r.Cluster, &r.Namespace, &r.Workload, &r.Revision); err != nil {
		return WorkloadRevisionRef{}, false, fmt.Errorf("rollout ref for pod: %w", err)
	}
	if r.Workload == "" || r.Revision == "" {
		return WorkloadRevisionRef{}, false, nil
	}
	return r, true, nil
}

// ArgoCDMapperWorkloadsMax — v0.10.985 — Rollouts v2 P3.2 eşleyicisinin tur
// başına okuduğu en çok iş yükü (tüm filo). Aşılırsa çağıran turu ATLAR:
// kısmi listeyle ad kenarları yanlışlıkla kaldırılırdı (argocd/mapper.go).
const ArgoCDMapperWorkloadsMax = 200_000

// argocdMapperWorkloadsSQL — SAF. serviceWorkloadsSQL'in filo kardeşi: servis
// süzgeci yok, tür MV'nin anyLast(workload_kind) SimpleAggregate kolonundan
// (RolloutsForWorkloads emsali). ORDER BY öneki (cluster, k8s_namespace,
// workload) üzerinde GROUP BY; zaman sınırı bucket (günlük bölüm budaması),
// LIMIT tavan+1 (kesiklik bilinsin), max_execution_time 25 s (işçi yolu;
// istemci ReadTimeout 30 s — query_budget_test).
func argocdMapperWorkloadsSQL() string {
	return `SELECT cluster, k8s_namespace, anyLast(workload_kind) AS kind, workload
		FROM workload_revision_activity_1m
		WHERE bucket >= toDateTime64(?, 3, 'UTC') AND bucket <= toDateTime64(?, 3, 'UTC')
		  AND workload != ''
		GROUP BY cluster, k8s_namespace, workload
		ORDER BY cluster, k8s_namespace, workload
		LIMIT ` + fmt.Sprint(ArgoCDMapperWorkloadsMax+1) + ` SETTINGS max_execution_time = 25`
}

// ArgoCDMapperWorkloads — v0.10.985 — [from, to] içinde span üreten tekil
// (span cluster değeri, ns, workload) + tür; capped: tavandan fazlası vardı.
// Telemetri okuma havuzu (MV; state tablosu yok).
func (s *Store) ArgoCDMapperWorkloads(ctx context.Context, from, to time.Time) ([]argocd.WorkloadObs, bool, error) {
	rows, err := s.telemetryReadConn().Query(ctx, argocdMapperWorkloadsSQL(), chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return nil, false, fmt.Errorf("argocd mapper workloads: %w", err)
	}
	defer rows.Close()
	out := []argocd.WorkloadObs{}
	for rows.Next() {
		var w argocd.WorkloadObs
		if err := rows.Scan(&w.SpanCluster, &w.Namespace, &w.Kind, &w.Workload); err != nil {
			return nil, false, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	capped := len(out) > ArgoCDMapperWorkloadsMax
	if capped {
		out = out[:ArgoCDMapperWorkloadsMax]
	}
	return out, capped, nil
}
