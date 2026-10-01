package chstore

// problem_verdict.go — v0.10.1015 — Problems sekmesinde ÖĞRETME: operatörün bir
// satırın İMZASINA verdiği karar ("gerçek problem" / "problem değil").
//
// Operatör (2026-10-01): "Problems sekmesinde bütün hepsi gelsin, ben hangisi
// gerçek problem hangisi değil zamanla öğretelim." v0.10.1014 varsayılanı her
// şeye açtı; bu dosya öğretmenin kalıcı yarısı.
//
// Karar OLAYA değil İMZAYA bağlıdır: aynı kural + servis (ya da aynı exception
// grubu) yeniden geldiğinde kendiliğinden aynı sınıfa düşer — "öğrenme" budur,
// deterministik ve geri alınabilir (model yok). İmzayı FE üretir
// (lib/problemVerdict.ts inboxSignature); sunucu yalnız biçimini doğrular.
//
// Anomali OLAYI kararından (anomaly_verdict.go, v0.10.181: «anomali / değil»,
// event_id başına, dedektör istatistiği için) AYRIDIR: o tek bir olayı etiketler,
// bu triage listesinde bir imzanın yerini belirler. Problem yaşam döngüsüne
// DOKUNMAZ; bildirimlere de — yönetici politikayı açmadıkça (v0.10.1016,
// dosya sonu: ProblemVerdictPolicy; varsayılan kapalı = yalnız görünüm).
//
// Depolama: ortak durum tablosu saved_views (mimari değişmez 5: "kayıtlı durum
// için tek tablo, yüzey başına yeni şema yok"). Satır = bir imza:
//
//	id            "pv:" + imza        (aynı imza → aynı satır; RMT son yazanı tutar)
//	owner_id      ""                  (ekip ortak)
//	page          "problem-verdict"
//	name          karar: real | noise ("" = silinmiş; DeleteSavedView mezar taşı)
//	query_string  JSON {signature, label, kind, service, by}
//	created_at    karar anı

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const (
	ProblemVerdictPage  = "problem-verdict"
	ProblemVerdictReal  = "real"  // gerçek problem
	ProblemVerdictNoise = "noise" // problem değil

	// ProblemVerdictMax — saklanan en çok imza (liste okuması bu tavanla sınırlı).
	ProblemVerdictMax = 5000
	// problemVerdictSigMax — imza uzunluk tavanı (kural kimliği + servis adı sığar).
	problemVerdictSigMax = 400
)

// ProblemVerdict — bir imzanın kararı.
type ProblemVerdict struct {
	Signature string `json:"signature"`
	Verdict   string `json:"verdict"`           // real | noise
	Label     string `json:"label,omitempty"`   // işaretlenen satırın başlığı (gösterim)
	Kind      string `json:"kind,omitempty"`    // problem | exception | httperror | anomaly
	Service   string `json:"service,omitempty"` // işaretlenen satırın öznesi (gösterim)
	By        string `json:"by,omitempty"`
	At        int64  `json:"at"` // unix ns
}

// ValidProblemVerdict — SAF: yalnız iki karar.
func ValidProblemVerdict(v string) bool { return v == ProblemVerdictReal || v == ProblemVerdictNoise }

