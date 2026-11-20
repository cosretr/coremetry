package chstore

// sre_investigation.go — v0.10.1282 (AI SRE Faz 0a: sre rolü, kuyruk, drain,
// durum). AI SRE ajanının araştırma kaydı = kuyruk satırı.
//
// Yeni tablo YOK (invariant 5): saved_views(page='sre-investigation'),
// id = "sre-" + problemId, owner_id = '' (sistem satırı), name = durum
// (queued|running|done|partial|shared|error|skipped), query_string = kayıt
// JSON'u (≤ 64 KB; kodlayıcı internal/sre). created_at = kuyruğa giriş anı ve
// her yazımda TAŞINIR — kuyruk sırası (FIFO) buradan okunur.
//
// Neden root_cause_hypotheses / problems kolonu DEĞİL: ikisinin de tek yazıcısı
// var (sentezleyici / dedektörler, /aiops §6); ajan yalnız OKUR. Neden Redis
// listesi DEĞİL: kuyruk sre pod'u yokken de (Deployment kapalı, pod düşmüş)
// kalıcı olmalı ve durum ekranı onu sayabilmeli.
//
// Güven: sayfa sistem sayfası (systemSavedViewPages) — genel /api/views ucu
// yazamaz. Okuma ayrıca SQL'de owner_id = '' + "sre-" öneki ister; kodlayıcı
// gövdedeki problemId'nin kimlikle eşleştiğini doğrular (problem_verdict
// emsali, v0.10.1024).

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// SREInvestigationPage — saved_views page değeri.
	SREInvestigationPage = "sre-investigation"
	// SREInvestigationIDPrefix — satır kimliği öneki ("sre-" + problemId).
	SREInvestigationIDPrefix = "sre-"
	// SREInvestigationMaxBytes — query_string tavanı (kayıt JSON'u).
	SREInvestigationMaxBytes = 64 << 10
	// SREInvestigationListMax — liste okuma tavanı.
	SREInvestigationListMax = 200
	// SREInvestigationRetention — süpürücü bundan eski satırları siler.
	SREInvestigationRetention = 30 * 24 * time.Hour
)

// SREInvestigationID — problemId → satır kimliği.
func SREInvestigationID(problemID string) string { return SREInvestigationIDPrefix + problemID }

