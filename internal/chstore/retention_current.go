package chstore

// retention_current.go — v0.10.1869 (operatör onayı 2026-11-06).
//
// NEDEN: açılışta ApplyPersistedRetention, kayıtlı saklama sürelerini HER
// boot'ta koşulsuz yeniden uyguluyordu: 7 tablo ALTER'ı + 6 exemplar kolon
// ALTER'ı. Küme kipinde bunlar ON CLUSTER DDL kuyruğundan geçer; kuyruğu
// yavaş bir kümede ifade başına ~20 sn → pod ~4,5 dakika HAZIR OLAMIYOR
// (test ortamı boot logu, 2026-11-06). Oysa değer neredeyse her boot'ta
// zaten yerindedir.
//
// NE: boot yolu, ALTER'dan önce tablonun GERÇEK TTL'ini okur ve istenenle
// aynıysa ALTER'ı atlar. Admin'in Settings'ten yaptığı değişiklik (PUT) bu
// dosyadan GEÇMEZ; her zaman uygular.
//
// GÜVENLİK KURALI — "emin değilsen uygula": atlama kararı yalnız şu üç şart
// birlikte sağlanınca verilir, aksi her durumda bugünkü davranış (ALTER):
//   1. okuma hatasız (küme kipinde erişilemeyen replika = hata; bilerek
//      skip_unavailable_shards YOK),
//   2. tabloyu taşıyan host sayısı = kümedeki host sayısı (v0.9.620 dersi:
//      TTL tek bir host'ta ayrışmış olabilir; tek host'a bakıp atlamak o
//      hata sınıfını geri getirir),
//   3. HER host'taki TTL ifadesi istenenle aynı (kanonik karşılaştırma).
//
// Karşılaştırma create_table_query metni üzerinden: ClickHouse ifadeyi
// yeniden biçimler (`INTERVAL 30 DAY` → `toIntervalDay(30)`), sürüme göre
// de değişebilir. canonicalTTL iki yazımı aynı kalıba indirir; tanımadığı
// bir biçim "farklı" çıkar → ALTER koşar (yalnız hız kaybı, yanlış atlama
// yok).

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var (
	ttlIntervalRe   = regexp.MustCompile(`(?i)\binterval\s+(\d+)\s+(day|hour)\b`)
	ttlToIntervalRe = regexp.MustCompile(`(?i)\btointerval(day|hour)\(\s*(\d+)\s*\)`)
)

// canonicalTTL — TTL ifadesini karşılaştırılabilir tek kalıba indirir:
// `INTERVAL N DAY|HOUR` ve `toIntervalDay|Hour(N)` aynı belirtece döner,
// boşluk / ters tırnak / harf büyüklüğü farkı silinir.
func canonicalTTL(expr string) string {
	s := ttlIntervalRe.ReplaceAllStringFunc(expr, func(m string) string {
		p := ttlIntervalRe.FindStringSubmatch(m)
		return "interval:" + p[1] + ":" + strings.ToLower(p[2])
	})
	s = ttlToIntervalRe.ReplaceAllStringFunc(s, func(m string) string {
		p := ttlToIntervalRe.FindStringSubmatch(m)
		return "interval:" + p[2] + ":" + strings.ToLower(p[1])
	})
	s = strings.ToLower(s)
	s = strings.NewReplacer(" ", "", "\t", "", "\n", "", "`", "").Replace(s)
	return s
}

// tableTTLFromCreate — create_table_query'den TABLO düzeyi TTL ifadesi.
// Kolon TTL'leri kolon listesinin içindedir; tablo TTL'i ENGINE'den
// SONRA gelir, o yüzden yalnız ENGINE sonrası metne bakılır. TTL yoksa "".
func tableTTLFromCreate(create string) string {
	i := strings.LastIndex(create, " ENGINE = ")
	if i < 0 {
		return ""
	}
	tail := create[i:]
	j := strings.Index(tail, " TTL ")
	if j < 0 {
		return ""
	}
	expr := tail[j+len(" TTL "):]
	if k := strings.Index(expr, " SETTINGS "); k >= 0 {
		expr = expr[:k]
	}
	return strings.TrimSpace(expr)
}

// columnTTLFromCreate — create_table_query'den bir KOLONUN TTL ifadesi.
// Kolon tanımı, kolon listesinde parantez derinliği 0'daki virgüle kadar
// sürer (tip kendi içinde virgül taşır: AggregateFunction(argMax, String,
// Int64)); TTL tanımın son parçasıdır. Kolon ya da TTL yoksa "".
func columnTTLFromCreate(create, col string) string {
	open := strings.Index(create, "(")
	end := strings.LastIndex(create, " ENGINE = ")
	if open < 0 || end < 0 || end <= open {
		return ""
	}
	list := create[open+1 : end]
	depth, start := 0, 0
	var defs []string
	for i := 0; i < len(list); i++ {
		switch list[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 { // kolon listesinin kapanışı
				defs = append(defs, list[start:i])
				i = len(list)
			}
		case ',':
			if depth == 0 {
				defs = append(defs, list[start:i])
				start = i + 1
			}
		}
	}
	for _, d := range defs {
		d = strings.TrimSpace(d)
		name := d
		if sp := strings.IndexByte(d, ' '); sp >= 0 {
			name = d[:sp]
		}
		if strings.Trim(name, "`") != col {
			continue
		}
		j := strings.LastIndex(d, " TTL ")
		if j < 0 {
			return ""
		}
		return strings.TrimSpace(d[j+len(" TTL "):])
	}
	return ""
}

