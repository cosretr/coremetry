package chstore

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// entity_schema.go — K8s ENTITY KATMANI, span-türevli "görüldü" indeksi
// (v0.10.127, AŞAMA 3 adım 1; docs/plans/entity-layer-design-2026-08-28.md §2.4).
//
// ── NEDEN ŞİMDİ MV, pod_seen NEDEN ERTELENMİŞTİ ──────────────────────────
//
// pod_inventory.go (v0.10.40) bir pod_seen MV'sini bilinçli ertelemişti:
// namespace/pod terfi kolonu değildi, MV her span INSERT'inde
// `res_values[indexOf(res_keys, …)]` koşacaktı. Bu MV o koşulu KALDIRARAK
// geliyor: k8s_namespace / k8s_pod / k8s_node artık MATERIALIZED terfi
// kolonları (promoted_attr.go, res kapsamı) — indexOf INSERT'te bir kez,
// kolon olarak, MV düz kolon okur (yerel CH 26.2'de ölçüldü: combined MV
// kaynak tablonun MATERIALIZED kolonunu görür). `WHERE k8s_pod != ''`
// kapısı k8s bağlamsız span'leri MV'ye hiç sokmaz.
//
// ── ŞEKİL ────────────────────────────────────────────────────────────────
//
// Filtre öznesi service_name ÖNDE: "bu servisi hangi pod'lar taşıyor"
// sorusu ORDER BY önekinden okunur. Ters yön (pod → servisler) MV'yi
// önek dışı okurdu; o soru entity_relations.runs ile cevaplanır, MV yalnız
// zaman-dilimli sayım için. tDigest YOK (satır ~1.5 KB; 2.5k pod × 288
// kova/gün) — yüzdelik ham yoldan, pod filtresi set(0) index ile sınırlı.
// Tek kademe: 5m, 30 gün. v0.10.2265'e kadar bir 1m kademesi (3 gün) da
// vardı; okuyucusu yoktu, INSERT fan-out'u 5m ile aynıydı ve ~5 kat satır
// üretiyordu (docs/architecture/entity-s6-cost-note.md madde 3) — operatör
// onayıyla düştü (dropRetiredEntitySeen1m, migrations/0016).
//
// Gün-bir üç kayıt (cluster.go): highVolumeTables + defaultShardPolicy
// (cityHash64(service_name)) + tablesWithoutTraceID.

const entitySeen5mTTLDays = 30

// entitySeenMVDDL — kademe şablonu. Saf; tablo-testli.
func entitySeenMVDDL(name, interval string, ttlDays int) string {
	return fmt.Sprintf(`CREATE MATERIALIZED VIEW IF NOT EXISTS %s
		 ENGINE = AggregatingMergeTree
		 PARTITION BY toDate(time_bucket)
		 ORDER BY (service_name, cluster, k8s_namespace, k8s_pod, time_bucket)
		 TTL toDate(time_bucket) + INTERVAL %d DAY
		 SETTINGS index_granularity = 8192
		 AS SELECT
		   toStartOfInterval(time, INTERVAL %s)    AS time_bucket,
		   service_name,
		   cluster,
		   k8s_namespace,
		   k8s_pod,
		   anyLastSimpleState(k8s_node)             AS k8s_node,
		   countState()                             AS span_count_state,
		   countIfState(status_code = 'error')      AS error_count_state,
		   sumState(duration)                       AS duration_sum_state,
		   minState(time)                           AS first_seen_state,
		   maxState(time)                           AS last_seen_state
		 FROM spans
		 WHERE k8s_pod != ''
		 GROUP BY time_bucket, service_name, cluster, k8s_namespace, k8s_pod`,
		name, ttlDays, interval)
}

// ── v0.10.2265: entity_seen_1m KALDIRILDI ────────────────────────────────

// retiredEntitySeen1m — kaldırılan 1m kademesinin çıplak adı. Küme kipinde
// `<ad>_local` combined MV (gizli `.inner_id.<uuid>` iç tablosuyla) +
// `<ad>` Distributed sarmalayıcı; tek düğümde çıplak adlı combined MV.
const retiredEntitySeen1m = "entity_seen_1m"

// retiredDropStep — düşürme planının bir adımı.
type retiredDropStep struct {
	Name string
	// MV — true: combined MV, dropCombinedMV (iç tablo önce, hacim guard'ı
	// kapalı). false: Distributed sarmalayıcı, düz DROP … SYNC.
	MV bool
}

