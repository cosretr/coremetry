package copilot

// v0.10.1316 — kayıt yüzeyi öneki: işaretsiz ctx'te yüzey aynen (üretim),
// işaretli ctx'te önek bir kez eklenir; RecordUsage / RecordExchange satırı
// öneki taşır, profil yönlendirmesi etiketi okumaya devam eder.

import (
	"context"
	"testing"
	"time"
)

type recSurfaceRec struct{ ch chan CallRecord }

func (r *recSurfaceRec) RecordCall(_ context.Context, c CallRecord) { r.ch <- c }

func TestRecordSurfacePrefix(t *testing.T) {
	bg := context.Background()
	if WithRecordSurfacePrefix(bg, "") != bg {
		t.Fatal("boş önek ctx'i değiştirmemeli")
	}
	for _, tc := range []struct {
		ctx  context.Context
		in   string
		want string
	}{
		{bg, "chat-guided", "chat-guided"},
		{WithRecordSurfacePrefix(bg, "evalset-"), "chat-guided", "evalset-chat-guided"},
		{WithRecordSurfacePrefix(bg, "evalset-"), "evalset-ChatLoop", "evalset-ChatLoop"},
		{nil, "chat", "chat"},
	} {
		if got := recordSurface(tc.ctx, tc.in); got != tc.want {
			t.Errorf("recordSurface(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	s := New(ProviderOpenAI, "k", "m")
	rec := &recSurfaceRec{ch: make(chan CallRecord, 4)}
	s.SetRecorder(rec)
	ctx := WithMeta(WithRecordSurfacePrefix(bg, "evalset-"), CallMeta{Surface: "chat-intent-none", ExchangeID: "ev/1:chat-intent-none"})
	s.RecordUsage(ctx, time.Now(), 1, 1, 0, "ok", "", "q", "a")
	s.RecordExchange(WithMeta(WithRecordSurfacePrefix(bg, "evalset-"), CallMeta{Surface: "cosre-turn:loop"}), time.Now(), "ok", "", "q", "a")
	for i := 0; i < 2; i++ {
		select {
		case r := <-rec.ch:
			if r.Surface != "evalset-chat-intent-none" && r.Surface != "evalset-cosre-turn:loop" {
				t.Errorf("kayıt yüzeyi önek taşımıyor: %q", r.Surface)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("kayıt gelmedi")
		}
	}
}

// v0.10.2134 (router boşlukları D1) — RecordAs yalnız KAYIT etiketini değiştirir:
// profil, çağrının gerçekte çözüldüğü profil olarak sabitlenir (etiket haritada
// yok → varsayılana düşüp satıra yanlış modeli yazmasın); ctx'te zaten
// sabitlenmiş profil ezilmez; boş etiket ctx'i aynen döndürür.
func TestRecordAsPinsResolvedProfile(t *testing.T) {
	s := New("openai", "kd", "default-model")
	s.SetProfiles([]ModelProfile{
		{ID: "big", Provider: ProviderOpenAI, BaseURL: "http://big/v1", APIKey: "kb", Model: "big-model"},
		{ID: "loop", Provider: ProviderOpenAI, BaseURL: "http://loop/v1", APIKey: "kl", Model: "loop-model"},
	}, "big", map[string]string{"chat": "loop"})
	bg := WithMeta(context.Background(), CallMeta{Surface: "chat", ExchangeID: "x1"})

	got := s.RecordAs(bg, "chat-prefetch")
	if m := MetaFromContext(got); m.Surface != "chat-prefetch" || m.ExchangeID != "x1" {
		t.Errorf("meta = %+v", m)
	}
	if id := s.profileID(got); id != "loop" {
		t.Errorf("profil %q, want loop (döngünün profili sabitlenmeli)", id)
	}
	if id := s.profileID(s.RecordAs(WithProfile(bg, "big"), "chat-prefetch")); id != "big" {
		t.Errorf("ctx profili ezildi: %q", id)
	}
	if s.RecordAs(bg, "") != bg {
		t.Error("boş etiket ctx'i değiştirmemeli")
	}
	var nilSvc *Service
	if m := MetaFromContext(nilSvc.RecordAs(bg, "chat-prefetch")); m.Surface != "chat-prefetch" {
		t.Errorf("nil servis etiket: %q", m.Surface)
	}
}
