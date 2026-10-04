package chstore

// promoted_source_event.go — v0.10.1106: terfi Problem'inin (`anomaly-auto:`)
// detay sayfası kaynak olayın "Desen sayısı" grafiğini çizer.
//
// v0.10.1060 grafiği yalnız anomali OLAYI detayına koydu ve kararında
// ("Terfi Problem'i (`anomaly-auto:`) detayına taşınmadı: kaynak olayı ayrıca
// okumak gerekir") bunu erteledi. Operatör şimdi istiyor: Problem detayında
// artışın ne zaman başladığı, kaynak olayı açmadan görünsün.
//
// Bu dosya o "ayrıca okuma"yı taşır — TEK olay, kimlikle, yalnız grafiğin
// okuduğu kolonlar (id, kind, pattern, service, started_at, last_seen,
// türetilmiş status, varsa verified_ratio). sample / oranlar / bölüm kolonları
// okunmaz: grafik onları kullanmaz ve sahte alan taşımayalım diye tip de
// AnomalyEvent değil, dar bir özet (PromotedSourceEvent).
//
// Olay kimliği SORGUSUZ çözülür: PromotedAnomalyEventID (v0.10.1054) hem
// saklanan rule_id'yi hem Problem kimliğini (`anomaly-auto:<fp>:<servis>`)
// kabul eder; problems tablosu okunmaz.
//
// Sınır: anomaly_events küçük bir state tablosu (ORDER BY id, 30 gün TTL,
// partition yok — v0.9.1335); okuma PK eşitliği + FINAL + ORDER BY last_seen
// DESC LIMIT 1 (0010'u uygulamamış kurulumda FINAL'in döndürebileceği bayat
// kopya yerine en yeni sürüm — foldAnomalyCarryRow ile aynı seçim) +
// max_execution_time = 2. Zaman sınırı EKLENMEDİ: GetAnomalyEvent ve
// promotedSourceSQL emsali; spans/metric_points kuralı bu tabloya değil, ve
// FINAL altında PK dışı kolona süzgeç bilinçli olarak yeni bir şey sınırlamaz
// (TTL zaten 30 gün).

import (
	"context"
	"time"
)

// PromotedSourceEvent — terfi Problem'inin kaynak olayının grafik özeti.
// Alan adları ve anlamları AnomalyEvent ile birebir (FE: Pick<AnomalyEvent>).
type PromotedSourceEvent struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Pattern   string `json:"pattern"`
	Service   string `json:"service"`
	StartedAt int64  `json:"startedAt"` // unix ns — CURRENT bölümün başlangıcı
	LastSeen  int64  `json:"lastSeen"`  // unix ns
	Status    string `json:"status"`    // "active" | "cleared" (GetAnomalyEvent türetimi)
	// VerifiedRatio — yalnız log_pattern + ES (v0.10.1080); 0 = örneklenmedi.
	VerifiedRatio float64 `json:"verifiedRatio,omitempty"`
}

// promotedSourceEventSQL — PromotedSourceEvent okumasının SAF kurucusu.
// verified: verified_ratio kolonu bu süreçte görüldü mü (hasAnomalyVerifiedCol).
func promotedSourceEventSQL(verified bool) string {
	cols := `id, kind, pattern, service,
		       toUnixTimestamp64Nano(started_at), toUnixTimestamp64Nano(last_seen),
		       if(last_seen >= now64() - INTERVAL ? SECOND, 'active', 'cleared') AS status`
	if verified {
		cols += `,
		       verified_ratio`
	}
	return `SELECT ` + cols + `
		FROM anomaly_events FINAL
		WHERE id = ?
		ORDER BY last_seen DESC
		LIMIT 1
		SETTINGS max_execution_time = 2`
}

// GetPromotedSourceEvent — kimliği verilen anomali olayının grafik özeti; TEK
// sınırlı okuma. Satır yok (TTL ile düşmüş / hiç yazılmamış) → (nil, nil).
// Boş kimlik → okuma yok, (nil, nil). Hata → (nil, err): uç hatayı döner
// (önbelleğe boş cevap yazılmaz), FE grafik bölümünü çizmez — sayfanın geri
// kalanı etkilenmez.
func (s *Store) GetPromotedSourceEvent(ctx context.Context, eventID string) (*PromotedSourceEvent, error) {
	if eventID == "" {
		return nil, nil
	}
	vr := s.hasAnomalyVerifiedCol.Load()
	rows, err := s.conn.Query(ctx, promotedSourceEventSQL(vr), int64(anomalyActiveAge/time.Second), eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var e PromotedSourceEvent
	dst := []any{&e.ID, &e.Kind, &e.Pattern, &e.Service, &e.StartedAt, &e.LastSeen, &e.Status}
	if vr {
		dst = append(dst, &e.VerifiedRatio)
	}
	if err := rows.Scan(dst...); err != nil {
		return nil, err
	}
	return &e, rows.Err()
}
