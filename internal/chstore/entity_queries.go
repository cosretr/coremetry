package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
)

// entity_queries.go — ENTITY SORGU KATMANI (v0.10.130, design §4).
//
// Her sorgu: cluster_id (ya da id'nin taşıdığı cluster) + zaman/geçerlilik
// filtresi + LIMIT + max_execution_time, FINAL okuma. "O an geçerli" =
// valid_from <= t AND (valid_to = 0 OR valid_to >= t); at sıfırsa "şu an
// açık". Pivotlar:
//   servis → pod'lar   entity_seen_5m ORDER BY öneki (service_name önde)
//   (v0.10.2225: pod yönlü sorular — pod → servis, node → pod — CH satırı
//   değil, entity.Resolver: canlı dizin, yoksa entity_seen_5m)
//   pod/pod'lar sağlık entity_seen_5m (cluster, ns, pod) — zaman sınırlı tarama

// EntityRecord — entities satırı (okuma).
type EntityRecord struct {
	Type       string            `json:"type"`
	ClusterID  string            `json:"clusterId"`
	ID         string            `json:"id"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	UID        string            `json:"uid,omitempty"`
	ParentID   string            `json:"parentId,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Source     string            `json:"source"`
	ValidFrom  time.Time         `json:"validFrom"`
	ValidTo    *time.Time        `json:"validTo,omitempty"`
	FirstSeen  time.Time         `json:"firstSeen"`
	LastSeen   time.Time         `json:"lastSeen"`
	Stale      bool              `json:"stale,omitempty"`
	NameStable bool              `json:"nameStable,omitempty"` // uid yok → ömür ayrımı podGap'e bağlı (UI ilanı)
}

// PodRecord — v0.10.2225: çözülmüş pod (entity.Resolver; canlı dizin ya da
// MV) → liste/detay yanıtlarının EntityRecord şekli. Pod'un CH satırı yok;
// ömür = görülme penceresi, ValidTo yok (açık).
func PodRecord(p entity.ResolvedPod) EntityRecord {
	return EntityRecord{Type: entity.TypePod, ClusterID: p.ClusterID, ID: p.ID, Namespace: p.Namespace, Name: p.Name,
		UID: p.UID, ParentID: p.ParentID(), Source: p.Source, ValidFrom: p.FirstSeen, FirstSeen: p.FirstSeen,
		LastSeen: p.LastSeen, NameStable: p.UID == ""}
}

// PodRecords — PodRecord dilimi (nil değil).
func PodRecords(ps []entity.ResolvedPod) []EntityRecord {
	out := make([]EntityRecord, len(ps))
	for i, p := range ps {
		out[i] = PodRecord(p)
	}
	return out
}

// EntityListQuery — liste/picker girdisi.
type EntityListQuery struct {
	ClusterID string
	Type      string
	Namespace string
	Search    string
	ParentID  string // v0.10.135 — kardeş/çocuk listesi: parent_id = ?
	Name      string // v0.10.135 — tam ad: name = ?
	ExcludeID string // v0.10.135 — kendisi hariç (kardeş listesi)
	At        time.Time
	Limit     int
}

