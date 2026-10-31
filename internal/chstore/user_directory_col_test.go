package chstore

// v0.10.1741 (operator-reported) — users.directory probe'u false kalınca
// UpsertUser dizini düşürüyordu; istek üzerine yeniden probe dakikada en çok
// bir kez koşar (her detay açılışı CH'ye sorgu atmasın). Ayrıca bayrak açıkken
// INSERT / SELECT kolonu taşır.

import (
	"strings"
	"testing"
	"time"
)

func TestReprobeDue(t *testing.T) {
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC).UnixNano()
	cases := []struct {
		name      string
		last, now int64
		every     time.Duration
		want      bool
	}{
		{"hiç denenmedi", 0, base, time.Minute, true},
		{"30 sn önce", base - int64(30*time.Second), base, time.Minute, false},
		{"tam 1 dk", base - int64(time.Minute), base, time.Minute, true},
		{"5 dk önce", base - int64(5*time.Minute), base, time.Minute, true},
	}
	for _, c := range cases {
		if got := reprobeDue(c.last, c.now, c.every); got != c.want {
			t.Errorf("%s: reprobeDue = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUserColsCarryDirectoryWhenFlagOn(t *testing.T) {
	s := &Store{}
	if strings.Contains(userInsertColumns(s.userCols()), "directory") {
		t.Fatal("bayrak kapalıyken kolon listede olmamalı")
	}
	s.hasDirectoryCol.Store(true)
	if !strings.Contains(userInsertColumns(s.userCols()), "directory") || !strings.Contains(userSelectExprFor(s.userCols()), "directory") {
		t.Fatal("bayrak açıkken INSERT ve SELECT directory taşımalı")
	}
	if !s.HasUserDirectoryCol() || !s.ReprobeUserDirectoryCol(t.Context()) {
		t.Fatal("açık bayrak yeniden probe sorgusuz true dönmeli")
	}
}
