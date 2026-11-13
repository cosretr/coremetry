// unifiedAnomalySort.ts — v0.10.2083 (operatör 2026-11-13: /anomalies başlık
// sıralaması geri). SAF URL ↔ sunucu kodeği; rolloutSort.ts emsali.
//
// Birleşik liste SUNUCU sıralı ve sayfalı (internal/ml/unified_sort.go
// SortUnified, sayfadan ÖNCE): başlık tıklaması DataTable serverSort kipinde
// `?sort=<id>.<dir>` (süren tablo) / `?doneSort=<id>.<dir>` (bitenler) yazar,
// bölümün varsayılanı URL'ye YAZILMAZ. Sayfa sorguyu doğrudan URL'den türetir.
// Sözlük sunucuyla aynı: ml.UnifiedSortCols + NormalizeUnifiedSort
// (tanınmayan → varsayılan). "Ne oldu" cümlesi tarayıcıda kurulur → sıralanmaz.
import { parseSortParam, sortIdKnown, type SortState } from './dataTable';

export type UnifiedSection = 'open' | 'done';

export const UNIFIED_SORT_IDS = ['started', 'service', 'signals', 'status'] as const;

export const UNIFIED_SORT_URL_KEY: Record<UnifiedSection, string> = { open: 'sort', done: 'doneSort' };

/** Bölüm varsayılanı — sunucunun sort'suz sırası (açık: durum, biten: başlangıç). */
export const UNIFIED_SORT_DEFAULT: Record<UnifiedSection, SortState> = {
  open: { id: 'status', dir: 'desc' },
  done: { id: 'started', dir: 'desc' },
};

/** URL değeri → geçerli sıra; yok/bozuk/tanınmayan → bölüm varsayılanı. */
export function parseUnifiedSort(section: UnifiedSection, v: string | null): SortState {
  const s = parseSortParam(v);
  return s && s.id && sortIdKnown(s.id, UNIFIED_SORT_IDS) ? s : UNIFIED_SORT_DEFAULT[section];
}

/** Sıra → API parametreleri; varsayılanda boş (sorgu + RQ anahtarı değişmez). */
export function unifiedSortApiParams(section: UnifiedSection, s: SortState): { sort?: string; dir?: 'asc' | 'desc' } {
  const d = UNIFIED_SORT_DEFAULT[section];
  if (!s.id || (s.id === d.id && s.dir === d.dir)) return {};
  return { sort: s.id, dir: s.dir };
}
