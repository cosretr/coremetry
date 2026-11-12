// lib/dashboardList — v0.10.1995 (Dashboards Faz 3 dilim 3.1; audit §5.2).
//
// /dashboards listesinin SAF süzgeç çekirdeği: arama (ad + açıklama +
// etiket), etiket çipleri (seçilen HER etiket panoda olmalı: çip eklemek
// listeyi daraltır, Datadog/Grafana etiket süzgeci gibi) ve "yalnız
// favoriler". Değerler URL'den gelir (?q / ?tags / ?fav); sayfa yalnız çağırır.
import type { DashboardSummary } from './types';

export interface DashListFilter {
  q: string;
  tags: readonly string[];
  fav: boolean;
}

/** SAF: süzgeçten geçen panolar (sıra korunur). */
export function filterDashboards<T extends DashboardSummary>(items: readonly T[], f: DashListFilter, starred: ReadonlySet<string>): T[] {
  const needle = f.q.trim().toLowerCase();
  const want = f.tags.map(t => t.toLowerCase());
  return items.filter(d => {
    if (f.fav && !starred.has(d.id)) return false;
    const tags = (d.tags ?? []).map(t => t.toLowerCase());
    if (want.some(t => !tags.includes(t))) return false;
    if (!needle) return true;
    return d.name.toLowerCase().includes(needle)
      || (d.description ?? '').toLowerCase().includes(needle)
      || tags.some(t => t.includes(needle));
  });
}

export interface TagCount { tag: string; n: number }

/** SAF: çip satırı — en sık `max` etiket (eşitlikte alfabetik); seçili
 *  etiketler sıklığı ne olursa olsun HEP görünür (seçimi geri almak için). */
export function tagChips(items: readonly DashboardSummary[], selected: readonly string[], max = 12): TagCount[] {
  const counts = new Map<string, number>();
  for (const d of items) for (const t of new Set(d.tags ?? [])) {
    if (t.trim()) counts.set(t, (counts.get(t) ?? 0) + 1);
  }
  const all = [...counts.entries()].map(([tag, n]) => ({ tag, n }))
    .sort((a, b) => b.n - a.n || a.tag.localeCompare(b.tag));
  const top = all.slice(0, max);
  for (const s of selected) {
    if (!top.some(t => t.tag === s)) top.push({ tag: s, n: counts.get(s) ?? 0 });
  }
  return top;
}

/** SAF: çip aç/kapa → yeni seçim (sıra korunur). */
export function toggleTag(selected: readonly string[], tag: string): string[] {
  return selected.includes(tag) ? selected.filter(t => t !== tag) : [...selected, tag];
}

/** SAF: "Çoğalt" adı — "Ad (kopya)", çakışırsa "Ad (kopya 2)", … */
export function copyName(name: string, existing: readonly string[]): string {
  const base = `${name} (kopya)`;
  const taken = new Set(existing);
  if (!taken.has(base)) return base;
  for (let i = 2; i < 1000; i++) {
    const n = `${name} (kopya ${i})`;
    if (!taken.has(n)) return n;
  }
  return `${base} ${Date.now()}`;
}
