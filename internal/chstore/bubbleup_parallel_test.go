package chstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// v0.10.1119 regresyon testi — /rootcause soğuk yol (operatör: ilk açılış
// 30–45 sn). runBubbleUp totals ile anahtar keşfini AYNI ANDA koşar; bu
// dosya (1) sonucun ve hata sırasının eski ARDIŞIK orkestrasyonla birebir
// aynı olduğunu, (2) keşfin gerçekten totals'ı beklemediğini, (3) erken
// dönüşlerde keşif goroutine'inin iptal edilip beklendiğini sabitler.

// sequentialBubbleUpRef — v0.10.1119 ÖNCESİ orkestrasyonun birebir kopyası
// (totals → keşif → anahtar başı, ardışık aşamalar). Eşdeğerlik referansı.
func sequentialBubbleUpRef(ctx context.Context, ops bubbleUpOps) (*BubbleUpResult, error) {
	selTotal, baseTotal, err := ops.totals(ctx)
	if err != nil {
		return nil, fmt.Errorf("bubbleup totals: %w", err)
	}
	if selTotal == 0 || baseTotal == 0 {
		return &BubbleUpResult{SelectionTotal: int64(selTotal), BaselineTotal: int64(baseTotal), Attributes: []BubbleUpAttribute{}}, nil
	}
	raw, err := ops.keys(ctx)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, k := range raw {
		if !isHighCardinalityKey(k) {
			keys = append(keys, k)
		}
	}
	out := &BubbleUpResult{SelectionTotal: int64(selTotal), BaselineTotal: int64(baseTotal), Attributes: []BubbleUpAttribute{}}
	if len(keys) == 0 {
		return out, nil
	}
	for _, k := range keys {
		a, err := ops.perKey(ctx, k, selTotal, baseTotal)
		if err != nil {
			return nil, err
		}
		if len(a.Values) > 0 {
			out.Attributes = append(out.Attributes, a)
		}
	}
	for i := 0; i < len(out.Attributes); i++ {
		topI := scoreOf(out.Attributes[i])
		for j := i + 1; j < len(out.Attributes); j++ {
			if topJ := scoreOf(out.Attributes[j]); topJ > topI {
				out.Attributes[i], out.Attributes[j] = out.Attributes[j], out.Attributes[i]
				topI = topJ
			}
		}
	}
	return out, nil
}

// fakeBubbleOps — sahte üç adım. perKey skoru anahtardan türer (deterministik).
type fakeBubbleOps struct {
	sel, base uint64
	totalsErr error
	keys      []string
	keysErr   error
	perKeyErr map[string]error
	empty     map[string]bool // değer üretmeyen anahtar
}

func (f fakeBubbleOps) ops() bubbleUpOps {
	return bubbleUpOps{
		totals: func(context.Context) (uint64, uint64, error) { return f.sel, f.base, f.totalsErr },
		keys:   func(context.Context) ([]string, error) { return f.keys, f.keysErr },
		perKey: func(_ context.Context, k string, sel, base uint64) (BubbleUpAttribute, error) {
			if e := f.perKeyErr[k]; e != nil {
				return BubbleUpAttribute{}, e
			}
			if f.empty[k] {
				return BubbleUpAttribute{Key: k}, nil
			}
			score := float64(len(k)%7) / 10
			return BubbleUpAttribute{Key: k, Values: []BubbleUpValue{{
				Value: "v-" + k, SelectionCount: int64(sel), BaselineCount: int64(base), Score: score,
			}}}, nil
		},
	}
}

func TestRunBubbleUpMatchesSequentialReference(t *testing.T) {
	boom := errors.New("code: 159, timeout exceeded")
	cases := []struct {
		name string
		f    fakeBubbleOps
	}{
		{"normal", fakeBubbleOps{sel: 40, base: 900, keys: []string{"http.route", "k8s.pod.name", "db.system", "net.peer.name", "service.version"}}},
		{"yüksek kardinalite süzülür", fakeBubbleOps{sel: 40, base: 900, keys: []string{"trace_id", "http.route", "request_id", "user.id"}}},
		{"hepsi yüksek kardinalite", fakeBubbleOps{sel: 40, base: 900, keys: []string{"trace_id", "span_id"}}},
		{"seçim boş", fakeBubbleOps{sel: 0, base: 900, keys: []string{"http.route"}}},
		{"taban boş", fakeBubbleOps{sel: 5, base: 0, keys: []string{"http.route"}}},
		{"totals hatası", fakeBubbleOps{totalsErr: boom, keys: []string{"http.route"}}},
		{"totals hatası + keşif hatası → totals kazanır", fakeBubbleOps{totalsErr: boom, keysErr: errors.New("keys down")}},
		{"seçim boş + keşif hatası → boş sonuç", fakeBubbleOps{sel: 0, base: 10, keysErr: errors.New("keys down")}},
		{"keşif hatası", fakeBubbleOps{sel: 3, base: 10, keysErr: errors.New("bubbleup keys: down")}},
		{"anahtar hatası", fakeBubbleOps{sel: 3, base: 10, keys: []string{"a.b", "c.d"}, perKeyErr: map[string]error{"c.d": boom}}},
		{"değersiz anahtar atlanır", fakeBubbleOps{sel: 3, base: 10, keys: []string{"a.b", "c.d", "e.f"}, empty: map[string]bool{"c.d": true}}},
		{"keşif boş", fakeBubbleOps{sel: 3, base: 10}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, wantErr := sequentialBubbleUpRef(context.Background(), c.f.ops())
			got, gotErr := runBubbleUp(context.Background(), c.f.ops())
			if fmt.Sprint(wantErr) != fmt.Sprint(gotErr) {
				t.Fatalf("hata ayrıştı: ref=%v yeni=%v", wantErr, gotErr)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("sonuç ayrıştı:\nref=%+v\nyeni=%+v", want, got)
			}
			if got != nil && got.Attributes == nil {
				t.Fatal("Attributes nil — JSON null (v0.9.836)")
			}
		})
	}
}

