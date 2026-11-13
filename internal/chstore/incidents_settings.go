package chstore

// incidents_settings.go — v0.10.1826 Incidents emekliliği (operatör kararı
// 2026-11-05: "Incident sekmesi yerine Anomalies olsun"). v0.10.2024
// (operatör 2026-11-12: menüde hâlâ "Incidents" — "sen default yap onu"):
// ana anahtar KALDIRILDI. Eski akış her kurulumda emekli; system_settings
// `incidents` blobu artık okunmaz (kayıtlı {"enabled":true} yok sayılır),
// boot / 30 s cfgRefresh / config-reload yolu yok.
//
// Kapalı durumun anlamı (değişmedi):
//   - otomatik incident açma / katılma yok (kapılar Incidents ölü kod
//     dilimi 1'de silindi) — Problem'ler aynen açılır ve bildirilir;
//   - yaşam döngüsü kancası (incidentLifecycleNotifies) susar; açık eski
//     incident'ların otomatik kademeli kapanışı sessizce sürer;
//   - /api/incidents* ve /api/settings/incidents her metotta 410
//     (v0.10.2075, api/incidents_gone.go). Veri silinmez.
//
// Süreç-içi atomic yalnız testlerin eski akış dallarını sınaması için
// kalır: üretim kodu SetIncidentsEnabled çağırmaz
// (incidents_retire_test.go TestIncidentsSwitchNoProductionWriter).

import "sync/atomic"

// incidentsEnabled — sıfır değeri false = emekli.
var incidentsEnabled atomic.Bool

// IncidentsEnabled — eski Incidents akışı açık mı. Üretimde hep false.
func IncidentsEnabled() bool { return incidentsEnabled.Load() }

// SetIncidentsEnabled — YALNIZ testler (eski akış dallarını kurar, t.Cleanup
// ile geri alır). Üretim kodundan çağrılmaz.
func SetIncidentsEnabled(on bool) { incidentsEnabled.Store(on) }
