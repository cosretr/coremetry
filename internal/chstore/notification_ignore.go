package chstore

// notification_ignore.go — v0.10.749: bildirim bağlantısından "Sustur"
// işareti. DDL YOK: işaret notification_log'a bir satır (channel_kind
// 'ignore', channel_name = kim, target = IP, related_id = problem/
// incident id). /events günlüğünde de görünür — kim, ne zaman.
//
// Susturulmuş kimlik için SendProblemAlert HİÇ fan-out yapmaz (ekip
// maili, kanallar, çözüm dahil). Sorgu 30 günle sınırlı (ay partition'ı
// + max_execution_time): bir problem/incident bundan uzun yaşamaz.

import (
	"context"
	"fmt"
)

// NotificationIgnoreKind — notification_log.channel_kind değeri.
const NotificationIgnoreKind = "ignore"

// MarkNotificationIgnored — kind ∈ {problem, incident}; who = jeton
// kimliği ya da oturum e-postası; ip = tıklayan.
func (s *Store) MarkNotificationIgnored(ctx context.Context, kind, id, who, ip string) error {
	if kind == "" || id == "" {
		return fmt.Errorf("notification ignore: kind and id required")
	}
	return s.InsertNotificationLog(ctx, NotificationLog{
		ChannelKind: NotificationIgnoreKind,
		ChannelName: who,
		Target:      ip,
		Subject:     "ignored via notification link",
		RelatedKind: kind,
		RelatedID:   id,
		OK:          true,
	})
}

// NotificationIgnored — bu kimlik son 30 günde susturulmuş mu.
func (s *Store) NotificationIgnored(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	row := s.conn.QueryRow(ctx, `
		SELECT count()
		FROM notification_log
		WHERE related_id = ? AND channel_kind = 'ignore'
		  AND sent_at >= now64(9) - INTERVAL 30 DAY
		SETTINGS max_execution_time = 2`, id)
	var n uint64
	if err := row.Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
