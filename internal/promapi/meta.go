package promapi

// v0.10.944 (CoSRE araştırma asistanı) — cevabın VERİ DIŞINDAKİ yarısı.
//
// DecodeSeries yalnız serileri döndürüyordu ve iki gerçeği sessizce yutuyordu:
//
//   - MaxSeriesParsed (1000) tavanı: kesilen bir cevap tam cevap gibi
//     okunuyordu ("bu servisin 1000 pod'u var" değil, "1000'den fazlası
//     var ve gerisi düştü").
//   - VictoriaMetrics'in üst düzey `isPartial` alanı: cluster kurulumunda
//     bir vmstorage düğümü cevap vermediyse VM yine 200 + success döner ve
//     bunu yalnız bu alanla söyler. Çözülmeyen bayrak = eksik sayının tam
//     sayı diye modele gitmesi.
//
// HTTP hatası da tiplendi: 401/403 metin sinyaline bırakılmıyor,
// sourcestate.ErrUnauthorized'a açılıyor (errors.Is). Metin eskisiyle
// bayt-aynı kaldı — api'nin 502 eşlemesi ve operatörün gördüğü mesaj
// değişmedi.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// SeriesResult — v0.10.944 — DecodeSeriesMeta'nın sonucu.
type SeriesResult struct {
	Series []Series
	// Total — tavandan ÖNCE backend'in döndürdüğü seri sayısı.
	Total int
	// Truncated — Total > MaxSeriesParsed; Series ilk MaxSeriesParsed'dir.
	Truncated bool
	// IsPartial — VM `isPartial:true`: bazı depolama düğümleri cevap
	// vermedi, sonuç eksik kapsamlı olabilir.
	IsPartial bool
}

// DecodeSeriesMeta — DecodeSeries + tavan/kısmi bayrakları. SAF, tablo
// testli (meta_test.go). Hata metinleri DecodeSeries ile bayt-aynı (aynı
// decodeEnvelopeFull gövdesi).
func DecodeSeriesMeta(label string, body []byte) (SeriesResult, error) {
	env, err := decodeEnvelopeFull(label, body)
	if err != nil {
		return SeriesResult{}, err
	}
	res := SeriesResult{IsPartial: env.IsPartial}
	if len(env.Data) == 0 {
		return res, nil
	}
	var sd seriesData
	if err := json.Unmarshal(env.Data, &sd); err != nil {
		return SeriesResult{}, fmt.Errorf("%s decode data: %w", label, err)
	}
	res.Total = len(sd.Result)
	if len(sd.Result) > MaxSeriesParsed {
		sd.Result = sd.Result[:MaxSeriesParsed]
		res.Truncated = true
	}
	res.Series = sd.Result
	return res, nil
}

// QuerySeriesMeta = Do + DecodeSeriesMeta.
func QuerySeriesMeta(ctx context.Context, req Request) (SeriesResult, error) {
	body, err := Do(ctx, req)
	if err != nil {
		return SeriesResult{}, err
	}
	return DecodeSeriesMeta(labelOr(req.Label), body)
}

// StringsResult — v0.10.944 — DecodeStringsMeta'nın sonucu (etiket adları /
// etiket değerleri). VM cluster'ında vmselect `isPartial`'ı etiket uçlarında
// da döndürür: eksik bir etiket listesi "bu metrikte yok" diye okunmamalı.
type StringsResult struct {
	Values    []string
	IsPartial bool
}

// DecodeStringsMeta — DecodeStrings + isPartial. SAF, tablo testli
// (meta_test.go). Hata metinleri DecodeStrings ile bayt-aynı (tek gövde:
// DecodeStrings bunun sarmalayıcısı).
func DecodeStringsMeta(label string, body []byte) (StringsResult, error) {
	env, err := decodeEnvelopeFull(label, body)
	if err != nil {
		return StringsResult{}, err
	}
	res := StringsResult{IsPartial: env.IsPartial}
	if len(env.Data) == 0 {
		return res, nil
	}
	var out []string
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return StringsResult{}, fmt.Errorf("%s decode data: %w", label, err)
	}
	res.Values = out
	return res, nil
}

// QueryStringsMeta = Do + DecodeStringsMeta.
func QueryStringsMeta(ctx context.Context, req Request) (StringsResult, error) {
	body, err := Do(ctx, req)
	if err != nil {
		return StringsResult{}, err
	}
	return DecodeStringsMeta(labelOr(req.Label), body)
}

// HTTPError — v0.10.944 — backend'in HTTP >= 300 cevabı. Error() metni
// v0.9.1150'den beri operatörün gördüğüyle BAYT-AYNI ("<label>: HTTP <kod>:
// <gövdenin ilk 200 rune'u>"); fark yalnız tipte: çağıran durumu metinden
// değil koddan okuyabilir.
type HTTPError struct {
	Label  string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Label, e.Status, e.Body)
}

// Unwrap — 401/403 → sourcestate.ErrUnauthorized. Diğer kodlar bir
// sentinel'e açılmaz: 5xx metin sinyaliyle (http 502/503 → unreachable,
// 504 → timeout) sourcestate.Classify'da zaten doğru sınıfa düşüyor; bir
// sentinel daha eklemek ikinci bir yazım olurdu.
func (e *HTTPError) Unwrap() error {
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden {
		return sourcestate.ErrUnauthorized
	}
	return nil
}