// ttlCurrentEverywhere — SAF karar: atlanabilir mi?
// observed: host başına okunan TTL ifadesi (TTL yoksa ""); wantHosts:
// kümedeki host sayısı (tek düğümde 1).
func ttlCurrentEverywhere(desired string, observed []string, wantHosts int) bool {
	if wantHosts <= 0 || len(observed) != wantHosts {
		return false
	}
	want := canonicalTTL(desired)
	if want == "" {
		return false
	}
	for _, o := range observed {
		if canonicalTTL(o) != want {
			return false
		}
	}
	return true
}

// clusterHostCount — kümedeki host (replika) sayısı; tek düğümde 1.
func (s *Store) clusterHostCount(ctx context.Context) (int, error) {
	if !s.clusterMode() {
		return 1, nil
	}
	var n uint64
	if err := s.conn.QueryRow(ctx,
		`SELECT count() FROM system.clusters WHERE cluster = ? SETTINGS max_execution_time = 5`,
		strings.TrimSpace(s.cfg.ClusterName)).Scan(&n); err != nil {
		return 0, err
	}
	return int(n), nil
}

// createQueriesEverywhere — verilen adlardaki DEPOLAMA tablolarının (Distributed
// sarmalayıcı ve MV hariç) create_table_query'si, host başına. Küme kipinde
// erişilemeyen replika HATADIR (bilerek skip_unavailable_shards yok).
func (s *Store) createQueriesEverywhere(ctx context.Context, names []string) (map[string][]string, error) {
	src := "system.tables"
	if s.clusterMode() {
		src = fmt.Sprintf("clusterAllReplicas(`%s`, system.tables)",
			strings.ReplaceAll(strings.TrimSpace(s.cfg.ClusterName), "`", ""))
	}
	rows, err := s.conn.Query(ctx, `
		SELECT name, create_table_query
		FROM `+src+`
		WHERE database = currentDatabase() AND name IN (?)
		  AND engine NOT IN ('Distributed', 'MaterializedView')
		LIMIT 2000
		SETTINGS max_execution_time = 5`, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var name, create string
		if err := rows.Scan(&name, &create); err != nil {
			return nil, err
		}
		out[name] = append(out[name], create)
	}
	return out, rows.Err()
}

// retentionProbe — boot yolunun tek seferlik okuması (tablo başına sorgu yok).
type retentionProbe struct {
	hosts   int
	creates map[string][]string
}

// probeRetention — boot'ta bir kez: host sayısı + ilgili depolama tablolarının
// tanımları. Hata → nil (çağıran her şeyi uygular = bugünkü davranış).
func (s *Store) probeRetention(ctx context.Context, tables []string) *retentionProbe {
	hosts, err := s.clusterHostCount(ctx)
	if err != nil || hosts <= 0 {
		return nil
	}
	names := make([]string, 0, len(tables)*2)
	for _, t := range tables {
		names = append(names, t, t+"_local")
	}
	creates, err := s.createQueriesEverywhere(ctx, names)
	if err != nil {
		return nil
	}
	return &retentionProbe{hosts: hosts, creates: creates}
}

// tableCurrent — tablonun TTL'i her host'ta istenenle aynı mı?
// Depolama tablosu küme kipinde <ad>_local, tek düğümde <ad>; ikisi de
// varsa (beklenmeyen) emin değiliz → false.
func (p *retentionProbe) tableCurrent(table, desired string) bool {
	if p == nil {
		return false
	}
	bare, local := p.creates[table], p.creates[table+"_local"]
	if len(bare) > 0 && len(local) > 0 {
		return false
	}
	creates := bare
	if len(local) > 0 {
		creates = local
	}
	observed := make([]string, 0, len(creates))
	for _, c := range creates {
		observed = append(observed, tableTTLFromCreate(c))
	}
	return ttlCurrentEverywhere(desired, observed, p.hosts)
}

// columnsCurrent — bir depolama tablosunda verilen kolonların HEPSİNİN
// TTL'i her host'ta istenenle aynı mı?
func (p *retentionProbe) columnsCurrent(creates []string, cols []string, desired string) bool {
	if p == nil || len(cols) == 0 {
		return false
	}
	for _, col := range cols {
		observed := make([]string, 0, len(creates))
		for _, c := range creates {
			observed = append(observed, columnTTLFromCreate(c, col))
		}
		if !ttlCurrentEverywhere(desired, observed, p.hosts) {
			return false
		}
	}
	return true
}
