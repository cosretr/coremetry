package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// seed.go — v0.10.1132: açılışta env'den MCP sunucu kaydı TOHUMLAMA.
//
// Helm chart'ı grafanaMcp.autoRegister=true iken Coremetry pod'larına
// COREMETRY_MCP_SEED_JSON verir (charts/coremetry _helpers.tpl
// coremetry.grafanaMcp.seedEnv); operatör aynı env'i compose/elle de
// verebilir. Sözleşme — "operatörün düzenlemesini ASLA ezme":
//
//   - Tohum, ad (SanitizedName) başına ÖMÜR BOYU bir kez uygulanır. Uygulanan
//     adlar system_settings "mcp_client_seeded" işaretinde tutulur; operatör
//     kaydı sonradan silerse bir sonraki açılış onu GERİ EKLEMEZ.
//   - O adla kayıt zaten varsa (operatör elle eklemiş) hiçbir alanına
//     dokunulmaz; ad yalnız işarete yazılır.
//   - Liste MaxServers'ta doluysa tohum atlanır ve işarete YAZILMAZ (yer
//     açılınca sonraki açılış dener).
//   - Yalnız "http" taşıması: env'den gelen stdio tohumu açılışta keyfî
//     komut çalıştırmak olurdu.
//   - Token JSON'a GİRMEZ: "tokenEnv" COREMETRY_MCP_SEED_ önekli bir env adı
//     verir; önek kısıtı, tohumun COREMETRY_JWT_SECRET gibi bir sırrı Bearer
//     başlığıyla keyfî bir URL'ye taşımasını yapısal olarak engeller.
//
// Çok-pod: her pod aynı girdiden aynı blob'u üretir (idempotent); eşzamanlı
// iki açılış en kötü ihtimalle aynı yazımı iki kez yapar.

// SeedEnv — tohum JSON'unun env adı.
const SeedEnv = "COREMETRY_MCP_SEED_JSON"

// SeedTokenEnvPrefix — tokenEnv'in taşıması zorunlu önek.
const SeedTokenEnvPrefix = "COREMETRY_MCP_SEED_"

// seedMarkerKey — uygulanmış tohum adlarının system_settings anahtarı.
const seedMarkerKey = "mcp_client_seeded"

// MaxServers — kayıtlı dış MCP sunucusu üst sınırı (api PUT doğrulaması da
// bunu kullanır).
const MaxServers = 8

// seedServer — env'deki tek tohum girdisi.
type seedServer struct {
	Name               string   `json:"name"`
	Transport          string   `json:"transport,omitempty"`
	URL                string   `json:"url"`
	TokenEnv           string   `json:"tokenEnv,omitempty"`
	Enabled            *bool    `json:"enabled,omitempty"` // nil = true
	AllowTools         []string `json:"allowTools,omitempty"`
	DenyTools          []string `json:"denyTools,omitempty"`
	InsecureSkipVerify bool     `json:"insecureSkipVerify,omitempty"`
}

// seedMarker — işaret blob'u.
type seedMarker struct {
	Names []string `json:"names"`
}

// ParseSeed — SAF: env değerini doğrulanmış ServerConfig listesine çevirir.
// Kabul: JSON dizi ya da {"servers":[…]}. Boş girdi → nil, nil.
func ParseSeed(raw string, getenv func(string) string) ([]ServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var list []seedServer
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return nil, fmt.Errorf("%s: %w", SeedEnv, err)
		}
	} else {
		var wrap struct {
			Servers []seedServer `json:"servers"`
		}
		if err := json.Unmarshal([]byte(raw), &wrap); err != nil {
			return nil, fmt.Errorf("%s: %w", SeedEnv, err)
		}
		list = wrap.Servers
	}
	out := make([]ServerConfig, 0, len(list))
	seen := map[string]bool{}
	for _, sv := range list {
		name := strings.TrimSpace(sv.Name)
		key := SanitizedName(name)
		if key == "" {
			return nil, fmt.Errorf("%s: sunucu adı boş olamaz", SeedEnv)
		}
		if seen[key] {
			return nil, fmt.Errorf("%s: sunucu adı tekrar ediyor: %s", SeedEnv, name)
		}
		seen[key] = true
		tr := strings.TrimSpace(sv.Transport)
		if tr == "" {
			tr = "http"
		}
		if tr != "http" {
			return nil, fmt.Errorf("%s: %s: tohumda yalnız http taşıması desteklenir", SeedEnv, name)
		}
		url := strings.TrimSpace(sv.URL)
		lower := strings.ToLower(url)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			return nil, fmt.Errorf("%s: %s: http(s):// ile başlayan URL gerekli", SeedEnv, name)
		}
		token := ""
		if te := strings.TrimSpace(sv.TokenEnv); te != "" {
			if !strings.HasPrefix(te, SeedTokenEnvPrefix) {
				return nil, fmt.Errorf("%s: %s: tokenEnv %s önekini taşımalı", SeedEnv, name, SeedTokenEnvPrefix)
			}
			if getenv != nil {
				token = getenv(te)
			}
			if token == "" {
				return nil, fmt.Errorf("%s: %s: tokenEnv %s boş", SeedEnv, name, te)
			}
		}
		enabled := true
		if sv.Enabled != nil {
			enabled = *sv.Enabled
		}
		out = append(out, ServerConfig{
			Name: name, Transport: tr, URL: url, Token: token, Enabled: enabled,
			AllowTools: trimList(sv.AllowTools), DenyTools: trimList(sv.DenyTools),
			InsecureSkipVerify: sv.InsecureSkipVerify,
		})
	}
	return out, nil
}

