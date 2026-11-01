package chstore

// problem_bulk.go — Inbox toplu kapat / kalıcı sil (v0.10.1777, operatör:
// "eski problemleri de silebilmek isterim" → "ikisi de"). Kapat: RMT tam
// satır yeniden yazımı (MarkResolved, değer korunur). Sil: ALTER DELETE
// mutasyonu (dashboard.go deseni); aynı imza yeniden oluşursa yeni kayıt
// olarak döner — geri alınamaz, çağıran onay + audit düşer.

import (
	"context"
	"time"
)

// ResolveProblems — seçili açık/onaylı problemler resolved; döner: kapanan sayı.
func (s *Store) ResolveProblems(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	all, err := s.ListProblems(ctx, ProblemFilter{Limit: 1000})
	if err != nil {
		return 0, err
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	now := time.Now().UnixNano()
	n := 0
	for i := range all {
		p := all[i]
		if !want[p.ID] || p.Status == "resolved" {
			continue
		}
		MarkResolved(&p, now)
		if err := s.UpsertProblem(ctx, p); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// DeleteProblems — kalıcı; problem_verdicts dokunulmaz (imza bazlı, ayrı karar).
func (s *Store) DeleteProblems(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.conn.Exec(ctx, `ALTER TABLE problems DELETE WHERE id IN (?)`, ids)
}

// DeleteExceptionGroups — kalıcı (grup satırı; oluşum span'leri kalır).
func (s *Store) DeleteExceptionGroups(ctx context.Context, fps []string) error {
	if len(fps) == 0 {
		return nil
	}
	return s.conn.Exec(ctx, `ALTER TABLE exception_groups DELETE WHERE fingerprint IN (?)`, fps)
}
