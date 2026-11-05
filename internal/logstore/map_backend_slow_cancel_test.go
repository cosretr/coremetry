package logstore

// map_backend_slow_cancel_test.go — v0.10.1822 (Log Search Faz 1 dilim 1.6).
//
// Regresyon: FE her log isteğine iptal sinyali vermeye başladı. İptal edilen
// bir ES çağrısı dial sırasında *net.OpError ile dönebilir; MapBackendSlow
// onu "yavaş/erişilemez" sayıyor, handler 200 {degraded} üretiyor ve
// serveCached bu boş cevabı 15–60 sn saklıyordu — bir sonraki gerçek arama
// "log backend yavaş" görürdü. İstemci iptali artık context.Canceled.

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestMapBackendSlowClientCancel(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel2 := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel2()
	<-expired.Done()
	live := context.Background()
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("operation was canceled")}

	cases := []struct {
		name         string
		err          error
		op, parent   context.Context
		wantCanceled bool
		wantSlow     bool
	}{
		{"istemci iptali + OpError → iptal, yavaş DEĞİL", opErr, canceled, canceled, true, false},
		{"istemci iptali + context.Canceled → iptal", context.Canceled, canceled, canceled, true, false},
		{"kendi zaman aşımımız → yavaş", context.DeadlineExceeded, expired, live, false, true},
		{"bağlantı reddi (canlı istek) → yavaş", opErr, live, live, false, true},
		{"gerçek sorgu hatası → aynen", ErrBadQuery, live, live, false, false},
	}
	for _, c := range cases {
		got := MapBackendSlow(c.err, c.op, c.parent)
		if errors.Is(got, context.Canceled) != c.wantCanceled || errors.Is(got, ErrBackendSlow) != c.wantSlow {
			t.Errorf("%s: got %v (canceled=%v slow=%v)", c.name, got, errors.Is(got, context.Canceled), errors.Is(got, ErrBackendSlow))
		}
	}
}
