package chstore

// user_directory_col.go — v0.10.1741 (operator-reported): kullanıcı detayında
// "AD ile denetle" fark panelinde yeni değerler göründü, kartlar "—" kaldı.
// users.directory (v0.10.1732) küme kipinde ertelenen ALTER ile iner (ddl_defer);
// boot probe'u kolonu göremezse hasDirectoryCol süreç boyunca false kalıyordu
// ve UpsertUser / okumalar kolonu SESSİZCE atlıyordu. İki yeniden deneme:
// ertelenen DDL bitince (reprobePromotedAttrs döngüsü) ve istek üzerine
// (ReprobeUserDirectoryCol, dakikada en çok bir sorgu).

import (
	"context"
	"log"
	"time"
)

// userDirReprobeEvery — istek üzerine yeniden probe aralığı.
const userDirReprobeEvery = time.Minute

// reprobeDue — SAF: son denemeden bu yana aralık doldu mu (0 = hiç denenmedi).
func reprobeDue(lastNs, nowNs int64, every time.Duration) bool {
	return lastNs == 0 || nowNs-lastNs >= int64(every)
}

func (s *Store) probeUserDirectoryCol(ctx context.Context) bool {
	rows, err := s.conn.Query(ctx, `SELECT directory FROM users LIMIT 1 SETTINGS max_execution_time = 3`)
	maybeCloseRows(rows, err)
	return err == nil
}

// reprobeUserDirectoryCol — bayrak false ise kolonu yeniden yoklar; açılırsa
// restart'sız devreye alır. Dönen: bayrağın son hâli.
func (s *Store) reprobeUserDirectoryCol(ctx context.Context) bool {
	if s.hasDirectoryCol.Load() {
		return true
	}
	if s.probeUserDirectoryCol(ctx) && s.hasDirectoryCol.CompareAndSwap(false, true) {
		log.Printf("[chstore] users.directory görüldü — dizin öznitelikleri yazılıyor (restart gerekmedi)")
	}
	return s.hasDirectoryCol.Load()
}

// HasUserDirectoryCol — users.directory okunup yazılıyor mu.
func (s *Store) HasUserDirectoryCol() bool { return s.hasDirectoryCol.Load() }

// ReprobeUserDirectoryCol — istek üzerine yeniden probe (kullanıcı detayı /
// "AD ile denetle" yazmadan önce). Dakikada en çok bir CH sorgusu.
func (s *Store) ReprobeUserDirectoryCol(ctx context.Context) bool {
	if s.hasDirectoryCol.Load() {
		return true
	}
	now := time.Now().UnixNano()
	last := s.dirReprobeAt.Load()
	if !reprobeDue(last, now, userDirReprobeEvery) || !s.dirReprobeAt.CompareAndSwap(last, now) {
		return false
	}
	return s.reprobeUserDirectoryCol(ctx)
}
