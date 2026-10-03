package chstore

// anomaly_sensitivity_oplatency_migrate.go — v0.10.1085: trace_op_latency
// varsayılanı yeniden AÇIK (iki ardışık kova sürdürme kuralıyla). Operatör,
// sürdürme kuralı önerisine: "Önerini yapalım".
//
// Sorun: v0.10.1056'nın Normalize'ı nil'i false'a SOMUTLAŞTIRIYORDU ve Ayarlar
// sayfası da her kayıtta açık boolean gönderiyordu — o dönemde Kaydet'e bir kez
// basılmış her kurulumun blobu `"opLatency": false` taşır, operatör kutuya hiç
// dokunmamış olsa da. Yeni varsayılan (nil = açık) oraya hiç ulaşmazdı. O
// dönemin false'u "operatör kapattı" ile "varsayılan" arasında AYIRT EDİLEMEZ;
// varsayılan da kapalıydı ve operatör şimdi açılmasını onayladı → varsayılan
// sayılır. Tek seferlik göç:
//
//   - satır yok                         → dokunma (varsayılan zaten açık)
//   - alan yok / null                   → dokunma (nil = açık)
//   - opLatency true                    → dokunma
//   - opLatencyDwellBuckets alanı VAR   → dokunma: blob bu sürümde kaydedildi,
//                                         false operatörün BU sürümdeki kararı
//   - opLatency false, dwell alanı yok  → alan SİLİNİR (nil = açık) + audit
//
// İşaret `system_settings[anomaly_sensitivity_oplatency_v2]`: varlığı = göç
// bitti; varken bir daha koşmaz (göçten sonra kapatan operatörün false'u
// kalır). Blobun diğer alanları AYNEN korunur — json.RawMessage haritası
// üstünde değişir (problem_priority_inbox_migrate.go emsali). Çok-pod yarışı
// zararsız: iki pod aynı girdiden aynı çıktıyı yazar.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

const (
	// opLatencyMigrateKey — göçün system_settings işareti.
	opLatencyMigrateKey = "anomaly_sensitivity_oplatency_v2"
	// opLatencyMigrateVersion — işaret + audit damgası.
	opLatencyMigrateVersion = "v0.10.1085"
	// opLatencyField / opLatencyDwellField — anomaly_sensitivity blobundaki
	// alan adları (JSON etiketleri).
	opLatencyField      = "opLatency"
	opLatencyDwellField = "opLatencyDwellBuckets"
)

// Göç sonuçları (işarete ve loga yazılır).
const (
	OpLatencyMigrateNoRow      = "no-row"     // anomaly_sensitivity satırı yok
	OpLatencyMigrateDefault    = "default"    // alan yok/null → zaten açık
	OpLatencyMigrateOn         = "on"         // açıkça true
	OpLatencyMigrateCustomised = "customised" // bu sürümde kaydedilmiş false, dokunulmadı
	OpLatencyMigrateReenabled  = "reenabled"  // 1056 dönemi false → nil (açık)
)

// opLatencyMigrateStore — göçün dokunduğu store yüzeyi (*Store karşılar;
// testler bellek içi sahteyle). inbox göçüyle aynı yüzey.
type opLatencyMigrateStore = inboxKeepMigrateStore

// opLatencyMigrateMarker — işaretin JSON gövdesi (teşhis için).
type opLatencyMigrateMarker struct {
	Version   string `json:"version"`
	AppliedAt int64  `json:"appliedAt"`
	Outcome   string `json:"outcome"`
}

// planOpLatencyMigration — SAF: kayıtlı anomaly_sensitivity blobu → (yazılacak
// yeni blob ya da nil, sonuç).
func planOpLatencyMigration(raw []byte) (newRaw []byte, outcome string, err error) {
	if len(raw) == 0 {
		return nil, OpLatencyMigrateNoRow, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", fmt.Errorf("anomaly_sensitivity blob'u çözülemedi: %w", err)
	}
	v, ok := fields[opLatencyField]
	if !ok || string(v) == "null" {
		return nil, OpLatencyMigrateDefault, nil
	}
	var on bool
	if err := json.Unmarshal(v, &on); err != nil {
		return nil, "", fmt.Errorf("%s çözülemedi: %w", opLatencyField, err)
	}
	if on {
		return nil, OpLatencyMigrateOn, nil
	}
	if _, saved := fields[opLatencyDwellField]; saved {
		return nil, OpLatencyMigrateCustomised, nil
	}
	delete(fields, opLatencyField)
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, "", err
	}
	return out, OpLatencyMigrateReenabled, nil
}

