package chstore

import (
	"context"
	"time"
)

// service_cluster_envs.go — v0.10.2211 (operatör, prod: endpoint detayında
// cluster=cl-a seçilince VM karoları boş). Cluster VARLIĞI → o servisin o
// cluster'da span'lerinin taşıdığı deploy_env değerleri ("cl-a" →
// "prod-cl-a"). Span tarafının cluster kapsamıyla AYNI boyut
// (service_env_summary_5m cluster × deploy_env); metrik süzgeci ham cluster
// adını ortam etiketiyle birebir kıyaslamaz, bu eşlemeyi kullanır.

// serviceClusterEnvsSQL — SAF (test pinler): MV-first, LIMIT + süre tavanı.
func serviceClusterEnvsSQL() string {
	return `
		SELECT DISTINCT deploy_env
		FROM service_env_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ? AND service_name = ? AND cluster = ? AND deploy_env != ''
		ORDER BY deploy_env
		LIMIT 50
		SETTINGS max_execution_time = 5`
}

// ClusterEnvPair — cluster × deploy_env çifti (filo geneli).
type ClusterEnvPair struct {
	Cluster string
	Env     string
}

// clusterEnvPairsSQL — SAF (test pinler): follow-up 2026-11-21, yalnız-küme
// ortam değerinin (deployment_environment_name = "<küme>") ortamını bulmak
// için cluster VARLIĞI → span ortamları. MV-first, LIMIT + süre tavanı.
func clusterEnvPairsSQL() string {
	return `
		SELECT cluster, deploy_env
		FROM service_env_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ? AND cluster != '' AND deploy_env != ''
		GROUP BY cluster, deploy_env
		ORDER BY cluster, deploy_env
		LIMIT 2000
		SETTINGS max_execution_time = 5`
}

// ClusterEnvPairs — [from, to) içinde görülen (cluster, deploy_env) çiftleri.
func (s *Store) ClusterEnvPairs(ctx context.Context, from, to time.Time) ([]ClusterEnvPair, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, clusterEnvPairsSQL(), alignBucketStart(from), to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClusterEnvPair
	for rows.Next() {
		var p ClusterEnvPair
		if err := rows.Scan(&p.Cluster, &p.Env); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ServiceClusterEnvs — servisin [from, to) içinde cluster'da yaydığı
// deploy_env değerleri (sıralı). Boş servis/cluster → nil.
func (s *Store) ServiceClusterEnvs(ctx context.Context, service, cluster string, from, to time.Time) ([]string, error) {
	if s == nil || service == "" || cluster == "" {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, serviceClusterEnvsSQL(), alignBucketStart(from), to, service, cluster)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
