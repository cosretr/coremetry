package chstore

// problem_graph_merge_setting.go — v0.10.2473: servisler arası Problem
// birleştirmesinin GÖRÜNÜM kipi artık operatör ayarı (`system_settings`
// anahtarı `problem_graph_merge`), kod sabiti değil.
//
//	off     küme / köken hesaplanmaz, hiçbir yüzeyde gösterilmez
//	shadow  v0.10.2472 davranışı: yalnız ölçülür; Problem detayında "gölge"
//	        bölümü ve liste alt satırı, liste katlanmaz
//	on      VARSAYILAN: Problems listesi bağlı kümeyi tek satırda katlar,
//	        manşete "Köken: <varlık>" yazılır, emme sınırı (90 dk) uygulanır
//
// Kayıtlı geçerli değer her zaman kazanır; satır yoksa ya da kip tanınmıyorsa
// varsayılan (on). Okuma hatasında son yayınlanan değer korunur.
//
// BİLDİRİM DEĞİŞMEZ: bu ayar yalnız görünümü (liste, sayaç, manşet, detay)
// ve küme kurulumunu yönetir. Sonradan bağlanan üyenin açılış bildirimini
// tutan yol (notify merge_hold.go) ayrı anahtardadır
// (problemGraphMergeEnforce, problem_graph_enforce.go) ve KAPALI kalır;
// pin: notify/merge_display_pin_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
)

// ProblemGraphMergeKey — system_settings anahtarı.
const ProblemGraphMergeKey = "problem_graph_merge"

// Görünüm kipleri — SÖZLEŞME (API gövdesi, ayar blobu, önbellek anahtarı).
const (
	GraphMergeOff    = "off"
	GraphMergeShadow = "shadow"
	GraphMergeOn     = "on"
)

// ProblemGraphMergeSetting — ayar blobu.
type ProblemGraphMergeSetting struct {
	Mode      string `json:"mode"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// DefaultProblemGraphMerge — varsayılan: açık.
func DefaultProblemGraphMerge() ProblemGraphMergeSetting {
	return ProblemGraphMergeSetting{Mode: GraphMergeOn}
}

// ValidGraphMergeMode — SAF: kip tanınıyor mu.
func ValidGraphMergeMode(m string) bool {
	return m == GraphMergeOff || m == GraphMergeShadow || m == GraphMergeOn
}

// NormalizeProblemGraphMerge — SAF: tanınmayan kip varsayılana döner.
func NormalizeProblemGraphMerge(c ProblemGraphMergeSetting) ProblemGraphMergeSetting {
	if !ValidGraphMergeMode(c.Mode) {
		c.Mode = DefaultProblemGraphMerge().Mode
	}
	return c
}

// decodeProblemGraphMerge — SAF: blob → ayar. Boş blob = satır yok = varsayılan.
func decodeProblemGraphMerge(raw []byte) (ProblemGraphMergeSetting, error) {
	if len(raw) == 0 {
		return DefaultProblemGraphMerge(), nil
	}
	var c ProblemGraphMergeSetting
	if err := json.Unmarshal(raw, &c); err != nil {
		return ProblemGraphMergeSetting{}, fmt.Errorf("problem_graph_merge blob'u çözülemedi: %w", err)
	}
	return NormalizeProblemGraphMerge(c), nil
}

var (
	problemGraphMergeCfg         atomic.Pointer[ProblemGraphMergeSetting]
	problemGraphMergeReadFailing atomic.Bool
)

// CurrentProblemGraphMerge — hidrate edilmemişse varsayılan (on).
func CurrentProblemGraphMerge() ProblemGraphMergeSetting {
	if c := problemGraphMergeCfg.Load(); c != nil {
		return *c
	}
	return DefaultProblemGraphMerge()
}

// SetProblemGraphMerge — normalize edilmiş hâli yayınlar.
func SetProblemGraphMerge(c ProblemGraphMergeSetting) {
	n := NormalizeProblemGraphMerge(c)
	problemGraphMergeCfg.Store(&n)
}

// ProblemGraphMergeMode — o anki görünüm kipi (off | shadow | on).
func ProblemGraphMergeMode() string { return CurrentProblemGraphMerge().Mode }

// ProblemGraphMergeOn — liste katlaması + köken manşeti + emme sınırı açık mı.
func ProblemGraphMergeOn() bool { return ProblemGraphMergeMode() == GraphMergeOn }

// SetProblemGraphMergeModeForTest — TEST KAPISI: kipi değiştirir; dönen
// işlev eski hâline getirir. Üretim kodu çağırmaz.
func SetProblemGraphMergeModeForTest(mode string) (restore func()) {
	prev := problemGraphMergeCfg.Load()
	SetProblemGraphMerge(ProblemGraphMergeSetting{Mode: mode})
	return func() { problemGraphMergeCfg.Store(prev) }
}

// ReadProblemGraphMerge — kayıtlı blob; satır yok → varsayılan; CH / JSON
// hatası → hata (ayar ekranı varsayılanla dolup kaydı ezmesin).
func (s *Store) ReadProblemGraphMerge(ctx context.Context) (ProblemGraphMergeSetting, error) {
	raw, err := s.GetSetting(ctx, ProblemGraphMergeKey)
	if err != nil {
		return ProblemGraphMergeSetting{}, err
	}
	return decodeProblemGraphMerge(raw)
}

// SaveProblemGraphMerge — normalize edilmiş hâli yazar.
func (s *Store) SaveProblemGraphMerge(ctx context.Context, c ProblemGraphMergeSetting) error {
	raw, err := json.Marshal(NormalizeProblemGraphMerge(c))
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, ProblemGraphMergeKey, raw)
}

// LoadProblemGraphMerge — boot hidrasyonu + 30 sn yenileme (main.go; her pod).
func (s *Store) LoadProblemGraphMerge(ctx context.Context) {
	if s == nil {
		return
	}
	LoadProblemGraphMergeWith(func() (ProblemGraphMergeSetting, error) { return s.ReadProblemGraphMerge(ctx) })
}

// LoadProblemGraphMergeWith — boot + 30 sn yenileme gövdesi; okuma hatasında
// SON İYİ DEĞER korunur (notify_policy emsali).
func LoadProblemGraphMergeWith(read func() (ProblemGraphMergeSetting, error)) ProblemGraphMergeSetting {
	c, err := read()
	if err != nil {
		if problemGraphMergeReadFailing.CompareAndSwap(false, true) {
			log.Printf("[settings] problem_graph_merge okunamadı: %v — son yayınlanan değer korunuyor", err)
		}
		return CurrentProblemGraphMerge()
	}
	problemGraphMergeReadFailing.Store(false)
	SetProblemGraphMerge(c)
	return CurrentProblemGraphMerge()
}
