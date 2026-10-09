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
