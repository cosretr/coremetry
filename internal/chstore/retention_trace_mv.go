package chstore

// retention_trace_mv.go — trace MV'lerinin satır TTL'i retention.spans'e bağlı.
//
// NEDEN: canonicalMVs()'teki CREATE trace_summary_5m / trace_service_index_5m
// için sabit bir TTL taşır; ham spans ise retention.spans (varsayılan 30g)
// kadar yaşar. 90g'lük eski literal, 60 gün boyunca silinmiş trace'lere
// işaret eden indeks satırı demekti (disk + /traces'te açılmayan satır).
// v0.10.263'ten beri saatlik enforcer bu MV'lerin partition'larını
// spans+1 günde düşürüyor (retention_mv.go) — yani hiçbir okuyucu zaten
// spans saklamasının ötesini göremiyordu; bu dosya TANIMI o gerçeğe
// hizalar ve enforcer koşmayan kurulumlarda (worker yok) da veriyi düşürür.
// Gerekçe docs/SCHEMA.md §5 "İstisna: trace MV'leri".
//
// NASIL: combined MV TTL'i reddeder ("Engine MaterializedView doesn't
// support TTL clause") → ALTER inner tabloya (`.inner_id.<uuid>`) gider,
// küme genelinde çözülür (mvInnerTablesCluster, v0.9.620 dersi), her uuid
// için ayrı ALTER. İki ifade:
//   - MODIFY SETTING ttl_only_drop_parts = 1 — günlük partition'da süresi
//     dolan part bütün düşer, TTL-merge yeniden yazımı yok;
//   - MODIFY TTL … materialize_ttl_after_modify = 0 — yalnız metadata;
//     eski part'lar sonraki merge'de / enforcer DROP PARTITION'ında gider.
// İkisi de idempotent. Açılış yolunda (skipCurrent) her host'ta TTL ve
// ayar zaten güncelse ALTER gönderilmez (retention_current.go emsali).

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// traceRetentionMVs — satır TTL'i retention.spans'i izleyen MV'ler.
var traceRetentionMVs = []string{"trace_summary_5m", "trace_service_index_5m"}

// traceMVTTLStmts — SAF; testli. inner çıplak ad (`.inner_id.<uuid>`).
func traceMVTTLStmts(inner, onCluster, ttl string) []string {
	name := "`" + strings.Trim(inner, "`") + "`"
	return []string{
		"ALTER TABLE " + name + onCluster + " MODIFY SETTING ttl_only_drop_parts = 1",
		"ALTER TABLE " + name + onCluster + " MODIFY TTL " + ttl +
			" SETTINGS materialize_ttl_after_modify = 0, alter_sync = 0",
	}
}

// traceMVTTLCurrent — SAF; testli. Her host'un inner create_table_query'sinde
// TTL istenenle aynı VE ttl_only_drop_parts = 1 ise true.
func traceMVTTLCurrent(creates []string, ttl string, hosts int) bool {
	observed := make([]string, 0, len(creates))
	for _, c := range creates {
		if !strings.Contains(c, "ttl_only_drop_parts = 1") {
			return false
		}
		observed = append(observed, tableTTLFromCreate(c))
	}
	return ttlCurrentEverywhere(ttl, observed, hosts)
}

// applyTraceMVTTL — best-effort: hata loglanır, retention apply'ı düşürmez
// (applyExemplarColTTL ile aynı sözleşme). Dönüş: (atlanan, gönderilen) MV.
func (s *Store) applyTraceMVTTL(ctx context.Context, spansVal string, skipCurrent bool) (skipped, applied int) {
	ttl, err := buildRetentionTTL(spansVal, "time_bucket")
	if err != nil {
		log.Printf("[chstore] trace MV TTL skipped: bad retention %q: %v", spansVal, err)
		return 0, 0
	}
	hosts := 0
	if skipCurrent {
		if h, herr := s.clusterHostCount(ctx); herr == nil {
			hosts = h
		}
	}
	for _, mv := range traceRetentionMVs {
		inners := s.mvInnerTablesCluster(ctx, s.mvStorageName(mv))
		if len(inners) == 0 {
			continue // MV yok ya da TO-table
		}
		if hosts > 0 && len(inners) == 1 {
			if creates, cerr := s.createQueriesEverywhere(ctx, inners); cerr == nil &&
				traceMVTTLCurrent(creates[inners[0]], ttl, hosts) {
				skipped++
				continue
			}
		}
		applied++
		for _, inner := range inners {
			for _, stmt := range traceMVTTLStmts(inner, s.onCluster(), ttl) {
				// Ayrışmış uuid'de sahibi olmayan host code 60 verir — beklenen.
				if err := s.execWithReadonlyRetry(ctx, stmt); err != nil && len(inners) == 1 {
					log.Printf("[chstore] trace MV TTL on %s not applied: %v", mv, err)
				}
			}
		}
	}
	return skipped, applied
}

// spansRetentionValue — ApplyPersistedRetention'da kayıtlı override yoksa
// trace MV'leri config varsayılanını izler ("" = retention kapalı/ayarsız).
func spansRetentionValue(persisted string, defaultDays int) string {
	if persisted != "" {
		return persisted
	}
	if defaultDays > 0 {
		return fmt.Sprintf("%dd", defaultDays)
	}
	return ""
}
