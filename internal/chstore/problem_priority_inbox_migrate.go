package chstore

// problem_priority_inbox_migrate.go — v0.10.1083: inbox istisna listesinin
// varsayılanı genişledi (dış kaynak hata serisi `anomaly:ext:*:ext:error_count`
// + kümesi `anomaly-cluster:ext:*` + tavan özeti `anomaly:ext-cap:*`). Operatör: "Oracle hataları da problemse
// hâlâ düşmüyor" → onay "Yap".
//
// Sorun: v0.10.1072'nin PUT'u listeyi NORMALIZE EDİLMİŞ hâliyle saklar — Ayarlar
// sayfasında bir kez Kaydet'e basılmış bir kurulumda blob eski varsayılanı
// AÇIKÇA taşır ve yeni varsayılan oraya hiç ulaşmaz (nil = varsayılan kuralı
// yalnız alan YOKKEN işler). Tek seferlik, EKLEYİCİ göç:
//
//   - alan yok / null           → dokunma (nil zaten yeni varsayılan)
//   - kayıtlı liste == ESKİ varsayılan (küme eşitliği, sıra önemsiz)
//                               → yeni varsayılanla değiştir + audit
//   - kayıtlı liste == yeni varsayılan → dokunma
//   - başka her şey (operatör özelleştirdi, `[]` dahil) → dokunma, BİR KEZ logla
//
// İşaret `system_settings[problem_priority_inbox_keep_v2]`: varlığı = göç bitti;
// varken bir daha hiç koşmaz (operatörün sonradan daralttığı liste geri
// genişletilmez). Blobun diğer alanları (bigBreachRatio, staleCriticalHours,
// bilinmeyen alanlar) AYNEN korunur — json.RawMessage haritası üstünde değişir.
// Çok-pod yarışı zararsız: iki pod aynı girdiden aynı çıktıyı yazar.
//
// v0.10.1091 — AYNI desenle ikinci adım (v3): varsayılana yaygın yavaşlama
// (`svc-slowdown:*`) eklendi; kayıtlı liste 1083 varsayılanına KÜME olarak
// eşitse yeniye taşınır, işaret `problem_priority_inbox_keep_v3`. Adımlar sırayla
// koşar (v2 → v3): v2 eski 1072 listesini doğrudan GÜNCEL varsayılana taşır,
// v3 o hâlde "current" der.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync/atomic"
	"time"
)

const (
	// inboxKeepMigrateKey — göçün system_settings işareti.
	inboxKeepMigrateKey = "problem_priority_inbox_keep_v2"
	// inboxKeepMigrateVersion — işaret + audit damgası.
	inboxKeepMigrateVersion = "v0.10.1083"
	// inboxKeepMigrateKeyV3 / inboxKeepMigrateVersionV3 — v0.10.1091 adımı.
	inboxKeepMigrateKeyV3     = "problem_priority_inbox_keep_v3"
	inboxKeepMigrateVersionV3 = "v0.10.1091"
	// inboxKeepField — problem_priority blobundaki alan adı (JSON etiketi).
	inboxKeepField = "inboxKeepSourcePriority"
)

// inboxKeepMigrateStep — tek göç adımı: işaret, sürüm, "önceki varsayılan"
// (kayıtlı liste buna KÜME olarak eşitse güncel varsayılana taşınır), audit
// gerekçesi ve özelleştirilmiş listede bir kez yazılan ipucu.
type inboxKeepMigrateStep struct {
	key, version string
	from         func() []string
	reason, hint string
}

var (
	inboxKeepStepV2 = inboxKeepMigrateStep{
		key: inboxKeepMigrateKey, version: inboxKeepMigrateVersion, from: legacyInboxKeepSourcePriority,
		reason: "inbox istisna listesi eski varsayılandaydı — yeni varsayılana taşındı (dış kaynak hata serisi + kümesi + tavan özeti)",
		hint:   "dış kaynak hata kalıplarını (" + InboxKeepExtErrorCount + ", " + InboxKeepExtCluster + ", " + InboxKeepExtCap + ") istersen elle ekle",
	}
	inboxKeepStepV3 = inboxKeepMigrateStep{
		key: inboxKeepMigrateKeyV3, version: inboxKeepMigrateVersionV3, from: inboxKeepDefault1083,
		reason: "inbox istisna listesi önceki (v0.10.1083) varsayılandaydı — yeni varsayılana taşındı (yaygın yavaşlama)",
		hint:   "yaygın yavaşlama kalıbını (" + InboxKeepSvcSlowdown + ") istersen elle ekle",
	}
)

