package evaluator

// builtins_default_off.go — v0.10.1069: yerleşik alarm kuralları
// varsayılan KAPALI. Operatör (prod, Alert rules sayfası): "Built-in
// alertleri kaldıralım, çok false pozitif geliyor." Kanıt: "HTTP P99
// latency >3s (sustained 10 min)" 24 saatte 340×, "HTTP P99 latency >5s
// (5 min)" 215× açıldı; ikisinden 26 + 12 açık problem kalmıştı. Binlerce
// servisin gecikme profili birbirinden çok farklı — filo geneli MUTLAK
// eşik gürültüdür.
//
// Bu bir varsayılan değişikliğidir, özellik kaldırma DEĞİL: tanımlar ve
// satırlar durur, operatör Alert rules sayfasından istediğini açar
// (BUILT-IN · OFF). İki parça:
//
//  1. Yeni kurulum: `builtins` dilimi Enabled:false gemiye biner
//     (seedBuiltinRules yalnız EKSİK satırı eker, mevcut satıra dokunmaz).
//  2. Yükseltme: TEK SEFERLİK göç (applyBuiltinsDefaultOff). Lider tikinde
//     koşar — her replika değil, böylece iki pod aynı anda iki audit
//     satırı yazmaz. Bitince system_settings'e işaret bırakır; işaret
//     varken göç bir daha HİÇ koşmaz, yani operatörün sonradan elle
//     açtığı kural yeniden kapatılmaz. (deprecatedBuiltinIDs emsali her
//     boot'ta yeniden kapatır — işaretsizdir; burada bilinçli olarak
//     ondan ayrılıyoruz, çünkü bu kurallar emekli değil, isteğe bağlı.)
//
// Açık problemler: kapatılan kuralın satırları bugün kimse tarafından
// kapatılmaz — evaluateAll devre dışı kuralı atlar, bayat süpürme ~3
// aralık sonra "source silent" gerekçesiyle kapatır (yalan gerekçe:
// kaynak susmadı, kural kapandı). Göç onları AYNI kapatma yolundan
// (MarkResolved + UpsertProblem; Value ezilmez — v0.9.977) dürüst
// gerekçeyle hemen kapatır; incident'ları aynı tikin sonundaki kaskad
// (cascadeResolveIncidents) normal kapanıştaki gibi kapatır.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// builtinsDefaultOffKey — göçün system_settings işareti. Varlığı =
	// "göç bitti"; içerik yalnız teşhis içindir (hangi sürüm, ne kapattı).
	builtinsDefaultOffKey = "builtin_rules_default_off"
	// builtinsDefaultOffVersion — sürüm damgası (işaret + audit + gerekçe).
	builtinsDefaultOffVersion = "v0.10.1069"
	// builtinsDefaultOffReason — kapatılan problemlerin açıklamasına
	// eklenen gerekçe (appendResolveSuffix: " · auto-resolved: <gerekçe>").
	builtinsDefaultOffReason = "rule disabled by default " + builtinsDefaultOffVersion
	// builtinsDefaultOffAction — audit eylem adı ("kind.action" deseni).
	builtinsDefaultOffAction = "alert_rule.builtin_default_off"
)

// builtinsOffStore — göçün dokunduğu store yüzeyi. *chstore.Store bunu
// karşılar; testler bellek içi sahteyle koşar (CH'siz).
type builtinsOffStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
	InvalidateAlertRulesCache()
	ListAlertRules(ctx context.Context) ([]chstore.AlertRule, error)
	SetAlertRuleEnabled(ctx context.Context, id string, enabled bool) error
	OpenProblemsSnapshot(ctx context.Context) (*chstore.OpenProblems, error)
	UpsertProblem(ctx context.Context, p chstore.Problem) error
	AppendAudit(ctx context.Context, e chstore.AuditEntry) error
}

// builtinsDefaultOffMarker — işaretin JSON gövdesi.
type builtinsDefaultOffMarker struct {
	Version          string   `json:"version"`
	AppliedAt        int64    `json:"appliedAt"`
	Disabled         []string `json:"disabled"`
	ResolvedProblems int      `json:"resolvedProblems"`
}

