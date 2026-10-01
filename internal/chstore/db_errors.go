package chstore

// db_errors.go — v0.10.1020 — bir veritabanına giden BAŞARISIZ çağrıların hata
// türüne göre kırılımı (operatör 2026-10-01: Databases için "Dynatrace
// databases kısmını baz al; o database ile ilgili veriler gelebilir" — dilim 2,
// Dynatrace'in hata analizi karşılığı).
//
// /database sayfası hata ORANINI gösteriyordu ama "hangi hata" sorusunu
// cevaplamıyordu. İmza, en belirleyiciden en kabaya üç basamak:
//
//	code     Oracle hata kodu (ORA-/PLS-/TNS-NNNNN) — status_msg ya da exception
//	         mesajında geçen ilk kod
//	type     exception tipi (event) ya da error.type attribute'u
//	message  hata mesajının ilk 80 karakteri
//	none     hiçbiri yok ("mesajsız hata")
//
// İki okuma:
//  1. db_caller_summary_5m (MV) — bu kimliğe HATA üretmiş çağıran servisler.
//     Hiç yoksa ham okuma YAPILMAZ.
//  2. ham spans — yalnız o servisler (birincil anahtar service_name, time),
//     pencere, aynı kimlik yüklemi (GetDatabaseDetail ile birebir) ve
//     status_code='error'. Ham okuma bilinçli: hata mesajı / event hiçbir
//     ön-toplamda yok; tarama hatalı çağıranlara ve hata satırlarına budanır.

import (
	"context"
	"time"
)

const (
	// dbErrorGroupLimit — dönen en çok imza; bir fazlası "kesildi" işaretidir.
	dbErrorGroupLimit = 20
	// dbErrorCallerLimit — ham okumanın daraldığı en çok çağıran servis.
	dbErrorCallerLimit = 200
	// dbErrorCodeRe — Oracle ailesi hata kodu (ilk eşleşme).
	dbErrorCodeRe = `(?:ORA|PLS|TNS)-[0-9]{4,5}`
)

// DBErrorGroup — bir hata imzası.
type DBErrorGroup struct {
	Signature     string `json:"signature"`
	Kind          string `json:"kind"` // code | type | message | none
	Count         uint64 `json:"count"`
	Services      uint64 `json:"services"`
	TopService    string `json:"topService"`
	Sample        string `json:"sample"`
	LastSeen      int64  `json:"lastSeen"` // unix ns
	SampleTraceID string `json:"sampleTraceId"`
}

// DBErrors — /api/databases/errors yükü.
type DBErrors struct {
	System   string `json:"system"`
	Instance string `json:"instance"`
	DBName   string `json:"dbName,omitempty"`
	// Total — listelenen imzaların toplam hatalı çağrısı.
	Total  uint64         `json:"total"`
	Groups []DBErrorGroup `json:"groups"`
	// Truncated — dbErrorGroupLimit'ten fazla imza vardı; liste en sık olanlar.
	Truncated bool `json:"truncated"`
	// CallersCapped — hata üreten çağıran sayısı tavanı aştı; kırılım en çok
	// hata üreten dbErrorCallerLimit servisi kapsar.
	CallersCapped bool `json:"callersCapped"`
}

// dbErrorCallersSQL — SAF: bu kimliğe hata üretmiş çağıranlar (MV).
// Arg sırası: bucketStart, to, system, instance, [dbName].
func dbErrorCallersSQL(mvNameSQL string) string {
	return `
		SELECT service_name
		FROM db_caller_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?
		  AND db_system = ? AND instance = ?` + mvNameSQL + `
		  AND service_name != ''
		GROUP BY service_name
		HAVING countMerge(error_count_state) > 0
		ORDER BY countMerge(error_count_state) DESC
		LIMIT ` + itoa(dbErrorCallerLimit+1) + `
		SETTINGS max_execution_time = 8`
}

