package chstore

// db_slow_statement.go — v0.10.325 (operatör isteği 2026-09-03: "bir db
// sorgusu 1 saniyenin üzerine çıktığında problem tetiklesin, SRE'ye mail
// gitsin" → "Gerekiyor"; spec onayı "Uygun"). Kaynak: db_statement_summary_5m
// MV — statement (db_system, db_name, service, stmt_hash) başına 5 dk
// kovada p50/p95/p99 tDigest, sayı, hata, en yavaş exemplar, örnek SQL.
// Bu dosya yalnız OKUR (MV) ve ayar blobunu taşır; karar + Problem açma
// evaluator/db_slow_statement.go'da (lider kilidi, dedup, notify).
//
// Ayar `system_settings['db_slow_query']` (invariant #6). Yönlendirme:
// Problem Kind=db özneyle açılınca notify DBOwnerForSubject → sahibi (ug)
// + SRE (sy) takımlarına mail — mevcut kanal, yeni bildirim yolu yok.

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type DBSlowQueryConfig struct {
	Enabled bool `json:"enabled"`
	// ThresholdMs — 5 dk kovada p95 bu değeri aşınca "yavaş" (varsayılan 1000).
	ThresholdMs float64 `json:"thresholdMs"`
	// CriticalMs — son kovanın p95'i bunu aşarsa şiddet critical (P1).
	CriticalMs float64 `json:"criticalMs"`
	// MinExecutions — kova başına yürütme tabanı; altı gürültü sayılır.
	MinExecutions uint64 `json:"minExecutions"`
	// ForBuckets — ardışık kaç 5 dk kovada aşılmalı (2 = ~10 dk sürmüş).
	ForBuckets int `json:"forBuckets"`
	// CooldownSec — açık Problem, eşik altına inse bile bu süre dolmadan
	// çözülmez (flap önleyici tutma süresi).
	CooldownSec int `json:"cooldownSec"`
	// Health — v0.10.1073 veritabanı sağlık kuralı (db-health, evaluator/
	// db_health.go) vidaları. AYNI blobda (yeni ayar anahtarı yok — aiops §11).
	// İşaretçi: alanı hiç yazılmamış eski blob (nil) ile operatörün kaydettiği
	// değer ayrılsın; Normalize daima doldurur. PUT'ta nil gelirse (eski
	// sekme) saklı değer korunur (api/db_slow_query_routes.go).
	Health *DBHealthConfig `json:"health,omitempty"`
}