// entityValidAtSQL — geçerlilik yan tümcesi. Saf; tablo-testli.
func entityValidAtSQL(at time.Time) (string, []any) {
	if at.IsZero() {
		return "valid_to = toDateTime(0)", nil
	}
	return "valid_from <= ? AND (valid_to = toDateTime(0) OR valid_to >= ?)", []any{at, at}
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

const entityRecordCols = `entity_type, cluster_id, entity_id, namespace, name, uid, parent_id,
	       label_keys, label_values, source, valid_from, valid_to, first_seen, last_seen, stale`

// entityListSQL — liste SQL'i. Saf; tablo-testli. Limit 1..500 (varsayılan 100).
func entityListSQL(q EntityListQuery) (string, []any) {
	where := []string{"cluster_id = ?"}
	args := []any{q.ClusterID}
	if q.Type != "" {
		where = append(where, "entity_type = ?")
		args = append(args, q.Type)
	} else {
		// v0.10.2225 (E1/E3): pod/container satırları artık yazılmıyor; S1'in
		// açık kalmış satırları TTL'e bırakıldı — tipsiz liste yalnız kalıcı
		// katmanı döner (pod'lar entity.Resolver.Pods'tan).
		where = append(where, "entity_type IN (?)")
		args = append(args, entity.StableTypes)
	}
	if q.Namespace != "" {
		where = append(where, "namespace = ?")
		args = append(args, q.Namespace)
	}
	if s := strings.TrimSpace(q.Search); s != "" {
		where = append(where, "name ILIKE ?")
		args = append(args, "%"+escapeLike(s)+"%")
	}
	// v0.10.135 — pivot yardımcıları: aynı workload'ın pod'ları (kardeşler),
	// bir pod'un konteynerleri (çocuklar), tam ad çözümü. cluster_id her
	// zaman ilk koşul: aynı pod adı iki cluster'da iki ayrı kayıttır.
	if q.ParentID != "" {
		where = append(where, "parent_id = ?")
		args = append(args, q.ParentID)
	}
	if q.Name != "" {
		where = append(where, "name = ?")
		args = append(args, q.Name)
	}
	if q.ExcludeID != "" {
		where = append(where, "entity_id != ?")
		args = append(args, q.ExcludeID)
	}
	v, vargs := entityValidAtSQL(q.At)
	where = append(where, v)
	args = append(args, vargs...)
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	args = append(args, limit)
	return `SELECT ` + entityRecordCols + `
		FROM entities FINAL
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY entity_type, namespace, name
		LIMIT ?
		SETTINGS max_execution_time = 10`, args
}

func scanEntityRecord(rows interface {
	Scan(dest ...any) error
}) (EntityRecord, error) {
	var r EntityRecord
	var keys, vals []string
	var validTo time.Time
	var stale uint8
	if err := rows.Scan(&r.Type, &r.ClusterID, &r.ID, &r.Namespace, &r.Name, &r.UID, &r.ParentID,
		&keys, &vals, &r.Source, &r.ValidFrom, &validTo, &r.FirstSeen, &r.LastSeen, &stale); err != nil {
		return r, err
	}
	if len(keys) > 0 {
		r.Labels = make(map[string]string, len(keys))
		for i, k := range keys {
			if i < len(vals) {
				r.Labels[k] = vals[i]
			}
		}
	}
	if validTo.Unix() > 0 {
		t := validTo
		r.ValidTo = &t
	}
	r.Stale = stale == 1
	r.NameStable = r.Type == "pod" && r.UID == ""
	return r, nil
}

// EntityList — picker/liste (cluster zorunlu; çağıran doğrular).
func (s *Store) EntityList(ctx context.Context, q EntityListQuery) ([]EntityRecord, error) {
	sql, args := entityListSQL(q)
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("entity list: %w", err)
	}
	defer rows.Close()
	out := []EntityRecord{}
	for rows.Next() {
		r, err := scanEntityRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EntityLifetimes — bir id'nin tüm ömürleri (en yeni önce, ≤ 50) + at'e
// göre geçerli olanı (yoksa en yeni).
func (s *Store) EntityLifetimes(ctx context.Context, id string, at time.Time) (current *EntityRecord, all []EntityRecord, err error) {
	cur, _, all, err := s.EntityLifetimesAt(ctx, id, at)
	return cur, all, err
}

// EntityLifetimesAt — v0.10.135 (DETAY SAYFALARI: zaman geçerliliği).
// match=false: dönen kayıt istenen anı KAPSAMIYOR (at verildiyse "o an
// geçerli değildi", at sıfırsa "artık mevcut değil") — en yeni ömür yine
// döner ki sayfa 404 yerine "son görülme X + tarihçe" gösterebilsin.
func (s *Store) EntityLifetimesAt(ctx context.Context, id string, at time.Time) (current *EntityRecord, match bool, all []EntityRecord, err error) {
	rows, err := s.conn.Query(ctx, `SELECT `+entityRecordCols+`
		FROM entities FINAL
		WHERE entity_id = ? AND last_seen >= now() - INTERVAL 180 DAY
		ORDER BY valid_from DESC
		LIMIT 50
		SETTINGS max_execution_time = 10`, id)
	if err != nil {
		return nil, false, nil, fmt.Errorf("entity lifetimes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanEntityRecord(rows)
		if err != nil {
			return nil, false, nil, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, nil, err
	}
	idx, match := pickLifetime(all, at)
	if idx < 0 {
		return nil, false, all, nil
	}
	return &all[idx], match, all, nil
}

// pickLifetime — saf. `all` valid_from DESC sıralı. at sıfır → açık ömür;
// at verili → at'i kapsayan ömür (sınırlar dahil). Kapsayan yoksa en yeni
// kayıt (idx 0) + match=false; boş → -1.
func pickLifetime(all []EntityRecord, at time.Time) (idx int, match bool) {
	for i := range all {
		r := &all[i]
		if at.IsZero() {
			if r.ValidTo == nil {
				return i, true
			}
			continue
		}
		if !r.ValidFrom.After(at) && (r.ValidTo == nil || !r.ValidTo.Before(at)) {
			return i, true
		}
	}
	if len(all) > 0 {
		return 0, false
	}
	return -1, false
}

// walkEntityParents — parent_id ile yukarı (en çok 8, döngü korumalı). Saf.
func walkEntityParents(get func(id string) (EntityRecord, bool), id string) []EntityRecord {
	var out []EntityRecord
	seen := map[string]bool{id: true}
	cur, ok := get(id)
	if !ok {
		return out
	}
	for i := 0; i < 8 && cur.ParentID != "" && !seen[cur.ParentID]; i++ {
		seen[cur.ParentID] = true
		p, ok := get(cur.ParentID)
		if !ok {
			break
		}
		out = append(out, p)
		cur = p
	}
	return out
}

// EntityParents — ebeveyn zinciri (at anına göre).
func (s *Store) EntityParents(ctx context.Context, id string, at time.Time) []EntityRecord {
	get := func(x string) (EntityRecord, bool) {
		cur, _, err := s.EntityLifetimes(ctx, x, at)
		if err != nil || cur == nil {
			return EntityRecord{}, false
		}
		return *cur, true
	}
	return walkEntityParents(get, id)
}

// EntityChildrenCounts — parent_id = id olan geçerli varlıklar, tipe göre.
func (s *Store) EntityChildrenCounts(ctx context.Context, cid, id string, at time.Time) (map[string]int, error) {
	v, vargs := entityValidAtSQL(at)
	args := append([]any{cid, id}, vargs...)
	rows, err := s.conn.Query(ctx, `SELECT entity_type, count() FROM entities FINAL
		WHERE cluster_id = ? AND parent_id = ? AND `+v+`
		GROUP BY entity_type LIMIT 20 SETTINGS max_execution_time = 10`, args...)
	if err != nil {
		return nil, fmt.Errorf("entity children: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var n uint64
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = int(n)
	}
	return out, rows.Err()
}

// EntitySeenAgg — entity_seen_5m özeti (pod × servis).
type EntitySeenAgg struct {
	Cluster   string    `json:"cluster"`
	Namespace string    `json:"namespace"`
	Pod       string    `json:"pod"`
	Node      string    `json:"node,omitempty"`
	Service   string    `json:"service"`
	Spans     int64     `json:"spans"`
	Errors    int64     `json:"errors"`
	AvgMs     float64   `json:"avgMs"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

const entitySeenAggCols = `cluster, k8s_namespace, k8s_pod, anyLast(k8s_node) AS node, service_name,
	       toInt64(countMerge(span_count_state)) AS spans,
	       toInt64(countIfMerge(error_count_state)) AS errors,
	       sumMerge(duration_sum_state) / greatest(countMerge(span_count_state), 1) / 1e6 AS avg_ms,
	       minMerge(first_seen_state) AS first_seen, maxMerge(last_seen_state) AS last_seen`

func (s *Store) scanSeenAgg(ctx context.Context, sql string, args ...any) ([]EntitySeenAgg, error) {
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("entity seen: %w", err)
	}
	defer rows.Close()
	out := []EntitySeenAgg{}
	for rows.Next() {
		var r EntitySeenAgg
		if err := rows.Scan(&r.Cluster, &r.Namespace, &r.Pod, &r.Node, &r.Service, &r.Spans, &r.Errors, &r.AvgMs, &r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EntitySeenForService — servis → pod'lar (ORDER BY öneki). cluster = span
// cluster DEĞERİ (SpanClusterKey), boşsa tüm cluster'lar (yanıt clusterAmbiguous ilan eder).
func (s *Store) EntitySeenForService(ctx context.Context, service string, clusterValues []string, from, to time.Time) ([]EntitySeenAgg, error) {
	where := "service_name = ? AND time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= ?"
	args := []any{service, from, to}
	if len(clusterValues) > 0 { // v0.10.139 — bir kayıt birden çok değer
		where += " AND cluster IN (?)"
		args = append(args, clusterValues)
	}
	return s.scanSeenAgg(ctx, `SELECT `+entitySeenAggCols+`
		FROM entity_seen_5m
		WHERE `+where+`
		GROUP BY cluster, k8s_namespace, k8s_pod, service_name
		ORDER BY spans DESC
		LIMIT 500
		SETTINGS max_execution_time = 10`, args...)
}

// EntitySeenForPods — node/namespace pivotu: verilen pod adları (≤ 500).
func (s *Store) EntitySeenForPods(ctx context.Context, clusterValues []string, namespace string, pods []string, from, to time.Time) ([]EntitySeenAgg, error) {
	if len(pods) == 0 || len(clusterValues) == 0 {
		return []EntitySeenAgg{}, nil
	}
	if len(pods) > 500 {
		pods = pods[:500]
	}
	args := []any{from, to, clusterValues, pods}
	if namespace != "" {
		args = append(args, namespace)
	}
	return s.scanSeenAgg(ctx, entitySeenForPodsSQL(namespace != ""), args...)
}

// EntitySeenPods — v0.10.2225: cluster değerlerinin pencerede span basan
// pod'ları (namespace "" = tümü); entity.Resolver.Pods'un MV yedeği (canlı
// dizin ıskası ya da geçmiş an). Saf SQL testte pinli.
func (s *Store) EntitySeenPods(ctx context.Context, clusterValues []string, namespace string, from, to time.Time) ([]EntitySeenAgg, error) {
	if len(clusterValues) == 0 {
		return []EntitySeenAgg{}, nil
	}
	args := []any{from, to, clusterValues}
	if namespace != "" {
		args = append(args, namespace)
	}
	return s.scanSeenAgg(ctx, entitySeenPodsSQL(namespace != ""), args...)
}

// entitySeenPodsSQL — zaman sınırı + LIMIT 5000 + max_execution_time.
// Pod yönlü okuma MV öneki dışıdır (öneki service_name); pencere ≤ 1 saat.
func entitySeenPodsSQL(withNamespace bool) string {
	where := "time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= ? AND cluster IN (?) AND k8s_pod != ''"
	if withNamespace {
		where += " AND k8s_namespace = ?"
	}
	return `SELECT ` + entitySeenAggCols + `
		FROM entity_seen_5m
		WHERE ` + where + `
		GROUP BY cluster, k8s_namespace, k8s_pod, service_name
		ORDER BY spans DESC
		LIMIT 5000
		SETTINGS max_execution_time = 10`
}

// entitySeenForPodsSQL — v0.10.2193: entity.Resolver'ın pod okuması (Entity
// S0); şekli testte pinli (zaman sınırı, LIMIT, max_execution_time).
func entitySeenForPodsSQL(withNamespace bool) string {
	where := "time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= ? AND cluster IN (?) AND k8s_pod IN (?)"
	if withNamespace {
		// v0.10.190 (operatör-bildirimi, prod: "pod'dan geçen servis yok diyor
		// ama var"): bir cluster'ın collector'ı k8s.namespace.name BASMIYOR →
		// MV satırları k8s_namespace = '' ile duruyor; pod sayfası namespace'i
		// Thanos'tan bilip `= ?` ile arıyordu → sıfır satır, oysa altındaki
		// trace listesi (yalnız pod adı) aynı span'leri gösteriyordu.
		// Namespace'siz satır da eşleşir: pod adı cluster içinde tek
		// varsayılır, çağıran boş satır sayısını İLAN eder (nsMissingRows).
		// (Tekil EntitySeenForPod çağıransızdı, v0.10.190'da silindi.)
		where += " AND k8s_namespace IN (?, '')"
	}
	return `SELECT ` + entitySeenAggCols + `
		FROM entity_seen_5m
		WHERE ` + where + `
		GROUP BY cluster, k8s_namespace, k8s_pod, service_name
		ORDER BY spans DESC
		LIMIT 2000
		SETTINGS max_execution_time = 10`
}
