package cosrelearn

// signal_weights.go — v0.10.1601 (CoSRE v2 Faz 1 sonrası, operatör kararı
// 2026-10-29 §1): kart "ilgili / ilgisiz" ve mesaj notu ağırlıkları kod
// sabiti değil, system_settings("cosre") blobunun signals.weights yolu.
// Varsayılanlar +0.10 / −0.10.
//
// v0.10.1605 (operatör kararı, kesin): not ağırlığı 0'a KİLİTLİ — blob
// şeklinde alanı yok, PUT'ta note ≠ 0 → 400 (0 ya da yok → kabul, yazılmaz),
// puanlama sabit NoteWeight (0) kullanır. Ağırlık başına sınır ±0.25
// (WeightLimit): PUT katı, okuma saklı değeri kırpar. Kart kırpması ±0.5.
//
// Kart puanı: aynı cevabın (exchange_id) aynı kartına (ayrıntı = adım
// kimliği s<i>) düşen evidence_* sinyallerinin toplamı, [-0.5, +0.5]'e
// kırpılır — tek kartı art arda tıklamak cevabın puanını sürükleyemez.
// Mesaj notları cevap başına tek bir "kart" (anahtar "") gibi toplanır ve
// aynı sınırla kırpılır.
//
// Her şey SAF; G/Ç LoadSignalWeights'te (gece işi her koşuda okur — ayar
// api podunda yazılır, worker podunda okunur; boot-anı kopyası bayatlardı).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
)

// SignalSettingsKey — system_settings anahtarı (blob {"signals":{"weights":{…}}}).
const SignalSettingsKey = "cosre"

// CardScoreLimit — kart (ve mesaj notu) puanının mutlak tavanı.
const CardScoreLimit = 0.5

// WeightLimit — ayarlanabilir tek bir ağırlığın mutlak tavanı (v0.10.1605).
const WeightLimit = 0.25

// NoteWeight — mesaj notunun KİLİTLİ ağırlığı (v0.10.1605): serbest not
// içeriği puana çevrilmez, insan incelemesi içindir.
const NoteWeight = 0

// messageCard — not sinyallerinin toplandığı kart anahtarı (mesaj düzeyi).
const messageCard = ""

const (
	defaultEvidenceRelevantWeight   = 0.10
	defaultEvidenceIrrelevantWeight = -0.10
	defaultNoteWeight               = NoteWeight
)

// SignalWeights — operatörün ayarlayabildiği istemci sinyali ağırlıkları
// (blob şeklinin tamamı; note yok — kilitli 0).
type SignalWeights struct {
	EvidenceRelevant   float64 `json:"evidence_relevant"`
	EvidenceIrrelevant float64 `json:"evidence_irrelevant"`
}

// SignalWeightsInput — admin PUT gövdesi. Note yalnız "0 ya da yok"
// sözleşmesini denetlemek için okunur; saklanmaz.
type SignalWeightsInput struct {
	EvidenceRelevant   float64  `json:"evidence_relevant"`
	EvidenceIrrelevant float64  `json:"evidence_irrelevant"`
	Note               *float64 `json:"note,omitempty"`
}

// DefaultSignalWeights — ayar yok / bozuk iken geçerli ağırlıklar.
func DefaultSignalWeights() SignalWeights {
	return SignalWeights{
		EvidenceRelevant:   defaultEvidenceRelevantWeight,
		EvidenceIrrelevant: defaultEvidenceIrrelevantWeight,
	}
}

// For — sinyal türünün ağırlığı; ayarlanabilir türlerden değilse ok=false.
func (w SignalWeights) For(kind string) (float64, bool) {
	switch kind {
	case SigEvidenceRelevant:
		return w.EvidenceRelevant, true
	case SigEvidenceIrrelevant:
		return w.EvidenceIrrelevant, true
	case SigNote:
		return NoteWeight, true
	}
	return 0, false
}

// ClampCardScore — SAF: [-CardScoreLimit, +CardScoreLimit]; NaN → 0.
func ClampCardScore(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	return math.Max(-CardScoreLimit, math.Min(CardScoreLimit, x))
}

