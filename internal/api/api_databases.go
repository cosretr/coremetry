package api

// Databases + messaging read handlers. Split out of api.go for
// code organisation (behaviour-preserving). All handlers hit the
// pre-aggregated db_*_5m / messaging MVs via chstore and front the
// read with s.serveCached. Shared helpers (parseFromTo, cacheBucket)
// stay in api.go because many other clusters use them too.

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func (s *Server) getDatabases(w http.ResponseWriter, r *http.Request) {
	from, to := parseFromTo(r, time.Hour)
	// v0.9.433 (desen paritesi, kuyruk #3a) — ?compare=prior: messaging
	// v0.8.364 sözleşmesinin birebiri. Opt-in (CH maliyeti ikiye katlar);
	// Prior hatası ölümcül değil: sayfa delta'sız gelir, 500 olmaz.
	compare := r.URL.Query().Get("compare") == "prior"
	// v0.9.821 — global Topbar env filtresi. Bugüne dek bu uç env'i
	// SESSİZCE YOK SAYIYORDU: /endpoints ve /services daralırken
	// /databases tüm ortamları göstermeye devam ediyor ve hiçbir şey
	// bunu söylemiyordu. db_summary_5m'de deploy_env boyutu olmadığı
	// için env okumayı ham span'lere düşürüyor (endpoints v0.8.385 ile
	// aynı forcesRaw takası) — zarf kaynağı ilan ediyor.
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	// v0.9.821 — anahtar öneki v2'ye çıktı ÇÜNKÜ zarf değişti (çıplak
	// dizi → DatabasesOverview). Önek sürümlenmeseydi yuvarlanan deploy
	// sırasında eski dizi payload'ı 30 sn boyunca yeni SPA'ya servis
	// edilir ve sayfa boş açılırdı (v0.9.443/458 + messaging v0.9.813
	// dersi). Warm loop (api.go) aynı öneki kullanır — yoksa ısıtılan
	// slot hiç okunmaz. env de anahtarda: farklı env = farklı liste.
	key := fmt.Sprintf("databases:v2:%s:env=%s:cmp=%v", cacheBucket(from, to), env, compare)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		ov, err := s.store.GetDatabases(ctx, chstore.DatabasesQuery{
			From: from, To: to, Env: env,
			IncludeCallers: true, IncludeReceivers: true,
		})
		if err != nil {
			return nil, err
		}
		if !compare || len(ov.Rows) == 0 {
			return ov, nil
		}
		// Prior penceresi HAFİF ikizden (v0.9.821): receiver keşfini ve
		// çağıran turunu atlar — delta rozetleri yalnız sayaç + quantile
		// okuyor. Öncesinde prior çağrısı TAM okumayı koşuyordu, yani
		// her compare'li sayfa yüklemesinde dört katalog probu, dört
		// metric_points taraması ve bir tam çağıran taraması BOŞUNA
		// ödeniyordu.
		// v0.10.1025 — pencere dbListPriorWindow'dan: eski `[from − dur,
		// from)` hizasız from'da current'ın ilk 5 dk kovasını İKİ pencereye
		// de sayıyordu (chstore/prior_window.go).
		pFrom, pTo := dbListPriorWindow(from, to, env)
		// v0.10.1025 (inceleme R3+R4) — prior OKUNABİLİR mi: okunan yolun
		// saklama ufku (zarfın SpanHorizonDays'i: MV 90 gün, env/ham yol span
		// saklaması — 7 günlük env + 7d + Compare prior'u çoktan silinmiş
		// [now−14g, now−7g] aralığına koyardı) ve kaynağın ileriye dönük
		// kapsaması. Düşerse prior alanı YOK → rozet yok; eksik bir prior
		// eşleşen her satırı sahte bir KÖTÜLEŞMEyle (Calls ↑%500) boyardı.
		now := time.Now()
		if !s.store.DBListPriorReadable(ctx, pFrom, pTo, now, env != "", ov.SpanHorizonDays) {
			return ov, nil
		}
		priorRows, err := s.store.GetDatabasesRollup(ctx, pFrom, pTo, env)
		if err != nil {
			return ov, nil
		}
		mergeDBPrior(ov.Rows, priorRows, dbListPriorScale(from, to, now, env))
		return ov, nil
	})
}

