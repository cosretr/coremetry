package api

// ai_evalset_daily.go — v0.10.2109 (CoSRE kalite döngüsü D4,
// docs/cosre/quality-loop-audit.md §3.4): günlük v2 evalset koşusunun
// koşucu tarafı. Zamanlama, lider kilidi ve acil fren
// internal/cosrelearn/evalset_daily.go'da; burada yalnız "koşabilir mi" ve
// koşunun kendisi. Rota YOK (api.go ve rota kaydı değişmez).
//
// Admin koşusuyla aynı yol (runEvalJob): aynı defter (süreç içi 409), aynı
// çapraz-pod kapısı (CH'de taze "running"), aynı kayıt (ai_eval_runs,
// path=v2), aynı ai_calls etiketi ("evalset-<Yüzey>"). Farklar:
//   - started_by = "system:daily", kimlik "system" (rol admin: elle koşuyu
//     başlatan admin'in gördüğü araç kataloğuyla aynı ölçüm);
//   - tavan 30 dk (operatör kararı; ajan vakası payı yok — tavanı aşan
//     koşu "failed: koşu tavanı aşıldı" yazar, panel görür);
//   - CH log_comment / ES sayacı yüzeyi "worker:cosre-evalset-daily";
//   - worker kapanırsa koşu "cancelled" biter (yarım "done" değil).
//
// Yerel model kuralı (operatör: kod içeren LLM isteği yalnız self-hosted
// modele): koşunun ulaşabileceği HER profil — varsayılan, yüzey haritasındaki
// her profil ve plan Derin vaka içeriyorsa Derin profili — "Yerel
// (self-hosted) model" işaretli (copilot.LocalOnlyAllowed) değilse koşu
// başlamaz, satır yazılmaz, gerekçe günde bir loglanır. Profil kümesi boşsa
// (eski düz alanlar) yerel beyan yok sayılır → koşu yok.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

// evalDailyQueryTag — günlük koşunun CH log_comment / ES sayacı yüzeyi.
const evalDailyQueryTag = "worker:cosre-evalset-daily"

// evalDailyBudget — test dikişi (üretimde cosrelearn.EvalsetDailyBudget).
var evalDailyBudget = cosrelearn.EvalsetDailyBudget

// evalDailyNonLocalProfiles — SAF: koşunun ulaşabileceği profillerden
// yerel işaretli OLMAYANLARIN kimlikleri (sıralı). Boş = koşabilir.
func evalDailyNonLocalProfiles(profiles []copilot.ModelProfile, defaultID string, surface map[string]string, withDeep bool) []string {
	if len(profiles) == 0 {
		return []string{"(profil kümesi boş)"}
	}
	byID := make(map[string]copilot.ModelProfile, len(profiles))
	for _, p := range profiles {
		byID[p.ID] = p
	}
	reach := map[string]bool{defaultID: true}
	for _, id := range surface {
		reach[id] = true
	}
	if withDeep {
		for _, p := range profiles {
			if p.Deep {
				reach[p.ID] = true
			}
		}
	}
	var bad []string
	for id := range reach {
		p, ok := byID[id]
		if !ok {
			continue // haritada bayat kimlik: çözüm varsayılana düşer (o zaten kümede)
		}
		if !copilot.LocalOnlyAllowed(p) {
			bad = append(bad, id)
		}
	}
	sort.Strings(bad)
	return bad
}

// evalPlanHasDeep — SAF: planda Derin vaka var mı.
func evalPlanHasDeep(plan []evalCase) bool {
	for _, c := range plan {
		if c.Deep {
			return true
		}
	}
	return false
}

