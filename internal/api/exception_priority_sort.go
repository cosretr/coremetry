package api

// exception_priority_sort.go — v0.10.703 (operatör 2026-09-13: "Problems
// inbox'ta P1 patlamalar üstte, Exceptions'ta ilk sayfada yok").
//
// Exceptions sayfası "First seen" ya da "Last seen" ile sıralanınca yüzlerce
// tek seferlik grup ilk sayfayı doldurur; 700+ occurrence'lı P1 grup sonraki
// sayfalara iner. Problems inbox aynı tabloyu ÖNCELİK sırasıyla kırptığı
// için orada görünür. Bu dosya Exceptions listesine aynı sırayı verir.
//
// Neden SQL'de değil: exceptionPriority Go'da hesaplanır (occurrence,
// first/last seen, exception_triage ayarı ve ŞİMDİ — taze pencereler zamana
// bağlı). O yüzden sort=priority için liste TAVANLI kümede (ListExceptionGroups
// zaten 3000'de kırpıyor; küçük FINAL state tablosu) çekilir, öncelik satır
// başına hesaplanır, Go'da sıralanır ve istenen sayfa dilimlenir. Tavanı
// aşan kurulumda (>3000 grup aynı süzgeçte) en yeni 3000 sıralanır —
// dürüstlük: `capped` alanı cevapta söyler.

import (
	"context"
	"net/url"
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	exceptionPrioritySortKey = "priority"
	// exceptionPrioritySortCap — ListExceptionGroups'un tavanıyla aynı.
	exceptionPrioritySortCap = 3000
)

// exceptionPriorityRank — P1 en önde; bilinmeyen/boş en sona.
func exceptionPriorityRank(p string) int {
	switch p {
	case "P1":
		return 0
	case "P2":
		return 1
	case "P3":
		return 2
	}
	return 3
}

// sortExceptionGroupsByPriority — SAF, kararlı. dir "desc" (varsayılan) =
// P1 → P2 → P3; "asc" tersi. Eşitlikte last_seen DESC (en taze önce),
// sonra fingerprint ASC — SQL sıralamasının eş-kuralı (determinizm).
func sortExceptionGroupsByPriority(items []chstore.ExceptionGroup, dir string) {
	asc := dir == "asc"
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := exceptionPriorityRank(items[i].Priority), exceptionPriorityRank(items[j].Priority)
		if ri != rj {
			if asc {
				return ri > rj
			}
			return ri < rj
		}
		if items[i].LastSeen != items[j].LastSeen {
			return items[i].LastSeen > items[j].LastSeen
		}
		return items[i].Fingerprint < items[j].Fingerprint
	})
}

