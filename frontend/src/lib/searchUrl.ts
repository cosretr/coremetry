// searchUrl — v0.10.1335: /search sayfasının SAF URL codec'i.
//
// Sahip olunan parametreler: q · types · service · sev · status · sort ·
// group · sel. Varsayılanlar URL'e YAZILMAZ (temiz paylaşım linki); types
// CSV kümesi inboxUrl'ün decodeCsvSet/encodeCsvSet yardımcılarıyla (kanonik
// sıra, tekrarsız, bilinmeyen değer düşer, boş = varsayılan = tüm türler).
//
// YABANCI parametreler korunur: `range` (useUrlRange), `env` (global
// EnvPicker + yapışkan seçim — useUrlEnv sahibi; Environment facet'i de
// AYNI paramı onun üzerinden yazar), `ai` (CoSRE çekmecesi) ve sayfanın
// bilmediği her şey. Yazıcı daima `prev`i kopyalar, sorgu dizesini sıfırdan
// kurmaz (frontend-design-system §4 (eski frontend-conventions)).
//
// sel = `<type>:<encodeURIComponent(id)>` — kimlik ':' ya da endpoint'in
// \x1f ayracını taşıyabilir; tür öneki ilk ':'a kadardır.
//
// searchUrlSig — URL→durum içe aktarımının bekçisi (Logs.tsx urlSig emsali,
// v0.8.253): yalnız SAHİP olunan süzgeç paramlarını özetler; `sel` bilerek
// DIŞARIDA (satır seçmek yazılmakta olan taslağı sıfırlamasın) ve yabancı
// paramlar dışarıda (range değişimi taslağı silmesin).
import { decodeCsvSet, encodeCsvSet } from './inboxUrl';
import type {
  SearchQueryParams, SearchResponse, SearchResultType, SearchSev, SearchSort, SearchStatus,
} from './types';

export const SEARCH_TYPES: readonly SearchResultType[] = ['service', 'endpoint', 'problem', 'exception', 'trace', 'log'];
export const SEARCH_SORTS: readonly SearchSort[] = ['relevance', 'newest', 'oldest', 'severity'];
export const SEARCH_SEVS: readonly SearchSev[] = ['critical', 'warning', 'info'];
export type SearchGroup = 'list' | 'type';

export interface SearchSel { type: SearchResultType; id: string }

export interface SearchUrlState {
  q: string;
  types: SearchResultType[];
  service: string;
  sev: SearchSev | '';
  status: SearchStatus;
  sort: SearchSort;
  group: SearchGroup;
  sel: SearchSel | null;
}

export const SEARCH_DEFAULTS: SearchUrlState = {
  q: '', types: [...SEARCH_TYPES], service: '', sev: '', status: 'open', sort: 'relevance', group: 'list', sel: null,
};

export const SEARCH_OWNED_PARAMS = ['q', 'types', 'service', 'sev', 'status', 'sort', 'group', 'sel'] as const;

function isType(v: string): v is SearchResultType {
  return (SEARCH_TYPES as readonly string[]).includes(v);
}

export function encodeSel(sel: SearchSel): string {
  return `${sel.type}:${encodeURIComponent(sel.id)}`;
}

export function decodeSel(raw: string | null): SearchSel | null {
  if (!raw) return null;
  const at = raw.indexOf(':');
  if (at <= 0) return null;
  const type = raw.slice(0, at);
  if (!isType(type)) return null;
  let id: string;
  try {
    id = decodeURIComponent(raw.slice(at + 1));
  } catch {
    return null; // bozuk yüzde kodlaması → seçim yok (çekmece açılmaz)
  }
  return id ? { type, id } : null;
}

export function decodeSearchUrl(sp: URLSearchParams): SearchUrlState {
  const sev = sp.get('sev') ?? '';
  const sort = sp.get('sort') ?? '';
  return {
    q: sp.get('q') ?? '',
    // decodeCsvSet URL sırasını korur; tür listesi KANONİK sırada tutulur.
    types: SEARCH_TYPES.filter(t => decodeCsvSet(sp.get('types'), SEARCH_TYPES, SEARCH_TYPES).includes(t)),
    service: (sp.get('service') ?? '').trim(),
    sev: (SEARCH_SEVS as readonly string[]).includes(sev) ? (sev as SearchSev) : '',
    status: sp.get('status') === 'all' ? 'all' : 'open',
    sort: (SEARCH_SORTS as readonly string[]).includes(sort) ? (sort as SearchSort) : 'relevance',
    group: sp.get('group') === 'type' ? 'type' : 'list',
    sel: decodeSel(sp.get('sel')),
  };
}

