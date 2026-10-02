package chstore

// anomaly_event_active.go — v0.10.1046: batch gecikme kapısının "zaten
// AKTİF olana dokunma" okuması.
//
// Operatör (prod): "Batch servislerde yük altındaki gecikme artışı da
// anomali sayılmasın." Kural yalnız YENİ olay açılışını keser; yük
// sıçramasından ÖNCE başlamış, hâlâ süren bir gecikme olayı (trace_op_latency
// ya da behavior_change p99) yük gelince tazelenmeyi bırakıp "anomaly
// cleared" diye kapanmamalı. Kapı bu yüzden bir türün o an aktif olay
// kimliklerine bakar.
//
// "Aktif" = ListAnomalyEvents / CountActiveAnomalyEvents'teki AYNI tazelik
// koşulu (last_seen ≥ now − activeAge, varsayılan 10 dk) — yeni bir kavram yok;
// çağıran pencereyi uzatabilir (trace_op_latency kapısı 15 dk verir, gerekçe
// anomaly.opLatActiveAge).
//
// SINIRLI: tür + (isteğe bağlı) pattern + aktiflik + çağıranın servis koşulu
// (batch kalıpları) SQL'de, LIMIT çağıranın tavanı + max_execution_time.
// anomaly_events küçük bir state tablosu (ORDER BY id); FINAL bu boyutta ucuz.

import (
	"context"
	"time"
)

// ActiveAnomalyKey — aktif bir anomali olayının kimliği: fingerprint ve
// fingerprint'i kuran (servis, pattern) çifti.
type ActiveAnomalyKey struct {
	ID      string
	Service string
	Pattern string
}

// activeAnomalyKeysQuery — ListActiveAnomalyKeys'in SAF kurucusu (şekil
// testli). svcCond bir SABİT kolon koşulu olmalı (BatchServiceSQL("service")
// çıktısı; kullanıcı girdisi değil); boşsa servis daraltması yok. Sıra: en
// son görülen önce, eşitlikte id — tavan ısırırsa EN TAZE olaylar kalır ve
// aynı tik iki podda aynı kümeyi okur. pattern "" → tüm pattern'ler; dolu →
// yalnız o pattern (ör. davranış motorunun p99 olayı — diğer metriklerin
// olayları tavana sayılmaz).
func activeAnomalyKeysQuery(kind, pattern string, activeAge time.Duration, svcCond string, svcArgs []any, limit int) (string, []any) {
	if activeAge <= 0 {
		activeAge = anomalyActiveAge
	}
	q := `
		SELECT id, service, pattern
		FROM anomaly_events FINAL
		WHERE kind = ?
		  AND last_seen >= now64() - INTERVAL ? SECOND`
	args := []any{kind, int64(activeAge.Seconds())}
	if pattern != "" {
		q += `
		  AND pattern = ?`
		args = append(args, pattern)
	}
	if svcCond != "" {
		q += `
		  AND ` + svcCond
		args = append(args, svcArgs...)
	}
	q += `
		ORDER BY last_seen DESC, id
		LIMIT ?
		SETTINGS max_execution_time = 5`
	args = append(args, limit)
	return q, args
}

// ListActiveAnomalyKeys — `kind` türünün (pattern doluysa yalnız o pattern'in)
// AKTİF olaylarının kimlikleri, svcCond ile daraltılmış, en çok `limit` satır
// DÖNER. Çağıran tavan aşımını görebilmek için tavan+1 ister. activeAge 0 →
// 10 dk (status türetimiyle aynı); çağıran daha uzun bir pencere verebilir.
func (s *Store) ListActiveAnomalyKeys(ctx context.Context, kind, pattern string, activeAge time.Duration, svcCond string, svcArgs []any, limit int) ([]ActiveAnomalyKey, error) {
	q, args := activeAnomalyKeysQuery(kind, pattern, activeAge, svcCond, svcArgs, limit)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActiveAnomalyKey
	for rows.Next() {
		var k ActiveAnomalyKey
		if err := rows.Scan(&k.ID, &k.Service, &k.Pattern); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
