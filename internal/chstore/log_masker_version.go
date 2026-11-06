package chstore

// log_masker_version.go — v0.10.1829: log kalıbı maskesi sürüm geçişi.
//
// Operator-reported ("GUID, UUID, id değerlerini yeni log pattern'i
// algılamasın") düzeltmesi Drain maskesini değiştirdi
// (logstore.MaskerVersion 1 → 2). Etkilenen şablonların log_templates
// kimliği (sha1(maskelenmiş belirteçler)) değişir: puller onları yeni
// kimlikle, first_seen = şimdi olarak yazar → `log_template_new` ve ML
// `rare_template` için toptan "yeni" — düzeltmenin kendisi bir uyarı seli
// doğururdu. Koruma iki katmanlı:
//
//  1. Karşılaştırma: "bilinen şablon" yüklemleri saklanmış şablonu GÜNCEL
//     maskeyle yeniden maskeler (templater.Remask / ParseTemplate) — eski
//     satır yeni adayla eşleşir.
//  2. Sessiz pencere (bu dosya): templater puller'ı ilk lider tikinde
//     system_settings `log_masker_version` blobundaki sürümü çalışan
//     sürümle karşılaştırır; farklıysa (blob yok = v1) yeni sürümü + 24 sa
//     sessiz pencereyi yazar. Pencere boyunca `log_template_new` ve ML
//     `rare_template` olay ÜRETMEZ (defter yazımı sürer; Logs › Şablonlar
//     etkilenmez). Kimlik değişen şablonun eski kimliğine bağlı susturmalar
//     (FingerprintAnomaly(…, TemplateID, …)) yeni kimliğe UYMAZ — bilinçli,
//     raporlandı.
//
// Taze kurulumda blob yoktur → v1 sayılır → ilk 24 sa yine sessiz; taze
// kurulumda her şablon zaten "yeni" olduğundan bu da istenen yön.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"
)

const (
	logMaskerVersionKey = "log_masker_version"
	// LogMaskerQuietWindow — maske sürümü değişince "yeni şablon"
	// uyarılarının susturulduğu süre: puller 5 dk'da bir örnekler, etkilenen
	// şablonların yeni kimlikleri bir günlük trafik döngüsünde doğar ve
	// pencereden sonra "bilinen" olur.
	LogMaskerQuietWindow = 24 * time.Hour
	// logMaskerQuietCacheTTL — okuyucu (recorder / ML) tarafında blobun
	// süreç-içi önbelleği; tik başına CH okuması olmasın.
	logMaskerQuietCacheTTL = time.Minute
)

// LogMaskerState — system_settings `log_masker_version` blobu.
type LogMaskerState struct {
	Version    int   `json:"version"`
	From       int   `json:"from,omitempty"`       // önceki sürüm (1 = blob yoktu)
	ChangedAt  int64 `json:"changedAt,omitempty"`  // unix ns
	QuietUntil int64 `json:"quietUntil,omitempty"` // unix ns; bu ana dek yeni-şablon olayı yok
}

// QuietAt — pencere now anında açık mı?
func (st LogMaskerState) QuietAt(now time.Time) bool { return st.QuietUntil > now.UnixNano() }

// NextLogMaskerState — SAF geçiş kararı. found=false (blob yok) v1 sayılır
// (v0.10.1496–1828 kurulumları blob yazmadı). Sürüm eşitse değişiklik yok;
// farklıysa (yükseltme ya da geri alma) yeni sürüm + now+LogMaskerQuietWindow.
func NextLogMaskerState(stored LogMaskerState, found bool, current int, now time.Time) (LogMaskerState, bool) {
	prev := stored.Version
	if !found || prev <= 0 {
		prev = 1
	}
	if prev == current {
		if !found || stored.Version != current {
			// Blob yok ve çalışan sürüm 1: geçiş yok, yalnız kayıt.
			return LogMaskerState{Version: current}, true
		}
		return stored, false
	}
	return LogMaskerState{
		Version:    current,
		From:       prev,
		ChangedAt:  now.UnixNano(),
		QuietUntil: now.Add(LogMaskerQuietWindow).UnixNano(),
	}, true
}

// GetLogMaskerState — blob; found=false = yok.
func (s *Store) GetLogMaskerState(ctx context.Context) (LogMaskerState, bool, error) {
	raw, err := s.GetSetting(ctx, logMaskerVersionKey)
	if err != nil {
		return LogMaskerState{}, false, err
	}
	if len(raw) == 0 {
		return LogMaskerState{}, false, nil
	}
	var st LogMaskerState
	if err := json.Unmarshal(raw, &st); err != nil {
		return LogMaskerState{}, false, nil // bozuk blob = yok (v1): pencere açılır, güvenli yön
	}
	return st, true, nil
}

// PutLogMaskerState — blobu yazar ve bu sürecin okuyucu önbelleğini tazeler.
func (s *Store) PutLogMaskerState(ctx context.Context, st LogMaskerState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := s.PutSetting(ctx, logMaskerVersionKey, raw); err != nil {
		return err
	}
	logMaskerQuietCache.Store(&logMaskerQuietEntry{state: st, at: time.Now()})
	return nil
}

type logMaskerQuietEntry struct {
	state LogMaskerState
	at    time.Time
}

var logMaskerQuietCache atomic.Pointer[logMaskerQuietEntry]

// LogMaskerStateCached — okuyucular (anomaly recorder, ML, durum ucu) için
// 1 dk önbellekli blob. Okuma hatasında son başarılı değer döner; hiç
// okunamadıysa hata (çağıran fail-closed davranır: o tik yeni-şablon olayı
// üretmez — v0.10.1030 bilinen-okuması yönü).
func (s *Store) LogMaskerStateCached(ctx context.Context) (LogMaskerState, error) {
	if e := logMaskerQuietCache.Load(); e != nil && time.Since(e.at) < logMaskerQuietCacheTTL {
		return e.state, nil
	}
	st, _, err := s.GetLogMaskerState(ctx)
	if err != nil {
		if e := logMaskerQuietCache.Load(); e != nil {
			return e.state, nil
		}
		return LogMaskerState{}, err
	}
	logMaskerQuietCache.Store(&logMaskerQuietEntry{state: st, at: time.Now()})
	return st, nil
}
