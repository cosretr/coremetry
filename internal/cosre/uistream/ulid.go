package uistream

// ulid.go — v0.10.1603 (operatör kararı 2026-10-29 §3): `start` parçasının
// sunucu üretimi messageId'si. Repo'da ULID yardımcısı ve go.mod'da ulid
// kütüphanesi yok; yeni bağımlılık yerine en küçük spec-uyumlu üretici:
// 48 bit milisaniye zaman damgası + 80 bit rastgelelik, Crockford base32,
// 26 karakter, sözlük sırası = zaman sırası.
//
// Monotonluk: aynı milisaniyede (ya da saat geri gittiğinde) önceki
// rastgelelik 1 artırılır ve önceki zaman damgası korunur — aynı üreticiden
// çıkan kimlikler kesin artandır. 80 bitlik taşma (pratikte olanaksız)
// zaman damgasını 1 ms ileri iter; üretici asla hata döndürmez.
//
// Golden'lar sabit saat + sıfır entropi ile deterministik üretir
// (Options.MessageID / NewULIDGenerator enjeksiyonu).

import (
	"crypto/rand"
	"io"
	"sync"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ULIDGenerator — eşzamanlı-güvenli, monotonik ULID üreticisi.
type ULIDGenerator struct {
	mu      sync.Mutex
	now     func() time.Time
	entropy io.Reader
	lastMs  uint64
	last    [10]byte
	has     bool
}

// NewULIDGenerator — now/entropy nil → time.Now / crypto/rand.
func NewULIDGenerator(now func() time.Time, entropy io.Reader) *ULIDGenerator {
	if now == nil {
		now = time.Now
	}
	if entropy == nil {
		entropy = rand.Reader
	}
	return &ULIDGenerator{now: now, entropy: entropy}
}

var defaultULIDs = NewULIDGenerator(nil, nil)

// NewMessageID — süreç-genel monotonik ULID.
func NewMessageID() string { return defaultULIDs.New() }

// New — sıradaki ULID.
func (g *ULIDGenerator) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now().UnixMilli()
	if now < 0 {
		now = 0 // 1970 öncesi saat: maskelenmiş negatif sayı geleceğe sıçrardı
	}
	ms := uint64(now) & (1<<48 - 1)
	if g.has && ms <= g.lastMs {
		ms = g.lastMs
		if !increment(&g.last) {
			ms++ // 80 bit taşması: zamanı ileri it, rastgelelik sıfırdan
		}
	} else {
		var r [10]byte
		if _, err := io.ReadFull(g.entropy, r[:]); err != nil {
			// entropi yok: önceki değerden türet (yine artan, yine geçerli)
			r = g.last
			increment(&r)
		}
		g.last = r
	}
	g.lastMs, g.has = ms, true
	return encodeULID(ms, g.last)
}

// increment — 80 bitlik büyük-endian sayıyı 1 artırır; taşmada false.
func increment(b *[10]byte) bool {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return true
		}
	}
	return false
}

// encodeULID — 128 biti 26 Crockford karakterine (5 bit/karakter, ilk
// karakter 3 bit) yazar.
func encodeULID(ms uint64, r [10]byte) string {
	var id [16]byte
	for i := 0; i < 6; i++ {
		id[i] = byte(ms >> (40 - 8*uint(i)))
	}
	copy(id[6:], r[:])
	var out [26]byte
	// 130 bitlik pencere: en üstte 2 sıfır bit; 26×5 = 130.
	for i := 0; i < 26; i++ {
		bit := i*5 - 2 // id içindeki başlangıç biti (negatif = ön dolgu)
		v := 0
		for k := 0; k < 5; k++ {
			p := bit + k
			v <<= 1
			if p >= 0 && id[p/8]&(0x80>>uint(p%8)) != 0 {
				v |= 1
			}
		}
		out[i] = crockford[v]
	}
	return string(out[:])
}

// GoldenMessageID — golden/fixture'lar için sabit, geçerli ULID (sabit saat
// 2026-10-29T00:00:00Z + sıfır entropi). Yalnız testler kullanır; üretim
// yolu Options.MessageID'yi nil bırakır.
func GoldenMessageID() string {
	return encodeULID(uint64(time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC).UnixMilli()), [10]byte{})
}
