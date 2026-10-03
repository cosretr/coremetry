// pickerRecents — v0.10.1089 (seçici açılır listesi: ortak popover).
//
// Seçicilerin "Son kullanılan" grubu: operatörün o seçicide LİSTEDEN
// seçtiği son 5 değer. Tarayıcı başına (localStorage), sunucuya gitmez —
// recentServices (v0.7.89) / recentMetrics (v0.8.417) ile aynı sözleşme:
// kişisel kolaylık durumu, ekip durumu saved_views'te yaşar.
//
// Kapsam (`scope`) seçici türü + bağlam: 'service', 'operation:<servis>',
// 'metric:<servis>'. Operasyon/metrik servis başına tutuluyor çünkü başka
// bir servisin operasyonunu "son kullanılan" diye önermek boş sonuçlu bir
// süzgeç üretir. Tek anahtar altında { kapsam: string[] } — kapsam sayısı
// SCOPE_CAP ile sınırlı (en eski kapsam düşer), yani disk büyümesi sınırlı.
//
// Depolama kapalı / özel pencere / kota / bozuk JSON: okuma boş liste,
// yazma sessizce geçer (storage.ts getItem/setItem zaten try/catch'li;
// burada ikinci bir kat var çünkü değerin ŞEKLİ de güvenilmez — elle
// düzenlenmiş ya da eski bir sürümün yazdığı bir kayıt seçiciyi çökertemez).

import { getItem, setItem, STORAGE_KEYS } from './storage';

/** Kapsam başına tutulan en fazla değer. */
export const RECENT_CAP = 5;
/** Tutulan en fazla kapsam (servis başına operasyon/metrik kapsamları dahil). */
export const SCOPE_CAP = 24;

type Store = Record<string, string[]>;

function readStore(): Store {
  try {
    const v = getItem<unknown>(STORAGE_KEYS.pickerRecents, {});
    if (typeof v !== 'object' || v === null || Array.isArray(v)) return {};
    const out: Store = {};
    for (const [k, list] of Object.entries(v as Record<string, unknown>)) {
      if (!Array.isArray(list)) continue;
      out[k] = list.filter((x): x is string => typeof x === 'string' && x !== '').slice(0, RECENT_CAP);
    }
    return out;
  } catch {
    return {};
  }
}

export function getPickerRecents(scope: string): string[] {
  if (!scope) return [];
  return readStore()[scope] ?? [];
}

// recordPickerRecent — değeri kapsamın başına alır (tekilleştirir), 5'te
// keser. Kapsam yeniden yazılınca nesnenin SONUNA taşınır; böylece kapsam
// tavanı aşılınca en uzun süredir dokunulmayan kapsam düşer.
export function recordPickerRecent(scope: string, value: string): void {
  if (!scope || !value) return;
  try {
    const store = readStore();
    const next = [value, ...(store[scope] ?? []).filter(v => v !== value)].slice(0, RECENT_CAP);
    delete store[scope];
    store[scope] = next;
    const scopes = Object.keys(store);
    for (const k of scopes.slice(0, Math.max(0, scopes.length - SCOPE_CAP))) delete store[k];
    setItem(STORAGE_KEYS.pickerRecents, store);
  } catch {
    // En iyi çaba: "son kullanılan" kaybolursa seçici yine çalışır.
  }
}
