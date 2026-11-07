package chstore

// login_summary.go — v0.10.1903 (login özeti, docs/frontend/login-redesign.md §5.1, §5.2, §5.5).
//
// Anonim login özetinin ClickHouse'tan okunan beş sayısı. Hepsi ön-toplulaştırılmış
// kaynaktan (MV) ya da metadata tablosundan okunur — ham `spans` YOK (invariant #3).
// Çağıran: internal/loginsummary Builder (lider kilitli worker, 60 sn / 5 dk turları).
// İstek yolu (anonim uç) buraya ASLA gelmez.
//
// Her sorgu: LIMIT + SETTINGS max_execution_time, tablo niteliksiz. SQL metinleri
// SAF fonksiyonlardır (login_summary_test.go metni pinler).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LoginCounts — bugünün (ClickHouse günü) üç sayısı. Alan nil = o sorgu
// başarısız oldu (Builder "degraded" der, eski değeri korumaz uydurmaz).
type LoginCounts struct {
	Spans    *uint64
	Traces   *uint64
	Services *uint64
}

// loginSpansTodaySQL — bugünün toplam span sayısı. Giriş-span süzgeci yok:
// "toplanan telemetri" hacmi (service_summary_5m'in her satırı).
func loginSpansTodaySQL() string {
	return `
		SELECT countMerge(span_count_state)
		FROM service_summary_5m
		WHERE time_bucket >= toStartOfDay(now())
		LIMIT 1
		SETTINGS max_execution_time = 5`
}

// loginTracesTodaySQL — bugünün yaklaşık tekil trace sayısı (uniq HLL, MV).
func loginTracesTodaySQL() string {
	return `
		SELECT uniqMerge(trace_count_state)
		FROM trace_summary_1d
		WHERE day = today()
		LIMIT 1
		SETTINGS max_execution_time = 5`
}

// loginServicesSQL — son 24 saatte span üreten servis sayısı (Coremetry'nin
// kendi `coremetry-*` servisleri hariç).
func loginServicesSQL() string {
	return `
		SELECT uniq(service_name)
		FROM service_summary_5m
		WHERE time_bucket >= now() - toIntervalDay(1)
		  AND NOT startsWith(service_name, 'coremetry-')
		LIMIT 1
		SETTINGS max_execution_time = 8`
}

// LoginSummaryCounts — üç sayıyı ayrı ayrı, soft-fail koşar: biri düşerse o
// alan nil kalır, diğerleri döner; dönen hata ilk hatadır (Builder durumu
// "degraded" yapar).
func (s *Store) LoginSummaryCounts(ctx context.Context) (LoginCounts, error) {
	var out LoginCounts
	var errs []error
	read := func(sql string, dst **uint64) {
		var n uint64
		if err := s.telemetryReadConn().QueryRow(ctx, sql).Scan(&n); err != nil {
			errs = append(errs, err)
			return
		}
		*dst = &n
	}
	read(loginSpansTodaySQL(), &out.Spans)
	read(loginTracesTodaySQL(), &out.Traces)
	read(loginServicesSQL(), &out.Services)
	return out, errors.Join(errs...)
}

// loginEndpointsSQL — v0.10.1915: son 1 saatte istek alan endpoint sayısı
// (yaklaşık, uniq). Giriş-span ilkesi: yalnız `server` / `consumer` türü
// (servisin KENDİ uçları; giden çağrılar ve iç span'ler endpoint değildir).
// Kaynak `spanmetrics_1m` (tür boyutunu taşıyan MV; ham `spans` yok). Pencere
// 1 saat: 1 dk'lık MV'de 24 saat taramak pahalı olurdu; "şu an izlenen" için
// yeterli. Coremetry'nin kendi servisleri hariç.
func loginEndpointsSQL() string {
	return `
		SELECT uniq(service_name, name)
		FROM spanmetrics_1m
		WHERE time_bucket >= now() - toIntervalHour(1)
		  AND kind IN ('server', 'consumer')
		  AND NOT startsWith(service_name, 'coremetry-')
		LIMIT 1
		SETTINGS max_execution_time = 8`
}