func trimList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// PlanSeed — SAF: mevcut ayar + işaret + tohumlardan yeni ayar, yeni işaret
// ve eklenen adları üretir. Mevcut kayıtların hiçbir alanı değişmez.
func PlanSeed(cur Settings, seeded []string, seeds []ServerConfig) (next Settings, nextSeeded []string, added []string) {
	done := map[string]bool{}
	for _, n := range seeded {
		done[n] = true
	}
	exists := map[string]bool{}
	next.Servers = append(next.Servers, cur.Servers...)
	for _, sv := range cur.Servers {
		exists[SanitizedName(sv.Name)] = true
	}
	nextSeeded = append(nextSeeded, seeded...)
	for _, sd := range seeds {
		key := SanitizedName(sd.Name)
		if done[key] {
			continue // bir kez uygulandı; silinmiş olsa da geri gelmez
		}
		if exists[key] {
			done[key] = true // operatörün kaydı — dokunma, yalnız işaretle
			nextSeeded = append(nextSeeded, key)
			continue
		}
		if len(next.Servers) >= MaxServers {
			continue // yer yok; işaretlenmez, sonraki açılış dener
		}
		next.Servers = append(next.Servers, sd)
		exists[key] = true
		done[key] = true
		nextSeeded = append(nextSeeded, key)
		added = append(added, sd.Name)
	}
	return next, nextSeeded, added
}

// SeedStore — ApplySeed'in dar deposu (chstore.Store karşılar).
type SeedStore interface {
	settingsStore
	GetSetting(ctx context.Context, key string) ([]byte, error)
	PutSetting(ctx context.Context, key string, value []byte) error
}

// ApplySeed — env tohumunu bir kez uygular: işareti + kayıtlı ayarı okur,
// PlanSeed, eklenen varsa ayarı yazıp canlıyı günceller, sonra işareti yazar.
// Sıra bilinçli: ayar yazılıp işaret düşerse sonraki açılış kaydı "zaten
// var" görür, çift kayıt olmaz. Dönen liste eklenen adlar (audit için).
func (s *Service) ApplySeed(ctx context.Context, store SeedStore, raw string, getenv func(string) string) ([]string, error) {
	if s == nil || store == nil {
		return nil, nil
	}
	seeds, err := ParseSeed(raw, getenv)
	if err != nil || len(seeds) == 0 {
		return nil, err
	}
	var marker seedMarker
	mraw, err := store.GetSetting(ctx, seedMarkerKey)
	if err != nil {
		return nil, fmt.Errorf("read seed marker: %w", err)
	}
	if len(mraw) > 0 {
		if err := json.Unmarshal(mraw, &marker); err != nil {
			return nil, fmt.Errorf("decode seed marker: %w", err)
		}
	}
	var cur Settings
	craw, err := store.GetMCPClientSettingsRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("read mcp settings: %w", err)
	}
	if len(craw) > 0 {
		if err := json.Unmarshal(craw, &cur); err != nil {
			return nil, fmt.Errorf("decode mcp settings: %w", err)
		}
	}
	next, nextSeeded, added := PlanSeed(cur, marker.Names, seeds)
	if len(added) > 0 {
		if err := s.SavePersisted(ctx, store, next); err != nil {
			return nil, fmt.Errorf("write mcp settings: %w", err)
		}
	}
	if len(nextSeeded) != len(marker.Names) {
		body, _ := json.Marshal(seedMarker{Names: nextSeeded})
		if err := store.PutSetting(ctx, seedMarkerKey, body); err != nil {
			return added, fmt.Errorf("write seed marker: %w", err)
		}
	}
	return added, nil
}