// DBHealthConfig — veritabanı sağlık kuralı (v0.10.1073). İki ardışık 5 dk
// kovada db hata % ≥ ErrorPct YA DA db p99 ≥ P99Ms; kova başına ≥ MinCalls
// çağrı VE ≥ MinCallers etkilenen (batch olmayan) çağıran servis. v0.10.1083:
// YA DA mutlak hata sayısı kolu (MinErrorCount / ErrorRiseFactor /
// MinCallerErrors).
type DBHealthConfig struct {
	// Enabled — varsayılan AÇIK (operatör onayı "go"); *bool: false ile
	// "alan yok" ayrılsın (aiops §11). Normalize nil'i true yapar.
	Enabled *bool `json:"enabled"`
	// ErrorPct — db hata oranı tabanı (yüzde, varsayılan 5).
	ErrorPct float64 `json:"errorPct"`
	// P99Ms — db p99 tabanı (ms, varsayılan 2000). Mutlak taban TEK BAŞINA
	// yetmez: p99 boyutu yalnız p99 ≥ P99RiseFactor × aynı veritabanının 24 sa
	// önceki aynı kovasının p99'uyken ihlaldir (v0.10.1069: filo geneli mutlak
	// gecikme eşiği gürültü). Dünkü kova yoksa p99 boyutu o kova için KAPALI.
	P99Ms float64 `json:"p99Ms"`
	// P99RiseFactor — p99'un dünkü aynı kovaya göre kat tabanı (varsayılan 3).
	P99RiseFactor float64 `json:"p99RiseFactor"`
	// MinCallerCalls — bir çağıranın "etkilenen" sayılması için kovadaki
	// çağrı tabanı (varsayılan 10): tek hatalı çağrı çağıran kapısını geçirmesin.
	MinCallerCalls uint64 `json:"minCallerCalls"`
	// MinCalls — kova başına çağrı tabanı (varsayılan 100); altı gürültü.
	MinCalls uint64 `json:"minCalls"`
	// MinCallers — etkilenen farklı çağıran servis tabanı (varsayılan 2);
	// batch kalıbına uyan çağıranlar sayılmaz.
	MinCallers int `json:"minCallers"`
	// MaxNewPerTick — tik başına yeni açılış tavanı (fırtına kemeri, vars. 20).
	MaxNewPerTick int `json:"maxNewPerTick"`
	// v0.10.1083 — MUTLAK hata SAYISI kolu (operatör: "Oracle hataları da
	// problemse hâlâ düşmüyor"): span tarafındaki ORA patlaması tüm
	// veritabanının çağrıları içinde %5'e ulaşmıyordu. Kova ihlali: hata sayısı
	// ≥ MinErrorCount VE ≥ ErrorRiseFactor × aynı veritabanının 24 sa önceki
	// aynı kovası (dünkü kova yoksa kol o kova için KAPALI) VE ≥ MinCallers
	// çağıranın her biri ≥ MinCallerErrors hata. 0 = varsayılan (eski blob /
	// alanı bilmeyen eski sekme).
	MinErrorCount   uint64  `json:"minErrorCount"`   // varsayılan 50 / 5 dk; 2× → critical
	ErrorRiseFactor float64 `json:"errorRiseFactor"` // varsayılan 3 (dünkü aynı kovaya göre)
	MinCallerErrors uint64  `json:"minCallerErrors"` // varsayılan 10 — çağıran başına hata tabanı
	// ExcludeSystems — v0.10.1084 (operatör: "%100 hata oranı gerçek değil"):
	// hata oranı ANLAMSIZ olan istemcilerin db.system'leri. Couchbase SDK'sı KV
	// "bulunamadı" (DocumentNotFound) cevabını span'de ERROR işaretliyor; önbellek
	// deseninde (get → yoksa yükle) her ıska hata sayılır ve %100/%67 hata oranı
	// gerçek bir olay değildir. Hariç sistem ana okumada ve referans okumasında
	// SQL'de düşer (Go'da sonradan değil); açık problemleri bir sonraki tikte
	// "system excluded" gerekçesiyle kapanır. Varsayılanı DOLU liste → *[]string
	// (aiops §11): nil = varsayılan ["couchbase"], [] = hiçbir sistem hariç değil.
	// Normalize küçük harf + kırpılmış + tekrarsız, daima nil olmayan dilim yazar.
	ExcludeSystems *[]string `json:"excludeSystems,omitempty"`
}

// On — etkin mi (nil = varsayılan açık).
func (h DBHealthConfig) On() bool { return h.Enabled == nil || *h.Enabled }

// dbHealthDefaultExclude — hariç sistemlerin varsayılanı (v0.10.1084).
func dbHealthDefaultExclude() []string { return []string{"couchbase"} }

// DBHealthMaxExcludeSystems — hariç sistem listesinin tavanı (PUT doğrulaması).
const DBHealthMaxExcludeSystems = 20

