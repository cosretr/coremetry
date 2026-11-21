package chstore

// exception_resolve_source.go — v0.10.2432 (operatör 2026-11-21: "Prod'da
// problemler regressed gözüken çok fazla var").
//
// Kök neden: bayat süpürme (AutoResolveStaleExceptionGroups, 24 sa sessiz)
// grubu `resolved` yapıyor, sonraki ilk oluşum onu insan resolve'u geri
// gelmiş gibi `regressed` (P2 "regressed") açıyordu. Prod'da trace'ler
// upstream örnekleniyor (tüm hatalar + %5) ve çoğu exception seyrek; her
// sessiz gün bir "regressed" satırı demekti.
//
// Kural: resolve'un KAYNAĞI saklanır (exception_groups.resolve_source):
//   - "auto" — bayat süpürme. Yeniden oluşum grubu SIRADAN açık grup olarak
//     açar (state=new; resolve_source="auto" + resolved_at "yeniden açıldı"
//     izi olarak kalır). Regressed durumu / P2 "regressed" önceliği YOK.
//   - "user" — insan (tekil / toplu resolve). Grace (exResolveGrace) sonrası
//     oluşum bugünkü gibi `regressed`.
//   - "" — kaynak bilinmiyor (bu sürümden önce yazılmış satır ya da kolon
//     henüz inmemiş). Süpürme resolved_at'ı grubun KENDİ last_seen'ine
//     damgalar (insan resolve'u time.Now()); resolved satırda
//     resolved_at == last_seen ⇒ süpürme kanıtı.
//
// Eski `regressed` satırlar (last_seen ilerlemiş, eşitlik kanıtı gitmiş):
// CorrectAutoResolvedRegressions BİR KEZ (lider, refresher goroutine'i)
// resolved_at anında o serviste span var mı diye bakar — süpürme damgası
// gerçek bir span zamanıdır, insan tıklamasının bir span'in nanosaniyesine
// denk gelmesi pratikte imkânsız. Kanıt yoksa (span TTL'i geçmiş, Oracle
// grubu) satıra dokunulmaz; sessiz kalırsa süpürme onu "auto" ile kapatır ve
// sonraki oluşum sıradan açık grup olur.

import (
	"context"
	"fmt"
	"log"
	"time"
)

const (
	ExResolveSourceAuto = "auto"
	ExResolveSourceUser = "user"
)

// exGroupCols — exception_groups okuma/yazımlarının kolon bayrakları; tek
// anlık görüntü (SELECT ile Scan aynı değerden türer).
type exGroupCols struct {
	snap   bool // occurrences_at_resolve (v0.10.1072)
	source bool // resolve_source (v0.10.2432)
}

func (s *Store) exGroupColFlags() exGroupCols {
	return exGroupCols{snap: s.hasExResolveSnapCol.Load(), source: s.hasExResolveSourceCol.Load()}
}

// exceptionAutoResolved — SAF: resolved grubun kapanışı bayat süpürmeden mi?
func exceptionAutoResolved(g ExceptionGroup) bool {
	switch g.ResolveSource {
	case ExResolveSourceAuto:
		return true
	case ExResolveSourceUser:
		return false
	}
	return g.ResolvedAt != nil && *g.ResolvedAt == g.LastSeen
}

