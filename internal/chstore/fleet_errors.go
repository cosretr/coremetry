package chstore

// fleet_errors.go — CoSRE karşılamasının "hatalı istek" sayısı.
//
// v0.10.1255 bunu service_summary_5m'in countIfMerge(error_count_state)'inden
// okuyordu: TÜM span türleri (server/client/db/internal), TÜM ortamlar. Tek
// başarısız istek, zincirdeki her hatalı client/db/internal span'i kadar
// sayılıyordu (prod: "Son 1 saatte 445.394 hata" — operatör: "occurrences mı
// bu sayı?").
//
// v0.10.1278 (operatör 2026-10-17) — giriş-span ilkesi (2026-07-25 direktifi,
// evaluator error_rate v0.10.783 emsali): hatalı İSTEK = status_code='error'
// olan GİRİŞ span'i (kind IN ('server','consumer')). Kind taşıyan tek MV
// spanmetrics_1m (evaluator'ın giriş-span hata oranı da buradan); ham spans
// taraması yok.
//
// ORTAM: spanmetrics_1m'de deploy_env boyutu YOK, service_env_summary_5m'de
// (ortam MV'si) kind boyutu YOK — ikisini birden taşıyan MV yok ve MV state
// kolonu eklemek DROP+RECREATE ister (bir hata düzeltmesinin kapsamı değil).
// Ortam seçiliyken ham spans'e düşmek yerine iki MV birleştirilir: servis
// başına giriş-span hata sayısı, o servisin ortam MV'sindeki hata payıyla
// (env hataları / tüm hatalar) seçili ortama dağıtılır. Tek ortamda koşan
// serviste sonuç KESİN; çok ortamlı serviste "giriş/tüm hata oranı ortamlar
// arasında aynı" varsayımına dayanan bir dağıtımdır — UI bunu söyler.

import (
	"context"
	"time"
)

// fleetFailedRequestsSQL — SAF (pinli): tüm ortamlar, giriş span'leri,
// pencere sınırlı + LIMIT + süre tavanı. Bağ sırası: from, to.
func fleetFailedRequestsSQL() string {
	return `SELECT countMerge(error_state) AS failed_req
		FROM spanmetrics_1m
		WHERE time_bucket >= ? AND time_bucket < ?
		  AND ` + EntrySpanKindsWhere + ` AND status_code = 'error'
		LIMIT 1
		SETTINGS max_execution_time = 10`
}

// fleetFailedRequestsEnvSQL — SAF (pinli): tek ortam. Giriş-span hataları
// (spanmetrics_1m) × servisin ortam hata payı (service_env_summary_5m).
// Bağ sırası: from, to (spanmetrics_1m), env, from, to (ortam MV'si).
func fleetFailedRequestsEnvSQL() string {
	return `SELECT toUInt64(round(sum(e.failed * a.env_errs / a.all_errs))) AS failed_req
		FROM (
			SELECT service_name, countMerge(error_state) AS failed
			FROM spanmetrics_1m
			WHERE time_bucket >= ? AND time_bucket < ?
			  AND ` + EntrySpanKindsWhere + ` AND status_code = 'error'
			GROUP BY service_name
		) AS e
		INNER JOIN (
			SELECT service_name, sumIf(errs, deploy_env = ?) AS env_errs, sum(errs) AS all_errs
			FROM (
				SELECT service_name, deploy_env, countIfMerge(error_count_state) AS errs
				FROM service_env_summary_5m
				WHERE time_bucket >= ? AND time_bucket < ?
				GROUP BY service_name, deploy_env
			)
			GROUP BY service_name
			HAVING env_errs > 0 AND all_errs > 0
		) AS a ON e.service_name = a.service_name
		LIMIT 1
		SETTINGS max_execution_time = 10`
}

// FleetFailedRequests — [from, to) içinde başarısız giriş isteği sayısı.
// env "" = tüm ortamlar. env doluyken çağıran EnvSummaryCovers ile ortam
// MV'sinin pencereyi kapsadığını ölçmüş olmalı (MV geriye dolmaz). İki
// MV aynı kova başından okunur (5 dk hizası) ki pay ve payda aynı pencere
// olsun.
func (s *Store) FleetFailedRequests(ctx context.Context, env string, from, to time.Time) (uint64, error) {
	from = alignBucketStart(from)
	var n uint64
	var err error
	if env == "" {
		err = s.telemetryReadConn().QueryRow(ctx, s.weighSQL(fleetFailedRequestsSQL(), from, "spanmetrics_1m"), from, to).Scan(&n) // v0.10.2435 — sampling-weight Phase 2
	} else {
		err = s.telemetryReadConn().QueryRow(ctx, s.weighSQL(fleetFailedRequestsEnvSQL(), from, "spanmetrics_1m", "service_env_summary_5m"), from, to, env, from, to).Scan(&n) // v0.10.2435 — sampling-weight Phase 2
	}
	return n, err
}
