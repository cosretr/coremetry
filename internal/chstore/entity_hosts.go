package chstore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
)

// entity_hosts.go — v0.10.2260 (Entity S6 parça 1; entity-s6-cost-note.md
// seçenek C): host adayının KALICI katmana terfi kanıtı. entity_seen_5m,
// yalnız namespace'siz satırlar, yalnız henüz kanıtlanmamış adaylar (geçiş
// başına ≤ entity.MaxHostProofNames ad, 15 dk'da bir). ORDER BY öneki
// service_name (adayın kendi servisleri) — gün partition'ı + önek; ham
// spans yok. Zaman sınırlı (24 sa + 1 sa) + LIMIT + max_execution_time.

// entityHostProofMaxServices — servis öneki listesinin tavanı.
const entityHostProofMaxServices = 2_000

// entityHostProofSQL — SAF; testte pinli. Hours: hoursFrom'dan sonra kova
// taşıyan saatlik dilim sayısı (kesintisizlik ölçüsü).
const entityHostProofSQL = `
		SELECT k8s_pod, minMerge(first_seen_state) AS first_seen, maxMerge(last_seen_state) AS last_seen,
		       toInt64(uniqExactIf(toStartOfHour(time_bucket), time_bucket >= toStartOfFiveMinute(?))) AS hours
		FROM entity_seen_5m
		WHERE service_name IN (?) AND k8s_namespace = '' AND k8s_pod IN (?)
		  AND time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= now()
		GROUP BY k8s_pod
		LIMIT 1000
		SETTINGS max_execution_time = 10`

// EntityHostProof — adayların since'ten beri ilk/son görülmesi ve saatlik
// doluluğu (ad başına tek satır, tüm servisler / cluster değerleri birlikte).
func (s *Store) EntityHostProof(ctx context.Context, keys []entity.HostKey, since, hoursFrom time.Time) ([]entity.HostProof, error) {
	names, svcs := hostProofArgs(keys)
	if len(names) == 0 || len(svcs) == 0 {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, entityHostProofSQL, hoursFrom, svcs, names, since)
	if err != nil {
		return nil, fmt.Errorf("entity host proof: %w", err)
	}
	defer rows.Close()
	var out []entity.HostProof
	for rows.Next() {
		var p entity.HostProof
		var hours int64
		if err := rows.Scan(&p.Name, &p.FirstSeen, &p.LastSeen, &hours); err != nil {
			return nil, err
		}
		p.Hours = int(hours)
		out = append(out, p)
	}
	return out, rows.Err()
}

// hostProofArgs — SAF: anahtarlar → tekil, sıralı ad ve servis listeleri
// (ad ≤ entity.MaxHostProofNames, servis ≤ entityHostProofMaxServices).
func hostProofArgs(keys []entity.HostKey) (names, svcs []string) {
	n, sv := map[string]bool{}, map[string]bool{}
	for _, k := range keys {
		if k.Name == "" || k.Service == "" {
			continue
		}
		n[k.Name], sv[k.Service] = true, true
	}
	for k := range n {
		names = append(names, k)
	}
	for k := range sv {
		svcs = append(svcs, k)
	}
	sort.Strings(names)
	sort.Strings(svcs)
	if len(names) > entity.MaxHostProofNames {
		names = names[:entity.MaxHostProofNames]
	}
	if len(svcs) > entityHostProofMaxServices {
		svcs = svcs[:entityHostProofMaxServices]
	}
	return names, svcs
}