// dbListPriorScale — prior SAYAÇLARININ kapsama oranı (v0.10.1025 R1). SAF.
// MV yolunda canlı pencerenin son kovası henüz doluyor (chstore.PriorCoverage);
// ham yolda pencere birebir [from, to] ve yalnız geleceğe uzanan özel aralıkta
// 1'in altına düşer (chstore.RawPriorCoverage).
func dbListPriorScale(from, to, now time.Time, env string) float64 {
	if env != "" {
		return chstore.RawPriorCoverage(from, to, now)
	}
	return chstore.PriorCoverage(from, to, now)
}

// dbListPriorWindow — /databases ?compare=prior okumasının penceresi. SAF
// (v0.10.1025, Databases dilim 3).
//
// İki okuma yolu var ve prior'un şekli yolu izlemek ZORUNDA:
//   - env yok → MV yolu (db_summary_5m, 5 dk kova). chstore.PriorWindow:
//     ortak kova yok, iki pencere aynı sayıda kova.
//   - env var → ham spans yolu (getDatabasesRaw, `time >= from AND time <=
//     to`, kova ızgarası YOK). Burada PriorWindow'un kovaya yuvarlanmış
//     boyu (N × 5 dk) current'tan on dakikaya kadar UZUN olurdu ve sayaç
//     deltası sahte bir düşüş basardı; doğru prior birebir aynı süre geri.
//     (İki pencere yalnız `from` ANINI paylaşır — ham sınırlar iki uçta da
//     kapalı; o anda tam düşen bir span iki kez sayılabilir. Kova değil,
//     tek bir an; bu sürümde dokunulmadı.)
func dbListPriorWindow(from, to time.Time, env string) (time.Time, time.Time) {
	if env != "" {
		return from.Add(-to.Sub(from)), from
	}
	return chstore.PriorWindow(from, to)
}

// mergeDBPrior — prior pencere sayaçlarını (system, instance, dbName)
// kimliğiyle mevcut satırlara kopyalar; mergeMessagingPrior'un ikizi.
// Prior'da olmayan satır alanları boş bırakır (omitempty → rozet gizli).
//
// v0.10.1025 (R1) — scale: SAYAÇLAR kapsama oranıyla ölçeklenir
// (chstore.ScalePriorCount; taban 1, yani eşleşen satırın prior çağrısı
// asla 0'a — omitempty ile "eşleşmemiş"e — düşmez). Gecikmeler
// ölçeklenmez.
func mergeDBPrior(cur, prior []chstore.DBInstance, scale float64) {
	type key struct{ system, instance, dbName string }
	idx := make(map[key]*chstore.DBInstance, len(prior))
	for i := range prior {
		idx[key{prior[i].System, prior[i].Instance, prior[i].DBName}] = &prior[i]
	}
	for i := range cur {
		p, ok := idx[key{cur[i].System, cur[i].Instance, cur[i].DBName}]
		if !ok {
			continue
		}
		cur[i].PriorSpanCount = chstore.ScalePriorCount(p.SpanCount, scale)
		cur[i].PriorErrorCount = chstore.ScalePriorCount(p.ErrorCount, scale)
		cur[i].PriorAvgMs = p.AvgMs
		cur[i].PriorP50Ms = p.P50Ms
		cur[i].PriorP99Ms = p.P99Ms
	}
}

// getDBTrends serves the per-row RED sparklines (#1) + latest-bucket
// health snapshot (#6) for the /databases + /messaging overview
// grid. One DBTrend per (db_system, instance, db_name) — keyed
// identically to the /api/databases rows so the frontend joins
// trends → rows by (system, instance, dbName). Read-only; no auth
// gate / audit. Cache key hashes the (minute-bucketed) window via
// the shared cacheBucket helper; 30s TTL matches the overview so a
// page load and its sparkline fetch share the same warm window.
func (s *Server) getDBTrends(w http.ResponseWriter, r *http.Request) {
	from, to := parseFromTo(r, time.Hour)
	key := "db-trends:" + cacheBucket(from, to)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		return s.store.GetDBTrends(ctx, from, to)
	})
}

// getMessagingTrends (v0.9.434) — getDBTrends'in messaging ikizi:
// /messaging grid'inin satır-içi sparkline + sağlık chip kaynağı.
// Aynı 30s TTL — sayfa yüklemesiyle sparkline fetch'i sıcak pencereyi
// paylaşır; anahtar tüm girdileri (pencere) hashler.
func (s *Server) getMessagingTrends(w http.ResponseWriter, r *http.Request) {
	from, to := parseFromTo(r, time.Hour)
	key := "msg-trends:" + cacheBucket(from, to)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		return s.store.GetMessagingTrends(ctx, from, to)
	})
}