// builtinsOffResult — bir göç koşusunun sonucu. Ran=false: işaret zaten
// vardı, hiçbir şey yapılmadı.
type builtinsOffResult struct {
	Ran      bool
	Disabled []string
	Resolved []chstore.Problem
}

// isBuiltinRule — SAF: yerleşik kural mı? Bayrak ya da önek; eski bir
// sürümün bayraksız yazdığı builtin-* satırı da kapsansın.
func isBuiltinRule(r chstore.AlertRule) bool {
	return r.BuiltIn || strings.HasPrefix(r.ID, "builtin-")
}

// builtinsToDisable — SAF: şu an AÇIK olan yerleşik kuralların id'leri,
// sıralı (audit metni deterministik olsun). Kullanıcı kuralı asla.
func builtinsToDisable(rules []chstore.AlertRule) []string {
	var out []string
	for _, r := range rules {
		if r.Enabled && isBuiltinRule(r) {
			out = append(out, r.ID)
		}
	}
	sort.Strings(out)
	return out
}

// builtinProblemsToResolve — SAF: kapatılacak açık problemler — RuleID'si
// yerleşik bir kuralın id'si olanlar. Göç anında her yerleşik kural kapalı
// olduğundan (az önce kapattık ya da zaten kapalıydı) küme TÜM yerleşik
// id'lerdir; böylece yarıda kalmış bir koşunun yeniden denemesi, ilk
// koşunun kapattığı kuralların problemlerini de toplar. Kullanıcı
// kuralının problemi asla.
func builtinProblemsToResolve(rules []chstore.AlertRule, open []*chstore.Problem) []chstore.Problem {
	ids := make(map[string]bool)
	for _, r := range rules {
		if isBuiltinRule(r) {
			ids[r.ID] = true
		}
	}
	var out []chstore.Problem
	for _, p := range open {
		if p == nil || !ids[p.RuleID] {
			continue
		}
		out = append(out, *p) // kopya — snapshot işaretçisi salt-okunur (v0.10.156)
	}
	return out
}

