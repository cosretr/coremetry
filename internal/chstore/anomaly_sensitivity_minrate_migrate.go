package chstore

// anomaly_sensitivity_minrate_migrate.go — v0.10.1585 (Davis paritesi FAZ
// 0.5 / A, operatör onayı 2026-10-29): minBaselineRate varsayılanı error_rate
// için 0.2, p99_ms için 0.5 istek/sn.
//
// Sorun: NormalizeAnomalySensitivity her metriğin alanlarını PUT'ta
// somutlaştırır; kayıtlı 0 "operatör 0 seçti" diye korunur (clampFloor yalnız
// negatif/NaN'ı varsayılana çevirir). Bu sürüme kadar varsayılan da 0'dı ve
// Ayarlar sayfası her kayıtta alanı gönderir → Kaydet'e bir kez basılmış her
// kurulumun blobu "minBaselineRate": 0 taşır; yeni varsayılan oraya hiç
// ulaşmazdı. O dönemin 0'ı varsayılandan AYIRT EDİLEMEZ → varsayılan sayılır.
// Tek seferlik göç (yalnız error_rate ve p99_ms):
//
//   - satır yok / metrics yok            → dokunma (Normalize yeni varsayılanı verir)
//   - metrik nesnesi yok                 → dokunma (aynı gerekçe)
//   - alan 0 ya da metrik nesnesinde yok → yeni taban (alan yoksa Go 0 okur)
//   - alan sıfır değil                   → dokunma (operatörün değeri)
//
// İşaret `system_settings[anomaly_sensitivity_minrate_v2]`: varken bir daha
// koşmaz (göçten sonra 0'a çeken operatörün değeri kalır). Blobun diğer
// alanları ve metrik nesnelerinin diğer anahtarları AYNEN korunur
// (json.RawMessage haritası; opLatency göçü emsali). Çok-pod yarışı zararsız.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	minRateMigrateKey     = "anomaly_sensitivity_minrate_v2"
	minRateMigrateVersion = "v0.10.1585"
	minRateField          = "minBaselineRate"
	minRateMetricsField   = "metrics"
)

// Göç sonuçları (işarete ve loga yazılır).
const (
	MinRateMigrateNoRow      = "no-row"     // anomaly_sensitivity satırı yok
	MinRateMigrateDefault    = "default"    // metrics alanı yok → varsayılan zaten yeni
	MinRateMigrateCustomised = "customised" // iki metrik de sıfır olmayan değer taşıyor
	MinRateMigrateApplied    = "applied"    // en az bir metrik 0 → yeni taban
)

// minRateMigrationTargets — göçün yazdığı metrik → yeni taban.
var minRateMigrationTargets = []struct {
	metric string
	rate   float64
}{
	{"error_rate", MinBaselineRateErrorDefault},
	{"p99_ms", MinBaselineRateP99Default},
}

// planMinRateMigration — SAF: kayıtlı blob → (yazılacak yeni blob ya da nil,
// sonuç, değişen metrikler).
func planMinRateMigration(raw []byte) (newRaw []byte, outcome string, err error) {
	newRaw, outcome, _, err = planMinRateMigrationChanged(raw)
	return newRaw, outcome, err
}

func planMinRateMigrationChanged(raw []byte) ([]byte, string, []string, error) {
	if len(raw) == 0 {
		return nil, MinRateMigrateNoRow, nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", nil, fmt.Errorf("anomaly_sensitivity blob'u çözülemedi: %w", err)
	}
	mraw, ok := fields[minRateMetricsField]
	if !ok || string(mraw) == "null" {
		return nil, MinRateMigrateDefault, nil, nil
	}
	var metrics map[string]map[string]json.RawMessage
	if err := json.Unmarshal(mraw, &metrics); err != nil {
		return nil, "", nil, fmt.Errorf("%s çözülemedi: %w", minRateMetricsField, err)
	}
	var changed []string
	for _, tgt := range minRateMigrationTargets {
		obj, ok := metrics[tgt.metric]
		if !ok || obj == nil {
			continue // Normalize eksik metriğe yeni varsayılanı verir
		}
		cur := 0.0
		if v, ok := obj[minRateField]; ok && string(v) != "null" {
			if err := json.Unmarshal(v, &cur); err != nil {
				return nil, "", nil, fmt.Errorf("%s.%s çözülemedi: %w", tgt.metric, minRateField, err)
			}
		}
		if cur != 0 {
			continue
		}
		obj[minRateField] = json.RawMessage(strconv.FormatFloat(tgt.rate, 'f', -1, 64))
		changed = append(changed, tgt.metric)
	}
	if len(changed) == 0 {
		return nil, MinRateMigrateCustomised, nil, nil
	}
	mout, err := json.Marshal(metrics)
	if err != nil {
		return nil, "", nil, err
	}
	fields[minRateMetricsField] = mout
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, "", nil, err
	}
	return out, MinRateMigrateApplied, changed, nil
}