// Keşif totals'ı BEKLEMEZ: totals, keşif başlamadan dönmeyecek şekilde
// kilitlenir. Ardışık orkestrasyonda bu kilitlenme (zaman aşımı) olurdu.
func TestRunBubbleUpOverlapsTotalsAndKeyDiscovery(t *testing.T) {
	keysStarted := make(chan struct{})
	ops := bubbleUpOps{
		totals: func(ctx context.Context) (uint64, uint64, error) {
			select {
			case <-keysStarted:
				return 10, 100, nil
			case <-time.After(2 * time.Second):
				return 0, 0, errors.New("keşif totals'ı bekledi — paralel değil")
			}
		},
		keys: func(context.Context) ([]string, error) {
			close(keysStarted)
			return []string{"http.route"}, nil
		},
		perKey: func(_ context.Context, k string, _, _ uint64) (BubbleUpAttribute, error) {
			return BubbleUpAttribute{Key: k, Values: []BubbleUpValue{{Value: "/checkout", Score: 0.5}}}, nil
		},
	}
	got, err := runBubbleUp(context.Background(), ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Attributes) != 1 || got.Attributes[0].Key != "http.route" {
		t.Fatalf("sonuç %+v", got)
	}
}

// Erken dönüşte (boş taraf / totals hatası) keşif iptal edilir ve dönüşten
// ÖNCE biter — arka planda süren bir ham-spans taraması bırakılmaz.
func TestRunBubbleUpCancelsAndJoinsDiscoveryOnEarlyReturn(t *testing.T) {
	for _, totalsErr := range []error{nil, errors.New("totals down")} {
		var finished atomic.Bool
		ops := bubbleUpOps{
			totals: func(context.Context) (uint64, uint64, error) { return 0, 50, totalsErr },
			keys: func(ctx context.Context) ([]string, error) {
				defer finished.Store(true)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(5 * time.Second):
					return nil, errors.New("iptal edilmedi")
				}
			},
			perKey: func(context.Context, string, uint64, uint64) (BubbleUpAttribute, error) {
				t.Error("erken dönüşte anahtar okuması koşmamalı")
				return BubbleUpAttribute{}, nil
			},
		}
		start := time.Now()
		_, _ = runBubbleUp(context.Background(), ops)
		if !finished.Load() {
			t.Fatalf("totalsErr=%v: keşif goroutine'i dönüşten önce bitmedi", totalsErr)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatalf("totalsErr=%v: keşif iptal edilmedi", totalsErr)
		}
	}
}

// Anahtar başı eşzamanlılık tavanı (6) korunur.
func TestRunBubbleUpKeyConcurrencyCap(t *testing.T) {
	var cur, peak int32
	var mu sync.Mutex
	keys := make([]string, 20)
	for i := range keys {
		keys[i] = fmt.Sprintf("attr.k%02d", i)
	}
	ops := bubbleUpOps{
		totals: func(context.Context) (uint64, uint64, error) { return 10, 10, nil },
		keys:   func(context.Context) ([]string, error) { return keys, nil },
		perKey: func(_ context.Context, k string, _, _ uint64) (BubbleUpAttribute, error) {
			n := atomic.AddInt32(&cur, 1)
			mu.Lock()
			if n > peak {
				peak = n
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt32(&cur, -1)
			return BubbleUpAttribute{Key: k, Values: []BubbleUpValue{{Value: strings.ToUpper(k)}}}, nil
		},
	}
	got, err := runBubbleUp(context.Background(), ops)
	if err != nil {
		t.Fatal(err)
	}
	if peak > bubbleUpKeyConcurrency {
		t.Fatalf("eşzamanlılık tavanı aşıldı: %d > %d", peak, bubbleUpKeyConcurrency)
	}
	if len(got.Attributes) != len(keys) {
		t.Fatalf("anahtar sayısı %d", len(got.Attributes))
	}
	for i, a := range got.Attributes { // eşit skor → keşif sırası korunur
		if a.Key != keys[i] {
			t.Fatalf("sıra bozuldu: %d → %s", i, a.Key)
		}
	}
}
