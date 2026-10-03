package chstore

// db_health.go — v0.10.1073: veritabanı SAĞLIK kuralının (db-health) okuma
// yarısı + kural id / özne sözleşmesi. Karar ve Problem yaşam döngüsü
// evaluator/db_health.go'da (lider kilidi, dedup, notify, incident).
//
// Neden (prod olayı 2026-10-02 ~21:30–22:30): bir veritabanının hata oranı
// ~%0'dan ~%10'a, p99'u ms'lerden ~5 s'ye çıktı; çağıranların p99'u 8 s'ye
// vurdu, ardından 55k HTTP 503 geldi. VERİTABANI öznesinde hiçbir Problem
// açılmadı: db-capacity yalnız doyma gauge'larını, db-slow-stmt tek tek
// ifadeleri okuyor; `db_p99_ms` yerleşikleri ÇAĞIRAN servis başına ve
// varsayılan kapalı (v0.10.1069).
//
// KAYNAK: db_caller_summary_5m — Databases sayfasının detay okumasıyla
// (dbDetailAggregate) AYNI MV ve AYNI kimlik üçlüsü (db_system, instance,
// db_name); ÇAĞIRAN boyutu (service_name) taşıyan tek db MV'si. db_summary_5m
// çağıran taşımıyor ("≥2 çağıran" kapısı onda kurulamaz), ham spans yasak.
//
// TEK okuma, tik başına, tüm veritabanları için: iç sorgu (db, çağıran, kova)
// başına sayım + hata + tDigest DURUMU (-MergeState) ve çağıranın "etkilendi"
// bayrağı; dış sorgu (db, kova) başına toplar, durumları birleştirip db p99'u
// çıkarır, etkilenen çağıranları sayar ve en çok çağıran ≤3'ünü listeler.
// Batch kalıbına uyan çağıranlar İÇ WHERE'de düşer: hem sayımdan hem
// agregelerden (batch işinin kendi hatası/gecikmesi DB'yi "hasta" yapmasın).
//
// HAVING (inceleme düzeltmesi): yalnız tabanı aşan (db, kova) satırları ve
// ŞU AN AÇIK db-health problemlerinin TÜM satırları döner. Eskiden LIMIT tüm
// filonun agregesinden sonra kesiyordu ve "ORDER BY affected DESC" sağlıklı
// satırları önce atıyordu: kesik okumada düzelmiş veritabanının "temiz"
// satırı hiç gelmez, problem açık kalıp eskalasyonla büyürdü.
//
// p99 GÖRELİ (inceleme düzeltmesi, v0.10.1069 kararıyla uyum: filo geneli
// mutlak gecikme eşiği gürültü — raporlama veritabanları gün boyu 2 s
// üstünde): ikinci okuma DBHealthReference aynı veritabanının 24 sa önceki
// aynı kovalarının p99'unu db_summary_5m'den getirir; karar evaluator'da.
//
// v0.10.1083 — MUTLAK hata sayısı kolu (span tarafı ORA patlaması tüm
// veritabanının çağrıları içinde %5'e ulaşmıyordu): ana okuma çağıran başına
// "≥ minCallerErrors hata" bayrağını sayar, referans okuması dünkü kovanın
// hata sayısını da getirir (DBHealthReference). Okuma sayısı DEĞİŞMEDİ.
//
// v0.10.1084 — HARİÇ SİSTEMLER (operatör: "%100 hata oranı gerçek değil"):
// DBHealthConfig.ExcludeSystems'teki db.system'ler (varsayılan couchbase — SDK
// KV "bulunamadı" cevabını ERROR işaretliyor) iki okumada da İÇ WHERE'de düşer
// (`lower(trimBoth(db_system)) NOT IN ?`); boş listede koşul YOK. Go'da sonradan
// süzmek LIMIT'i ve HAVING'in açık-id dalını hariç satırlara harcardı.

import (
	"context"
	"math"
	"strings"
	"time"
)