// getDatabaseDetail returns the drawer payload for one
// (db_system, instance, db_name) TRIPLE — per-(service, pod) caller
// breakdown plus the top db_statement prefixes. Cached 30s.
//
// v0.9.821 — ?dbName= imzaya girdi. Öncesinde uç yalnız (system,
// instance) soruyordu, oysa TABLO SATIRI üçlü kimlikteydi: bir host'ta
// N veritabanı olan her kurulumda — Oracle SID'leri, PostgreSQL
// şemaları, MSSQL DB'leri — hangi satıra tıklanırsa tıklansın AYNI
// çekmece açılıyor ve o host'un TOPLAMINI gösteriyordu. Satır 4.200
// sorgu diyor, çekmece 31.000 gösteriyordu; ikisi de doğru görünüyordu.
//
// dbName CACHE ANAHTARINA da girer — girmeseydi ilk açılan
// veritabanının çekmecesi 30 sn boyunca diğerlerine de servis edilirdi,
// yani düzeltilen yalan cache katmanında yaşamaya devam ederdi.
func (s *Server) getDatabaseDetail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	system := q.Get("system")
	instance := q.Get("instance")
	dbName := q.Get("dbName")
	if system == "" {
		http.Error(w, `{"error":"system required"}`, http.StatusBadRequest)
		return
	}
	from, to := parseFromTo(r, time.Hour)
	// v0.10.1025 — yük artık önceki eşit pencerenin agregesini de taşıyor
	// (hasPrior + prior*). Anahtar BİLEREK değişmedi: prior penceresi
	// (from, to)'nun saf türevi (chstore.PriorWindow), yeni bir girdi değil;
	// zarf da yalnız alan EKLEDİ — rolling deploy'da eski yük 30 sn boyunca
	// hasPrior'suz gelir ve arayüz delta'sız çizer, bozulmaz.
	key := dbDetailKey(system, instance, dbName, cacheBucket(from, to))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		return s.store.GetDatabaseDetail(ctx, system, instance, dbName, from, to)
	})
}

// dbDetailKey — çekmece cache anahtarı. Alan sınırları FNV digest ile
// çözülüyor (endpointKeyDigest'in üç alanlı ikizi): instance ve dbName
// operatör verisi ve `:` ile birleştirilirse "a:b" + "c" ile "a" + "b:c"
// aynı anahtara düşerdi — v0.5.187 belirsizlik sınıfının string hâli.
// SAF; regresyon testi db_detail_key_test.go.
func dbDetailKey(system, instance, dbName, bucket string) string {
	h := fnv.New64a()
	h.Write([]byte(system))
	h.Write([]byte{0})
	h.Write([]byte(instance))
	h.Write([]byte{0})
	h.Write([]byte(dbName))
	return fmt.Sprintf("db-detail:v2:%x:%s", h.Sum64(), bucket)
}

// getOracleMetrics returns the OracleDB-receiver drill-down for
// one instance — sessions/processes utilisation, cumulative
// counter rates, tablespace usage. Falls back to deterministic
// synthetic data (flagged Synthetic=true in the payload) when
// no oracledb.* metric_points exist in the window so the panel
// still renders during integration setup.
func (s *Server) getOracleMetrics(w http.ResponseWriter, r *http.Request) {
	instance := r.URL.Query().Get("instance")
	from, to := parseFromTo(r, time.Hour)
	key := fmt.Sprintf("oracle:%s:%s", instance, cacheBucket(from, to))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		m, err := s.store.GetOracleMetrics(ctx, instance, from, to)
		if err == nil && m != nil && !m.Synthetic { // v0.10.909 — "kaç saat kaldı"
			m.Sessions.Forecast = s.dbForecast(ctx, "oracledb.sessions.usage", instance, m.Sessions.Limit)
			m.Processes.Forecast = s.dbForecast(ctx, "oracledb.processes.usage", instance, m.Processes.Limit)
		}
		return m, err
	})
}

