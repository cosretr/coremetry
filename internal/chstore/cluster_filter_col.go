package chstore

// cluster_filter_col.go — v0.10.2466 (Operator-reported 2026-11-22, prod):
// Traces, 12 saat, `resource.openshift.cluster.name = ocp4` → liste
// "code: 159, Timeout exceeded: elapsed 25068 ms" (şerit/KPI MV'den geldi).
//
// Kök neden: resource önekli cluster anahtarı FilterExpr'de dizi aramasına
// düşüyordu — `res_values[indexOf(res_keys, 'openshift.cluster.name')]`
// (+ kvh bloom öneki). Cluster değeri baskın olduğundan bloom HİÇBİR granülü
// elemez; her span için iki şişman Array(String) kolonu açılır. Üst çubuktaki
// Cluster seçicisi ise aynı soruyu terfi edilmiş `cluster` LowCardinality
// kolonundan sorar (clusterExpr / addClusterConjunct).
//
// Ölçüldü (lokal, CH 26.2, 12 saat ≈ 826k span, liste şekli GROUP BY
// trace_id … LIMIT 51):
//
//	dizi araması        470 MiB okuma  66 ms   (2 saatlik basamak 98.5 MiB)
//	dizi + kvh bloom    430 MiB        98 ms   (budama yok — değer baskın)
//	`cluster` kolonu   31.5 MiB        15 ms   (2 saatlik basamak 7.9 MiB)
//
// Çare: kolon okunabilirken (hasClusterCol probu, v0.8.162) resource önekli
// k8s.cluster.name / openshift.cluster.name ve çıplak `cluster` anahtarı
// kolona derlenir — Cluster seçicisinin yoluyla aynı. Kolon yoksa (harici
// Distributed, cluster_name ayarsız) dizi yolu aynen (code 47 sınıfı).
//
// Anlam farkı (bilinçli, k8s_namespace terfisiyle aynı sınıf): `cluster`
// 6 basamaklı coalesce'tir (clusterDeriveExpr); bir span hem
// k8s.cluster.name=X hem openshift.cluster.name=Y taşırsa kolon X döner.
// İki anahtarı farklı değerle basan kurulum görülmedi; tek anahtarlı her
// kurulumda sonuç kümesi aynıdır.

import "sync/atomic"

// clusterColReady — Store.hasClusterCol'un paket düzeyi yansıması
// (FilterExpr.SQL saf, Store'u görmez — opGroupColReady emsali).
var clusterColReady atomic.Bool

// clusterResourceKeys — kolona derlenen resource anahtarları.
var clusterResourceKeys = []string{"k8s.cluster.name", "openshift.cluster.name"}

var (
	wellKnownClusterCol         = withClusterCol(wellKnown, "cluster")
	wellKnownResourceClusterCol = withClusterCol(wellKnownResource, clusterResourceKeys...)
)

// withClusterCol — SAF: haritanın kopyası, verilen anahtarlar `cluster` kolonuna.
func withClusterCol(base map[string]string, keys ...string) map[string]string {
	out := make(map[string]string, len(base)+len(keys))
	for k, v := range base {
		out[k] = v
	}
	for _, k := range keys {
		out[k] = clusterColExpr
	}
	return out
}

// spansFilterMaps — spans derleyicisinin (wellKnown, resource) haritaları.
func spansFilterMaps(colReady bool) (map[string]string, map[string]string) {
	if colReady {
		return wellKnownClusterCol, wellKnownResourceClusterCol
	}
	return wellKnown, wellKnownResource
}
