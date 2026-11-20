package chstore

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
)

// spanmetrics_exemplar.go — spanmetrics_{1m,10s,1s} exemplar şekli
// (depolama sıkıştırması; ölçüm CH 26.2, 10M span: spanmetrics_1m
// 190 → 113 MiB, exemplar kolonu 107 → 52 MiB).
//
// Yeni şekil: `slow_exemplar_bin_state` / `error_exemplar_bin_state` —
// argMax(If)State(unhex(trace_id), duration). trace_id HAM BAYT (hex'in
// yarısı, 16 B); okuma lower(hex(…)) ile hex'e döner. Hex olmayan (ya da
// tek uzunluklu) trace_id MV'de '' olur → okuma '' döner = "exemplar yok";
// yanlış bir kimlik üretmekten iyidir.
//
// Eski şekil (`slow_exemplar_state` / `error_exemplar_state`, hex String)
// yalnız şekil kapısının göçü REDDETTİĞİ kurulumda yaşar
// (mvMigrationShapeOK); okuyucu boot probe'uyla o tabloyu eski adlarla
// okur. Varsayılan (probe öncesi / taze kurulum) YENİ şekil.

const (
	smSlowExemplarCol       = "slow_exemplar_bin_state"
	smErrorExemplarCol      = "error_exemplar_bin_state"
	smLegacySlowExemplarCol = "slow_exemplar_state"
	smLegacyErrExemplarCol  = "error_exemplar_state"
)

// smExemplarTables — exemplar taşıyan spanmetrics kademeleri.
var smExemplarTables = []string{"spanmetrics_1m", "spanmetrics_10s", "spanmetrics_1s"}

// smLegacyExemplar — boot probe'unun ESKİ şekilde bulduğu kademeler.
var smLegacyExemplar atomic.Pointer[map[string]bool]

func smIsLegacy(table string) bool {
	if m := smLegacyExemplar.Load(); m != nil {
		return (*m)[table]
	}
	return false
}

// smExemplarStateCols — (slow, error) state kolon adları.
func smExemplarStateCols(table string) (slow, errCol string) {
	if smIsLegacy(table) {
		return smLegacySlowExemplarCol, smLegacyErrExemplarCol
	}
	return smSlowExemplarCol, smErrorExemplarCol
}

// smExemplarFinal — bir argMax(If)Merge ifadesini hex trace_id'ye çevirir.
// Kombinatör çağıranda kalır (argMaxMerge vs argMaxIfMerge — v0.8.51).
func smExemplarFinal(table, mergeExpr string) string {
	if smIsLegacy(table) {
		return mergeExpr
	}
	return "lower(hex(" + mergeExpr + "))"
}

// smExemplarMerges — tek kademeli okuma: (slow, error) hex trace_id ifadeleri.
func smExemplarMerges(table string) (slow, errExpr string) {
	sc, ec := smExemplarStateCols(table)
	return smExemplarFinal(table, "argMaxMerge("+sc+")"), smExemplarFinal(table, "argMaxIfMerge("+ec+")")
}

// probeSpanmetricsExemplarShape — her kademenin depolama tablosunda yeni
// kolon yok ama eski kolon varsa o kademe ESKİ şekilde okunur.
func (s *Store) probeSpanmetricsExemplarShape(ctx context.Context) {
	legacy := map[string]bool{}
	for _, t := range smExemplarTables {
		var hasNew, hasOld uint8
		q := `SELECT countIf(name = ?) > 0, countIf(name = ?) > 0 FROM system.columns
			WHERE database = currentDatabase() AND table = ?`
		if err := s.conn.QueryRow(ctx, q, smSlowExemplarCol, smLegacySlowExemplarCol, s.mvStorageName(t)).Scan(&hasNew, &hasOld); err != nil {
			continue
		}
		if hasNew == 0 && hasOld == 1 {
			legacy[t] = true
		}
	}
	if len(legacy) > 0 {
		log.Printf("[chstore] spanmetrics exemplar ESKİ şekilde okunuyor (göç atlandı): %v", legacy)
	}
	smLegacyExemplar.Store(&legacy)
}

// smExemplarTraceIDDDL — MV tarafı ifade (store.go DDL'iyle birebir; test pinler).
var smExemplarTraceIDDDL = fmt.Sprintf("if(match(trace_id, '%s'), unhex(trace_id), '')", `^([0-9a-f]{2})+$`)
