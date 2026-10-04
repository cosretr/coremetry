package chstore

// oracle_exception_groups.go — v0.10.1092 (operatör: "Oracle hataları
// Exceptions gibi görünsün, hatta Exceptions altında da olabilir.")
//
// Oracle hata tablosunun satırları DURGUN bir akıştır (dakikada binlerce);
// sıçrama-anomalisi yolu (ext:error_count → dış tarayıcı) tasarım gereği
// onlarda ateşlemez. Bu dosya o satırları Exceptions'ın grup modeline taşır:
// (kaynak, hata kodu, operasyon kodu) başına BİR exception_groups satırı.
//
//   - Parmak izi `ora:` + sha1(kaynak | kod | operasyon)[:16] — kanal, host,
//     instance, servis kırılımdır, anahtar DEĞİL (aynı hata farklı kanaldan
//     gelince yeni grup açmasın).
//   - ex_type = hata kodu (kod kimliği olarak gösterilir), ex_message =
//     operasyon kodu, service = baskın servis (trace → servis çözümü) yoksa
//     `oracle:<kaynak adı>`.
//   - Yazım worker liderinin tazeleyicisinden (internal/oracle/exgroups.go);
//     okuma burada: KAPANMIŞ dakikaların toplamı (OracleGroupAggregates), örnek
//     ve oluşum serisi oracle_error_log'dan (GetExceptionGroupSamples /
//     GetExceptionOccurrences `ora:` dalı).
//   - Fırtına sayımına, paylaşılan patlamaya, ölümcül dedektöre, yayılım
//     işaretine ve span tazeleyicisinin boot tohumuna GİRMEZ (NotOracleGroupSQL):
//     hacimleri o dedektörlere hükmederdi, last_seen'leri span checkpoint'ini
//     ileri iterdi.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// OracleGroupPrefix — Oracle kökenli exception grubunun parmak izi öneki.
const OracleGroupPrefix = "ora:"

// OracleGroupServicePrefix — servis çözülemeyen grubun sentetik servisi.
const OracleGroupServicePrefix = "oracle:"

// NotOracleGroupSQL — exception_groups okuyan span-merkezli dedektörlerin
// (fırtına, paylaşılan patlama, ölümcül, yayılım, boot tohumu) ortak süzgeci.
const NotOracleGroupSQL = "NOT startsWith(fingerprint, '" + OracleGroupPrefix + "')"

// OracleGroupFingerprint — SAF ve KARARLI: aynı (kaynak kimliği, kod,
// operasyon) her zaman aynı parmak izini verir. Girdiler kırpılır (Oracle
// CHAR dolgusu ayrı grup açmasın — sayaçla aynı kural, counter.go).
func OracleGroupFingerprint(sourceID, code, op string) string {
	h := sha1.New()
	h.Write([]byte(strings.TrimSpace(sourceID)))
	h.Write([]byte("|"))
	h.Write([]byte(strings.TrimSpace(code)))
	h.Write([]byte("|"))
	h.Write([]byte(strings.TrimSpace(op)))
	return OracleGroupPrefix + hex.EncodeToString(h.Sum(nil))[:16]
}

// IsOracleGroup — SAF: parmak izi Oracle kökenli mi.
func IsOracleGroup(fingerprint string) bool { return strings.HasPrefix(fingerprint, OracleGroupPrefix) }

// OracleGroupFallbackService — SAF: servis çözülemeyen grubun servis alanı.
func OracleGroupFallbackService(sourceName string) string {
	return OracleGroupServicePrefix + strings.TrimSpace(sourceName)
}

// isOracleFallbackService — SAF: servis alanı sentetik mi.
func isOracleFallbackService(svc string) bool {
	return svc == "" || strings.HasPrefix(svc, OracleGroupServicePrefix)
}

// oracleWeightExpr — satırın ağırlığı SQL'de: OracleWeightAttr attribute'u
// (ön-toplanmış satır, v0.10.904) yoksa 1 — EffectiveWeight'in SQL ikizi.
const oracleWeightExpr = "greatest(toUInt64OrZero(attr_values[indexOf(attr_keys, '" + OracleWeightAttr + "')]), 1)"

