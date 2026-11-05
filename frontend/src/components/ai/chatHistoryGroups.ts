// chatHistoryGroups — v0.10.1137: /cosre sol kenar çubuğunun konuşma listesi (SAF).
//
// Operatör: geçmiş Claude'daki gibi solda dursun. Veri AYNI kaynak
// (GET /api/ai/conversations, "🕘 Geçmiş" düğmesinin okuduğu liste); burada
// yalnız sıralama (en yeni üstte), başlık araması (istemci tarafı) ve
// "Bugün / Dün / Son 7 gün / Daha eski" gruplaması var.

import type { AiConversationSummary } from '@/lib/types';

export interface ConversationGroup {
  label: 'Bugün' | 'Dün' | 'Son 7 gün' | 'Daha eski';
  items: AiConversationSummary[];
}

const DAY_MS = 86_400_000;
export const SIDEBAR_TITLE_MAX = 60;

/** Türkçe katlama: büyük/küçük harf + noktalı/noktasız i farkı aramada yok sayılır. */
function fold(s: string): string {
  return s.toLocaleLowerCase('tr').replace(/ı/g, 'i').normalize('NFKD').replace(/[̀-ͯ]/g, '');
}

/** Liste başlığı: tek satır, en çok SIDEBAR_TITLE_MAX karakter + "…". */
export function sidebarTitle(title: string): string {
  const t = (title || '').replace(/\s+/g, ' ').trim() || 'Adsız konuşma';
  return t.length > SIDEBAR_TITLE_MAX ? `${t.slice(0, SIDEBAR_TITLE_MAX - 1).trimEnd()}…` : t;
}

/**
 * groupConversations — `nowMs` anına göre gruplar (yerel takvim günü).
 * updatedAt unix NANOsaniye (AiConversationSummary). Boş gruplar dönmez.
 */
export function groupConversations(
  threads: readonly AiConversationSummary[] | undefined,
  nowMs: number,
  query = '',
): ConversationGroup[] {
  const q = fold(query.trim());
  const list = [...(threads ?? [])]
    .filter(t => !q || fold(t.title ?? '').includes(q))
    .sort((a, b) => b.updatedAt - a.updatedAt);
  const d = new Date(nowMs);
  const today = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const groups: ConversationGroup[] = [
    { label: 'Bugün', items: [] },
    { label: 'Dün', items: [] },
    { label: 'Son 7 gün', items: [] },
    { label: 'Daha eski', items: [] },
  ];
  for (const t of list) {
    const ms = t.updatedAt / 1e6;
    const g = ms >= today ? 0 : ms >= today - DAY_MS ? 1 : ms >= today - 7 * DAY_MS ? 2 : 3;
    groups[g].items.push(t);
  }
  return groups.filter(g => g.items.length > 0);
}

/** v0.10.1788 — gün başına grup (CoSRE v2 sayfası, mockup A). */
export interface DayGroup {
  /** YYYY-MM-DD (operatör saat diliminde takvim günü). */
  key: string;
  /** "Bugün" / "Dün" / "3 Kasım" (başka yılda "3 Kasım 2025"). */
  label: string;
  items: AiConversationSummary[];
}

const ymdFmt = new Map<string, Intl.DateTimeFormat>();
/** `ms` anının `timeZone`daki takvim günü (YYYY-MM-DD). SAF. */
export function dayKey(ms: number, timeZone?: string): string {
  const k = timeZone ?? '';
  let f = ymdFmt.get(k);
  if (!f) {
    f = new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' });
    ymdFmt.set(k, f);
  }
  return f.format(new Date(ms));
}

/** YYYY-MM-DD → bir önceki takvim günü (UTC tarih aritmetiği; DST'den bağımsız). */
function prevDay(key: string): string {
  const [y, m, d] = key.split('-').map(Number);
  return new Date(Date.UTC(y, m - 1, d - 1)).toISOString().slice(0, 10);
}

/**
 * groupConversationsByDay — v0.10.1788 (CoSRE v2 sayfası, mockup A): "Bugün /
 * Dün / <tarih>" gün grupları, en yeni üstte. Gün, operatörün saat diliminde
 * (varsayılan tarayıcı dilimi; test dikişi `timeZone`) — UTC günüyle
 * bölünmez (memory feedback-model-timestamps-operator-zone). updatedAt unix
 * NANOsaniye. Arama groupConversations ile aynı katlama. v1 kenar çubuğu
 * groupConversations'ı (Bugün/Dün/Son 7 gün/Daha eski) kullanmaya devam eder.
 */
export function groupConversationsByDay(
  threads: readonly AiConversationSummary[] | undefined,
  nowMs: number,
  query = '',
  timeZone?: string,
): DayGroup[] {
  const q = fold(query.trim());
  const list = [...(threads ?? [])]
    .filter(t => !q || fold(t.title ?? '').includes(q))
    .sort((a, b) => b.updatedAt - a.updatedAt);
  const today = dayKey(nowMs, timeZone);
  const yesterday = prevDay(today);
  const out: DayGroup[] = [];
  for (const t of list) {
    const key = dayKey(t.updatedAt / 1e6, timeZone);
    let g = out.at(-1);
    if (!g || g.key !== key) {
      g = { key, label: dayLabel(key, today, yesterday), items: [] };
      out.push(g);
    }
    g.items.push(t);
  }
  return out;
}

function dayLabel(key: string, today: string, yesterday: string): string {
  if (key === today) return 'Bugün';
  if (key === yesterday) return 'Dün';
  const [y, m, d] = key.split('-').map(Number);
  const sameYear = key.slice(0, 4) === today.slice(0, 4);
  return new Intl.DateTimeFormat('tr-TR', { timeZone: 'UTC', day: 'numeric', month: 'long', ...(sameYear ? {} : { year: 'numeric' }) })
    .format(new Date(Date.UTC(y, m - 1, d)));
}

// Kenar çubuğu daraltma tercihi — localStorage (gizli pencere / engelli
// depolama atar: try/catch, varsayılan açık).
export const SIDEBAR_COLLAPSED_KEY = 'coremetry.cosre.sidebarCollapsed';

export function readSidebarCollapsed(): boolean {
  try {
    return window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === '1';
  } catch {
    return false;
  }
}

export function writeSidebarCollapsed(v: boolean): void {
  try {
    window.localStorage.setItem(SIDEBAR_COLLAPSED_KEY, v ? '1' : '0');
  } catch { /* depolama yok — tercih bu oturumla sınırlı */ }
}
