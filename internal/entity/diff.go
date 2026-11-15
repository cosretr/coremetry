package entity

import (
	"sort"
	"time"
)

// diff.go — ZAMAN GEÇERLİLİĞİ: ömür farkı (saf; design §1.3).
//
// Her varlık/ilişki satırı bir ÖMÜR: (id, valid_from) dedup anahtarı,
// valid_to (sıfır = açık), last_seen. v0.10.2225 (entity-storage-cost-note
// C, E1): yalnız KALICI katman (cluster, node, namespace, workload, service
// + aralarındaki parent kenarları) ömür taşır; kimlik doğal anahtardır.
// Gözlemcinin susması varlığın yeniden doğması DEĞİLDİR: S1'in "last_seen
// podGap'ten eskiyse kapat + yeniden aç" kuralı kalktı. Kapanış yalnız art
// arda BAŞARILI görüntülerde ≥ closeAfter süren yokluktur; uid değişimi
// (aynı ad, yeni nesne) kapatıp yeniden açar. Ulaşılamayan cluster'da diff
// hiç koşmaz (yokluk bilinmiyor); silme yok.

// Lifetime — açık ya da kapanmış bir ömür.
type Lifetime struct {
	ID        string
	UID       string
	ValidFrom time.Time
	ValidTo   time.Time // sıfır = açık
	LastSeen  time.Time
}

// Change — bir tick'in yazım kararı.
type Change struct {
	Open    []Lifetime // yeni ömürler (valid_from = now)
	Close   []Lifetime // kapanan ömürler (valid_to = yokluktan önceki son görülme)
	Refresh []Lifetime // last_seen tazelenen açık ömürler
}

// Absence — açık bir ömrün art arda başarılı görüntülerdeki yokluğu.
type Absence struct {
	Since    time.Time // ilk yokluk gözlemi
	LastSeen time.Time // yokluktan önce görüldüğü bilinen en geç an
}

// DiffLifetimes — prev: id → AÇIK ömür (CH); seen: id → bu (başarılı)
// tick'te görülen; absent: önceki tick'lerden süren yokluklar; lastOK: bu
// süreçte cluster'ın bir önceki başarılı tick'i (sıfır = bilinmiyor).
// Dönüş: değişiklik + bir sonraki tick'e taşınacak yokluklar.
func DiffLifetimes(now time.Time, prev map[string]Lifetime, seen map[string]Entity, absent map[string]Absence, lastOK time.Time, closeAfter time.Duration) (Change, map[string]Absence) {
	var ch Change
	next := map[string]Absence{}
	for id, e := range seen {
		p, open := prev[id]
		switch {
		case !open:
			ch.Open = append(ch.Open, Lifetime{ID: id, UID: e.UID, ValidFrom: now, LastSeen: now})
		case uidChanged(p.UID, e.UID):
			closed := p
			closed.ValidTo = p.LastSeen
			ch.Close = append(ch.Close, closed)
			ch.Open = append(ch.Open, Lifetime{ID: id, UID: e.UID, ValidFrom: now, LastSeen: now})
		default:
			r := p
			r.LastSeen = now
			if r.UID == "" {
				r.UID = e.UID
			}
			ch.Refresh = append(ch.Refresh, r)
		}
	}
	for id, p := range prev {
		if _, ok := seen[id]; ok {
			continue
		}
		a, tracked := absent[id]
		if !tracked {
			a = Absence{Since: now, LastSeen: p.LastSeen}
			if lastOK.After(a.LastSeen) {
				a.LastSeen = lastOK
			}
		}
		if now.Sub(a.Since) < closeAfter {
			next[id] = a
			continue
		}
		closed := p
		closed.ValidTo, closed.LastSeen = a.LastSeen, a.LastSeen
		ch.Close = append(ch.Close, closed)
	}
	byID := func(ls []Lifetime) { sort.Slice(ls, func(i, j int) bool { return ls[i].ID < ls[j].ID }) }
	byID(ch.Open)
	byID(ch.Close)
	byID(ch.Refresh)
	return ch, next
}

// ThrottleRefresh — v0.10.2214 (S1 yazım diyeti, plan §3.3; v0.10.2225'te
// every = RefreshEvery = 24 saat): Refresh'ten
// yalnız saklı last_seen'i `every`'den eski olanları (ya da uid'i yeni
// dolanları) tutar; gerisi yazılmaz. Karar yalnız CH'den okunan prev'e
// dayanır — lider devrinde yeni lider aynı girdiyle aynı kararı verir.
// Dönüş: yazılacaklar + atlanan sayısı.
func ThrottleRefresh(now time.Time, prev map[string]Lifetime, refresh []Lifetime, every time.Duration) ([]Lifetime, int) {
	keep := refresh[:0:0]
	skipped := 0
	for _, r := range refresh {
		p, ok := prev[r.ID]
		if !ok || now.Sub(p.LastSeen) > every || p.UID != r.UID {
			keep = append(keep, r)
			continue
		}
		skipped++
	}
	return keep, skipped
}

func uidChanged(prev, cur string) bool {
	return prev != "" && cur != "" && prev != cur
}