// getPostgresMetrics serves the Postgres receiver drill-down
// for the row-click drawer on /databases. Mirrors getOracleMetrics:
// 30s cache TTL bucketed to a 30s grid so morning-triage hits
// share one query trip even with rolling time windows.
func (s *Server) getPostgresMetrics(w http.ResponseWriter, r *http.Request) {
	instance := r.URL.Query().Get("instance")
	from, to := parseFromTo(r, time.Hour)
	key := fmt.Sprintf("postgres:%s:%s", instance, cacheBucket(from, to))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		m, err := s.store.GetPostgresMetrics(ctx, instance, from, to)
		if err == nil && m != nil { // v0.10.909
			m.Backends.Forecast = s.dbForecast(ctx, "postgresql.backends", instance, m.Backends.Limit)
		}
		return m, err
	})
}

// getMySQLMetrics — MySQL receiver drill-down (buffer pool /
// threads / row-lock / slow queries / handlers / replica lag).
func (s *Server) getMySQLMetrics(w http.ResponseWriter, r *http.Request) {
	instance := r.URL.Query().Get("instance")
	from, to := parseFromTo(r, time.Hour)
	key := fmt.Sprintf("mysql:%s:%s", instance, cacheBucket(from, to))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		m, err := s.store.GetMySQLMetrics(ctx, instance, from, to)
		if err == nil && m != nil { // v0.10.909
			m.Connections.Forecast = s.dbForecast(ctx, "mysql.connection.count", instance, m.Connections.Limit)
		}
		return m, err
	})
}

// getRedisMetrics — Redis receiver drill-down (clients / memory /
// commands / hit rate / per-keyspace / replication / role).
func (s *Server) getRedisMetrics(w http.ResponseWriter, r *http.Request) {
	instance := r.URL.Query().Get("instance")
	from, to := parseFromTo(r, time.Hour)
	key := fmt.Sprintf("redis:%s:%s", instance, cacheBucket(from, to))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		return s.store.GetRedisMetrics(ctx, instance, from, to)
	})
}

// getMessaging is the parallel handler for queues / topics
// (Kafka / RabbitMQ / IBM MQ / etc.). Same caching semantics.
//
// v0.8.364 (Stage-2 M1) — optional ?compare=prior (the endpoints
// v0.5.404 pattern): a second scan of the SAME MVs over the
// immediately-preceding equal-length window, merged onto the
// current rows by (system, cluster, destination) identity. Opt-in
// because it doubles the CH cost.
func (s *Server) getMessaging(w http.ResponseWriter, r *http.Request) {
	from, to := parseFromTo(r, time.Hour)
	compare := r.URL.Query().Get("compare") == "prior"
	// Cache key hashes all inputs (window + compare). The default
	// read keeps the pre-M1 key byte-identical so the background
	// warm loop in api.go (warm("messaging", …)) still primes the
	// slot the page load hits; compare rides its own slot.
	// v0.9.813 — anahtar öneki v2'ye çıktı ÇÜNKÜ zarf değişti (çıplak
	// dizi → MessagingOverview). Önek sürümlenmeseydi rolling deploy
	// sırasında eski dizi payload'ı 30 sn boyunca yeni SPA'ya servis
	// edilir ve sayfa boş açılırdı (v0.9.443/458 dersi). Warm loop
	// (api.go) aynı öneki kullanır — yoksa ısıtılan slot hiç okunmaz.
	key := "messaging:v2:" + cacheBucket(from, to)
	if compare {
		key = "messaging:v2:cmp:" + cacheBucket(from, to)
	}
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		ov, err := s.store.GetMessaging(ctx, from, to)
		if err != nil {
			return nil, err
		}
		if !compare || len(ov.Rows) == 0 {
			return ov, nil
		}
		// Prior window: same length, shifted back by exactly the
		// window width so the comparison stays apples-to-apples.
		// Rollup variant skips the top-callers pass — the delta
		// merge only reads counts + quantiles. Prior failure is
		// non-fatal: return current rows without trends rather
		// than 500'ing the page.
		// v0.10.1025 — /databases ile AYNI kusur burada da vardı:
		// getMessaging alt sınırı alignBucketStart ile kovaya indiriyor,
		// eski prior ise `< from` (hizasız) ile bitiyordu; from'u içeren
		// 5 dk kovası iki pencereye de giriyordu. messaging_summary_5m
		// yalnız MV yolu (env yolu yok), dolayısıyla doğrudan PriorWindow.
		pFrom, pTo := chstore.PriorWindow(from, to)
		// v0.10.1025 (inceleme R3+R4) — /databases ile aynı iki kapı: MV
		// TTL ufku ve iki MV'nin (sayaçlar + üretim/tüketim) ileriye dönük
		// kapsaması. Düşerse prior alanı yok, rozet yok.
		now := time.Now()
		if !s.store.MessagingPriorReadable(ctx, pFrom, pTo, now) {
			return ov, nil
		}
		priorRows, err := s.store.GetMessagingRollup(ctx, pFrom, pTo)
		if err != nil {
			return ov, nil
		}
		// v0.10.1025 (R1) — canlı pencerenin son kovası henüz doluyor;
		// prior sayaçları dolu kısma oranlanır (bu liste de v0.8.364'ten beri
		// aynı aşağı-yanlılığı taşıyordu).
		mergeMessagingPrior(ov.Rows, priorRows, chstore.PriorCoverage(from, to, now))
		return ov, nil
	})
}

