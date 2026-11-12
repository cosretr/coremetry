package chstore

// dashboard_editors.go — v0.10.1996 (Dashboards Faz 3 dilim 3.2; audit
// §5.2 "Sahip", operatör kararı S7: "Son düzenleyen audit_log'dan türetilir,
// kolon eklenmez").
//
// Panolar paylaşılan (sahip kolonu yok). Kim son dokundu sorusunun cevabı
// zaten audit_log'da: dashboard.create / update / panels_append /
// create_from_chat satırları. audit_log ORDER BY (time, id) — zaman sınırlı
// WHERE birincil anahtarı kullanır; pencere tablonun kendi TTL'i (365 gün).
// Boot tohumu (hazır panolar) audit yazmaz → o panolar listede "—" kalır.
//
// NEDEN dashboards'a kolon DEĞİL: şema değişikliği + dağıtık kurulumda
// ALTER; audit_log bilgiyi zaten taşıyor (operatör S7).

import (
	"context"
	"time"
)

// DashboardEditor — panonun son yazma kaydı.
type DashboardEditor struct {
	DashboardID string `json:"dashboardId"`
	Actor       string `json:"actor"` // actor_email; boşsa actor_id
	At          int64  `json:"at"`    // unix ns
}

// DashboardEditActions — "düzenleme" sayılan audit eylemleri. Silme hariç:
// silinen pano listede yok.
var DashboardEditActions = []string{
	"dashboard.create", "dashboard.update",
	"dashboard.panels_append", "dashboard.create_from_chat",
}

// dashboardEditorsWindow — audit_log TTL'i (store.go: INTERVAL 365 DAY).
const dashboardEditorsWindow = 365 * 24 * time.Hour

// DashboardLastEditors — pano başına son düzenleyen. Pano sayısı küçük
// (yüzler); LIMIT savunma amaçlı.
func (s *Store) DashboardLastEditors(ctx context.Context) ([]DashboardEditor, error) {
	since := time.Now().Add(-dashboardEditorsWindow)
	rows, err := s.conn.Query(ctx, `
		SELECT target_id,
		       argMax(if(actor_email != '', actor_email, actor_id), time),
		       toUnixTimestamp64Nano(max(time))
		FROM audit_log
		WHERE time >= ?
		  AND target_kind = 'dashboard'
		  AND action IN (?)
		  AND target_id != ''
		GROUP BY target_id
		LIMIT 10000
		SETTINGS max_execution_time = 10`, since, DashboardEditActions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DashboardEditor{}
	for rows.Next() {
		var e DashboardEditor
		if err := rows.Scan(&e.DashboardID, &e.Actor, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