// ValidProblemSignature — SAF: imza biçimi. Önek satırın kaynağını söyler
// (p: alarm kuralı, e: exception / HTTP hata grubu, a: anomali); gövde boş
// olamaz, kontrol karakteri taşıyamaz, tavanı aşamaz.
func ValidProblemSignature(sig string) bool {
	if len(sig) < 3 || len(sig) > problemVerdictSigMax {
		return false
	}
	switch sig[:2] {
	case "p:", "e:", "a:":
	default:
		return false
	}
	if strings.TrimSpace(sig[2:]) == "" {
		return false
	}
	for _, r := range sig {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// problemVerdictID — SAF: imza → saved_views satır kimliği.
func problemVerdictID(sig string) string { return "pv:" + sig }

type problemVerdictPayload struct {
	Signature string `json:"signature"`
	Label     string `json:"label,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Service   string `json:"service,omitempty"`
	By        string `json:"by,omitempty"`
}

// problemVerdictFromRow — SAF: saved_views satırı → karar. Bozuk gövde ya da
// geçersiz karar ok=false (liste onu atlar; yarım satır karar sayılmaz).
func problemVerdictFromRow(name, query string, createdAt time.Time) (ProblemVerdict, bool) {
	if !ValidProblemVerdict(name) {
		return ProblemVerdict{}, false
	}
	var p problemVerdictPayload
	if err := json.Unmarshal([]byte(query), &p); err != nil || !ValidProblemSignature(p.Signature) {
		return ProblemVerdict{}, false
	}
	return ProblemVerdict{Signature: p.Signature, Verdict: name, Label: p.Label, Kind: p.Kind,
		Service: p.Service, By: p.By, At: createdAt.UnixNano()}, true
}

// ListProblemVerdicts — tüm kararlar, en yeni önce (≤ ProblemVerdictMax).
// saved_views küçük bir durum tablosudur; okuma sayfa süzgeçli + LIMIT'li.
func (s *Store) ListProblemVerdicts(ctx context.Context) ([]ProblemVerdict, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT name, query_string, created_at
		FROM saved_views FINAL
		WHERE page = ? AND name != ''
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`, ProblemVerdictPage, ProblemVerdictMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProblemVerdict{}
	for rows.Next() {
		var name, query string
		var createdAt time.Time
		if err := rows.Scan(&name, &query, &createdAt); err != nil {
			return nil, err
		}
		if v, ok := problemVerdictFromRow(name, query, createdAt); ok {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// SetProblemVerdict — imzanın kararını yazar (aynı imza → aynı satır).
func (s *Store) SetProblemVerdict(ctx context.Context, v ProblemVerdict) error {
	raw, err := json.Marshal(problemVerdictPayload{Signature: v.Signature, Label: v.Label, Kind: v.Kind, Service: v.Service, By: v.By})
	if err != nil {
		return err
	}
	return s.UpsertSavedView(ctx, SavedView{ID: problemVerdictID(v.Signature), Name: v.Verdict,
		Page: ProblemVerdictPage, QueryString: string(raw), CreatedAt: v.At})
}

// ClearProblemVerdict — imzanın kararını kaldırır (mezar taşı; satır yoksa no-op).
func (s *Store) ClearProblemVerdict(ctx context.Context, signature string) error {
	return s.DeleteSavedView(ctx, problemVerdictID(signature))
}

// ── v0.10.1016 — karar POLİTİKASI: "problem değil" bildirimi de sustursun mu ──
//
// Varsayılan KAPALI: kararlar yalnız görünümdür (v0.10.1015). Yönetici açarsa
// "problem değil" imzalı alarm kuralı problemleri ve exception / HTTP hata
// grupları dış kanallara ve ekip mailine GİTMEZ (kapı: internal/notify/
// verdict_silence.go). Problem yine açılır, listede ve canlı akışta görünür.

// ProblemVerdictPolicyKey — system_settings anahtarı.
const ProblemVerdictPolicyKey = "problem_verdict_policy"

// ProblemVerdictPolicy — kararların görünüm dışındaki etkisi.
type ProblemVerdictPolicy struct {
	MuteNotifications bool   `json:"muteNotifications"`
	UpdatedBy         string `json:"updatedBy,omitempty"`
	UpdatedAt         int64  `json:"updatedAt,omitempty"` // unix ns
}

// parseProblemVerdictPolicy — SAF: blob → politika. Boş / bozuk blob = sıfır
// değer (susturma KAPALI): okunamayan ayar alarm kaybettirmez.
func parseProblemVerdictPolicy(raw []byte) ProblemVerdictPolicy {
	var p ProblemVerdictPolicy
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return ProblemVerdictPolicy{}
	}
	return p
}

// GetProblemVerdictPolicy — saklanan politika (yoksa sıfır değer).
func (s *Store) GetProblemVerdictPolicy(ctx context.Context) (ProblemVerdictPolicy, error) {
	raw, err := s.GetSetting(ctx, ProblemVerdictPolicyKey)
	if err != nil {
		return ProblemVerdictPolicy{}, err
	}
	return parseProblemVerdictPolicy(raw), nil
}

// PutProblemVerdictPolicy — politikayı yazar.
func (s *Store) PutProblemVerdictPolicy(ctx context.Context, p ProblemVerdictPolicy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, ProblemVerdictPolicyKey, raw)
}