// db-health Problem metrikleri — kategori (problem_category.go) ve FE
// biçimi bu iki dizgiye bakar. Noktalı ad bilerek: `db_error_rate` /
// `db_p99_ms` çağıran-servis alarm metrikleri (TransportOp), bu ise
// VERİTABANI öznesinin ölçüsü — karıştırılmasın.
const (
	DBHealthMetricErrorPct = "db.error_pct"
	DBHealthMetricP99Ms    = "db.p99_ms"
	// DBHealthMetricErrorCount — v0.10.1083 mutlak hata sayısı kolu (5 dk kova
	// başına hata; eşik MinErrorCount). Kategori ERROR.
	DBHealthMetricErrorCount = "db.error_count"
)

// dbHealthRowLimit — iki okumanın satır tavanı. HAVING yalnız ihlal eden ve
// açık problemlere ait satırları geçirdiği için gerçek sonuç küçük; tavan
// kemerdir. Tavana dayanınca okuma "kesik" döner ve dedektör görünmeyen açık
// problemleri KAPATMAZ (yalnız tazeler).
const dbHealthRowLimit = 20000

// dbHealthRuleIDSQL — DBHealthRuleID'nin SQL ikizi (aynı biçim; Go tarafı
// system'i kırpıp küçültür). Açık problem kimlikleriyle eşleşme ve referans
// okumasının süzgeci bunu kullanır. Önek sabitten gelir (kullanıcı girdisi değil).
const dbHealthRuleIDSQL = `concat('` + RuleDBHealthPrefix + `', lower(trimBoth(db_system)), '@', instance, '/', db_name)`

// dbHealthExcludeSQL — DBHealthConfig.SystemExcluded'ın SQL ikizi (aynı katlama:
// kırpılmış + küçük harf; kural id'si de böyle kurulur). Bağ: normalize liste
// (dizi). Liste boşsa koşul hiç yazılmaz (dbHealthExcludeCond).
const dbHealthExcludeSQL = `lower(trimBoth(db_system)) NOT IN ?`

// dbHealthExcludeCond — SAF: hariç liste boşsa ("", nil), değilse WHERE eki
// ve tek bağ (dizi).
func dbHealthExcludeCond(cfg DBHealthConfig) (string, []any) {
	ex := cfg.ExcludedSystems()
	if len(ex) == 0 {
		return "", nil
	}
	return `
			  AND ` + dbHealthExcludeSQL, []any{ex}
}

// DBHealthBucket — bir veritabanının bir 5 dk kovadaki sağlık ölçüsü
// (batch olmayan çağıranlar üzerinden).
type DBHealthBucket struct {
	DBSystem string
	Instance string
	DBName   string
	Bucket   time.Time
	Calls    uint64
	Errors   uint64
	P99Ms    float64
	// Callers — kovada bu veritabanını çağıran farklı (batch olmayan) servis.
	Callers uint64
	// AffectedCallers — kovada ≥ MinCallerCalls çağrı yapmış VE kendi
	// çağrılarında hata % ≥ ErrorPct YA DA p99 ≥ P99Ms olan farklı çağıran.
	AffectedCallers uint64
	// ErrorCallers — v0.10.1083: kovada ≥ MinCallerErrors HATA üretmiş farklı
	// (batch olmayan) çağıran — mutlak hata sayısı kolunun çağıran kapısı.
	ErrorCallers uint64
	// TopCallers — etkilenen (ya da ≥ MinCallerErrors hatalı) çağıranlardan
	// çağrı sayısına göre ilk ≤3.
	TopCallers []string
	// RefP99Ms / HasRef — aynı veritabanının 24 sa önceki aynı kovasındaki
	// p99'u (DBHealthReference; okumadan sonra evaluator doldurur). HasRef
	// false = referans yok → p99 boyutu bu kova için KAPALI.
	RefP99Ms float64
	HasRef   bool
	// RefErrors / HasErrRef — v0.10.1083: aynı kovanın 24 sa önceki HATA sayısı.
	// HasErrRef = dünkü kova VAR (çağrısı olan satır; hata 0 olabilir). false →
	// mutlak hata sayısı kolu bu kova için KAPALI.
	RefErrors uint64
	HasErrRef bool
}

