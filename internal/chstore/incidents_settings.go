package chstore

// incidents_settings.go — v0.10.1826 (operatör kararı 2026-11-05: "Incident
// sekmesi yerine Anomalies olsun; eski incident'lar devre dışı kalsın" +
// teyit "şu anki incidents akışı zaten sağlıklı çalışmıyor; anomaliler
// oluşsun, gerekirse Problem'e terfi olurlar"). Plan:
// docs/anomaly/learning-loop-audit.md §16.1 E1–E6.
//
// TEK ana anahtar system_settings `incidents` blobu: {"enabled":false}.
// Varsayılan KAPALI (blob yok = kapalı). Kapalıyken:
//   - iki otomatik kapı (AttachProblemToIncidentWith, JoinOpenIncidentWith)
//     CH'ye dokunmadan "incident yok" döner — Problem'ler aynen açılır ve
//     bildirilir, yalnız incident'a bağlanmaz;
//   - yaşam döngüsü kancası (incidentLifecycleNotifies) susar: created /
//     resolved / severity_raised bildirimi gitmez; açık eski incident'ların
//     otomatik kademeli kapanışı sessizce sürer (E4);
//   - elle ilan (POST /api/incidents) 410.
//
// Açıkken davranış bugünküyle aynı. Aile vidaları (attachToIncident,
// stormAttachToIncident, singleGroupJoinIncident) bu anahtarın ALTINDA
// anlamını korur. Veri silinmez.
//
// Neden ayrı küçük anahtar (anomaly_promotion vb. bloblardan biri değil):
// anahtar bir aileye değil incident yüzeyinin tamamına (kapılar, bildirim,
// Problems listesi, menü) aittir; aile blobuna gömülse o ailenin
// Normalize'ı / PUT'u ana anahtarı taşımak zorunda kalırdı.
//
// Şablon ai_loop_budget_settings.go: boot'ta yükle + 30 s cfgRefresh +
// süreç-içi atomic (sıcak yol CH okumaz); yazma yolu
// api/incidents_settings.go (admin PUT + audit incidents.settings +
// config-reload yayını).

import (
	"context"
	"encoding/json"
	"log"
	"sync/atomic"
)

// IncidentsSettingsKey — system_settings anahtarı.
const IncidentsSettingsKey = "incidents"

// IncidentsSettings — blob. Enabled sıfır değeri (false) = emekli.
type IncidentsSettings struct {
	Enabled bool `json:"enabled"`
}

// incidentsEnabled — süreç-içi son yüklenen değer; sıfır değeri false =
// varsayılan KAPALI (yüklenmeden önce de kapalı).
var incidentsEnabled atomic.Bool

// IncidentsEnabled — eski Incidents akışı açık mı (CH okuması değil).
func IncidentsEnabled() bool { return incidentsEnabled.Load() }

// SetIncidentsEnabled — yazımdan / yüklemeden sonra bu pod'u günceller.
// Testler iki durumu bununla kurar (t.Cleanup ile geri alır).
func SetIncidentsEnabled(on bool) { incidentsEnabled.Store(on) }

// parseIncidentsSettings — SAF: boş blob varsayılan (kapalı); bozuk blob
// ok=false (çağıran son değeri korur).
func parseIncidentsSettings(raw []byte) (IncidentsSettings, bool) {
	if len(raw) == 0 {
		return IncidentsSettings{}, true
	}
	var c IncidentsSettings
	if err := json.Unmarshal(raw, &c); err != nil {
		return IncidentsSettings{}, false
	}
	return c, true
}

// SaveIncidentsSettings — yazar ve bu pod'a yayınlar.
func (s *Store) SaveIncidentsSettings(ctx context.Context, c IncidentsSettings) (IncidentsSettings, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return IncidentsSettings{}, err
	}
	if err := s.PutSetting(ctx, IncidentsSettingsKey, raw); err != nil {
		return IncidentsSettings{}, err
	}
	SetIncidentsEnabled(c.Enabled)
	return c, nil
}

// LoadIncidentsSettings — boot + 30 s yenileme + config-reload. Okuma
// hatası hata döner (değer değişmez); bozuk blob son değeri korur.
func (s *Store) LoadIncidentsSettings(ctx context.Context) error {
	raw, err := s.GetSetting(ctx, IncidentsSettingsKey)
	if err != nil {
		return err
	}
	c, ok := parseIncidentsSettings(raw)
	if !ok {
		log.Printf("[incidents] incidents ayarı bozuk — son değer korunuyor")
		return nil
	}
	SetIncidentsEnabled(c.Enabled)
	return nil
}