// probeExResolveSourceCol — exception_groups.resolve_source var mı?
func (s *Store) probeExResolveSourceCol(ctx context.Context) (bool, error) {
	var n uint64
	err := s.conn.QueryRow(ctx,
		`SELECT count() FROM system.columns
		 WHERE database = currentDatabase() AND table = 'exception_groups'
		   AND name = 'resolve_source'
		 SETTINGS max_execution_time = 5`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// reprobeExResolveSourceCol — ertelenen DDL sonrası; bayrak yalnız false → true.
func (s *Store) reprobeExResolveSourceCol(ctx context.Context) bool {
	if s.hasExResolveSourceCol.Load() {
		return true
	}
	if ok, _ := s.probeExResolveSourceCol(ctx); ok && s.hasExResolveSourceCol.CompareAndSwap(false, true) {
		log.Printf("[chstore] exception_groups.resolve_source ertelenen DDL sonrası görüldü — resolve kaynağı yazılıyor (restart gerekmedi)")
	}
	return s.hasExResolveSourceCol.Load()
}

// exRegressFixCap — tek düzeltme turunun tavanı (boot başına).
const exRegressFixCap = 2000

// legacyRegressedSQL — düzeltme adayları: kaynağı bilinmeyen, resolved_at
// damgalı, span kaynaklı (Oracle değil) regressed gruplar.
func legacyRegressedSQL(c exGroupCols) string {
	where := `state = 'regressed' AND resolved_at IS NOT NULL AND NOT startsWith(fingerprint, 'ora:')`
	if c.source {
		where += ` AND resolve_source = ''`
	}
	return `SELECT ` + exGroupSelectCols(c) + `
		FROM exception_groups FINAL
		WHERE ` + where + `
		LIMIT ` + fmt.Sprint(exRegressFixCap) + `
		SETTINGS max_execution_time = 10`
}

// spanAtSQL — nokta sorgusu: (service, tam an) — birincil anahtar öneki,
// zaman sınırlı, LIMIT + max_execution_time (spans sert kuralı).
const spanAtSQL = `SELECT count() FROM (
		SELECT 1 FROM spans
		WHERE service_name = ?
		  AND time >= fromUnixTimestamp64Nano(toInt64(?))
		  AND time <= fromUnixTimestamp64Nano(toInt64(?))
		LIMIT 1)
		SETTINGS max_execution_time = 5`

func (s *Store) spanExistsAt(ctx context.Context, service string, atNs int64) (bool, error) {
	var n uint64
	if err := s.conn.QueryRow(ctx, spanAtSQL, service, atNs, atNs).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// CorrectAutoResolvedRegressions — BİR KEZLİK, idempotent düzeltme: süpürme
// kapanışından dönen eski `regressed` grupları sıradan açık gruba çevirir
// (state=new, resolve_source=auto). Tam satır yazılır (tüm alanlar FINAL
// okumadan taşınır). Düzeltilen satır regressed kümesinden çıktığı için
// ikinci koşu ona dokunmaz. Lider refresher goroutine'inde, yenilemeyle
// sıralı çağrılır (aynı satıra eşzamanlı upsert yok).
func (s *Store) CorrectAutoResolvedRegressions(ctx context.Context) (int, error) {
	c := s.exGroupColFlags()
	rows, err := s.conn.Query(ctx, legacyRegressedSQL(c))
	if err != nil {
		return 0, fmt.Errorf("scan regressed exception groups: %w", err)
	}
	var cands []ExceptionGroup
	for rows.Next() {
		var g ExceptionGroup
		var resolvedAt *time.Time
		if err := rows.Scan(exGroupScanDest(&g, &resolvedAt, c)...); err != nil {
			rows.Close()
			return 0, err
		}
		if resolvedAt != nil {
			ns := resolvedAt.UnixNano()
			g.ResolvedAt = &ns
		}
		cands = append(cands, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	fix, err := s.autoResolvedRegressionFixes(ctx, cands)
	if err != nil || len(fix) == 0 {
		return 0, err
	}
	if err := s.writeExceptionGroups(ctx, fix); err != nil {
		return 0, err
	}
	return len(fix), nil
}

// autoResolvedRegressionFixes — adaylardan span kanıtı olanları tam satır
// olarak (yalnız state + kaynak değişir) döner.
func (s *Store) autoResolvedRegressionFixes(ctx context.Context, cands []ExceptionGroup) ([]ExceptionGroup, error) {
	var fix []ExceptionGroup
	for _, g := range cands {
		if g.State != ExStateRegressed || g.ResolvedAt == nil || g.ResolveSource != "" || IsOracleGroup(g.Fingerprint) {
			continue
		}
		ok, err := s.spanExistsAt(ctx, g.Service, *g.ResolvedAt)
		if err != nil {
			return nil, fmt.Errorf("span evidence %s: %w", g.Fingerprint, err)
		}
		if !ok {
			continue // kanıt yok → dokunma (insan resolve'u olabilir)
		}
		g.State = ExStateNew
		g.ResolveSource = ExResolveSourceAuto
		fix = append(fix, g)
	}
	return fix, nil
}