// ValidateSignalWeights — SAF: admin PUT'un katı denetimi. note yalnız 0
// ya da yok; işaret sözleşmesi (ilgili ≥ 0, ilgisiz ≤ 0) ve |w| ≤
// WeightLimit; sonlu olmayan değer red. Geçerliyse saklanacak şekli döner.
func ValidateSignalWeights(in SignalWeightsInput) (SignalWeights, error) {
	if in.Note != nil && *in.Note != NoteWeight {
		return SignalWeights{}, fmt.Errorf("%s ağırlığı 0'a kilitli (%.4g)", SigNote, *in.Note)
	}
	for _, f := range []struct {
		name string
		v    float64
		lo   float64
		hi   float64
	}{
		{SigEvidenceRelevant, in.EvidenceRelevant, 0, WeightLimit},
		{SigEvidenceIrrelevant, in.EvidenceIrrelevant, -WeightLimit, 0},
	} {
		if math.IsNaN(f.v) || math.IsInf(f.v, 0) {
			return SignalWeights{}, fmt.Errorf("%s sonlu bir sayı olmalı", f.name)
		}
		if f.v < f.lo || f.v > f.hi {
			return SignalWeights{}, fmt.Errorf("%s [%.2f, %.2f] aralığında olmalı (%.4g)", f.name, f.lo, f.hi, f.v)
		}
	}
	return SignalWeights{EvidenceRelevant: in.EvidenceRelevant, EvidenceIrrelevant: in.EvidenceIrrelevant}, nil
}

// rawSignalWeights — blob okuması; eksik alan nil → varsayılan. Eski
// sürümün yazdığı "note" alanı bilerek okunmaz (kilitli 0).
type rawSignalWeights struct {
	EvidenceRelevant   *float64 `json:"evidence_relevant"`
	EvidenceIrrelevant *float64 `json:"evidence_irrelevant"`
}

// ParseSignalWeights — SAF, hoşgörülü: boş / bozuk blob → varsayılanlar;
// eksik alan → o alanın varsayılanı; aralık dışı → aralığa kırpılır
// (±WeightLimit, işaret sözleşmesi dahil). Okuma yolu hiçbir zaman hata döndürmez:
// bozuk bir ayar gece puanlamasını durdurmamalı.
func ParseSignalWeights(raw []byte) SignalWeights {
	w := DefaultSignalWeights()
	if len(raw) == 0 {
		return w
	}
	var blob struct {
		Signals struct {
			Weights rawSignalWeights `json:"weights"`
		} `json:"signals"`
	}
	if json.Unmarshal(raw, &blob) != nil {
		return w
	}
	r := blob.Signals.Weights
	pick := func(p *float64, def, lo, hi float64) float64 {
		if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) {
			return def
		}
		return math.Max(lo, math.Min(hi, *p))
	}
	w.EvidenceRelevant = pick(r.EvidenceRelevant, w.EvidenceRelevant, 0, WeightLimit)
	w.EvidenceIrrelevant = pick(r.EvidenceIrrelevant, w.EvidenceIrrelevant, -WeightLimit, 0)
	return w
}

// MergeSignalWeights — SAF: mevcut "cosre" blobunun diğer anahtarlarını
// koruyarak signals.weights'i yazar (blob ileride başka CoSRE ayarı da
// taşıyacak; tüm-blob ezme o alanları silerdi). Bozuk mevcut blob → yalnız
// yeni ağırlıklarla temiz blob.
func MergeSignalWeights(existing []byte, w SignalWeights) ([]byte, error) {
	top := map[string]json.RawMessage{}
	if len(existing) > 0 && json.Unmarshal(existing, &top) != nil {
		top = map[string]json.RawMessage{}
	}
	signals := map[string]json.RawMessage{}
	if raw, ok := top["signals"]; ok && json.Unmarshal(raw, &signals) != nil {
		signals = map[string]json.RawMessage{}
	}
	wb, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	signals["weights"] = wb
	sb, err := json.Marshal(signals)
	if err != nil {
		return nil, err
	}
	top["signals"] = sb
	return json.Marshal(top)
}

// SettingsReader — ağırlık okumasının G/Ç yüzeyi (*chstore.Store karşılar).
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) ([]byte, error)
}

// LoadSignalWeights — ayarı okur; okuma hatası → varsayılanlar (puanlama
// durmaz; hata çağırana döner ki loglansın).
func LoadSignalWeights(ctx context.Context, st SettingsReader) (SignalWeights, error) {
	if st == nil {
		return DefaultSignalWeights(), nil
	}
	raw, err := st.GetSetting(ctx, SignalSettingsKey)
	if err != nil {
		return DefaultSignalWeights(), err
	}
	return ParseSignalWeights(raw), nil
}