// NormalizeDBHealthSystems — SAF: kırp + küçük harf + boşları at + tekrarsız
// (ilk görülme sırası). Sonuç daima nil olmayan dilim (`[]` "hiçbiri" demek,
// `null` geri okununca varsayılana dönerdi).
func NormalizeDBHealthSystems(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ExcludedSystems — normalize edilmiş hariç sistem listesi (nil alan =
// varsayılan). SQL `NOT IN ?` bağı ve Go yüklemi aynı listeden okur.
func (h DBHealthConfig) ExcludedSystems() []string {
	if h.ExcludeSystems == nil {
		return dbHealthDefaultExclude()
	}
	return NormalizeDBHealthSystems(*h.ExcludeSystems)
}

// SystemExcluded — db.system hariç mi (DBHealthRuleID ile aynı katlama:
// kırpılmış + küçük harf). SQL ikizi dbHealthExcludeSQL.
func (h DBHealthConfig) SystemExcluded(system string) bool {
	s := strings.ToLower(strings.TrimSpace(system))
	for _, x := range h.ExcludedSystems() {
		if x == s {
			return true
		}
	}
	return false
}

func DefaultDBHealth() DBHealthConfig {
	on := true
	ex := dbHealthDefaultExclude()
	return DBHealthConfig{Enabled: &on, ErrorPct: 5, P99Ms: 2000, P99RiseFactor: 3, MinCallerCalls: 10, MinCalls: 100, MinCallers: 2, MaxNewPerTick: 20,
		MinErrorCount: 50, ErrorRiseFactor: 3, MinCallerErrors: 10, ExcludeSystems: &ex}
}

// NormalizeDBHealth — sıfır/negatif alan varsayılana; ErrorPct 100'de
// kesilir; Enabled daima nil olmayan işaretçi (JSON'da true/false yazılsın).
func NormalizeDBHealth(h DBHealthConfig) DBHealthConfig {
	d := DefaultDBHealth()
	on := h.On()
	h.Enabled = &on
	if h.ErrorPct <= 0 {
		h.ErrorPct = d.ErrorPct
	}
	if h.ErrorPct > 100 {
		h.ErrorPct = 100
	}
	if h.P99Ms <= 0 {
		h.P99Ms = d.P99Ms
	}
	if h.P99RiseFactor < 1 {
		// 0 (alan yok) ve <1 (dünden DÜŞÜK p99'u "artış" sayardı) → varsayılan.
		h.P99RiseFactor = d.P99RiseFactor
	}
	if h.MinCallerCalls == 0 {
		h.MinCallerCalls = d.MinCallerCalls
	}
	if h.MinCalls == 0 {
		h.MinCalls = d.MinCalls
	}
	if h.MinCallers <= 0 {
		h.MinCallers = d.MinCallers
	}
	if h.MaxNewPerTick <= 0 {
		h.MaxNewPerTick = d.MaxNewPerTick
	}
	if h.MinErrorCount == 0 {
		h.MinErrorCount = d.MinErrorCount
	}
	if h.ErrorRiseFactor < 1 {
		// 0 (alan yok) ve <1 (dünden AZ hatayı "artış" sayardı) → varsayılan.
		h.ErrorRiseFactor = d.ErrorRiseFactor
	}
	if h.MinCallerErrors == 0 {
		h.MinCallerErrors = d.MinCallerErrors
	}
	// v0.10.1084 — hariç sistemler: alan yok → varsayılan; değer → normalize
	// (yeni dilim; çağıranın dizisi paylaşılmasın).
	ex := h.ExcludedSystems()
	h.ExcludeSystems = &ex
	return h
}

func DefaultDBSlowQuery() DBSlowQueryConfig {
	h := DefaultDBHealth()
	return DBSlowQueryConfig{Enabled: true, ThresholdMs: 1000, CriticalMs: 5000, MinExecutions: 20, ForBuckets: 2, CooldownSec: 900, Health: &h}
}

const dbSlowQueryKey = "db_slow_query"

func NormalizeDBSlowQuery(c DBSlowQueryConfig) DBSlowQueryConfig {
	d := DefaultDBSlowQuery()
	if c.ThresholdMs <= 0 {
		c.ThresholdMs = d.ThresholdMs
	}
	if c.CriticalMs < c.ThresholdMs {
		c.CriticalMs = c.ThresholdMs
	}
	if c.MinExecutions == 0 {
		c.MinExecutions = d.MinExecutions
	}
	if c.ForBuckets <= 0 {
		c.ForBuckets = d.ForBuckets
	}
	if c.ForBuckets > 12 {
		c.ForBuckets = 12
	}
	if c.CooldownSec < 0 {
		c.CooldownSec = 0
	}
	var h DBHealthConfig
	if c.Health != nil {
		h = *c.Health
	}
	h = NormalizeDBHealth(h)
	c.Health = &h
	return c
}

func (s *Store) GetDBSlowQuery(ctx context.Context) DBSlowQueryConfig {
	c, err := s.LoadDBSlowQuery(ctx)
	if err != nil {
		return DefaultDBSlowQuery()
	}
	return c
}

// LoadDBSlowQuery — GetDBSlowQuery'nin hatayı SÖYLEYEN hâli (v0.10.1073).
// Satır yok → varsayılan, hata yok; okuma ya da JSON hatası → hata. db-health
// dedektörü bununla son iyi değeri korur (keep-last-good): okuma hatasında
// varsayılana düşmek, operatörün kapattığı kuralı o tik geri açardı.
func (s *Store) LoadDBSlowQuery(ctx context.Context) (DBSlowQueryConfig, error) {
	raw, err := s.GetSetting(ctx, dbSlowQueryKey)
	if err != nil {
		return DBSlowQueryConfig{}, err
	}
	if len(raw) == 0 {
		return DefaultDBSlowQuery(), nil
	}
	var c DBSlowQueryConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return DBSlowQueryConfig{}, err
	}
	return NormalizeDBSlowQuery(c), nil
}

func (s *Store) SaveDBSlowQuery(ctx context.Context, c DBSlowQueryConfig) (DBSlowQueryConfig, error) {
	c = NormalizeDBSlowQuery(c)
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	return c, s.PutSetting(ctx, dbSlowQueryKey, raw)
}

// SlowStatementBucket — bir statement'ın bir 5 dk kovadaki ölçüsü.
type SlowStatementBucket struct {
	DBSystem string
	DBName   string
	Service  string
	StmtHash uint64
	Bucket   time.Time
	Sample   string
	Count    uint64
	Errors   uint64
	P95Ms    float64
	P99Ms    float64
	Exemplar string
}

// slowStatementBucketsSQL — saf; HAVING eşiği ve tabanı MV'de uygular,
// pencere sınırı + LIMIT + max_execution_time (CH sözleşmesi).
func slowStatementBucketsSQL() string {
	return `
		SELECT db_system, db_name, service_name, stmt_hash, time_bucket,
		       anyMerge(sample_stmt_state)                                              AS sample_stmt,
		       countMerge(span_count_state)                                             AS execs,
		       countMerge(error_count_state)                                            AS errs,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms,
		       argMaxMerge(slow_exemplar_state)                                         AS exemplar
		FROM db_statement_summary_5m
		WHERE time_bucket >= ?
		GROUP BY db_system, db_name, service_name, stmt_hash, time_bucket
		HAVING execs >= ? AND p95_ms >= ?
		ORDER BY p95_ms DESC
		LIMIT 500
		SETTINGS max_execution_time = 10`
}

// SlowStatementBuckets — since'ten bu yana eşiği aşan statement-kova
// satırları. db_stmt_hash kolonu yoksa MV boş olur → nil (dedektör susar).
func (s *Store) SlowStatementBuckets(ctx context.Context, since time.Time, minExecs uint64, thresholdMs float64) ([]SlowStatementBucket, error) {
	if !s.hasDBStmtHashCol {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, slowStatementBucketsSQL(), since, minExecs, thresholdMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SlowStatementBucket{}
	for rows.Next() {
		var b SlowStatementBucket
		if err := rows.Scan(&b.DBSystem, &b.DBName, &b.Service, &b.StmtHash, &b.Bucket,
			&b.Sample, &b.Count, &b.Errors, &b.P95Ms, &b.P99Ms, &b.Exemplar); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