// MigrateMinRateDefault — tek seferlik göç. İdempotent: işaret varken hiçbir
// şey yapmaz ("" döner). Okuma/yazma düşerse işaret YAZILMAZ, hata döner —
// çağıran sonraki turda yeniden dener. Audit yalnız blob değiştiyse.
func MigrateMinRateDefault(ctx context.Context, st inboxKeepMigrateStore, now time.Time) (string, error) {
	marker, err := st.GetSetting(ctx, minRateMigrateKey)
	if err != nil {
		return "", fmt.Errorf("read marker: %w", err)
	}
	if len(marker) > 0 {
		return "", nil
	}
	raw, err := st.GetSetting(ctx, anomalySensitivityKey)
	if err != nil {
		return "", fmt.Errorf("read anomaly_sensitivity: %w", err)
	}
	newRaw, outcome, changed, err := planMinRateMigrationChanged(raw)
	if err != nil {
		return "", err
	}
	if newRaw != nil {
		if err := st.PutSetting(ctx, anomalySensitivityKey, newRaw); err != nil {
			return "", fmt.Errorf("write anomaly_sensitivity: %w", err)
		}
		details, _ := json.Marshal(map[string]any{
			"version": minRateMigrateVersion,
			"metrics": changed,
			"to": map[string]float64{
				"error_rate": MinBaselineRateErrorDefault,
				"p99_ms":     MinBaselineRateP99Default,
			},
			"reason": "minBaselineRate eski varsayılandaydı (0 = kapı kapalı) — düşük trafikli seride tek tük istekten anomali açılmasın diye metrik başına taban",
		})
		if err := st.AppendAudit(ctx, AuditEntry{
			Time: now.UnixNano(), ActorID: "system", ActorEmail: "system", ActorRole: "system",
			Action: "settings.anomaly_sensitivity.migrate", TargetKind: "settings", TargetID: anomalySensitivityKey,
			Details: string(details),
		}); err != nil {
			log.Printf("[settings] anomaly_sensitivity minBaselineRate göçü: audit: %v", err)
		}
		log.Printf("[settings] anomaly_sensitivity: minBaselineRate 0 → taban (%v)", changed)
	}
	body, _ := json.Marshal(opLatencyMigrateMarker{Version: minRateMigrateVersion, AppliedAt: now.UnixNano(), Outcome: outcome})
	if err := st.PutSetting(ctx, minRateMigrateKey, body); err != nil {
		return outcome, fmt.Errorf("write marker: %w", err)
	}
	return outcome, nil
}

var minRateMigrated atomic.Bool

// MigrateMinRateDefaultOnce — ayar tazeleyicisinin adımı: süreç başına bir
// kez BAŞARIYLA biter; hata hâlinde sonraki tur (30 sn) yeniden dener.
func MigrateMinRateDefaultOnce(ctx context.Context, st inboxKeepMigrateStore) {
	if minRateMigrated.Load() || st == nil {
		return
	}
	if _, err := MigrateMinRateDefault(ctx, st, time.Now()); err != nil {
		log.Printf("[settings] anomaly_sensitivity minBaselineRate göçü (sonraki turda yeniden): %v", err)
		return
	}
	minRateMigrated.Store(true)
}
