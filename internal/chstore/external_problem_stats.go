package chstore

// external_problem_stats.go — v0.10.998 — bir dış kaynağın (Oracle hata
// tablosu) Problem AÇILIŞ sayımı: "gölgeden canlıya geçersem ne olur"
// sorusunun verisi (Ayarlar › Oracle › canlıya geçiş önizlemesi; operatör
// kararı 2026-10-01 — kaynak kipi shadow → live).
//
// Canlı kipte dış tarayıcı bildirimi YALNIZ açılışta gönderir
// (anomaly/external.go: seri açılışı, küme açılışı). Gölgede aynı satırlar
// bildirimsiz açılıyor; yani geçmişteki açılış sayısı, canlıda gidecek
// bildirim sayısının doğrudan ölçüsüdür.
//
// Kaynak tablo `problems` (durum tablosu, FINAL): seri Problem'inin kimliği
// her açılışta YENİDİR (anomaly newID) → açılışlar sayılabilir. Küme
// Problem'inin kimliği anahtara sabittir (clusterProblemID) → yeniden açılış
// aynı satırı ezer; küme sayısı bu yüzden ALT SINIRdır ve öyle sunulur.

import (
	"context"
	"time"
)

// ExternalProblemTop — pencerede en çok Problem açılan özne.
type ExternalProblemTop struct {
	Subject string `json:"subject"` // problems.service: gerçek servis ya da ext:<kaynak>/<değerler>
	Opened  uint64 `json:"opened"`
}

// ExternalProblemStats — bir kaynağın seri + küme Problem sayımı.
type ExternalProblemStats struct {
	Opened24h  uint64 `json:"opened24h"`  // son 24 saatte açılan (seri + küme)
	Opened7d   uint64 `json:"opened7d"`   // son 7 günde açılan
	Critical7d uint64 `json:"critical7d"` // bunların kritik olanı
	Clusters7d uint64 `json:"clusters7d"` // küme satırı (alt sınır — dosya başı)
	OpenNow    uint64 `json:"openNow"`    // şu an çözülmemiş
	// Top — 7 günde en çok açılan özneler (≤ externalProblemTopN).
	Top []ExternalProblemTop `json:"top"`
}

const externalProblemTopN = 5

// ExternalRulePrefixes — SAF: kaynağın seri ve küme kural önekleri.
// Seri kuralı "anomaly:" + ExternalSubject + ":" + metrik, yani
// "anomaly:ext:<kaynak>/<v1>/…:<metrik>"; küme "anomaly-cluster:ext:<kaynak>/<ilk
// boyut>". Sondaki "/" bilinçli: "ora" kaynağı "ora2"nin satırlarını saymaz.
func ExternalRulePrefixes(source string) (series, cluster string) {
	return RuleExtSeriesPrefix + source + "/", RuleExtClusterPrefix + source + "/"
}

// ExternalSourceProblemStats — kaynağın açılış sayımı (dosya başı). İki
// sınırlı okuma; `problems` küçük bir durum tablosudur ama kural öneki
// sıralama anahtarında değil, o yüzden zaman tavanı + LIMIT taşır.
func (s *Store) ExternalSourceProblemStats(ctx context.Context, source string, now time.Time) (ExternalProblemStats, error) {
	out := ExternalProblemStats{Top: []ExternalProblemTop{}}
	series, cluster := ExternalRulePrefixes(source)
	d1, d7 := now.Add(-24*time.Hour).UnixNano(), now.Add(-7*24*time.Hour).UnixNano()
	row := s.conn.QueryRow(ctx, `
		SELECT
			countIf(toUnixTimestamp64Nano(started_at) >= ?),
			countIf(toUnixTimestamp64Nano(started_at) >= ?),
			countIf(toUnixTimestamp64Nano(started_at) >= ? AND severity = 'critical'),
			countIf(toUnixTimestamp64Nano(started_at) >= ? AND startsWith(rule_id, ?)),
			countIf(status != 'resolved')
		FROM problems FINAL
		WHERE (startsWith(rule_id, ?) OR startsWith(rule_id, ?))
		  AND (toUnixTimestamp64Nano(started_at) >= ? OR status != 'resolved')
		SETTINGS max_execution_time = 5`,
		d1, d7, d7, d7, cluster, series, cluster, d7)
	if err := row.Scan(&out.Opened24h, &out.Opened7d, &out.Critical7d, &out.Clusters7d, &out.OpenNow); err != nil {
		return out, err
	}
	rows, err := s.conn.Query(ctx, `
		SELECT service, count() AS c
		FROM problems FINAL
		WHERE (startsWith(rule_id, ?) OR startsWith(rule_id, ?))
		  AND toUnixTimestamp64Nano(started_at) >= ?
		GROUP BY service
		ORDER BY c DESC, service
		LIMIT ?
		SETTINGS max_execution_time = 5`,
		series, cluster, d7, externalProblemTopN)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var t ExternalProblemTop
		if err := rows.Scan(&t.Subject, &t.Opened); err != nil {
			return out, err
		}
		out.Top = append(out.Top, t)
	}
	return out, rows.Err()
}
