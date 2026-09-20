package chstore

// v0.10.810 — operatör hatası (test ortamı, 2×2 küme): Traces listesinde
// duran, 1 saat önce başlamış bir trace açılınca "Trace aged out of raw
// spans" kartı çıkıyordu — raw TTL 7 gün, Tempo kapalı, örnekleme yok.
// Mekanik: liste MV'yi bir replikadan okudu, detay ham span'ı Distributed
// üzerinden BAŞKA bir replikadan (load_balancing=random) — replikalar aynı
// veriyi taşımıyorsa 0 satır; stub MV'de olunca kart "yaşlandı" dedi.
//
// İki düzeltme: (1) TraceAgedOut — "yaşlandı" YALNIZ başlangıç raw TTL'i
// aşınca; (2) GetTraceAllReplicas — küme kipinde, MV stub'ının penceresiyle
// SINIRLI, clusterAllReplicas(spans_local) okuması: ıraksak replikaların
// birleşimi, span_id'ye göre tekilleştirilmiş. Yalnız "Distributed 0 döndü
// ve trace TTL içinde" dalında koşar (her ıskaya değil) — bedel o istekte.

import (
	"context"
	"fmt"
	"time"
)

// TraceAgedOut — SAF: trace başlangıcı raw spans TTL'inin dışında mı.
// retention "30d" / "48h" (parseRetentionDays, saat yukarı yuvarlanır);
// çözülemezse defaultDays; o da yoksa 30. Sınırda (tam TTL yaşında) "yaşlı"
// sayılır — TTL toDate(time)+N DAY ile partisyon grenli düşer, eşik sonrası
// yokluk beklenen durumdur; eşik öncesi yokluk ıraksama sinyalidir.
func TraceAgedOut(startNs int64, now time.Time, spansRetention string, defaultDays int) bool {
	if startNs <= 0 {
		return false // başlangıç bilinmiyor → yaşlı deme, ıraksamayı araştır
	}
	days := defaultDays
	if d, err := parseRetentionDays(spansRetention); err == nil && d > 0 {
		days = d
	}
	if days <= 0 {
		days = 30
	}
	cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
	return time.Unix(0, startNs).Before(cutoff)
}

// DefaultSpansDays — config.yaml retention.spans (gün); Settings override'ı
// GetRetention'dan gelir.
func (s *Store) DefaultSpansDays() int { return s.ret.SpansDays }

// traceAllReplicasSQL — SAF: pencereli, tavanlı, erişilemeyen shard'ı
// atlayan tüm-replika okuması (tablo yerel parça, Distributed değil).
//
// v0.10.826 — operatör hatası (test kümesi): tablo argümanı ÇIPLAK
// `spans_local` yazılmıştı ve ClickHouse her çağrıyı reddediyordu:
// "Table name was not found in function arguments" (kod 42). Tablo
// fonksiyonu KULLANICI tablosunu veritabanıyla nitelenmiş ister;
// system.* adları veritabanını kendi taşıdığı için kardeş okumalar
// çalışıyordu. Sonuç: yedek okuma kümede HİÇ koşmadı — başlatıcının
// replikasında bulunmayan her trace "replica_miss / aged-out" kartına
// düştü. Aynı şekil v0.10.823'te ham sayıma kopyalanmıştı
// (trace_health_raw.go); ikisi de bu sürümde nitelendi ve
// cluster_all_replicas_args_test.go ile kaynaktan pinlendi.
//
// currentDatabase() BİLEREK yok (replica_consistency.go disiplini):
// tablo fonksiyonunun argümanı başlatan düğümde çözülür, uzak düğümde
// başka bir veritabanına işaret edebilir. Yapılandırılmış ad chObjRe
// ile doğrulanıp backtick'le nitelenir — doğrulama düşerse SQL hiç
// kurulmaz, ad tırnaksız eklenmez.
func traceAllReplicasSQL(cluster, db string) (string, error) {
	if !chObjRe.MatchString(db) {
		return "", fmt.Errorf("geçersiz veritabanı adı %q — clusterAllReplicas argümanına eklenmedi", db)
	}
	return `SELECT ` + traceSpanCols + `
		FROM clusterAllReplicas('` + cluster + "', `" + db + "`.`spans_local`" + `)
		WHERE trace_id = ? AND time >= ? AND time <= ?
		ORDER BY time ASC
		LIMIT 50000
		SETTINGS max_execution_time = 20, skip_unavailable_shards = 1`, nil
}

// traceReplicaWindow — SAF: stub penceresi ± 60 sn (span time'ı MV kova
// sınırının biraz dışına taşabilir); end < start ise start + 60 sn.
func traceReplicaWindow(startNs, endNs int64) (lo, hi time.Time) {
	const slack = time.Minute
	st := time.Unix(0, startNs)
	en := time.Unix(0, endNs)
	if endNs < startNs {
		en = st
	}
	return st.Add(-slack), en.Add(slack)
}

// GetTraceAllReplicas — küme kipi dışında (ClusterName boş) nil,nil.
func (s *Store) GetTraceAllReplicas(ctx context.Context, traceID string, startNs, endNs int64) ([]SpanRow, error) {
	if s.cfg.ClusterName == "" || traceID == "" {
		return nil, nil
	}
	q, qerr := traceAllReplicasSQL(s.cfg.ClusterName, s.cfg.Database)
	if qerr != nil {
		return nil, fmt.Errorf("trace all-replica read: %w", qerr)
	}
	lo, hi := traceReplicaWindow(startNs, endNs)
	rows, err := s.telemetryReadConn().Query(ctx, q,
		traceID, chDateTime64Arg(lo), chDateTime64Arg(hi))
	if err != nil {
		return nil, fmt.Errorf("trace all-replica read: %w", err)
	}
	defer rows.Close()
	out, err := scanSpanRows(rows)
	if err != nil {
		return nil, err
	}
	return dedupSpanRows(out), nil
}

// dedupSpanRows — SAF: aynı span_id birden çok replikada (kısmen replike
// olmuş) → ilk görülen kalır; sıra korunur.
func dedupSpanRows(in []SpanRow) []SpanRow {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]SpanRow, 0, len(in))
	for _, sp := range in {
		key := sp.SpanID
		if key == "" {
			key = sp.TraceID + "\x00" + sp.Name + "\x00" + fmt.Sprint(sp.StartTime)
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, sp)
	}
	return out
}