// RuleID — satırın db-health kural id'si.
func (b DBHealthBucket) RuleID() string { return DBHealthRuleID(b.DBSystem, b.Instance, b.DBName) }

// ErrorPct — kovanın hata yüzdesi (çağrı yoksa 0).
func (b DBHealthBucket) ErrorPct() float64 {
	if b.Calls == 0 {
		return 0
	}
	return float64(b.Errors) * 100 / float64(b.Calls)
}

// dbHealthBucketsSQL — SAF. batchCond boşsa batch dalı YOK (kalıp listesi
// boş = kural kapalı); excludeCond boşsa hariç sistem koşulu YOK (v0.10.1084).
// Bind sırası: minCallerCalls, errorPct, p99Ms, minCallerErrors, since, until,
// [hariç sistemler (dizi)], [kalıplar], errorPct, p99Ms, minErrorCount, açık
// kural id'leri (dizi).
//
// Çağıranın p99'u TEK birleştirmeyle: durum bir kez -MergeState ile kurulur,
// finalizeAggregation onu sonlandırır (ikinci tDigest birleştirmesi yok).
//
// v0.10.1083 — mutlak hata sayısı kolu AYNI sorguda: çağıranın "hata
// üretti" bayrağı (c_err_affected, ≥ minCallerErrors hata), dışta sayısı
// (err_callers), HAVING'de hata sayısı tabanı. Yeni okuma YOK.
func dbHealthBucketsSQL(excludeCond, batchCond string) string {
	excl := ""
	if batchCond != "" {
		excl = `
			  AND NOT ` + batchCond
	}
	return `
		SELECT db_system, instance, db_name, time_bucket,
		       sum(c_calls)                                                       AS calls,
		       sum(c_errs)                                                        AS errs,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(c_q), 3) / 1e6 AS p99_ms,
		       count()                                                            AS callers,
		       countIf(c_affected)                                                AS affected,
		       countIf(c_err_affected)                                            AS err_callers,
		       arraySlice(arrayMap(x -> x.1, arrayReverseSort(x -> x.2,
		         groupArrayIf((service_name, c_calls), c_affected OR c_err_affected))), 1, 3) AS top_callers
		FROM (
			SELECT db_system, instance, db_name, service_name, time_bucket,
			       countMerge(span_count_state)                                  AS c_calls,
			       countIfMerge(error_count_state)                               AS c_errs,
			       quantilesTDigestMergeState(0.5, 0.95, 0.99)(duration_q_state) AS c_q,
			       c_calls >= ? AND ((c_errs * 100 >= ? * c_calls)
			         OR (arrayElement(finalizeAggregation(c_q), 3) / 1e6 >= ?))  AS c_affected,
			       c_errs >= ?                                                   AS c_err_affected
			FROM db_caller_summary_5m
			WHERE time_bucket >= ? AND time_bucket < ?` + excludeCond + excl + `
			GROUP BY db_system, instance, db_name, service_name, time_bucket
		)
		GROUP BY db_system, instance, db_name, time_bucket
		HAVING (errs * 100 >= ? * calls) OR (p99_ms >= ?) OR (errs >= ?)
		    OR ` + dbHealthRuleIDSQL + ` IN ?
		ORDER BY affected DESC, calls DESC
		LIMIT ` + itoa(dbHealthRowLimit) + `
		SETTINGS max_execution_time = 10`
}

// dbHealthBucketsQuery — SAF: SQL + argümanlar (golden test bunu pinler).
// Batch yüklemi IsBatchService'in SQL ikizi (BatchServiceSQL), aynı listeden.
// openIDs: açık db-health problemlerinin kural id'leri — onların satırları
// eşikten bağımsız döner (temiz kova görülebilsin diye). nil → boş dizi.
func dbHealthBucketsQuery(since, until time.Time, cfg DBHealthConfig, sens AnomalySensitivityConfig, openIDs []string) (string, []any) {
	if openIDs == nil {
		openIDs = []string{}
	}
	cond, bargs := sens.BatchServiceSQL("service_name")
	exCond, exArgs := dbHealthExcludeCond(cfg)
	args := []any{cfg.MinCallerCalls, cfg.ErrorPct, cfg.P99Ms, cfg.MinCallerErrors, since, until}
	args = append(args, exArgs...)
	args = append(args, bargs...)
	args = append(args, cfg.ErrorPct, cfg.P99Ms, cfg.MinErrorCount, openIDs)
	return dbHealthBucketsSQL(exCond, cond), args
}