// MigrateOpLatencyDefault — tek seferlik göç. İdempotent: işaret varken hiçbir
// şey yapmaz ("" döner). Hata yönü: herhangi bir okuma/yazma düşerse işaret
// YAZILMAZ ve hata döner — çağıran sonraki turda yeniden dener (aynı girdiden
// aynı çıktı). Audit yalnız blob değiştiyse, en-iyi-çaba.
func MigrateOpLatencyDefault(ctx context.Context, st opLatencyMigrateStore, now time.Time) (string, error) {
	marker, err := st.GetSetting(ctx, opLatencyMigrateKey)
	if err != nil {
		// İşaret okunamadı — kör koşma: göçten sonra kapatılmış dedektörü
		// yeniden açmak, bir tur beklemekten pahalı.
		return "", fmt.Errorf("read marker: %w", err)
	}
	if len(marker) > 0 {
		return "", nil
	}
	raw, err := st.GetSetting(ctx, anomalySensitivityKey)
	if err != nil {
		return "", fmt.Errorf("read anomaly_sensitivity: %w", err)
	}
	newRaw, outcome, err := planOpLatencyMigration(raw)
	if err != nil {
		return "", err
	}
	if newRaw != nil {
		if err := st.PutSetting(ctx, anomalySensitivityKey, newRaw); err != nil {
			return "", fmt.Errorf("write anomaly_sensitivity: %w", err)
		}
		details, _ := json.Marshal(map[string]any{
			"version": opLatencyMigrateVersion,
			"from":    false,
			"to":      nil,
			"reason":  "operasyon gecikmesi anahtarı v0.10.1056 varsayılanındaydı (kapalı) — iki ardışık kova sürdürme kuralıyla varsayılan açığa alındı",
		})
		// Aktör "system": göç ayar tazeleyicisinden gelir (inbox göçü emsali).
		if err := st.AppendAudit(ctx, AuditEntry{
			Time: now.UnixNano(), ActorID: "system", ActorEmail: "system", ActorRole: "system",
			Action: "settings.anomaly_sensitivity.migrate", TargetKind: "settings", TargetID: anomalySensitivityKey,
			Details: string(details),
		}); err != nil {
			log.Printf("[settings] anomaly_sensitivity göçü: audit: %v", err)
		}
	}
	body, _ := json.Marshal(opLatencyMigrateMarker{Version: opLatencyMigrateVersion, AppliedAt: now.UnixNano(), Outcome: outcome})
	if err := st.PutSetting(ctx, opLatencyMigrateKey, body); err != nil {
		return outcome, fmt.Errorf("write marker: %w", err)
	}
	switch outcome {
	case OpLatencyMigrateReenabled:
		log.Printf("[settings] anomaly_sensitivity: operasyon gecikmesi v0.10.1056 varsayılanındaydı (kapalı) → açık, %d ardışık kova sürdürme kuralıyla", opLatencyDwellDefault)
	case OpLatencyMigrateCustomised:
		log.Printf("[settings] anomaly_sensitivity: operasyon gecikmesi bu sürümde kapalı kaydedilmiş — dokunulmadı")
	}
	return outcome, nil
}

// opLatencyMigrated — süreç başına göç bitti mi (başarılı tur sonrası true).
var opLatencyMigrated atomic.Bool

// MigrateOpLatencyDefaultOnce — ayar tazeleyicisinin adımı: süreç başına bir
// kez BAŞARIYLA biter; hata hâlinde sonraki tur (30 sn) yeniden dener.
func MigrateOpLatencyDefaultOnce(ctx context.Context, st opLatencyMigrateStore) {
	if opLatencyMigrated.Load() || st == nil {
		return
	}
	if _, err := MigrateOpLatencyDefault(ctx, st, time.Now()); err != nil {
		log.Printf("[settings] anomaly_sensitivity göçü (sonraki turda yeniden): %v", err)
		return
	}
	opLatencyMigrated.Store(true)
}
