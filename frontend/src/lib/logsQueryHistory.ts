// logsQueryHistory — v0.10.1820 (Log Search Faz 1 dilim 1.4; audit §8).
//
// /logs sorgu geçmişi: kullanıcı + tarayıcı başına son 20 UYGULANAN arama,
// en yeni önde, tekilleştirilmiş (aynı metin yeniden uygulanınca öne gelir,
// kopya oluşmaz). Emsal: Explore `useQueryHistory` / `RecentQueries` ve
// lib/recentMetrics. localStorage YALNIZ kişisel kolaylık durumu — paylaşılan
// ya da kalıcı olması gereken bir şey değil; okuma/yazma lib/storage
// üzerinden (özel pencere / kota hatası sayfayı çökertmez, okuma boşa düşer).
//
// Saf çekirdek (pushHistory / matchHistory / sanitize) tablo testli; I/O
// sarmalayıcıları ince.
import { getItem, setItem, logsQueryHistoryKey } from './storage';
import type { LogsQueryHistoryEntry } from './types';

export const LOGS_HISTORY_CAP = 20;
/** Tek girdinin üst sınırı: 8 KiB'lık URL sınırının altında, çöp birikmesin. */
const MAX_QUERY_LEN = 2000;

/** Diskten gelen değeri doğrular; bozuk/yabancı girdiler atılır. SAF. */
export function sanitizeHistory(v: unknown): LogsQueryHistoryEntry[] {
  if (!Array.isArray(v)) return [];
  const out: LogsQueryHistoryEntry[] = [];
  const seen = new Set<string>();
  for (const x of v) {
    if (typeof x !== 'object' || x === null) continue;
    const r = x as Record<string, unknown>;
    if (typeof r.q !== 'string' || typeof r.at !== 'number' || !Number.isFinite(r.at)) continue;
    const q = r.q.trim();
    if (!q || q.length > MAX_QUERY_LEN || seen.has(q)) continue;
    seen.add(q);
    out.push({ q, at: r.at });
    if (out.length >= LOGS_HISTORY_CAP) break;
  }
  return out;
}

/** Yeni aramayı öne koyar (tekil, tavanlı). Boş/aşırı uzun metin no-op. SAF. */
export function pushHistory(list: LogsQueryHistoryEntry[], q: string, now: number, cap = LOGS_HISTORY_CAP): LogsQueryHistoryEntry[] {
  const t = q.trim();
  if (!t || t.length > MAX_QUERY_LEN) return list;
  return [{ q: t, at: now }, ...list.filter(e => e.q !== t)].slice(0, cap);
}

/** Yazılan metinle eşleşen girdiler (harf duyarsız alt dize), en yeni önde;
 *  metnin kendisiyle birebir aynı girdi gösterilmez (öneri değil). SAF. */
export function matchHistory(list: LogsQueryHistoryEntry[], typed: string, limit = 8): LogsQueryHistoryEntry[] {
  const t = typed.trim().toLowerCase();
  return list
    .filter(e => e.q.toLowerCase() !== t && (!t || e.q.toLowerCase().includes(t)))
    .slice(0, limit);
}

export function readLogsQueryHistory(uid: string | undefined): LogsQueryHistoryEntry[] {
  return sanitizeHistory(getItem<unknown>(logsQueryHistoryKey(uid ?? ''), []));
}

/** Uygulanan aramayı kaydeder; güncel listeyi döner. */
export function recordLogsQuery(uid: string | undefined, q: string, now = Date.now()): LogsQueryHistoryEntry[] {
  const cur = readLogsQueryHistory(uid);
  const next = pushHistory(cur, q, now);
  if (next !== cur) setItem(logsQueryHistoryKey(uid ?? ''), next);
  return next;
}