function setOrDelete(next: URLSearchParams, k: string, v: string | null) {
  if (v === null || v === '') next.delete(k);
  else next.set(k, v);
}

/** writeSearchUrl — `prev`in kopyasına yalnız `patch`teki alanları yazar;
 *  varsayılan değer = param silinir. Yabancı paramlara dokunmaz. */
export function writeSearchUrl(prev: URLSearchParams, patch: Partial<SearchUrlState>): URLSearchParams {
  const next = new URLSearchParams(prev);
  if ('q' in patch) setOrDelete(next, 'q', (patch.q ?? '').trim());
  if ('types' in patch) setOrDelete(next, 'types', encodeCsvSet(patch.types ?? SEARCH_TYPES, SEARCH_TYPES, SEARCH_TYPES));
  if ('service' in patch) setOrDelete(next, 'service', (patch.service ?? '').trim());
  if ('sev' in patch) setOrDelete(next, 'sev', patch.sev ?? '');
  if ('status' in patch) setOrDelete(next, 'status', patch.status === 'all' ? 'all' : null);
  if ('sort' in patch) setOrDelete(next, 'sort', patch.sort && patch.sort !== 'relevance' ? patch.sort : null);
  if ('group' in patch) setOrDelete(next, 'group', patch.group === 'type' ? 'type' : null);
  if ('sel' in patch) setOrDelete(next, 'sel', patch.sel ? encodeSel(patch.sel) : null);
  return next;
}

/** searchUrlSig — sahip olunan SÜZGEÇ paramlarının kanonik özeti (sel ve
 *  yabancı paramlar hariç; dosya başı). Varsayılanla yazılmış ve hiç
 *  yazılmamış URL aynı imzayı verir. */
export function searchUrlSig(sp: URLSearchParams): string {
  const s = decodeSearchUrl(sp);
  return [s.q.trim(), s.types.join(','), s.service, s.sev, s.status, s.sort, s.group].join('\u0001');
}

/** searchApiParams — durum + global env + pencere → istek. Varsayılanlar
 *  GÖNDERİLMEZ: sunucu da aynı varsayılanı uygular, istemci anahtarı kanonik
 *  kalır (types tümü = param yok). */
export function searchApiParams(s: SearchUrlState, env: string, win: { from: number; to: number }): SearchQueryParams {
  const p: SearchQueryParams = { q: s.q.trim(), from: win.from, to: win.to };
  const types = encodeCsvSet(s.types, SEARCH_TYPES, SEARCH_TYPES);
  if (types) p.types = types;
  if (s.service) p.service = s.service;
  if (env) p.env = env;
  if (s.sev) p.sev = s.sev;
  if (s.status === 'all') p.status = 'all';
  if (s.sort !== 'relevance') p.sort = s.sort;
  return p;
}

/** isUnfaceted — sonuç KÜMESİNİ daraltan hiçbir facet yok (tür/servis/env/
 *  sev). status=all genişletir, sort yalnız sıralar: ikisi de sayılmaz. */
export function isUnfaceted(p: SearchQueryParams): boolean {
  return !p.types && !p.service && !p.env && !p.sev;
}

/** hiddenByFilters — "N gizli": önbellekte AYNI sorgu + pencere için
 *  facet'siz bir cevap varsa onun toplamı; yoksa null (sayı uydurulmaz —
 *  operatör onaylı varsayılan). Ek istek ATILMAZ. */
export function hiddenByFilters(
  cached: ReadonlyArray<readonly [SearchQueryParams | undefined, SearchResponse | undefined]>,
  current: SearchQueryParams,
): number | null {
  if (isUnfaceted(current)) return null;
  for (const [p, data] of cached) {
    if (!p || !data) continue;
    if (p.q === current.q && p.from === current.from && p.to === current.to && isUnfaceted(p)) {
      return data.total;
    }
  }
  return null;
}

/** searchSeeAllHref — ⌘K'nın "Tüm sonuçlar" satırının hedefi; q < 2 → null. */
export function searchSeeAllHref(query: string): string | null {
  const q = query.trim();
  if (q.length < 2) return null;
  return `/search?q=${encodeURIComponent(q)}`;
}
