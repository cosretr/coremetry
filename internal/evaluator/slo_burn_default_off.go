package evaluator

// slo_burn_default_off.go — v0.10.1081: SLO burn-rate Problem'leri varsayılan
// KAPALI. Operatör (prod, Problems sayfası: "SLO burn-rate warning — <svc>"
// P3 problemleri ve onlardan açılan "Burn rate above warning threshold for SLO
// …" incident'ları): "SLO burn rate problem olmasın, çıkar. SLO ile ilgili
// beklentim yok."
//
// Varsayılan değişikliği, özellik kaldırma DEĞİL (v0.10.1069 emsali): SLO
// sayfası, burn hesabı ve grafikler durur; yalnız Problem/incident üretimi
// problem_priority.sloBurnProblems bayrağına bağlanır (nil = kapalı) ve
// Settings'ten geri açılabilir. Kapı slo_burn.go evaluateSLOs'ta.
//
// Yükseltme: TEK SEFERLİK göç (applySLOBurnDefaultOff). Lider tikinde,
// evaluateAll'dan ÖNCE koşar; açık (open + acknowledged) slo:* problemlerini
// normal kapatma yolundan (MarkResolved + UpsertProblem, Value ezilmez —
// v0.9.977) dürüst gerekçeyle kapatır — incident'ları aynı tikin sonundaki
// kaskad (cascadeResolveIncidents) normal kapanıştaki gibi kapatır. Kapanış
// bildirim göndermez (bugünkü resolve dalı gibi). Tek audit satırı; işaret
// system_settings'te, varken göç bir daha HİÇ koşmaz.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// sloBurnDefaultOffKey — göçün system_settings işareti. Varlığı = "göç
	// bitti"; içerik yalnız teşhis içindir.
	sloBurnDefaultOffKey = "slo_burn_problems_default_off"
	// sloBurnDefaultOffVersion — sürüm damgası (işaret + audit + gerekçe).
	sloBurnDefaultOffVersion = "v0.10.1081"
	// sloBurnDefaultOffReason — kapatılan problemlerin açıklamasına eklenen
	// gerekçe (appendResolveSuffix: " · auto-resolved: <gerekçe>").
	sloBurnDefaultOffReason = "slo burn problems disabled by default " + sloBurnDefaultOffVersion
	// sloBurnDefaultOffAction — audit eylem adı ("kind.action" deseni).
	sloBurnDefaultOffAction = "slo.burn_problems_default_off"
)

// sloOffStore — göçün dokunduğu store yüzeyi. *chstore.Store karşılar;
// testler bellek içi sahteyle koşar (CH'siz).
type sloOffStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
	OpenProblemsSnapshot(ctx context.Context) (*chstore.OpenProblems, error)
	UpsertProblem(ctx context.Context, p chstore.Problem) error
	AppendAudit(ctx context.Context, e chstore.AuditEntry) error
}

// sloBurnDefaultOffMarker — işaretin JSON gövdesi.
type sloBurnDefaultOffMarker struct {
	Version          string `json:"version"`
	AppliedAt        int64  `json:"appliedAt"`
	ResolvedProblems int    `json:"resolvedProblems"`
	// SkippedEnabled — göç anında bayrak AÇIKTI (operatör zaten açmış):
	// hiçbir şey kapatılmadı, yalnız işaret yazıldı.
	SkippedEnabled bool `json:"skippedEnabled,omitempty"`
}

// sloOffResult — bir göç koşusunun sonucu. Ran=false: işaret zaten vardı.
type sloOffResult struct {
	Ran      bool
	Resolved []chstore.Problem
}

