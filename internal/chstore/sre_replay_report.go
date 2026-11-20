package chstore

// sre_replay_report.go — CoSRE doğruluk ölçümü A2: replay
// notlama raporları. Yeni tablo YOK (invariant 5): saved_views sistem satırı
//
//	page='sre-replay-report', id='sre-replay-report-<reportId>', owner_id='',
//	name = rapor durumu (running | done), query_string = rapor JSON'u (≤96 KB:
//	≤30 Problem × ≤3 koşu × özet sonuç + not).
//
// Tekrar işlerinin kendisi değerlendirmeyle AYNI kuyruk satırlarıdır
// (sre_eval.go, "sre-replay-<runKey>-<problemId>"); bu dosya yalnız rapor
// satırını okur/yazar. Yazıcılar: POST /api/sre/replay/reports (admin,
// denetimli) ve sre pod'undaki notlayıcı tik (sre.StartReplayGrader).

import (
	"context"
	"fmt"
	"strings"
)

const (
	// SREReplayReportPage — rapor satırlarının saved_views page değeri.
	SREReplayReportPage = "sre-replay-report"
	// SREReplayReportIDPrefix — satır kimliği öneki.
	SREReplayReportIDPrefix = "sre-replay-report-"
	// SREReplayReportMaxBytes — gövde tavanı.
	SREReplayReportMaxBytes = 96 << 10
	// SREReplayReportListMax — liste okuma tavanı.
	SREReplayReportListMax = 50
)

// SREReplayReportID — reportId → satır kimliği.
func SREReplayReportID(id string) string { return SREReplayReportIDPrefix + id }

// listSREReplayReportsSQL — en yeni önce, ≤50.
const listSREReplayReportsSQL = `
		SELECT id, owner_id, name, page, query_string, pinned, created_at
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND startsWith(id, '` + SREReplayReportIDPrefix + `') AND name != ''
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// ListSREReplayReports — raporlar, en yeni önce (≤50).
func (s *Store) ListSREReplayReports(ctx context.Context, limit int) ([]SavedView, error) {
	if limit <= 0 || limit > SREReplayReportListMax {
		limit = SREReplayReportListMax
	}
	return s.querySavedViews(ctx, listSREReplayReportsSQL, SREReplayReportPage, limit)
}

// GetSREReplayReport — rapor satırı; yoksa ya da sistem satırı değilse (nil, nil).
func (s *Store) GetSREReplayReport(ctx context.Context, id string) (*SavedView, error) {
	if id == "" {
		return nil, nil
	}
	v, err := s.GetSavedView(ctx, SREReplayReportID(id))
	if err != nil || v == nil {
		return nil, err
	}
	if v.Page != SREReplayReportPage || v.OwnerID != "" || v.Name == "" {
		return nil, nil
	}
	return v, nil
}

// PutSREReplayReport — rapor satırını yazar (tam satır; createdAt taşınır).
func (s *Store) PutSREReplayReport(ctx context.Context, id, state, body string, createdAt int64) error {
	if id == "" || strings.TrimSpace(state) == "" {
		return fmt.Errorf("sre replay: id ve state zorunlu")
	}
	if len(body) > SREReplayReportMaxBytes {
		return fmt.Errorf("sre replay: gövde %d B > tavan %d B", len(body), SREReplayReportMaxBytes)
	}
	return s.UpsertSavedView(ctx, SavedView{ID: SREReplayReportID(id), Name: state,
		Page: SREReplayReportPage, QueryString: body, CreatedAt: createdAt})
}