// CountLoginEndpoints — son 1 saatte istek alan endpoint sayısı (yalnız adet).
func (s *Store) CountLoginEndpoints(ctx context.Context) (uint64, error) {
	var n uint64
	if err := s.telemetryReadConn().QueryRow(ctx, loginEndpointsSQL()).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// cosreQuestionsSQL — son penceredeki CoSRE sohbet turu sayısı. Her satır bir
// tur (tek yazıcı api/copilot_chat.go); `explain` ✨ düğmesidir, soru sayılmaz.
func cosreQuestionsSQL() string {
	return `
		SELECT count()
		FROM cosre_interactions
		WHERE created_at >= ? AND tier != 'explain'
		LIMIT 1
		SETTINGS max_execution_time = 5`
}

// CountCosreQuestions — [since, şimdi) CoSRE soru adedi (yalnız sayı; soru
// metni, kullanıcı okunmaz).
func (s *Store) CountCosreQuestions(ctx context.Context, since time.Time) (uint64, error) {
	var n uint64
	// cosre_interactions: append-only günlük ama state tablosu sınıfı —
	// in-order ana bağlantıdan (telemetryReadConn DEĞİL).
	if err := s.conn.QueryRow(ctx, cosreQuestionsSQL(), since).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// totalUncompressedBytesSQL — SAF: açılmış (sıkıştırılmamış) toplam bayt.
// Küme kipinde `cluster('<ad>', system.parts)` (shard başına TEK replika →
// çift sayım yok; clusterAllReplicas çift sayardı — sysstats.go), değilse
// `system.parts`. MV iç tabloları DAHİL ("toplam veri"). Küme adı yalnız
// güvenli karakterlerden oluşuyorsa gömülür (aksi hâlde yerel tabloya düşer).
func totalUncompressedBytesSQL(clusterName string) string {
	src := "system.parts"
	if cn := strings.TrimSpace(clusterName); cn != "" && safeClusterIdent(cn) {
		src = "cluster('" + cn + "', system.parts)"
	}
	return fmt.Sprintf(`
		SELECT sum(data_uncompressed_bytes)
		FROM %s
		WHERE database = currentDatabase() AND active = 1
		LIMIT 1
		SETTINGS max_execution_time = 8`, src)
}

func safeClusterIdent(s string) bool {
	for _, r := range s {
		ok := r == '_' || r == '-' || r == '.' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return false
		}
	}
	return s != ""
}

// TotalUncompressedBytes — depolanan verinin sıkıştırılmamış toplam boyutu.
func (s *Store) TotalUncompressedBytes(ctx context.Context) (uint64, error) {
	var n uint64
	if err := s.conn.QueryRow(ctx, totalUncompressedBytesSQL(s.cfg.ClusterName)).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// loginDBNamesSQL — v0.10.1931: verilen (db_system, instance) çiftleri için
// son pencerede en çok çağrılan veritabanı adı (db.name). MV okuması
// (db_summary_5m); süzgeç ORDER BY önekinde (db_system, instance) → yalnız
// seçili ≤30 db'nin granülleri okunur, servis sayısından bağımsız. Anahtar
// topoloji kimliğiyle aynı `instance` zinciri (dbInstanceExpr).
func loginDBNamesSQL() string {
	return `
		SELECT db_system, instance, argMax(db_name, c)
		FROM (
			SELECT db_system, instance, db_name, countMerge(span_count_state) AS c
			FROM db_summary_5m
			WHERE time_bucket >= ? AND db_system IN ? AND instance IN ?
			  AND db_name != 'default' AND db_name != ''
			GROUP BY db_system, instance, db_name
		)
		GROUP BY db_system, instance
		LIMIT 1000
		SETTINGS max_execution_time = 5`
}

// LoginDBNames — keys (`<db_system>@<instance>`) → veritabanı adı (login
// topolojisi db düğümlerini adlandırır; host cevaba çıkmaz, yalnız anahtardır).
// IN süzgeci iki ayrı listedir; çapraz eşleşmeler aşağıda anahtarla elenir.
func (s *Store) LoginDBNames(ctx context.Context, since time.Time, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	want := map[string]bool{}
	var systems, instances []string
	seenSys, seenInst := map[string]bool{}, map[string]bool{}
	for _, k := range keys {
		at := strings.Index(k, "@")
		if at <= 0 {
			continue
		}
		want[k] = true
		if sys := k[:at]; !seenSys[sys] {
			seenSys[sys] = true
			systems = append(systems, sys)
		}
		if inst := k[at+1:]; !seenInst[inst] {
			seenInst[inst] = true
			instances = append(instances, inst)
		}
	}
	if len(want) == 0 {
		return map[string]string{}, nil
	}
	rows, err := s.telemetryReadConn().Query(ctx, loginDBNamesSQL(), since, systems, instances)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sys, inst, name string
		if err := rows.Scan(&sys, &inst, &name); err != nil {
			return nil, err
		}
		if k := sys + "@" + inst; want[k] {
			out[k] = name
		}
	}
	return out, rows.Err()
}

// loginServiceVolumesSQL — v0.10.1937: servis başına son penceredeki span
// sayısı (login galaksisinin adsız servis yıldızları). MV okuması
// (service_summary_5m); Coremetry'nin kendi servisleri hariç; en çok 5000.
func loginServiceVolumesSQL() string {
	return `
		SELECT service_name, countMerge(span_count_state) AS c
		FROM service_summary_5m
		WHERE time_bucket >= ? AND NOT startsWith(service_name, 'coremetry-')
		GROUP BY service_name
		ORDER BY c DESC
		LIMIT 5000
		SETTINGS max_execution_time = 5`
}

// LoginServiceVolumes — servis → span sayısı (ad yalnız sunucuda; cevaba
// yalnız normalize yoğunluk çıkar, bkz. loginsummary.BuildStars).
func (s *Store) LoginServiceVolumes(ctx context.Context, since time.Time) (map[string]uint64, error) {
	rows, err := s.telemetryReadConn().Query(ctx, loginServiceVolumesSQL(), since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uint64{}
	for rows.Next() {
		var name string
		var c uint64
		if err := rows.Scan(&name, &c); err != nil {
			return nil, err
		}
		out[name] = c
	}
	return out, rows.Err()
}