// DBHealthBuckets — [since, until) arasındaki (db, kova) sağlık satırları:
// tabanı aşanlar + openIDs'in tüm satırları. truncated: satır tavanına
// dayandı (küme eksik olabilir).
func (s *Store) DBHealthBuckets(ctx context.Context, since, until time.Time, cfg DBHealthConfig, sens AnomalySensitivityConfig, openIDs []string) ([]DBHealthBucket, bool, error) {
	q, args := dbHealthBucketsQuery(since, until, cfg, sens, openIDs)
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []DBHealthBucket{}
	for rows.Next() {
		var b DBHealthBucket
		if err := rows.Scan(&b.DBSystem, &b.Instance, &b.DBName, &b.Bucket,
			&b.Calls, &b.Errors, &b.P99Ms, &b.Callers, &b.AffectedCallers, &b.ErrorCallers, &b.TopCallers); err != nil {
			return nil, false, err
		}
		b.P99Ms = finiteOrZero(b.P99Ms)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, len(out) >= dbHealthRowLimit, nil
}

// dbHealthReferenceSQL — SAF: aynı veritabanlarının 24 sa önceki kovalarının
// p99'u ve (v0.10.1083) çağrı + hata sayısı — aynı okuma, iki ek kolon.
// Kaynak db_summary_5m (çağıran boyutu gerekmez; Databases listesinin MV'si).
// Süzgeç: yalnız p99 ya da hata sayısı adayı veritabanlarının kural id'leri
// (dizi). Bind sırası: since, until, [hariç sistemler (dizi)], kural id'leri.
// v0.10.1084 — hariç sistemler burada da SQL'de düşer (adaylar zaten ana
// okumadan geliyor; koşul ikinci kemer — referans hariç sisteme hiç bakmaz).
//
// Bilinen asimetri: db_summary_5m batch çağıranları da sayar (cari okuma
// düşürür) — referans en kötü ihtimalle YÜKSEK çıkar, kol temkinli yönde
// susar (sahte açılış değil).
func dbHealthReferenceSQL(excludeCond string) string {
	return `
		SELECT db_system, instance, db_name, time_bucket,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms,
		       countMerge(span_count_state)                                                     AS calls,
		       countIfMerge(error_count_state)                                                  AS errs
		FROM db_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?` + excludeCond + `
		  AND ` + dbHealthRuleIDSQL + ` IN ?
		GROUP BY db_system, instance, db_name, time_bucket
		LIMIT ` + itoa(dbHealthRowLimit) + `
		SETTINGS max_execution_time = 10`
}

// dbHealthReferenceQuery — SAF: referans SQL'i + argümanlar (golden test).
func dbHealthReferenceQuery(since, until time.Time, cfg DBHealthConfig, ruleIDs []string) (string, []any) {
	exCond, exArgs := dbHealthExcludeCond(cfg)
	args := []any{since, until}
	args = append(args, exArgs...)
	args = append(args, ruleIDs)
	return dbHealthReferenceSQL(exCond), args
}

// DBHealthRefKey — referans haritasının anahtarı: kural id + kova (unix sn,
// 24 sa İLERİ kaydırılmış — cari kovayla doğrudan eşleşsin).
type DBHealthRefKey struct {
	RuleID string
	Bucket int64
}

// DBHealthRef — dünkü aynı kovanın ölçüsü. P99Ms 0 = p99 referansı yok (kova
// var ama tDigest boş); çağrısı olan satırın VARLIĞI hata sayısı referansıdır
// (Errors 0 olabilir: "dün bu saatte hiç hata yoktu" geçerli bir referans).
type DBHealthRef struct {
	P99Ms  float64
	Calls  uint64
	Errors uint64
}

// DBHealthReference — ruleIDs için [since, until) penceresindeki kova
// ölçüleri; anahtarın kovası shift kadar ileri kaydırılır (24 sa önceki kova
// → bugünkü karşılığı). ruleIDs boşsa okuma yok. Çağrısı da p99'u da olmayan
// satır atlanır (referans yok). cfg yalnız hariç sistemler için (v0.10.1084).
func (s *Store) DBHealthReference(ctx context.Context, since, until time.Time, shift time.Duration, cfg DBHealthConfig, ruleIDs []string) (map[DBHealthRefKey]DBHealthRef, error) {
	out := map[DBHealthRefKey]DBHealthRef{}
	if len(ruleIDs) == 0 {
		return out, nil
	}
	q, args := dbHealthReferenceQuery(since, until, cfg, ruleIDs)
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			sys, inst, db string
			bucket        time.Time
			ref           DBHealthRef
		)
		if err := rows.Scan(&sys, &inst, &db, &bucket, &ref.P99Ms, &ref.Calls, &ref.Errors); err != nil {
			return nil, err
		}
		ref.P99Ms = finiteOrZero(ref.P99Ms)
		if ref.Calls == 0 && ref.P99Ms <= 0 {
			continue
		}
		out[DBHealthRefKey{RuleID: DBHealthRuleID(sys, inst, db), Bucket: bucket.Add(shift).Unix()}] = ref
	}
	return out, rows.Err()
}

func finiteOrZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// ── Kural id ve özne sözleşmesi ───────────────────────────────────────────

// dbHealthNameSentinel — MV'nin "span db.name taşımıyordu" nöbetçisi
// (coalesce(…, 'default')); gerçek bir veritabanı adı değil.
const dbHealthNameSentinel = "default"

// DBHealthHasDBName — db.name gerçek bir ad mı (boş ya da nöbetçi değil).
func DBHealthHasDBName(dbName string) bool {
	n := strings.TrimSpace(dbName)
	return n != "" && n != dbHealthNameSentinel
}

// DBHealthRuleID — `db-health:<system>@<instance>/<db>`. system küçük harf
// (DBSubjectID ile aynı gerekçe). Kural id = problem id: veritabanı başına
// tek satır; Databases detay sayfasının üçlüsü bu dizgiden geri çözülür.
// SQL ikizi dbHealthRuleIDSQL — biri değişirse öbürü de.
func DBHealthRuleID(system, instance, dbName string) string {
	return RuleDBHealthPrefix + strings.ToLower(strings.TrimSpace(system)) + "@" + instance + "/" + dbName
}

// ParseDBHealthRuleID — DBHealthRuleID'nin tersi. Ayırıcılar: İLK '@'
// (system '@' taşımaz) ve SON '/' (db.name'de '/' beklenmez; instance bir
// host adı ya da peer.service). Bilinen sınır: '/' içeren bir db.name
// instance'a kayar — o veritabanının detay linki eksik daralır.
func ParseDBHealthRuleID(ruleID string) (system, instance, dbName string, ok bool) {
	if !strings.HasPrefix(ruleID, RuleDBHealthPrefix) {
		return "", "", "", false
	}
	rest := ruleID[len(RuleDBHealthPrefix):]
	at := strings.Index(rest, "@")
	if at <= 0 {
		return "", "", "", false
	}
	system, rest = rest[:at], rest[at+1:]
	slash := strings.LastIndex(rest, "/")
	if slash <= 0 || slash == len(rest)-1 {
		return "", "", "", false
	}
	return system, rest[:slash], rest[slash+1:], true
}

// DBHealthSubject — Problem.Service: gerçek db.name varsa dbName biçimi
// (`db:<system>@<db>`, yavaş ifadeyle aynı uzay — Databases satırı db.name
// ile eşleşir), yoksa instance biçimi. DBProblemSubjectForm aynı kuralı kural
// id'sinden türetir; ikisi ayrışırsa liste işareti ve detay kartı problemi
// kaçırır (db_health_test.go pinler).
func DBHealthSubject(system, instance, dbName string) string {
	if DBHealthHasDBName(dbName) {
		return DBSubjectID(system, dbName)
	}
	return DBSubjectID(system, instance)
}
