package chstore

// cosre_memory.go — CoSRE planı Faz E ("hafıza"): ekip notlarının deposu.
//
// Değişmez 5 (yeni şema yok): her not bir saved_views sistem satırıdır —
//
//	page='cosre_memory', id='cosre-memory-<itemId>', owner_id='',
//	name='note', query_string = cosrememory.Item JSON (≤4 KB).
//
// Silme = tombstone (DeleteSavedView, name=''). Yalnız /api/ai/memory uçları
// (editor+, denetimli) ve kök neden etiketi PUT'u yazar; genel /api/views
// yazamaz (systemSavedViewPages). Sıcak yol (model çağrısı) CH okumaz:
// boot + 30 s cfgRefresh ile süreç-içi atomic kopya (CurrentCosreMemory),
// yazım bu pod'u hemen günceller (cosre_rules.go şablonu).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/cilcenk/coremetry/internal/ai/cosrememory"
)

const (
	// CosreMemoryPage — notların saved_views page değeri.
	CosreMemoryPage = "cosre_memory"
	// CosreMemoryIDPrefix — satır kimliği öneki.
	CosreMemoryIDPrefix  = "cosre-memory-"
	cosreMemoryMaxBytes  = 4 << 10
	cosreMemoryRowName   = "note"
	cosreMemoryListLimit = cosrememory.MaxItems
)

// CosreMemoryRowID — SAF: not kimliği → satır kimliği.
func CosreMemoryRowID(id string) string { return CosreMemoryIDPrefix + id }

var cosreMemoryCur atomic.Pointer[[]cosrememory.Item]

// CurrentCosreMemory — süreç-içi son yüklenen notlar (model çağrısının sıcak yolu).
func CurrentCosreMemory() []cosrememory.Item {
	if p := cosreMemoryCur.Load(); p != nil {
		return *p
	}
	return nil
}

// SetCosreMemory — bu pod'un kopyasını günceller (diğer pod'lar 30 s'de yakınsar).
func SetCosreMemory(items []cosrememory.Item) {
	cp := append([]cosrememory.Item(nil), items...)
	cosreMemoryCur.Store(&cp)
}

// decodeCosreMemory — SAF: satır → not (başka sayfa / tombstone / bozuk → ok=false).
func decodeCosreMemory(v SavedView) (cosrememory.Item, bool) {
	if v.Page != CosreMemoryPage || v.OwnerID != "" || v.Name == "" {
		return cosrememory.Item{}, false
	}
	var it cosrememory.Item
	if err := json.Unmarshal([]byte(v.QueryString), &it); err != nil || it.ID == "" || v.ID != CosreMemoryRowID(it.ID) {
		return cosrememory.Item{}, false
	}
	it, err := cosrememory.Normalize(it)
	if err != nil {
		return cosrememory.Item{}, false
	}
	return it, true
}

const listCosreMemorySQL = `
		SELECT id, owner_id, name, page, query_string, pinned, created_at
		FROM saved_views FINAL
		WHERE page = ? AND owner_id = '' AND name != ''
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// ListCosreMemory — bütün notlar (≤500), en yeni önce.
func (s *Store) ListCosreMemory(ctx context.Context) ([]cosrememory.Item, error) {
	rows, err := s.querySavedViews(ctx, listCosreMemorySQL, CosreMemoryPage, cosreMemoryListLimit)
	if err != nil {
		return nil, err
	}
	return decodeCosreMemoryRows(rows), nil
}

// decodeCosreMemoryRows — SAF: satırlar → geçerli notlar, en yeni önce.
func decodeCosreMemoryRows(rows []SavedView) []cosrememory.Item {
	out := make([]cosrememory.Item, 0, len(rows))
	for _, v := range rows {
		if it, ok := decodeCosreMemory(v); ok {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Source.At > out[j].Source.At })
	return out
}

// GetCosreMemoryItem — tek not; yoksa (nil, nil).
func (s *Store) GetCosreMemoryItem(ctx context.Context, id string) (*cosrememory.Item, error) {
	v, err := s.GetSavedView(ctx, CosreMemoryRowID(id))
	if err != nil || v == nil {
		return nil, err
	}
	it, ok := decodeCosreMemory(*v)
	if !ok {
		return nil, nil
	}
	return &it, nil
}

// PutCosreMemoryItem — notu yazar (tam satır; normalize edilmiş).
func (s *Store) PutCosreMemoryItem(ctx context.Context, it cosrememory.Item) error {
	it, err := cosrememory.Normalize(it)
	if err != nil {
		return err
	}
	if it.ID == "" {
		return fmt.Errorf("cosre memory: id zorunlu")
	}
	raw, err := json.Marshal(it)
	if err != nil {
		return err
	}
	if len(raw) > cosreMemoryMaxBytes {
		return fmt.Errorf("cosre memory: gövde %d B > tavan %d B", len(raw), cosreMemoryMaxBytes)
	}
	return s.UpsertSavedView(ctx, SavedView{ID: CosreMemoryRowID(it.ID), Name: cosreMemoryRowName,
		Page: CosreMemoryPage, QueryString: string(raw), CreatedAt: it.Source.At})
}

// DeleteCosreMemoryItem — notu siler (tombstone).
func (s *Store) DeleteCosreMemoryItem(ctx context.Context, id string) error {
	return s.DeleteSavedView(ctx, CosreMemoryRowID(id))
}

// LoadCosreMemory — boot + 30 s yenileme; okuma hatası son kopyayı korur.
func (s *Store) LoadCosreMemory(ctx context.Context) error {
	items, err := s.ListCosreMemory(ctx)
	if err != nil {
		return err
	}
	SetCosreMemory(items)
	return nil
}