// OracleGroupAgg — bir (kod, operasyon, kanal, DAKİKA) dörtlüsünün kapanmış
// toplamı. Dakika tanesi: tazeleyici saatlik pencereleri (Oracle P1 kuralı)
// ve tavan kesimini (OracleGroupAggLimit) dakika sınırında kurar.
type OracleGroupAgg struct {
	Code, Op, Channel string
	Minute            time.Time // toStartOfMinute(time)
	Weight            uint64
	First, Last       time.Time
}

const (
	// OracleGroupAggLimit — tek toplamanın satır tavanı. Satırlar DAKİKA
	// sırasıyla gelir; tavana çarpan tur son (belki yarım) dakikayı atar ve
	// imleci oraya kadar ilerletir — kalan sonraki turda (ilerleyen yakalama).
	OracleGroupAggLimit   = 20000
	oracleGroupAggExecSec = 20
)

// oracleGroupAggSQL — SAF: tazeleyicinin tek okuması. PK öneki (source_id,
// time) taraması, FINAL (özel kipte her poll aynı pencereyi yeniden yazar,
// RMT son sürümü tutar), yarı açık [from, to), dakika SIRALI, LIMIT +
// max_execution_time.
func oracleGroupAggSQL() string {
	return fmt.Sprintf(`SELECT error_code, operation_code, channel_code,
		       toStartOfMinute(time) AS m,
		       sum(%s) AS w,
		       min(time) AS first_t, max(time) AS last_t
		FROM oracle_error_log FINAL
		WHERE source_id = ? AND time >= ? AND time < ?
		GROUP BY error_code, operation_code, channel_code, m
		ORDER BY m ASC, w DESC
		LIMIT %d
		SETTINGS max_execution_time = %d`, oracleWeightExpr, OracleGroupAggLimit, oracleGroupAggExecSec)
}