// listSREInvestigationsSQL — ListSREInvestigations okuması (testte pinli).
// FINAL (ReplacingMergeTree), sayfa + sistem sahibi + önek SQL'de, durum
// kümesi IN, en eski önce, LIMIT ≤ 200, max_execution_time 5.
const listSREInvestigationsSQL = `
		SELECT id, owner_id, name, page, query_string, pinned, created_at
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND startsWith(id, '` + SREInvestigationIDPrefix + `') AND name IN ?
		ORDER BY created_at ASC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// countSREQueuedSQL — kuyruk derinliği (durum ekranı + MaxQueue kapısı).
const countSREQueuedSQL = `
		SELECT count()
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND startsWith(id, '` + SREInvestigationIDPrefix + `') AND name = 'queued'
		SETTINGS max_execution_time = 5`

// clampSREListLimit — SAF: 1..SREInvestigationListMax (0/negatif → tavan).
func clampSREListLimit(limit int) int {
	if limit <= 0 || limit > SREInvestigationListMax {
		return SREInvestigationListMax
	}
	return limit
}

// ListSREInvestigations — verilen durumlardaki araştırma satırları, en eski
// önce. Boş durum listesi = okuma yok.
//
// Drain poll'u: nesil sayaçlı önbellekten (ctlcache.go; anahtar = durum
// kümesi + limit), miss FINAL ile CH'den. saved_views'ın her yazımı
// (UpsertSavedView, iki süpürme) nesli artırır.
func (s *Store) ListSREInvestigations(ctx context.Context, states []string, limit int) ([]SavedView, error) {
	if len(states) == 0 {
		return []SavedView{}, nil
	}
	lim := clampSREListLimit(limit)
	key := "sre-inv:" + strings.Join(states, "\x00") + ":" + strconv.Itoa(lim)
	v, err := ctlLoad(ctx, s, ctlSavedViews, key, func(ctx context.Context) ([]SavedView, error) {
		return s.listSREInvestigationsCH(ctx, states, lim)
	})
	if err != nil {
		return nil, err
	}
	return slices.Clone(v), nil
}

func (s *Store) listSREInvestigationsCH(ctx context.Context, states []string, lim int) ([]SavedView, error) {
	rows, err := s.conn.Query(ctx, listSREInvestigationsSQL, SREInvestigationPage, states, lim)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedView{}
	for rows.Next() {
		var v SavedView
		var pinned uint8
		var createdAt time.Time
		if err := rows.Scan(&v.ID, &v.OwnerID, &v.Name, &v.Page, &v.QueryString, &pinned, &createdAt); err != nil {
			return nil, err
		}
		v.Pinned = pinned == 1
		v.CreatedAt = createdAt.UnixNano()
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountSREQueued — kuyrukta bekleyen araştırma sayısı.
func (s *Store) CountSREQueued(ctx context.Context) (int, error) {
	var n uint64
	if err := s.conn.QueryRow(ctx, countSREQueuedSQL, SREInvestigationPage).Scan(&n); err != nil {
		return 0, err
	}
	return int(n), nil
}

// GetSREInvestigation — problemin araştırma satırı; yoksa ya da satır bir
// sistem satırı değilse (nil, nil).
func (s *Store) GetSREInvestigation(ctx context.Context, problemID string) (*SavedView, error) {
	v, err := s.GetSavedView(ctx, SREInvestigationID(problemID))
	if err != nil || v == nil {
		return nil, err
	}
	if v.Page != SREInvestigationPage || v.OwnerID != "" || v.Name == "" {
		return nil, nil
	}
	return v, nil
}

// PutSREInvestigation — araştırma satırını yazar (aynı problem → aynı satır).
// state name kolonuna, body query_string'e gider; createdAt kuyruk sırasıdır
// ve çağıran onu her yazımda TAŞIR. Gövde tavanı aşarsa yazım reddedilir —
// kodlayıcı (internal/sre) tavana sığdırmaktan sorumlu.
func (s *Store) PutSREInvestigation(ctx context.Context, problemID, state, body string, createdAt int64) error {
	if problemID == "" || strings.TrimSpace(state) == "" {
		return fmt.Errorf("sre investigation: problemId ve state zorunlu")
	}
	if len(body) > SREInvestigationMaxBytes {
		return fmt.Errorf("sre investigation: gövde %d B > tavan %d B", len(body), SREInvestigationMaxBytes)
	}
	return s.UpsertSavedView(ctx, SavedView{ID: SREInvestigationID(problemID), Name: state,
		Page: SREInvestigationPage, QueryString: body, CreatedAt: createdAt})
}

const sreInvestigationSweepCountSQL = `
		SELECT count()
		FROM saved_views FINAL
		WHERE page = ? AND version < ?
		SETTINGS max_execution_time = 10`

const sreInvestigationSweepDeleteSQL = `ALTER TABLE saved_views DELETE WHERE page = ? AND version < ?`

// SweepSREInvestigations — son yazımı cutoff'tan eski araştırma satırlarını
// sayar ve siler (StartAIChatSweeper'ın ikinci kolu). 0 ise mutasyon yok.
func (s *Store) SweepSREInvestigations(ctx context.Context, cutoff time.Time) (uint64, error) {
	cutNs := uint64(cutoff.UnixNano())
	var n uint64
	if err := s.conn.QueryRow(ctx, sreInvestigationSweepCountSQL, SREInvestigationPage, cutNs).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	if err := s.conn.Exec(ctx, sreInvestigationSweepDeleteSQL, SREInvestigationPage, cutNs); err != nil {
		return 0, err
	}
	s.ctlBump(ctx, ctlSavedViews)
	return n, nil
}
