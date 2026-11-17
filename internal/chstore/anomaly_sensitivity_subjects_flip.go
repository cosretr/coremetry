package chstore

// anomaly_sensitivity_subjects_flip.go — v0.10.2308 (operatör: Davis
// özneleri ile ML bir hafta yan yana koşsun; yükseltme elle adım
// istemesin). DefaultAnomalySubjects artık endpoint + queue açık, terfi
// gölge. Ama varsayılan yalnız blob YOKKEN uygulanır; kayıtlı blobun
// subjects bölümü Normalize'dan geçer ve eksik bölüm sıfır değerdir (üçü
// kapalı). Yeni varsayılan mevcut kurulumlara bu TEK SEFERLİK geçişle
// ulaşır (anomaly_sensitivity_minrate_migrate.go işaret + system audit
// emsali; koşturucu topology_hidden_seed.go gibi lider-yalnız):
//
//   - satır yok                                  → dokunma (varsayılan zaten yeni)
//   - bölüm bakir (üç tür kapalı VE kip off/boş) → endpoint + queue açık, kip shadow
//   - herhangi bir tür açık ya da kip shadow/live → dokunma (operatörün seçimi)
//
// Üç durumda da işaret yazılır `system_settings[anomaly_subjects_flip_v1]`:
// varken bir daha koşmaz — operatör sonra elle kapatırsa kapalı kalır.
// db türü hiçbir durumda açılmaz; kip ASLA "live" yapılmaz. Blobun diğer
// alanları ve tür nesnelerinin diğer anahtarları AYNEN korunur
// (json.RawMessage haritası).

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	subjectsFlipKey     = "anomaly_subjects_flip_v1"
	subjectsFlipVersion = "v0.10.2308"
	subjectsField       = "subjects"
)

// Geçiş sonuçları (işarete ve loga yazılır).
const (
	SubjectsFlipNoRow      = "no-row"     // anomaly_sensitivity satırı yok
	SubjectsFlipApplied    = "applied"    // bakir bölüm → endpoint + queue, gölge
	SubjectsFlipCustomised = "customised" // operatör bir şey açmış / kip seçmiş
)

// planSubjectsFlip — SAF: kayıtlı blob → (yazılacak yeni blob ya da nil, sonuç).
func planSubjectsFlip(raw []byte) ([]byte, string, error) {
	if len(raw) == 0 {
		return nil, SubjectsFlipNoRow, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", fmt.Errorf("anomaly_sensitivity blob'u çözülemedi: %w", err)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	subj := map[string]json.RawMessage{}
	if sraw, ok := fields[subjectsField]; ok && string(sraw) != "null" {
		if err := json.Unmarshal(sraw, &subj); err != nil {
			return nil, "", fmt.Errorf("%s çözülemedi: %w", subjectsField, err)
		}
		if subj == nil {
			subj = map[string]json.RawMessage{}
		}
	}
	kinds := map[string]map[string]json.RawMessage{}
	for _, k := range []string{"endpoint", "db", "queue"} {
		obj := map[string]json.RawMessage{}
		if kr, ok := subj[k]; ok && string(kr) != "null" {
			if err := json.Unmarshal(kr, &obj); err != nil {
				return nil, "", fmt.Errorf("%s.%s çözülemedi: %w", subjectsField, k, err)
			}
			if obj == nil {
				obj = map[string]json.RawMessage{}
			}
		}
		var on bool
		if v, ok := obj["enabled"]; ok && string(v) != "null" {
			if err := json.Unmarshal(v, &on); err != nil {
				return nil, "", fmt.Errorf("%s.%s.enabled çözülemedi: %w", subjectsField, k, err)
			}
		}
		if on {
			return nil, SubjectsFlipCustomised, nil
		}
		kinds[k] = obj
	}
	var mode string
	if v, ok := subj["promotionMode"]; ok && string(v) != "null" {
		if err := json.Unmarshal(v, &mode); err != nil {
			return nil, "", fmt.Errorf("%s.promotionMode çözülemedi: %w", subjectsField, err)
		}
	}
	if m := strings.TrimSpace(mode); m != "" && m != SubjectPromotionOff {
		return nil, SubjectsFlipCustomised, nil
	}
	for _, k := range []string{"endpoint", "queue"} {
		kinds[k]["enabled"] = json.RawMessage("true")
		b, err := json.Marshal(kinds[k])
		if err != nil {
			return nil, "", err
		}
		subj[k] = b
	}
	subj["promotionMode"] = json.RawMessage(`"` + SubjectPromotionShadow + `"`)
	sout, err := json.Marshal(subj)
	if err != nil {
		return nil, "", err
	}
	fields[subjectsField] = sout
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, "", err
	}
	return out, SubjectsFlipApplied, nil
}

// MigrateSubjectsDefaultFlip — tek seferlik geçiş. İdempotent: işaret varken
// hiçbir şey yapmaz ("" döner). Okuma/yazma düşerse işaret YAZILMAZ, hata
// döner — çağıran sonraki turda yeniden dener. Audit yalnız blob değiştiyse,
// aktör "system".
func MigrateSubjectsDefaultFlip(ctx context.Context, st inboxKeepMigrateStore, now time.Time) (string, error) {
	marker, err := st.GetSetting(ctx, subjectsFlipKey)
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
	newRaw, outcome, err := planSubjectsFlip(raw)
	if err != nil {
		return "", err
	}
	if newRaw != nil {
		if err := st.PutSetting(ctx, anomalySensitivityKey, newRaw); err != nil {
			return "", fmt.Errorf("write anomaly_sensitivity: %w", err)
		}
		details, _ := json.Marshal(map[string]any{
			"version":       subjectsFlipVersion,
			"enabled":       []string{"endpoint", "queue"},
			"promotionMode": SubjectPromotionShadow,
			"reason":        "Davis özneleri ML ile yan yana gölgede koşsun (yükseltme varsayılanı); gölge Problem açmaz, bildirim göndermez",
		})
		if err := st.AppendAudit(ctx, AuditEntry{
			Time: now.UnixNano(), ActorID: "system", ActorEmail: "system", ActorRole: "system",
			Action: "settings.anomaly_sensitivity.migrate", TargetKind: "settings", TargetID: anomalySensitivityKey,
			Details: string(details),
		}); err != nil {
			log.Printf("[settings] anomaly_sensitivity özne geçişi: audit: %v", err)
		}
		log.Printf("[settings] anomaly_sensitivity: özneler endpoint + queue açık, terfi gölge (tek seferlik)")
	}
	body, _ := json.Marshal(minRateMigrateMarker{Version: subjectsFlipVersion, AppliedAt: now.UnixNano(), Outcome: outcome})
	if err := st.PutSetting(ctx, subjectsFlipKey, body); err != nil {
		return outcome, fmt.Errorf("write marker: %w", err)
	}
	return outcome, nil
}
