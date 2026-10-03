package chstore

// exception_resolve_snapshot.go — v0.10.1072: regressed exception grubunda
// "yeniden açıldıktan sonra" hacim.
//
// Prod (2026-10-02 akşamı): 54.812 oluşumlu bir grup P2 görünüyordu — grup
// `regressed`, ve merdivende regressed dalı hacim kapısından (P1MinOccurrences)
// ÖNCE P2 dönüyor. Kapıyı regressed'in üstüne taşımak ÖMÜR BOYU toplamla
// karşılaştırmak demek: her kronik regressed grup P1 olurdu (sel). Doğru ölçü
// grubun yeniden açıldıktan SONRA ürettiği hacim; onun için resolve anındaki
// sayıyı saklamamız gerekiyor — bu dosya o anlık görüntünün yazımı/okuması.
//
// Sözleşme: occurrences_at_resolve = grubun EN SON resolve edildiği andaki
// Occurrences (manuel resolve ya da bayat süpürme). 0 = anlık görüntü yok
// (bu sürümden önce çözülmüş grup, ya da kolon henüz inmemişken yazılmış
// satır) → merdiven P2'de kalır; ömür boyu toplamla ASLA P1.

import (
	"context"
	"log"
	"time"
)

// exGroupSelectCols — exception_groups okumalarının ORTAK kolon listesi
// (dört okuma: tekil, toplu, liste, bayat süpürme). snap = probe
// (hasExResolveSnapCol): küme kipinde kolonu ekleyen boot DDL'i ertelediği
// için kolonu koşulsuz okumak her exception okumasını "no such column" ile
// düşürürdü. Sıra exGroupScanDest ile birebir (pozisyonel Scan).
func exGroupSelectCols(snap bool) string {
	cols := `fingerprint, ex_type, ex_message, service, state, assignee,
		       toUnixTimestamp64Nano(first_seen),
		       toUnixTimestamp64Nano(last_seen),
		       resolved_at, occurrences, notes, ai_summary,
		       toUnixTimestamp64Nano(ai_summary_at)`
	if snap {
		cols += `, occurrences_at_resolve`
	}
	return cols
}

// exGroupScanDest — exGroupSelectCols ile AYNI bool'dan türeyen Scan hedefi.
func exGroupScanDest(g *ExceptionGroup, resolvedAt **time.Time, snap bool) []any {
	dest := []any{&g.Fingerprint, &g.Type, &g.Message, &g.Service, &g.State, &g.Assignee,
		&g.FirstSeen, &g.LastSeen, resolvedAt, &g.Occurrences, &g.Notes, &g.AISummary, &g.AISummaryAt}
	if snap {
		dest = append(dest, &g.OccurrencesAtResolve)
	}
	return dest
}

// markExceptionResolved — resolve geçişinin TEK yeri (manuel resolve ve bayat
// süpürme): durum, damga ve hacim anlık görüntüsü birlikte. SAF.
func markExceptionResolved(g *ExceptionGroup, atNs int64) {
	g.State = ExStateResolved
	g.ResolvedAt = &atNs
	g.OccurrencesAtResolve = g.Occurrences
}

// ExceptionOccurrencesSinceResolve — yeniden açıldıktan sonraki oluşum.
// ok=false: anlık görüntü yok (0) ya da tutarsız (toplam < anlık görüntü;
// elle düzenlenmiş satır) — çağıran ömür boyu toplama DÜŞMEZ. SAF.
func ExceptionOccurrencesSinceResolve(g ExceptionGroup) (uint64, bool) {
	if g.OccurrencesAtResolve == 0 || g.Occurrences < g.OccurrencesAtResolve {
		return 0, false
	}
	return g.Occurrences - g.OccurrencesAtResolve, true
}

// probeExResolveSnapCol — exception_groups.occurrences_at_resolve var mı?
// probeAnomalyEpisodeCols'un şekli: system.columns METADATA okuması — veri
// hacminden bağımsız, sınırlı (max_execution_time 5).
func (s *Store) probeExResolveSnapCol(ctx context.Context) (bool, error) {
	var n uint64
	err := s.conn.QueryRow(ctx,
		`SELECT count() FROM system.columns
		 WHERE database = currentDatabase() AND table = 'exception_groups'
		   AND name = 'occurrences_at_resolve'
		 SETTINGS max_execution_time = 5`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// reprobeExResolveSnapCol — ertelenen DDL sonrası yeniden deneme; bayrak
// yalnız false → true döner, geçiş BİR KEZ loglanır. Dönüş: bayrağın son hâli.
func (s *Store) reprobeExResolveSnapCol(ctx context.Context) bool {
	if s.hasExResolveSnapCol.Load() {
		return true
	}
	if ok, _ := s.probeExResolveSnapCol(ctx); ok && s.hasExResolveSnapCol.CompareAndSwap(false, true) {
		log.Printf("[chstore] exception_groups.occurrences_at_resolve ertelenen DDL sonrası görüldü — resolve anlık görüntüsü devrede (restart gerekmedi)")
	}
	return s.hasExResolveSnapCol.Load()
}
