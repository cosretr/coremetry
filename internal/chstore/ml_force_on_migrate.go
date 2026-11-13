package chstore

// ml_force_on_migrate.go — v0.10.2074: op_latency + metric_kafka dedektörlerini
// kayıtlı ML ayarında BİR KEZ açar. Operatör kararı (2026-11-13): "iki ML
// dedektörünü prod'da zorla aç".
//
// Neden göç: v0.10.2059 / v0.10.2064 bu iki dedektörü yalnız boş blobda ve
// blobda HİÇ yazılmamış anahtarda varsayılan açık yaptı (mlDefaultOnDetectors);
// Ayarlar › ML'de bir kez Kaydet'e basılmış kurulumda blob `false`'u AÇIKÇA
// taşır ve yeni varsayılan oraya ulaşmaz. Inbox keep-list göçleriyle aynı
// desen (problem_priority_inbox_migrate.go):
//
//   - blob yok             → dokunma (varsayılan zaten açık)
//   - anahtar yok / true   → dokunma
//   - anahtar açıkça false → true + system audit (eski/yeni)
//
// İşaret `system_settings[ml_force_on_v1]`: varlığı = göç bitti; varken bir
// daha hiç koşmaz — operatör sonradan birini kapatırsa kapalı KALIR. Ana
// anahtar (enabled) ve diğer dedektörler/alanlar AYNEN korunur
// (json.RawMessage haritası üstünde yalnız detectors değişir). Çok-pod
// yarışı zararsız: aynı girdiden aynı çıktı.

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
	// mlForceOnMigrateKey — göçün system_settings işareti.
	mlForceOnMigrateKey = "ml_force_on_v1"
	// mlForceOnMigrateVersion — işaret + audit damgası.
	mlForceOnMigrateVersion = "v0.10.2074"
)

// mlForceOnDetectors — göçün açtığı dedektörler (operatör kararı; başka yok).
var mlForceOnDetectors = []string{"op_latency", "metric_kafka"}

// Göç sonuçları.
const (
	MLForceOnNoRow    = "no-row"   // ml blobu yok → varsayılan (açık)
	MLForceOnReplaced = "replaced" // en az bir dedektör false → true
	MLForceOnCurrent  = "current"  // hepsi zaten açık ya da anahtar yok (varsayılan açık)
)

// planMLForceOn — SAF: kayıtlı ml blobu → (yazılacak yeni blob ya da nil,
// sonuç, açılan dedektörler). Yalnız detectors[name] == false olan anahtar
// değişir; blobun diğer alanları bayt düzeyinde korunur.
func planMLForceOn(raw []byte, names []string) (newRaw []byte, outcome string, flipped []string, err error) {
	if len(raw) == 0 {
		return nil, MLForceOnNoRow, nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", nil, fmt.Errorf("ml blob'u çözülemedi: %w", err)
	}
	detRaw, ok := fields["detectors"]
	if !ok || string(detRaw) == "null" {
		return nil, MLForceOnCurrent, nil, nil
	}
	var dets map[string]bool
	if err := json.Unmarshal(detRaw, &dets); err != nil {
		return nil, "", nil, fmt.Errorf("ml.detectors çözülemedi: %w", err)
	}
	for _, n := range names {
		if on, present := dets[n]; present && !on {
			dets[n] = true
			flipped = append(flipped, n)
		}
	}
	if len(flipped) == 0 {
		return nil, MLForceOnCurrent, nil, nil
	}
	sort.Strings(flipped)
	next, err := json.Marshal(dets)
	if err != nil {
		return nil, "", nil, err
	}
	fields["detectors"] = next
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, "", nil, err
	}
	return out, MLForceOnReplaced, flipped, nil
}

// MigrateMLForceOn — tek seferlik göç. İdempotent: işaret varken hiçbir şey
// yapmaz ("" döner). Okuma/yazma düşerse işaret YAZILMAZ ve hata döner —
// çağıran sonraki turda yeniden dener. Audit yalnız blob değiştiyse.
func MigrateMLForceOn(ctx context.Context, st inboxKeepMigrateStore, now time.Time) (string, error) {
	marker, err := st.GetSetting(ctx, mlForceOnMigrateKey)
	if err != nil {
		// İşaret okunamadı — kör koşma: operatörün sonradan kapattığı
		// dedektörü yeniden açmak, bir tur beklemekten pahalı.
		return "", fmt.Errorf("read marker: %w", err)
	}
	if len(marker) > 0 {
		return "", nil
	}
	raw, err := st.GetSetting(ctx, mlSettingsKey)
	if err != nil {
		return "", fmt.Errorf("read ml: %w", err)
	}
	newRaw, outcome, flipped, err := planMLForceOn(raw, mlForceOnDetectors)
	if err != nil {
		return "", err
	}
	if newRaw != nil {
		if err := st.PutSetting(ctx, mlSettingsKey, newRaw); err != nil {
			return "", fmt.Errorf("write ml: %w", err)
		}
		from, to := map[string]bool{}, map[string]bool{}
		for _, n := range flipped {
			from[n], to[n] = false, true
		}
		details, _ := json.Marshal(map[string]any{
			"version": mlForceOnMigrateVersion, "from": from, "to": to,
			"reason": "operatör kararı 2026-11-13: op_latency ve metric_kafka bir kez açıldı (sonradan kapatılırsa kapalı kalır)",
		})
		if err := st.AppendAudit(ctx, AuditEntry{
			Time: now.UnixNano(), ActorID: "system", ActorEmail: "system", ActorRole: "system",
			Action: "settings.ml.migrate", TargetKind: "settings", TargetID: mlSettingsKey,
			Details: string(details),
		}); err != nil {
			log.Printf("[ml] ayar göçü: audit: %v", err)
		}
	}
	body, _ := json.Marshal(map[string]any{"version": mlForceOnMigrateVersion, "appliedAt": now.UnixNano(), "outcome": outcome, "flipped": flipped})
	if err := st.PutSetting(ctx, mlForceOnMigrateKey, body); err != nil {
		return outcome, fmt.Errorf("write marker: %w", err)
	}
	if outcome == MLForceOnReplaced {
		log.Printf("[ml] ayar göçü (%s): %v kayıtlı ayarda kapalıydı → açıldı (bir kez)", mlForceOnMigrateVersion, flipped)
	}
	return outcome, nil
}

var mlForceOnMigrated atomic.Bool

// MigrateMLForceOnOnce — ML kaydedicisinin (lider) tik öncesi adımı: süreç
// başına bir kez BAŞARIYLA biter; hata hâlinde sonraki tur yeniden dener.
func MigrateMLForceOnOnce(ctx context.Context, st inboxKeepMigrateStore) {
	if mlForceOnMigrated.Load() || st == nil {
		return
	}
	if _, err := MigrateMLForceOn(ctx, st, time.Now()); err != nil {
		log.Printf("[ml] ayar göçü (sonraki turda yeniden): %v", err)
		return
	}
	mlForceOnMigrated.Store(true)
}
