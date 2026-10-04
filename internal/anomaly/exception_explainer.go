package anomaly

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/rca"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// ExceptionExplainer (v0.9.415, operatör istegi #1) — ProblemExplainer'ın
// exception ikizi: inbox'a P1 olarak düşen ("anonslu") exception grupları
// için kök-sebep özetini PROAKTİF doldurur. Operatör gruba tıkladığında
// "Explain root cause"a basmadan hazır bir CoSRE özeti görür; buton yine
// durur (taze/derin soruşturma için).
//
// Tasarım birebir ProblemExplainer'dan (v0.5.254 + v0.9.200):
//   - Leader-lock'lu tek işçi; 60s tik.
//   - Copilot Active() + kota devre-kesici kapıları.
//   - Tik başına küçük batch (4): exception girdisi trace + log
//     prefetch'li — problem özetinden pahalı.
//   - Surface "exception-auto-explain" → /ai sayfasında ayrı satır.
//
// Aday ölçütü inbox'ın P1 formülüyle AYNI (internal/api/inbox.go
// exceptionPriority): last_seen ≤5dk VE occurrences ≥500 — yani tam da
// "anons edilen" gruplar. Regressed gruplar da listelenir: regresyonda
// UpsertExceptionGroup bayat özeti SIFIRLAR (yanıltıcıdır — v0.9.415),
// taze+yoğun bir regresyon burada yeniden doldurulur.
const exceptionExplainerLockKey = "exception-explainer:lock"
const exceptionExplainerBatch = 4

type ExceptionExplainer struct {
	store    *chstore.Store
	logs     logstore.Store
	copilot  *copilot.Service
	leader   *cache.LeaderHolder
	interval time.Duration
	batch    int
}

func NewExceptionExplainer(store *chstore.Store, logs logstore.Store, cop *copilot.Service, lock cache.Lock) *ExceptionExplainer {
	interval := 60 * time.Second
	return &ExceptionExplainer{
		store:    store,
		logs:     logs,
		copilot:  cop,
		leader:   cache.NewLeaderHolder(lock, exceptionExplainerLockKey, cache.LeaderTTL(interval)),
		interval: interval,
		batch:    exceptionExplainerBatch,
	}
}

func (e *ExceptionExplainer) Start(ctx context.Context) {
	if e == nil || e.copilot == nil {
		return
	}
	e.leader.Start(ctx)
	t := time.NewTicker(e.interval)
	defer t.Stop()
	e.tickIfLeader(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.tickIfLeader(ctx)
		}
	}
}

func (e *ExceptionExplainer) tickIfLeader(ctx context.Context) {
	// v0.9.1138 — otomatik açıklama vidası (Settings→AI). Kapalıyken
	// yalnız BU worker susar; tıklamalı ✨ yüzeyleri etkilenmez.
	if !e.copilot.AutoExplainEnabled() {
		return
	}
	if !e.copilot.Active() {
		return
	}
	if !e.leader.IsLeader() {
		return
	}
	if e.copilot.QuotaBackoffActive() {
		return
	}
	e.run(ctx)
}

