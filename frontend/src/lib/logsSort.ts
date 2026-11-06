// logsSort — v0.10.1833 (Log Search Faz 1 dilim 1.10; audit §3.3).
//
// /logs çoklu alan sıralaması (SUNUCUDA; internal/logstore/sort.go):
// URL `sort=service:asc,level:desc` (≤ LOGS_SORT_MAX). Zaman eşitlik bozucu
// sunucuda her zaman eklenir; yönü eski `asc=1` düğmesi.
//
// Başlık sözleşmesi (Kibana / AG Grid):
//   tık        → tek anahtar: X↑ → X↓ → (yok = zaman sırası)
//   Shift+tık  → ek anahtar: yoksa sona X↑ (≤3), varsa ↑ → ↓ → kaldır
//   Time tık   → özel sıralama varsa temizler, yoksa eski yön düğmesi
//                (asc) — eski `asc=1` linkleri aynen (çağıran yapar)
// Sıralanamaz: Message (gövde metni) ve Trace (bağlantı kolonu). Diğer
// kolonların sıralanabilirliği arka uca bağlı (ES eşlemesi / CH beyaz
// listesi) — reddedilirse satır içi hata + "sıralamayı kaldır".

import type { LogsSortKey } from './types';

export const LOGS_SORT_MAX = 3;

const FIELD_RE = /^[A-Za-z0-9_@][A-Za-z0-9_.@-]{0,199}$/;

/** URL `sort` → anahtarlar. Bozuk / fazla parça sessizce düşer (link asla 400 yemez). */
export function parseLogsSort(raw: string | null | undefined): LogsSortKey[] {
  if (!raw) return [];
  const out: LogsSortKey[] = [];
  for (const part of raw.split(',')) {
    const i = part.lastIndexOf(':');
    const field = (i >= 0 ? part.slice(0, i) : part).trim();
    const dir = i >= 0 ? part.slice(i + 1).trim() : 'asc';
    if (!FIELD_RE.test(field) || (dir !== 'asc' && dir !== 'desc')) continue;
    if (out.some(k => k.field === field)) continue;
    out.push({ field, desc: dir === 'desc' });
    if (out.length >= LOGS_SORT_MAX) break;
  }
  return out;
}

/** Anahtarlar → URL değeri; boş → null (parametre silinir). */
export function serializeLogsSort(keys: readonly LogsSortKey[]): string | null {
  if (keys.length === 0) return null;
  return keys.map(k => `${k.field}:${k.desc ? 'desc' : 'asc'}`).join(',');
}

export function isSortableLogColumn(id: string): boolean {
  return id !== 'message' && id !== 'trace';
}

/** Başlık tıkı → yeni anahtarlar (saf). */
export function logsSortClick(keys: readonly LogsSortKey[], field: string, shift: boolean): LogsSortKey[] {
  const i = keys.findIndex(k => k.field === field);
  if (!shift) {
    if (keys.length === 1 && i === 0) return keys[0].desc ? [] : [{ field, desc: true }];
    return [{ field, desc: false }];
  }
  if (i < 0) return keys.length >= LOGS_SORT_MAX ? [...keys] : [...keys, { field, desc: false }];
  if (!keys[i].desc) return keys.map((k, j) => (j === i ? { field, desc: true } : k));
  return keys.filter((_, j) => j !== i);
}

/** Kolon rozeti: ↓1 / ↑2 (tek anahtarda sayı yok). */
export function logsSortBadge(keys: readonly LogsSortKey[], field: string): string | null {
  const i = keys.findIndex(k => k.field === field);
  if (i < 0) return null;
  const arrow = keys[i].desc ? '↓' : '↑';
  return keys.length > 1 ? `${arrow}${i + 1}` : arrow;
}

/** Başlık düğmesinin erişilebilir adı. */
export function logsSortAriaLabel(keys: readonly LogsSortKey[], field: string, label: string): string {
  const i = keys.findIndex(k => k.field === field);
  const state = i < 0 ? 'sıralı değil' : `${keys[i].desc ? 'azalan' : 'artan'}${keys.length > 1 ? `, ${i + 1}. anahtar` : ''}`;
  return `${label} — ${state}. Tıkla: bu alana göre sırala; Shift+tık: ek sıralama anahtarı`;
}