// Göç sonuçları (işarete ve loga yazılır).
const (
	InboxKeepMigrateNoRow      = "no-row"     // problem_priority satırı yok
	InboxKeepMigrateDefault    = "default"    // alan yok/null → zaten yeni varsayılan
	InboxKeepMigrateReplaced   = "replaced"   // eski varsayılan → yeni varsayılan
	InboxKeepMigrateCurrent    = "current"    // zaten yeni varsayılan
	InboxKeepMigrateCustomised = "customised" // operatör listesi, dokunulmadı
)

// inboxKeepMigrateStore — göçün dokunduğu store yüzeyi (*Store karşılar;
// testler bellek içi sahteyle).
type inboxKeepMigrateStore interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
	AppendAudit(ctx context.Context, e AuditEntry) error
}

// inboxKeepMigrateMarker — işaretin JSON gövdesi (teşhis için).
type inboxKeepMigrateMarker struct {
	Version   string   `json:"version"`
	AppliedAt int64    `json:"appliedAt"`
	Outcome   string   `json:"outcome"`
	Stored    []string `json:"stored,omitempty"`
}

// sameStringSet — SAF: iki liste küme olarak eşit mi (sıra ve tekrar önemsiz).
func sameStringSet(a, b []string) bool {
	norm := func(in []string) []string {
		m := map[string]bool{}
		out := make([]string, 0, len(in))
		for _, v := range in {
			if !m[v] {
				m[v] = true
				out = append(out, v)
			}
		}
		sort.Strings(out)
		return out
	}
	x, y := norm(a), norm(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// planInboxKeepMigration — SAF: kayıtlı problem_priority blobu → (yazılacak
// yeni blob ya da nil, sonuç, kayıtlı liste). Karşılaştırma NORMALIZE edilmiş
// listeyle (elle düzenlenmiş boşluklu/tekrarlı eski varsayılan da eski sayılır).
func planInboxKeepMigration(raw []byte) (newRaw []byte, outcome string, stored []string, err error) {
	return planInboxKeepMigrationFrom(raw, legacyInboxKeepSourcePriority())
}

// planInboxKeepMigrationFrom — planInboxKeepMigration'ın "önceki varsayılan"ı
// parametreli gövdesi (v0.10.1091: v2 ve v3 adımları aynı planı kullanır).
func planInboxKeepMigrationFrom(raw []byte, from []string) (newRaw []byte, outcome string, stored []string, err error) {
	if len(raw) == 0 {
		return nil, InboxKeepMigrateNoRow, nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", nil, fmt.Errorf("problem_priority blob'u çözülemedi: %w", err)
	}
	listRaw, ok := fields[inboxKeepField]
	if !ok || string(listRaw) == "null" {
		return nil, InboxKeepMigrateDefault, nil, nil
	}
	var list []string
	if err := json.Unmarshal(listRaw, &list); err != nil {
		return nil, "", nil, fmt.Errorf("%s çözülemedi: %w", inboxKeepField, err)
	}
	stored = NormalizeInboxKeepSourcePriority(list)
	switch {
	case sameStringSet(stored, DefaultInboxKeepSourcePriority()):
		return nil, InboxKeepMigrateCurrent, stored, nil
	case len(stored) > 0 && sameStringSet(stored, from):
		next, err := json.Marshal(DefaultInboxKeepSourcePriority())
		if err != nil {
			return nil, "", stored, err
		}
		fields[inboxKeepField] = next
		out, err := json.Marshal(fields)
		if err != nil {
			return nil, "", stored, err
		}
		return out, InboxKeepMigrateReplaced, stored, nil
	}
	return nil, InboxKeepMigrateCustomised, stored, nil
}

// MigrateInboxKeepDefaults — tek seferlik göç (v0.10.1083 adımı, v2).
// İdempotent: işaret varken hiçbir şey yapmaz ("" döner). Hata yönü: herhangi
// bir okuma/yazma düşerse işaret YAZILMAZ ve hata döner — çağıran sonraki turda
// yeniden dener (aynı girdiden aynı çıktı, çift iş yok). Audit yalnız liste
// değiştiyse, en-iyi-çaba.
func MigrateInboxKeepDefaults(ctx context.Context, st inboxKeepMigrateStore, now time.Time) (string, error) {
	return migrateInboxKeepStep(ctx, st, now, inboxKeepStepV2)
}

// MigrateInboxKeepDefaultsV3 — v0.10.1091 adımı (yaygın yavaşlama); aynı
// sözleşme, ayrı işaret.
func MigrateInboxKeepDefaultsV3(ctx context.Context, st inboxKeepMigrateStore, now time.Time) (string, error) {
	return migrateInboxKeepStep(ctx, st, now, inboxKeepStepV3)
}

func migrateInboxKeepStep(ctx context.Context, st inboxKeepMigrateStore, now time.Time, step inboxKeepMigrateStep) (string, error) {
	marker, err := st.GetSetting(ctx, step.key)
	if err != nil {
		// İşaret okunamadı — kör koşma: operatörün sonradan daralttığı listeyi
		// genişletmek, bir tur beklemekten pahalı.
		return "", fmt.Errorf("read marker: %w", err)
	}
	if len(marker) > 0 {
		return "", nil
	}
	raw, err := st.GetSetting(ctx, problemPriorityKey)
	if err != nil {
		return "", fmt.Errorf("read problem_priority: %w", err)
	}
	newRaw, outcome, stored, err := planInboxKeepMigrationFrom(raw, step.from())
	if err != nil {
		return "", err
	}
	if newRaw != nil {
		if err := st.PutSetting(ctx, problemPriorityKey, newRaw); err != nil {
			return "", fmt.Errorf("write problem_priority: %w", err)
		}
		details, _ := json.Marshal(map[string]any{
			"version": step.version,
			"from":    stored,
			"to":      DefaultInboxKeepSourcePriority(),
			"reason":  step.reason,
		})
		// Aktör "system": göç bir HTTP isteğinden değil ayar tazeleyicisinden
		// gelir (builtins_default_off emsali). Düşerse loglanır, geri alınmaz.
		if err := st.AppendAudit(ctx, AuditEntry{
			Time: now.UnixNano(), ActorID: "system", ActorEmail: "system", ActorRole: "system",
			Action: "settings.problem_priority.migrate", TargetKind: "settings", TargetID: problemPriorityKey,
			Details: string(details),
		}); err != nil {
			log.Printf("[settings] problem_priority göçü: audit: %v", err)
		}
	}
	body, _ := json.Marshal(inboxKeepMigrateMarker{Version: step.version, AppliedAt: now.UnixNano(), Outcome: outcome, Stored: stored})
	if err := st.PutSetting(ctx, step.key, body); err != nil {
		return outcome, fmt.Errorf("write marker: %w", err)
	}
	switch outcome {
	case InboxKeepMigrateReplaced:
		log.Printf("[settings] problem_priority (%s): inbox istisna listesi önceki varsayılandaydı → yeni varsayılan %v", step.version, DefaultInboxKeepSourcePriority())
	case InboxKeepMigrateCustomised:
		log.Printf("[settings] problem_priority (%s): inbox istisna listesi özelleştirilmiş %v — dokunulmadı; %s",
			step.version, stored, step.hint)
	}
	return outcome, nil
}

// inboxKeepMigrated — süreç başına göç bitti mi (başarılı tur sonrası true).
var inboxKeepMigrated atomic.Bool

// MigrateInboxKeepDefaultsOnce — ayar tazeleyicisinin adımı: süreç başına bir
// kez BAŞARIYLA biter; hata hâlinde sonraki tur (30 sn) yeniden dener.
func MigrateInboxKeepDefaultsOnce(ctx context.Context, st inboxKeepMigrateStore) {
	if inboxKeepMigrated.Load() || st == nil {
		return
	}
	// v0.10.1091 — iki adım SIRAYLA (v2 → v3); v2 düşerse v3 o tur koşmaz
	// (v3'ün "önceki varsayılan" kıyası v2'nin sonucuna dayanır).
	now := time.Now()
	if _, err := MigrateInboxKeepDefaults(ctx, st, now); err != nil {
		log.Printf("[settings] problem_priority göçü (sonraki turda yeniden): %v", err)
		return
	}
	if _, err := MigrateInboxKeepDefaultsV3(ctx, st, now); err != nil {
		log.Printf("[settings] problem_priority göçü v3 (sonraki turda yeniden): %v", err)
		return
	}
	inboxKeepMigrated.Store(true)
}