// applySLOBurnDefaultOff — tek seferlik göç. İdempotent: işaret varken
// hiçbir şey yapmaz. `enabled` = bayrak şu an AÇIK mı (yayınlanmış ayardan);
// açıksa operatör burn problemlerini istiyor demektir — kapatma yok, yalnız
// işaret. Hata yönü (builtins_default_off emsali): okuma/yazma düşerse işaret
// YAZILMAZ ve hata döner, sonraki tik yeniden dener; zaten kapanmış problem
// ikinci koşuda snapshot'ta olmadığından yeniden deneme çift kapatma
// üretmez. Audit tek satır ve yalnız bir şey kapandıysa.
func applySLOBurnDefaultOff(ctx context.Context, st sloOffStore, enabled bool, now time.Time) (sloOffResult, error) {
	var res sloOffResult
	marker, err := st.GetSetting(ctx, sloBurnDefaultOffKey)
	if err != nil {
		// İşaret okunamadı — kör koşma, bir tik beklemek ucuz.
		return res, fmt.Errorf("read marker: %w", err)
	}
	if len(marker) > 0 {
		return res, nil
	}
	res.Ran = true
	nowNs := now.UnixNano()

	if !enabled {
		snap, err := st.OpenProblemsSnapshot(ctx)
		if err != nil {
			return res, fmt.Errorf("open problems: %w", err)
		}
		var failed int
		for _, p := range sloBurnProblemsToResolve(snap.All()) {
			// Evaluator'ın normal kapatma yolu: MarkResolved Value'yu ezmez,
			// satırın geri kalanı snapshot'tan aynen taşınır (invariant #4).
			chstore.MarkResolved(&p, nowNs)
			p.Description = appendResolveSuffix(p.Description, sloBurnDefaultOffReason)
			if err := st.UpsertProblem(ctx, p); err != nil {
				log.Printf("[evaluator] slo burn default-off: resolve %s: %v", p.ID, err)
				failed++
				continue
			}
			res.Resolved = append(res.Resolved, p)
		}
		if len(res.Resolved) > 0 {
			ids := make([]string, 0, len(res.Resolved))
			for _, p := range res.Resolved {
				ids = append(ids, p.ID)
			}
			details, _ := json.Marshal(map[string]any{
				"version":          sloBurnDefaultOffVersion,
				"resolvedProblems": len(res.Resolved),
				"reason":           "SLO burn-rate problemleri varsayılan kapalı (operatör kararı)",
			})
			// Aktör "system": göç bir HTTP isteğinden değil lider tikinden
			// gelir. Audit en-iyi-çaba — düşerse loglanır, göç geri alınmaz.
			if err := st.AppendAudit(ctx, chstore.AuditEntry{
				Time:       nowNs,
				ActorID:    "system",
				ActorEmail: "system",
				ActorRole:  "system",
				Action:     sloBurnDefaultOffAction,
				TargetKind: "problem",
				TargetID:   strings.Join(ids, ","),
				Details:    string(details),
			}); err != nil {
				log.Printf("[evaluator] slo burn default-off: audit: %v", err)
			}
		}
		if failed > 0 {
			// İşaret yok → sonraki tik kapanmamış kalanları yeniden dener.
			return res, fmt.Errorf("%d problem(s) could not be resolved", failed)
		}
	}

	body, _ := json.Marshal(sloBurnDefaultOffMarker{
		Version:          sloBurnDefaultOffVersion,
		AppliedAt:        nowNs,
		ResolvedProblems: len(res.Resolved),
		SkippedEnabled:   enabled,
	})
	if err := st.PutSetting(ctx, sloBurnDefaultOffKey, body); err != nil {
		return res, fmt.Errorf("write marker: %w", err)
	}
	return res, nil
}

// sloBurnDefaultOffStep — lider tikinin göç adımı. Süreç başına bir kez
// başarılı biter; hata hâlinde sonraki tik yeniden dener. evaluateAll'dan
// ÖNCE koşar: kapanan problemlerin incident'ları bu tikin kaskadında kapanır.
func (e *Evaluator) sloBurnDefaultOffStep(ctx context.Context) {
	if e.sloBurnOffDone.Load() || e.store == nil {
		return
	}
	enabled := chstore.ProblemPriorityPublished() && chstore.CurrentProblemPriority().SLOBurnProblemsEnabled()
	res, err := applySLOBurnDefaultOff(ctx, e.store, enabled, time.Now())
	for _, p := range res.Resolved {
		e.countResolved()
		log.Printf("[evaluator] PROBLEM AUTO-RESOLVED (%s): %s · %s", sloBurnDefaultOffReason, p.Service, p.RuleName)
	}
	if err != nil {
		log.Printf("[evaluator] slo burn default-off migration (retry next tick): %v", err)
		return
	}
	e.sloBurnOffDone.Store(true)
	if res.Ran {
		log.Printf("[evaluator] slo burn default-off migration applied: resolved=%d", len(res.Resolved))
	}
}