// RunDailyEvalset — cosrelearn.EvalsetDailyRunner. Koşu bitene dek bloklar.
func (s *Server) RunDailyEvalset(ctx context.Context) (bool, string, error) {
	if s == nil || s.copilot == nil || !s.copilot.Active() {
		return false, "AI yapılandırılmamış", nil
	}
	embedded, err := evalsetCasesLoader()
	if err != nil {
		return false, "", fmt.Errorf("fikstür yüklenemedi: %w", err)
	}
	// v0.10.2111 (D6) — kabul edilmiş aday vakalar gömülü kümenin ardına.
	cases := append(append([]evalCase{}, embedded...), s.evalDailyCandidateCases(ctx)...)
	profiles, defaultID, surface := s.copilot.ProfilesSnapshot()
	if bad := evalDailyNonLocalProfiles(profiles, defaultID, surface, evalPlanHasDeep(cases)); len(bad) > 0 {
		return false, "yerel (self-hosted) işaretli olmayan profil: " + strings.Join(bad, ", "), nil
	}

	now := time.Now().UTC()
	st := evalRunStoreFor(s)
	if evalJobs.current() != nil {
		return false, "başka bir değerlendirme koşusu sürüyor", nil
	}
	if st != nil {
		latest, lerr := st.ListEvalRuns(ctx, 1)
		if lerr != nil {
			return false, "", fmt.Errorf("çapraz-pod kontrolü okunamadı: %w", lerr)
		}
		if len(latest) == 1 && evalRunBlocksStart(latest[0], now) && !evalJobs.isLast(latest[0].ID) {
			return false, "başka bir pod'da değerlendirme koşusu sürüyor", nil
		}
	}

	pid, _, _, model, _ := s.evalProfileFor("")
	row := chstore.EvalRun{
		ID: newEvalRunID(now), StartedAt: now, UpdatedAt: now, Status: evalStatusRunning,
		StartedBy: cosrelearn.EvalsetDailyStartedBy, AppVersion: s.evalAppVersion(), PromptVersion: copilot.PromptVersion(),
		Model: model, ProfileID: pid, Surfaces: []string{}, Total: uint32(len(cases)),
		Path: chstore.EvalRunPathV2,
	}
	claims := &auth.Claims{UserID: "system", Role: auth.RoleAdmin}

	evalJobs.mu.Lock()
	if evalJobs.running != nil {
		evalJobs.mu.Unlock()
		return false, "başka bir değerlendirme koşusu sürüyor", nil
	}
	base := context.WithoutCancel(chstoreTagged(ctx))
	runCtx, cancel := context.WithCancel(base)
	runCtx, release := context.WithTimeout(runCtx, evalDailyBudget)
	job := &evalJob{row: row, plan: cases, cancel: cancel, deadline: evalDailyBudget,
		userID: claims.UserID, userRole: claims.Role, toolset: s.evalRunToolset(cases, "")}
	evalJobs.running = job
	first := job.rowLocked(false)
	evalJobs.mu.Unlock()

	// Worker kapanıyor → koşu "cancelled" (yarım koşu "done" görünmesin).
	stop := context.AfterFunc(ctx, func() {
		evalJobs.mu.Lock()
		job.cancelled = true
		evalJobs.mu.Unlock()
		cancel()
	})
	defer stop()

	_ = s.persistEvalRun(st, first)
	details := fmt.Sprintf("surfaces=all cases=%d daily=1", len(cases))
	if n := evalAgentCaseCount(cases); n > 0 {
		details += fmt.Sprintf(" agent=%d", n)
	}
	s.auditAs(claims, "", "ai.evalset.run", "ai_evalset", row.ID, details)
	log.Printf("[cosre-evalset-daily] koşu %s başladı: %d vaka, tavan %s", row.ID, len(cases), evalDailyBudget)
	s.runEvalJob(runCtx, func() { release(); cancel() }, job, st)
	return true, "", nil
}

// chstoreTagged — günlük koşunun bağlamına CH/ES yüzey etiketi.
func chstoreTagged(ctx context.Context) context.Context {
	return chstore.WithQueryTag(ctx, evalDailyQueryTag)
}
