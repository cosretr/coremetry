package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

// downCache — Redis kesintisi: her okuma/yazma hata döner.
type downCache struct{ fakeCache }

var errRedisDown = errors.New("redis: connection refused")

func (*downCache) Get(context.Context, string) ([]byte, bool, error) { return nil, false, errRedisDown }
func (*downCache) SetNX(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errRedisDown
}

// v0.10.2226 (Redis R0, G6): Redis hatası hakkı kaybetmek sayılmaz (hesap
// bu replikada, 45 sn bekleme yok) ve okuma hatası miss'ten ayrılır (istek
// yolu yerel kopyaya düşer).
func TestClusterFleetRedisDownDoesNotWait(t *testing.T) {
	s := &Server{cache: &downCache{}}
	if !s.clusterFleetClaim(context.Background(), "d") {
		t.Fatal("Redis hatasında hak bu replikada sayılmalı (B sınıfı: hata = işi yap)")
	}
	if snap, redisErr := s.clusterFleetLoad(context.Background(), "d"); snap != nil || !redisErr {
		t.Fatalf("okuma hatası redisErr=true dönmeli: snap=%v err=%v", snap, redisErr)
	}
	ok := &Server{cache: &fakeCache{}}
	if _, redisErr := ok.clusterFleetLoad(context.Background(), "d"); redisErr {
		t.Fatal("düz miss Redis hatası sayılmamalı")
	}

	m := &clusterFleetMemo{}
	m.setLocal(&clusterFleetSnapshot{Digest: "d"})
	if m.localSnap("d") == nil {
		t.Fatal("yerel kopya Redis kipinde de tutulmalı")
	}
}