// mergeMessagingPrior copies the prior-window counters onto the
// current rows by (system, cluster, destination) identity — the
// same key GetMessaging groups by, so a destination that moved
// rank between windows still matches. Rows absent from the prior
// window keep zero Prior* fields (omitempty → absent in JSON →
// the frontend renders no delta badge). Pure — table-driven
// tested in messaging_prior_test.go (v0.8.364).
//
// v0.10.1025 (R1) — scale: the COUNTERS (spans, errors, produce,
// consume) are scaled by the live-window coverage
// (chstore.ScalePriorCount, floor 1 for a non-zero count); latency
// quantiles are not.
func mergeMessagingPrior(cur, prior []chstore.MessagingInstance, scale float64) {
	type key struct{ system, cluster, dest string }
	idx := make(map[key]*chstore.MessagingInstance, len(prior))
	for i := range prior {
		idx[key{prior[i].System, prior[i].Cluster, prior[i].Destination}] = &prior[i]
	}
	for i := range cur {
		p, ok := idx[key{cur[i].System, cur[i].Cluster, cur[i].Destination}]
		if !ok {
			continue
		}
		cur[i].PriorSpanCount = chstore.ScalePriorCount(p.SpanCount, scale)
		cur[i].PriorErrorCount = chstore.ScalePriorCount(p.ErrorCount, scale)
		cur[i].PriorProduceCount = chstore.ScalePriorCount(p.ProduceCount, scale)
		cur[i].PriorConsumeCount = chstore.ScalePriorCount(p.ConsumeCount, scale)
		cur[i].PriorAvgMs = p.AvgMs
		cur[i].PriorP50Ms = p.P50Ms
		cur[i].PriorP99Ms = p.P99Ms
	}
}

// getMessagingDetail is the parallel handler for queues /
// topics. Takes ?system=&cluster=&destination=&from=&to=.
//
// v0.9.973 — the empty-cluster default is no longer SILENT. It used to
// read "defaults to (default) for single-cluster deployments where the
// SPA hasn't been updated yet", which understated it: GetMessagingDetail
// filters cluster by EXACT EQUALITY, so on a multi-cluster install the
// assumption doesn't pick the wrong topic — it returns a ZEROED drawer
// for a topic that is very much alive. The operator then reads "no
// traffic" where the truth is "you didn't say which cluster".
//
// Kept as a default rather than a 400 on purpose: the SPA always sends
// all three fields (destinationParam.ts guards it), so a 400 would only
// ever break hand-built API calls, and there may be non-SPA consumers.
// The lie is what gets fixed, not the tolerance — the envelope now
// carries assumedCluster so the surface can say so.
func (s *Server) getMessagingDetail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	system := q.Get("system")
	dest := q.Get("destination")
	cluster := q.Get("cluster")
	assumedCluster := false
	if cluster == "" {
		cluster = "(default)"
		assumedCluster = true
	}
	if system == "" {
		http.Error(w, `{"error":"system required"}`, http.StatusBadRequest)
		return
	}
	from, to := parseFromTo(r, time.Hour)
	// assumedCluster is IN THE KEY: an explicit ?cluster=(default) resolves
	// to the same cluster string but is NOT an assumption, so the two answers
	// differ by one field. Sharing a key would let whichever ran first decide
	// whether the other one admits the guess — the v0.5.187 cross-poisoning
	// class, one boolean wide.
	key := fmt.Sprintf("msg-detail:%s:%s:%s:%t:%s", system, cluster, dest, assumedCluster, cacheBucket(from, to))
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		d, err := s.store.GetMessagingDetail(ctx, system, cluster, dest, from, to)
		if err != nil || d == nil {
			return d, err
		}
		d.AssumedCluster = assumedCluster
		return d, nil
	})
}
