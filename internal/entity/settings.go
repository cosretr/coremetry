package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// settings.go — v0.10.2214 (entity S1, karar K1): katman HER ZAMAN AÇIK.
//
// Bayrak ve dört vida sabite indi (docs/architecture/entity-autodiscovery-
// plan.md §3.1, K1: "ayar yok" ilkesi). system_settings["entity_layer"]
// blobunda yalnız backfill durumu kalır (thanos_identity.go yazar, lider
// Tick'i okur — rol-güvenli yayılım). Eski sürümlerin yazdığı enabled /
// syncInterval / podGap / staleAfter / parallelClusters alanları
// json.Unmarshal'da yok sayılır ve bir sonraki backfill yazımında düşer;
// geri uyum katmanı yok.

const SettingsKey = "entity_layer"

// Sabitler (K1). RefreshEvery: açık ömrün last_seen'i yalnız bu kadar
// eskidiyse yeniden yazılır (S1 yazım diyeti, plan §3.3: podGap/2).
// Boşluk denetimi (podGap) CH'deki last_seen'i okur; saklı değer en çok
// RefreshEvery + bir tick eski olur, podGap'in altında kalır.
const (
	SyncInterval     = 60 * time.Second
	PodGap           = 10 * time.Minute
	StaleAfter       = 24 * time.Hour
	ParallelClusters = 4
	RefreshEvery     = PodGap / 2
	// MaxSpanOnlyClusters — K2: Remote Cluster kaydı olmayan span cluster
	// değerlerinden tick başına en çok bu kadarı cluster entity olur (en çok
	// satırı olanlar; yanlış değer çoğalmasına tavan).
	MaxSpanOnlyClusters = 64
)

// Settings — kayıtlı blob (yalnız backfill durumu).
type Settings struct {
	// UpdatedAt — v0.10.129: yazım damgası (UnixNano). LoadPersisted daha
	// eski bir blobu bellekteki yeninin üstüne yazmaz — kendi reload
	// sinyali replike olmamış eski satırı okuyup değeri geri alıyordu.
	UpdatedAt int64 `json:"updatedAt,omitempty"`
	// BackfillUntil — v0.10.141 (otomatik eşleme brief'i): bu ana (ms) kadar
	// span geçişi 24 saatlik pencereyle koşar — bir span cluster değeri bir
	// kayda ATANDIĞINDA geriye dönük pod/servis entity'leri üretilsin.
	BackfillUntil int64 `json:"backfillUntil,omitempty"`
	// BackfillValue — geriye dönük geçiş YALNIZ bu span cluster değeri için
	// (inceleme: küresel 24 s pencere her cluster'ın ölü pod'larını canlı
	// olarak yeniden açıyordu). Boş = backfill yok.
	BackfillValue string `json:"backfillValue,omitempty"`
}

// Resolved — Tick'in okuduğu çözülmüş backfill durumu.
type Resolved struct {
	BackfillUntil time.Time // sıfır = yok
	BackfillValue string
}

func (s Settings) Resolved() Resolved {
	return Resolved{BackfillUntil: backfillUntil(s.BackfillUntil), BackfillValue: strings.TrimSpace(s.BackfillValue)}
}

// settingsStore — chstore'un ham blob kapısı (thanos.go emsali).
type settingsStore interface {
	GetEntitySettingsRaw(ctx context.Context) ([]byte, error)
	PutEntitySettingsRaw(ctx context.Context, raw []byte) error
}

// SettingsService — bellekteki blob + kalıcılık.
type SettingsService struct {
	mu  sync.RWMutex
	cfg Settings
}

func NewSettingsService() *SettingsService { return &SettingsService{} }

func (s *SettingsService) Current() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *SettingsService) Resolved() Resolved { return s.Current().Resolved() }

func (s *SettingsService) Configure(cfg Settings) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// LoadPersisted — boot + çok-pod eşitlemesi: blob yoksa backfill yok.
func (s *SettingsService) LoadPersisted(ctx context.Context, store settingsStore) error {
	if s == nil || store == nil {
		return nil
	}
	raw, err := store.GetEntitySettingsRaw(ctx)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var cfg Settings
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("entity settings decode: %w", err)
	}
	s.applyLoaded(cfg)
	return nil
}

// applyLoaded — yüklenen blob bellektekinden ESKİ ise (damga küçük) atlar.
func (s *SettingsService) applyLoaded(cfg Settings) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.UpdatedAt < s.cfg.UpdatedAt {
		return false
	}
	s.cfg = cfg
	return true
}

// SavePersisted — backfill zamanlaması: tam blob + canlı swap.
func (s *SettingsService) SavePersisted(ctx context.Context, store settingsStore, cfg Settings) error {
	cfg.UpdatedAt = time.Now().UnixNano()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := store.PutEntitySettingsRaw(ctx, raw); err != nil {
		return err
	}
	s.Configure(cfg)
	return nil
}

// StartConfigRefresh — çok-pod blob eşitlemesi (30 s).
func (s *SettingsService) StartConfigRefresh(ctx context.Context, store settingsStore, interval time.Duration) {
	if s == nil || store == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.LoadPersisted(ctx, store); err != nil {
				log.Printf("[entity] ayar tazeleme: %v", err)
			}
		}
	}
}

func backfillUntil(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// BackfillLookback — Tick'in span geçişi penceresi: atama sonrası kısa bir
// süre (BackfillUntil) 24 saat, sonra normal seenLookback.
const BackfillLookback = 24 * time.Hour