// applyBuiltinsDefaultOff — tek seferlik göç. İdempotent: işaret varken
// hiçbir şey yapmaz. Hata yönü: herhangi bir okuma/yazma düşerse işaret
// YAZILMAZ ve hata döner — bir sonraki tik yeniden dener (zaten kapalı
// kural ve zaten kapanmış problem ikinci koşuda listeye girmez, yani
// yeniden deneme çift iş yapmaz). Audit tek satır ve yalnız bir şey
// değiştiyse (yeni kurulumda kapatılacak bir şey yok → satır yok).
func applyBuiltinsDefaultOff(ctx context.Context, st builtinsOffStore, now time.Time) (builtinsOffResult, error) {
	var res builtinsOffResult
	marker, err := st.GetSetting(ctx, builtinsDefaultOffKey)
	if err != nil {
		// İşaret okunamadı — kör koşma: operatörün açtığı kuralı yeniden
		// kapatmak, bir tik beklemekten pahalı.
		return res, fmt.Errorf("read marker: %w", err)
	}
	if len(marker) > 0 {
		return res, nil
	}
	res.Ran = true

	// 30 s'lik süreç önbelleği başka pod'un yazımını görmeyebilir; göç
	// taze okumayla karar verir.
	st.InvalidateAlertRulesCache()
	rules, err := st.ListAlertRules(ctx)
	if err != nil {
		return res, fmt.Errorf("list rules: %w", err)
	}

	for _, id := range builtinsToDisable(rules) {
		if err := st.SetAlertRuleEnabled(ctx, id, false); err != nil {
			return res, fmt.Errorf("disable %s: %w", id, err)
		}
		res.Disabled = append(res.Disabled, id)
	}

	snap, err := st.OpenProblemsSnapshot(ctx)
	if err != nil {
		return res, fmt.Errorf("open problems: %w", err)
	}
	nowNs := now.UnixNano()
	var failed int
	for _, p := range builtinProblemsToResolve(rules, snap.All()) {
		// Evaluator'ın normal kapatma yolu (evaluateOne !breached dalı /
		// bayat süpürme): MarkResolved Value'yu ezmez, satırın geri kalanı
		// snapshot'tan aynen taşınır (tam satır değiştirme, invariant #4).
		chstore.MarkResolved(&p, nowNs)
		p.Description = appendResolveSuffix(p.Description, builtinsDefaultOffReason)
		if err := st.UpsertProblem(ctx, p); err != nil {
			log.Printf("[evaluator] builtin default-off: resolve %s: %v", p.ID, err)
			failed++
			continue
		}
		res.Resolved = append(res.Resolved, p)
	}

	if len(res.Disabled) > 0 || len(res.Resolved) > 0 {
		details, _ := json.Marshal(map[string]any{
			"version":          builtinsDefaultOffVersion,
			"disabled":         nonNilStrings(res.Disabled),
			"resolvedProblems": len(res.Resolved),
			"reason":           "yerleşik kurallar varsayılan kapalı (operatör kararı)",
		})
		// Aktör "system": göç bir HTTP isteğinden değil boot sonrası lider
		// tikinden gelir. Audit en-iyi-çaba (s.audit ile aynı sözleşme) —
		// düşerse loglanır, göç geri alınmaz.
		if err := st.AppendAudit(ctx, chstore.AuditEntry{
			Time:       nowNs,
			ActorID:    "system",
			ActorEmail: "system",
			ActorRole:  "system",
			Action:     builtinsDefaultOffAction,
			TargetKind: "alert_rule",
			TargetID:   strings.Join(res.Disabled, ","),
			Details:    string(details),
		}); err != nil {
			log.Printf("[evaluator] builtin default-off: audit: %v", err)
		}
	}
	if failed > 0 {
		// İşaret yok → sonraki tik kapanmamış kalanları yeniden dener.
		return res, fmt.Errorf("%d problem(s) could not be resolved", failed)
	}

	body, _ := json.Marshal(builtinsDefaultOffMarker{
		Version:          builtinsDefaultOffVersion,
		AppliedAt:        nowNs,
		Disabled:         nonNilStrings(res.Disabled),
		ResolvedProblems: len(res.Resolved),
	})
	if err := st.PutSetting(ctx, builtinsDefaultOffKey, body); err != nil {
		return res, fmt.Errorf("write marker: %w", err)
	}
	return res, nil
}

// nonNilStrings — JSON'da `null` yerine `[]` yazılsın (teşhis okunaklı).
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// builtinsDefaultOffStep — lider tikinin göç adımı. Süreç başına bir kez
// başarılı biter; hata hâlinde sonraki tik yeniden dener. evaluateAll'dan
// ÖNCE koşar: aynı tikte kapatılan kural değerlendirilmez, tikin sonundaki
// incident kaskadı kapanan problemlerin incident'larını aynı tikte kapatır.
func (e *Evaluator) builtinsDefaultOffStep(ctx context.Context) {
	if e.builtinsOffDone.Load() || e.store == nil {
		return
	}
	res, err := applyBuiltinsDefaultOff(ctx, e.store, time.Now())
	// Kapanan her problem için normal kapanışın yan etkileri: cooldown
	// damgası silinir (kural yeniden açılırsa ilk gerçek ihlal hemen
	// problem açsın — bayat süpürme emsali) + kalp atışı sayacı.
	for _, p := range res.Resolved {
		e.clearResolved(ctx, breachKey{RuleID: p.RuleID, Service: p.Service})
		e.countResolved()
		log.Printf("[evaluator] PROBLEM AUTO-RESOLVED (%s): %s · %s", builtinsDefaultOffReason, p.Service, p.RuleName)
	}
	if err != nil {
		log.Printf("[evaluator] builtin default-off migration (retry next tick): %v", err)
		return
	}
	e.builtinsOffDone.Store(true)
	if res.Ran {
		log.Printf("[evaluator] builtin default-off migration applied: disabled=%v resolved=%d", res.Disabled, len(res.Resolved))
	}
}