// OracleGroupAggregates — kaynağın [from, to) aralığındaki satırları
// (kod, operasyon, kanal) başına ağırlık toplamı. Boş aralık → sorgu YOK.
func (s *Store) OracleGroupAggregates(ctx context.Context, sourceID string, from, to time.Time) ([]OracleGroupAgg, error) {
	if sourceID == "" || !to.After(from) {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, oracleGroupAggSQL(), sourceID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OracleGroupAgg
	for rows.Next() {
		var a OracleGroupAgg
		if err := rows.Scan(&a.Code, &a.Op, &a.Channel, &a.Minute, &a.Weight, &a.First, &a.Last); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ── okuma dalı: örnekler + oluşum serisi ─────────────────────────────────

const (
	// oracleGroupSampleLookback — örnek penceresi: son görülmeden geriye.
	oracleGroupSampleLookback = 24 * time.Hour
	// oracleGroupOccWindow — oluşum serisi penceresi (dakikalık kova; 1440).
	oracleGroupOccWindow = 24 * time.Hour
	oracleGroupOccStep   = 60 // sn
)

// oracleGroupSourceSQL — grubun kaynağını bulmak için ayrık source_id'ler:
// son görülme çevresinde (1 sa geri, 2 dk ileri), kod + operasyon eşitliği.
func oracleGroupSourceSQL() string {
	return `SELECT DISTINCT source_id
		FROM oracle_error_log
		WHERE time >= ? AND time <= ?
		  AND trimBoth(error_code) = ? AND trimBoth(operation_code) = ?
		LIMIT 50
		SETTINGS max_execution_time = 5`
}

// oracleGroupSource — parmak izi hash'li olduğu için kaynak geri çözülür:
// adayların her biri için parmak izi yeniden hesaplanır, eşleşen döner.
// Bulunamazsa "" (kaynak silinmiş / satırlar TTL'den düşmüş).
func (s *Store) oracleGroupSource(ctx context.Context, g *ExceptionGroup) (string, error) {
	last := time.Unix(0, g.LastSeen).UTC()
	rows, err := s.conn.Query(ctx, oracleGroupSourceSQL(), last.Add(-time.Hour), last.Add(2*time.Minute),
		strings.TrimSpace(g.Type), strings.TrimSpace(g.Message))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		if OracleGroupFingerprint(id, g.Type, g.Message) == g.Fingerprint {
			return id, nil
		}
	}
	return "", rows.Err()
}

// OracleGroupSource — API için dışa açık ikiz (detay paneli kaynağı adlandırır).
func (s *Store) OracleGroupSource(ctx context.Context, g *ExceptionGroup) (string, error) {
	return s.oracleGroupSource(ctx, g)
}

// oracleErrorsByOpCodeSQL — OracleErrorsByKey'in kanal-bağımsız ikizi
// (grup anahtarı kanal taşımaz): PK öneki + kırpılmış kod/operasyon eşitliği.
func oracleErrorsByOpCodeSQL(limit int) string {
	return fmt.Sprintf(`SELECT source_id, time, row_id, severity_num, severity_text, body, trace_id, span_id,
			host_name, instance_id, operation_code, error_code, external_code, error_type, channel_code, task_code,
			request_id, customer_id, teller_id, location, attr_keys, attr_values
		FROM oracle_error_log FINAL
		WHERE source_id = ? AND time >= ? AND time < ?
		  AND trimBoth(operation_code) = ? AND trimBoth(error_code) = ?
		ORDER BY time DESC
		LIMIT %d
		SETTINGS max_execution_time = %d`, clampOracleErrorsLimit(limit), oracleErrorsMaxExecSec)
}

// OracleErrorsByOpCode — grubun satırları (en yeni önce), sınırlı.
func (s *Store) OracleErrorsByOpCode(ctx context.Context, sourceID, op, code string, from, to time.Time, limit int) ([]OracleErrorRow, error) {
	if sourceID == "" || !to.After(from) {
		return []OracleErrorRow{}, nil
	}
	rows, err := s.conn.Query(ctx, oracleErrorsByOpCodeSQL(limit), sourceID, from, to, strings.TrimSpace(op), strings.TrimSpace(code))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OracleErrorRow{}
	for rows.Next() {
		var r OracleErrorRow
		if err := rows.Scan(
			&r.SourceID, &r.Time, &r.RowID, &r.SeverityNum, &r.SeverityText, &r.Body, &r.TraceID, &r.SpanID,
			&r.HostName, &r.InstanceID, &r.OperationCode, &r.ErrorCode, &r.ExternalCode, &r.ErrorType, &r.ChannelCode, &r.TaskCode,
			&r.RequestID, &r.CustomerID, &r.TellerID, &r.Location, &r.AttrKeys, &r.AttrValues,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// oracleSampleFromRow — SAF: Oracle satırı → örnek kartı. Mesaj satırın
// gövdesi (yoksa kod · operasyon), span adı operasyon, durum satırı kanal /
// host / ağırlık kırılımı. Stack yok (Oracle satırı stack taşımaz).
func oracleSampleFromRow(r OracleErrorRow) ExceptionSample {
	msg := strings.TrimSpace(r.Body)
	if msg == "" {
		msg = strings.TrimSpace(r.ErrorCode) + " · " + strings.TrimSpace(r.OperationCode)
	}
	parts := []string{}
	if c := strings.TrimSpace(r.ChannelCode); c != "" {
		parts = append(parts, "kanal "+c)
	}
	if h := strings.TrimSpace(r.HostName); h != "" {
		parts = append(parts, "host "+h)
	}
	if w := r.EffectiveWeight(); w > 1 {
		parts = append(parts, fmt.Sprintf("×%d", w))
	}
	return ExceptionSample{
		TraceID: r.TraceID, SpanID: r.SpanID, Time: r.Time.UnixNano(),
		Message: msg, SpanName: strings.TrimSpace(r.OperationCode), StatusMsg: strings.Join(parts, " · "),
	}
}

// oracleGroupSamples — `ora:` dalı: kaynak çözülür, satırlar OracleErrorsByOpCode
// ile (son görülmeden 24 sa geri, tavan 1000) okunur. Zarf dürüst: satır
// tavana ulaşmadıysa pencere bitti.
func (s *Store) oracleGroupSamples(ctx context.Context, g *ExceptionGroup, limit int) (ExceptionSamples, error) {
	src, err := s.oracleGroupSource(ctx, g)
	if err != nil {
		return ExceptionSamples{}, err
	}
	res := ExceptionSamples{Samples: []ExceptionSample{}}
	if src == "" {
		res.WindowExhausted = true
		return res, nil
	}
	last := time.Unix(0, g.LastSeen).UTC()
	from := last.Add(-oracleGroupSampleLookback)
	if first := time.Unix(0, g.FirstSeen).UTC(); first.After(from) {
		from = first
	}
	rows, err := s.OracleErrorsByOpCode(ctx, src, g.Message, g.Type, from, last.Add(2*time.Minute), limit)
	if err != nil {
		return ExceptionSamples{}, err
	}
	res.Scanned = len(rows)
	res.WindowExhausted = len(rows) < clampOracleErrorsLimit(limit)
	for _, r := range rows {
		res.Samples = append(res.Samples, oracleSampleFromRow(r))
	}
	// v0.10.1104 — Oracle satırının trace id'si Coremetry'de olmayabilir:
	// tek sınırlı sorguyla işaretlenir; hata = alan boş (bilinmiyor), çağrı düşmez.
	markOracleSampleTraces(ctx, res.Samples, s.TraceFactsByIDs)
	return res, nil
}

// oracleGroupOccSQL — SAF: dakikalık ağırlık serisi (FINAL, sınırlı).
func oracleGroupOccSQL() string {
	return fmt.Sprintf(`SELECT toStartOfInterval(time, INTERVAL %d SECOND) AS bucket,
		       sum(%s) AS c
		FROM oracle_error_log FINAL
		WHERE source_id = ? AND time >= ? AND time <= ?
		  AND trimBoth(operation_code) = ? AND trimBoth(error_code) = ?
		GROUP BY bucket
		ORDER BY bucket
		LIMIT %d
		SETTINGS max_execution_time = 10`, oracleGroupOccStep, oracleWeightExpr, occurrenceBucketCap)
}

// oracleOccWindow — SAF: oluşum serisinin penceresi — son görülmeden en çok
// 24 sa geri (dakikalık kova, 1440 nokta); ilk görülme daha yakınsa oradan.
func oracleOccWindow(firstNs, lastNs int64) (int64, int64) {
	from := lastNs - int64(oracleGroupOccWindow)
	if firstNs > from {
		from = firstNs
	}
	if lastNs <= from {
		lastNs = from + int64(time.Second)
	}
	return from, lastNs
}

// oracleGroupOccurrences — `ora:` dalı: dakika başına AĞIRLIK (Adet), sayım değil.
func (s *Store) oracleGroupOccurrences(ctx context.Context, g *ExceptionGroup) ([]OccurrencePoint, error) {
	src, err := s.oracleGroupSource(ctx, g)
	if err != nil {
		return nil, err
	}
	if src == "" {
		return []OccurrencePoint{}, nil
	}
	fromNs, toNs := oracleOccWindow(g.FirstSeen, g.LastSeen)
	rows, err := s.conn.Query(ctx, oracleGroupOccSQL(), src, time.Unix(0, fromNs).UTC(), time.Unix(0, toNs).UTC(),
		strings.TrimSpace(g.Message), strings.TrimSpace(g.Type))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[int64]uint64{}
	for rows.Next() {
		var bucket time.Time
		var c uint64
		if err := rows.Scan(&bucket, &c); err != nil {
			return nil, err
		}
		counts[bucket.UnixNano()] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillOccurrenceBuckets(fromNs, toNs, oracleGroupOccStep, counts), nil
}