func (e *ExceptionExplainer) run(ctx context.Context) {
	// new + regressed listelenir; P1 süzgeci saf fonksiyonda. MinOccurrences
	// 500 ön-süzgeci listeyi CH tarafında küçültür (P1 şartının parçası).
	now := time.Now()
	var candidates []chstore.ExceptionGroup
	for _, state := range []string{chstore.ExStateNew, chstore.ExStateRegressed} {
		groups, err := e.store.ListExceptionGroups(ctx, chstore.ExceptionGroupFilter{
			State: state, Limit: 100, MinOccurrences: 500,
		})
		if err != nil {
			log.Printf("[exception-explainer] list %s: %v", state, err)
			continue
		}
		for _, g := range groups {
			if len(candidates) >= e.batch {
				break
			}
			// v0.10.1100 — Oracle grubunun last_seen'i kapanmış dakika
			// gecikmesi kadar geriden gelir (≥16 dk); yaş o kadar geriden ölçülür.
			lag, ok := exceptionExplainLag(g)
			if ok && isExceptionExplainCandidateAt(g, now, lag) {
				candidates = append(candidates, g)
			}
		}
	}
	filled := 0
	for i := range candidates {
		g := candidates[i]
		// Arka planda tarayıcı yok → sunucu varsayılanı (COREMETRY_TZ, imajda
		// Europe/Istanbul; v0.10.746), prompt'ta etiketli (v0.10.745).
		// v0.10.1100 — `ora:` grubu Oracle bağlamı + kendi prompt'u (span
		// girdisi orada boş kalıyordu).
		in, system := e.explainInput(ctx, &g)
		cctx := copilot.WithMeta(ctx, copilot.CallMeta{
			Surface: "exception-auto-explain", UserID: "system",
			Shield: func(prompt, answer string) uint8 {
				return rca.CountUnknownEntities(rca.LowerKnownSet(), prompt, answer)
			}, // v0.10.421 (E6)
		})
		summary, err := e.copilot.Explain(cctx, system, in.User)
		if err != nil {
			log.Printf("[exception-explainer] %s: %v", g.Fingerprint, err)
			continue
		}
		if strings.TrimSpace(summary) == "" {
			continue
		}
		if err := e.store.UpsertExceptionGroupAISummary(ctx, g.Fingerprint, summary); err != nil {
			log.Printf("[exception-explainer] write %s: %v", g.Fingerprint, err)
			continue
		}
		filled++
	}
	if filled > 0 {
		log.Printf("[exception-explainer] filled %d summary/ies", filled)
	}
}

// explainInput — grubun kaynağına göre girdi + sistem prompt'u. Arka planda
// tarayıcı yok → sunucu varsayılan dilimi (COREMETRY_TZ, v0.10.746).
func (e *ExceptionExplainer) explainInput(ctx context.Context, g *chstore.ExceptionGroup) (ExceptionExplainInput, string) {
	if chstore.IsOracleGroup(g.Fingerprint) {
		// v0.10.1103 — e.logs: Coremetry trace'inin logları (nil-güvenli).
		return BuildOracleExceptionExplainInput(ctx, e.store, e.logs, g, tzdefault.Location()), copilot.SystemPromptOracleException()
	}
	return BuildExceptionExplainInput(ctx, e.store, e.logs, g, tzdefault.Location()), copilot.SystemPromptException()
}

// exceptionExplainLag — SAF olmayan ince sarmalayıcı: span grubu (0, true);
// Oracle grubu enjekte edilen GroupStatsCache gecikmesi (v0.10.1092 ile AYNI
// kaynak). Kaynağı bulunamayan Oracle grubu (silinmiş) aday DEĞİL: bildirim
// kapısıyla aynı duruş — sahipsiz grup sessiz kalır.
func exceptionExplainLag(g chstore.ExceptionGroup) (time.Duration, bool) {
	if !chstore.IsOracleGroup(g.Fingerprint) {
		return 0, true
	}
	f, ok := OracleExplainFactsFor(g)
	if !ok {
		return 0, false
	}
	return f.Lag, true
}

// isExceptionExplainCandidate — inbox P1 formülü (exceptionPriority,
// internal/api/inbox.go) + özet-boşluğu. Saf — tablo-testli.
func isExceptionExplainCandidate(g chstore.ExceptionGroup, now time.Time) bool {
	return isExceptionExplainCandidateAt(g, now, 0)
}

// isExceptionExplainCandidateAt — v0.10.1100: yaş "şimdi − lag"den ölçülür.
// Oracle grubu yalnız KAPANMIŞ dakikaları sayar; last_seen duvar saatinin
// ≥16 dk gerisinde olduğundan düz 5 dk kuralında HİÇ aday olamıyordu. Span
// grubunda lag 0 → kural bayt bayt eski. Saf — tablo-testli.
func isExceptionExplainCandidateAt(g chstore.ExceptionGroup, now time.Time, lag time.Duration) bool {
	if strings.TrimSpace(g.AISummary) != "" {
		return false
	}
	if lag < 0 {
		lag = 0
	}
	age := now.Add(-lag).UnixNano() - g.LastSeen
	return time.Duration(age) <= 5*time.Minute && g.Occurrences >= 500
}