// pageExceptionGroups — SAF: [offset, offset+limit) dilimi; sınır dışı → boş.
func pageExceptionGroups(items []chstore.ExceptionGroup, offset, limit int) []chstore.ExceptionGroup {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || offset >= len(items) {
		return []chstore.ExceptionGroup{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// exceptionPage — handler'ın döndüreceği sayfa sözleşmesi: Limit/Offset
// İSTENEN sayfa (tavanlı çekimde bile), Capped = öncelik sıralaması
// tavanı aştı (en yeni 3000 sıralandı). prio=false → apply no-op.
//
// v0.10.949 — taban alanları: MinOcc uygulanan taban, Hidden tabanın
// GİZLEDİĞİ grup sayısı (tabansız sayım − toplam; varsayılan kipte istisnalı
// satırlar — çoklu-servis ve regressed — toplamda olduğu için "5'in altında,
// tek serviste, regressed değil" demek), FloorDefault varsayılan kip
// (istisnalı taban) mı. SpreadOK yayılım okundu mu: false = okuma soft-fail etti (CH hatası/backoff), taban
// istisnasız uygulandı — Hidden o an çoklu-servis grupları da içerir.
type exceptionPage struct {
	Limit, Offset int
	Capped        bool
	MinOcc        uint64
	Hidden        int64
	FloorDefault  bool
	SpreadOK      bool
	prio          bool
	dir           string
}

// listExceptionGroupsPage — sort=priority ise tavanlı küme (offset 0,
// last_seen DESC — tavan aşımında en yeni 3000), değilse istenen sayfa;
// toplam sayım her iki yolda aynı süzgeçten. Öncelik hesabı ÇAĞIRANDA
// (satır başına exceptionPriority), apply ondan sonra.
//
// v0.10.949 — ?floor=default: taban SUNUCUDA seçilir (effectiveDefaultFloor)
// ve çoklu-servis + regressed istisnası uygulanır (Inbox varsayılanıyla aynı
// kural; regressed: operatör kararı 2026-09-26).
// Param yoksa ?minOccurrences= bugünkü gibi (yoksa taban yok — API uyumu).
// Taban varken tabansız bir COUNT daha: gizlenen sayı yanıtta (yeni alan).
// Yayılım işaretleri her kipte.
func (s *Server) listExceptionGroupsPage(ctx context.Context, f chstore.ExceptionGroupFilter, q url.Values) ([]chstore.ExceptionGroup, int64, exceptionPage, error) {
	pg := exceptionPage{Limit: f.Limit, Offset: f.Offset, prio: f.Sort == exceptionPrioritySortKey, dir: f.Dir}
	sp := s.exceptionSpread(ctx)
	pg.SpreadOK = sp != nil
	if q.Get("floor") == "default" {
		pg.FloorDefault = true
		f.MinOccurrences = effectiveDefaultFloor(currentExceptionTriage())
		f.FloorExempt = sp.ExemptBelow(f.MinOccurrences)
		f.FloorExemptRegressed = true // v0.10.949 — regressed tabandan muaf
	}
	pg.MinOcc = f.MinOccurrences
	if pg.prio {
		f.Limit, f.Offset = exceptionPrioritySortCap, 0
		f.Sort, f.Dir = "lastSeen", "desc"
	}
	items, err := s.store.ListExceptionGroups(ctx, f)
	if err != nil {
		return nil, 0, pg, err
	}
	total, err := s.store.CountExceptionGroups(ctx, f)
	if err != nil {
		return nil, 0, pg, err
	}
	if f.MinOccurrences > 0 {
		nf := f
		nf.MinOccurrences, nf.FloorExempt, nf.FloorExemptRegressed = 0, nil, false
		noFloor, err := s.store.CountExceptionGroups(ctx, nf)
		if err != nil {
			return nil, 0, pg, err
		}
		pg.Hidden = floorHidden(noFloor, total)
	}
	annotateExceptionSpread(items, sp)
	return items, total, pg, nil
}

// floorHidden — tabansız sayım − tabanlı toplam; iki okuma arasında yazım
// olursa negatife düşmez.
func floorHidden(noFloor, total int64) int64 {
	if noFloor <= total {
		return 0
	}
	return noFloor - total
}

// body — v0.10.949 — /api/exception-groups yanıt gövdesi (api.go büyümesin
// diye burada). Eski beş alan aynen; taban alanları EK (istemci opsiyonel
// okur). apply'dan SONRA çağrılır (Capped orada belirlenir).
func (pg *exceptionPage) body(items []chstore.ExceptionGroup, total int64) map[string]any {
	return map[string]any{
		"items":           items,
		"total":           total,
		"limit":           pg.Limit,
		"offset":          pg.Offset,
		"capped":          pg.Capped,
		"minOcc":          pg.MinOcc,
		"hiddenByMinOcc":  pg.Hidden,
		"floorDefault":    pg.FloorDefault,
		"spreadWindowMin": spreadWindowMin(),
		"spreadAvailable": pg.SpreadOK,
	}
}

// apply — öncelik sıralaması + sayfa dilimi; sort=priority değilse aynen.
func (pg *exceptionPage) apply(items []chstore.ExceptionGroup, total int64) []chstore.ExceptionGroup {
	if !pg.prio {
		return items
	}
	pg.Capped = total > exceptionPrioritySortCap
	sortExceptionGroupsByPriority(items, pg.dir)
	return pageExceptionGroups(items, pg.Offset, pg.Limit)
}