// dbErrorGroupsSQL — SAF: imza kırılımı (ham spans). whereSQL çağıranın kimlik
// + pencere + servis yüklemi; burada yalnız hata süzgeci ve gruplama eklenir.
func dbErrorGroupsSQL(whereSQL string) string {
	return `
		WITH
		  ` + exMsgExpr + ` AS ex_msg,
		  ` + exTypeExpr + ` AS ex_type,
		  extract(concat(status_msg, ' ', ex_msg), '` + dbErrorCodeRe + `') AS err_code
		SELECT
		  multiIf(err_code != '', err_code,
		          ex_type NOT IN ('', '<unknown>'), ex_type,
		          status_msg != '', substring(status_msg, 1, 80),
		          ex_msg != '', substring(ex_msg, 1, 80),
		          '') AS sig,
		  multiIf(err_code != '', 'code',
		          ex_type NOT IN ('', '<unknown>'), 'type',
		          status_msg != '' OR ex_msg != '', 'message',
		          'none') AS sig_kind,
		  count() AS c,
		  uniq(service_name) AS svcs,
		  topK(1)(service_name)[1] AS top_svc,
		  substring(any(if(ex_msg != '', ex_msg, status_msg)), 1, 300) AS sample,
		  toUnixTimestamp64Nano(max(time)) AS last_seen,
		  argMax(trace_id, time) AS sample_trace
		FROM spans
		WHERE ` + whereSQL + `
		  AND status_code = 'error'
		GROUP BY sig, sig_kind
		ORDER BY c DESC, sig ASC
		LIMIT ` + itoa(dbErrorGroupLimit+1) + `
		SETTINGS max_execution_time = 10`
}

// capDBErrorCallers — SAF: tavanı aşan çağıran listesini keser.
func capDBErrorCallers(svcs []string) ([]string, bool) {
	if len(svcs) > dbErrorCallerLimit {
		return svcs[:dbErrorCallerLimit], true
	}
	return svcs, false
}

// finishDBErrorGroups — SAF: bir fazla okunan satırı "kesildi" işaretine çevirir
// ve toplamı hesaplar.
func finishDBErrorGroups(out *DBErrors, groups []DBErrorGroup) {
	if len(groups) > dbErrorGroupLimit {
		groups, out.Truncated = groups[:dbErrorGroupLimit], true
	}
	out.Groups = groups
	for _, g := range groups {
		out.Total += g.Count
	}
}

// GetDatabaseErrors — dosya başı.
func (s *Store) GetDatabaseErrors(ctx context.Context, system, instance, dbName string, from, to time.Time) (*DBErrors, error) {
	if system == "" {
		return nil, nil
	}
	if from.IsZero() {
		from = time.Now().Add(-1 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now()
	}
	out := &DBErrors{System: system, Instance: instance, DBName: dbName, Groups: []DBErrorGroup{}}

	mvNameSQL, mvNameArgs := dbDetailNameFilter("db_name", dbName)
	rawNameSQL, rawNameArgs := dbDetailNameFilter(dbNameExpr, dbName)
	mvInstance := instance
	if instance == "" {
		mvInstance = "unknown"
	}
	// 1) Hata üretmiş çağıranlar — MV, 5 dk ızgarasına hizalı (GetDatabaseDetail ile aynı).
	mvArgs := append([]any{from.Truncate(5 * time.Minute), to, system, mvInstance}, mvNameArgs...)
	rows, err := s.telemetryReadConn().Query(ctx, dbErrorCallersSQL(mvNameSQL), mvArgs...)
	if err != nil {
		return nil, err
	}
	var svcs []string
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			rows.Close()
			return nil, err
		}
		svcs = append(svcs, svc)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(svcs) == 0 {
		return out, nil // bu pencerede hatalı çağrı yok — ham okuma yapılmaz
	}
	svcs, out.CallersCapped = capDBErrorCallers(svcs)

	// 2) İmza kırılımı — ham spans, hatalı çağıranlara budanmış.
	instancePredicate := dbInstanceExpr + " = ?"
	whereSQL := `time >= ? AND time <= ? AND db_system = ? AND ` + instancePredicate + rawNameSQL + ` AND service_name IN (?)`
	args := append([]any{from, to, system}, argIfNeeded(instancePredicate, instance)...)
	args = append(args, rawNameArgs...)
	args = append(args, svcs)
	grows, err := s.telemetryReadConn().Query(ctx, dbErrorGroupsSQL(whereSQL), args...)
	if err != nil {
		return nil, err
	}
	defer grows.Close()
	groups := []DBErrorGroup{}
	for grows.Next() {
		var g DBErrorGroup
		if err := grows.Scan(&g.Signature, &g.Kind, &g.Count, &g.Services, &g.TopService, &g.Sample, &g.LastSeen, &g.SampleTraceID); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	if err := grows.Err(); err != nil {
		return nil, err
	}
	finishDBErrorGroups(out, groups)
	return out, nil
}
