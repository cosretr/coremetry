package api

// v0.10.1090 regresyon testi — /shift "En çok kötüleşen servisler" yönsüz
// skorla ilk 10'u basıyordu; hatası %76.8 → %0'a inen servis "kötüleşen"
// diye listeleniyordu (v0.10.1063'ün kök-neden manşetindeki hatanın vardiya
// yüzü). Artık Direction'a göre üç grup; unknown hiçbirine girmez.

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestShiftChangeGroups(t *testing.T) {
	row := func(svc, dir string) chstore.ChangedService {
		return chstore.ChangedService{Service: svc, Direction: dir}
	}
	names := func(cs []chstore.ChangedService) []string {
		out := []string{}
		for _, c := range cs {
			out = append(out, c.Service)
		}
		return out
	}
	cases := []struct {
		name                  string
		in                    []chstore.ChangedService
		limit                 int
		worse, lost, improved []string
	}{
		{
			name: "karışık satırlar: skor sırası grup içinde korunur, unknown düşer",
			in: []chstore.ChangedService{
				row("svc-b", chstore.ChangeBetter), // skor 404 — eskiden "kötüleşen" #1
				row("svc-l", chstore.ChangeLost),
				row("svc-w1", chstore.ChangeWorse),
				row("svc-q", chstore.ChangeQuieter),
				row("svc-n", chstore.ChangeUnknown),
				row("svc-e", ""), // yönsüz (eski) satır
				row("svc-w2", chstore.ChangeWorse),
			},
			limit:    10,
			worse:    []string{"svc-w1", "svc-w2"},
			lost:     []string{"svc-l"},
			improved: []string{"svc-b", "svc-q"},
		},
		{
			name: "tavan grup başına: iyileşenler kötüleşeni kesmez",
			in: []chstore.ChangedService{
				row("b1", chstore.ChangeBetter), row("b2", chstore.ChangeBetter), row("b3", chstore.ChangeBetter),
				row("w1", chstore.ChangeWorse),
			},
			limit:    2,
			worse:    []string{"w1"},
			lost:     []string{},
			improved: []string{"b1", "b2"},
		},
		{
			name:  "boş girdi → boş (nil değil) diziler",
			in:    nil,
			limit: 10,
			worse: []string{}, lost: []string{}, improved: []string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, l, i := shiftChangeGroups(c.in, c.limit)
			if w == nil || l == nil || i == nil {
				t.Fatal("gruplar nil olmamalı — JSON `null` çıkar, sayfa .length'te çöker (v0.9.836 sınıfı)")
			}
			if got := names(w); !reflect.DeepEqual(got, c.worse) {
				t.Errorf("worse = %v, want %v", got, c.worse)
			}
			if got := names(l); !reflect.DeepEqual(got, c.lost) {
				t.Errorf("lost = %v, want %v", got, c.lost)
			}
			if got := names(i); !reflect.DeepEqual(got, c.improved) {
				t.Errorf("improved = %v, want %v", got, c.improved)
			}
		})
	}
}