// entitySeen1mDropPlan — SAF: ölçülen motorlardan düşürme planı.
//
// Sıra: önce MV'ler (INSERT trigger'ı kesilir), sonra sarmalayıcı. Plan
// yalnız beklenen şekillerde üretilir; başka bir motor (ör. MergeTree ya da
// cluster_name'siz bir Distributed) görülürse boş plan + GEREKÇE döner —
// ne olduğunu bilmeden DROP koşmayız (removed_tables.go sözleşme 3/5).
//
//	küme kipi: `_local` MaterializedView → MV adımı; çıplak ad
//	           Distributed → sarmalayıcı adımı (MaterializedView ise MV adımı)
//	tek düğüm: çıplak ad MaterializedView → MV adımı. `_local` ya da
//	           Distributed görülürse bu dış bir küme ve cluster_name boş:
//	           ON CLUSTER yazamayız → atla, 0016'yı göster.
func entitySeen1mDropPlan(clusterMode bool, bareEngines, localEngines []string) ([]retiredDropStep, string) {
	only := func(engines []string, want string) bool {
		for _, e := range engines {
			if e != want {
				return false
			}
		}
		return true
	}
	bare, local := retiredEntitySeen1m, retiredEntitySeen1m+"_local"
	var plan []retiredDropStep
	if !clusterMode {
		if len(localEngines) > 0 || !only(bareEngines, "MaterializedView") {
			return nil, fmt.Sprintf("tek düğüm kipinde %s beklenmeyen şekilde (çıplak=%v, _local=%v) — "+
				"dış küme ve cluster_name boş olabilir; Admin → ClickHouse → K8s entity katmanı → Uygula "+
				"(migrations/0016) ya da config.clickhouse.cluster_name", bare, bareEngines, localEngines)
		}
		if len(bareEngines) > 0 {
			plan = append(plan, retiredDropStep{Name: bare, MV: true})
		}
		return plan, ""
	}
	if !only(localEngines, "MaterializedView") {
		return nil, fmt.Sprintf("%s MaterializedView değil (%v) — ne olduğunu bilmeden DROP koşmayız", local, localEngines)
	}
	if len(localEngines) > 0 {
		plan = append(plan, retiredDropStep{Name: local, MV: true})
	}
	switch {
	case len(bareEngines) == 0:
	case only(bareEngines, "Distributed"):
		plan = append(plan, retiredDropStep{Name: bare})
	case only(bareEngines, "MaterializedView"):
		plan = append(plan, retiredDropStep{Name: bare, MV: true})
	default:
		return nil, fmt.Sprintf("%s beklenmeyen motorla duruyor (%v) — ne olduğunu bilmeden DROP koşmayız", bare, bareEngines)
	}
	return plan, ""
}

// dropRetiredEntitySeen1m — boot temizliği: ÖLÇ → PLAN → DROP → YENİDEN ÖLÇ.
//
// removed_tables.go'nun sözleşmelerini izler (küme geneli ölçüm, nesne yoksa
// DDL yok, senkron, BOOT'U DÜŞÜRMEZ, uygulandı/kuyrukta ayrımı); defterin
// basit DROP yolu MV'yi bilerek reddettiği için combined MV burada
// dropCombinedMV'den (iç `.inner_id.<uuid>` önce) geçer. Nesne yoksa her
// boot yalnız iki system.tables okuması.
func (s *Store) dropRetiredEntitySeen1m(ctx context.Context) {
	bare, local := retiredEntitySeen1m, retiredEntitySeen1m+"_local"
	measure := func() (hosts []string, bareEng, localEng []string, err error) {
		bh, be, err := s.removedTableSightings(ctx, bare)
		if err != nil {
			return nil, nil, nil, err
		}
		lh, le, err := s.removedTableSightings(ctx, local)
		if err != nil {
			return nil, nil, nil, err
		}
		return append(bh, lh...), be, le, nil
	}
	hosts, be, le, err := measure()
	if err != nil {
		log.Printf("[chstore] %s (v0.10.2265'te kaldırıldı) temizliği ATLANDI — şekli okunamadı: %v; "+
			"bir sonraki boot yeniden dener", bare, err)
		return
	}
	if len(hosts) == 0 {
		return
	}
	plan, reason := entitySeen1mDropPlan(s.clusterMode(), be, le)
	if reason != "" {
		log.Printf("[chstore] %s TEMİZLİĞİ ATLANDI — %s", bare, reason)
		return
	}
	log.Printf("[chstore] %s v0.10.2265'te kaldırıldı (okuyucusuz 1m kademesi) — %d satır görüldü, düşürülüyor",
		bare, len(hosts))
	for _, st := range plan {
		var err error
		if st.MV {
			err = s.dropCombinedMV(ctx, st.Name)
		} else {
			err = s.conn.Exec(ctx, "DROP TABLE IF EXISTS "+st.Name+s.onCluster()+" SYNC")
		}
		if err != nil {
			// TEMİZLİK BOOT'U DÜŞÜRMEZ; sonraki adım da koşmaz (MV önce).
			log.Printf("[chstore] %s düşürülemedi (%v) — kalıntı duruyor; bir sonraki boot yeniden dener", st.Name, err)
			return
		}
	}
	left, _, _, err := measure()
	switch {
	case err != nil:
		log.Printf("[chstore] %s DROP gönderildi; sonuç DOĞRULANAMADI (%v)", bare, err)
	case len(left) == 0:
		log.Printf("[chstore] %s düşürüldü (uygulandı)", bare)
	default:
		log.Printf("[chstore] %s DROP kuyruğa alındı ama hâlâ görülüyor (%s); bir sonraki boot yeniden dener",
			bare, strings.Join(left, ", "))
	}
}
