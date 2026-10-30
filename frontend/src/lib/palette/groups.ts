// lib/palette/groups — v0.10.1658 (etkileşim dilimi P2, plan §1.2, Q1 onaylı).
// ⌘K sonuçlarına grup BAŞLIKLARI: sıralama bugünkü sıra (v0.10.126 varlıklar
// önce) — gruplama yalnız ardışık aynı-türden satırları bir başlık altında
// toplar, satırları yeniden SIRALAMAZ. Aynı grup iki ayrı yerde görünürse
// (ör. operasyon araması "son hata trace'i" satırı, kimlik satırlarından önce) iki ayrı
// başlık olur — sıra her zaman önce gelir.
import type { PaletteResult } from './pages';

export type PaletteGroupId =
  | 'pinned' | 'recent' | 'trace' | 'actions' | 'services' | 'endpoints'
  | 'operations' | 'pages' | 'identity' | 'search';

export const GROUP_LABELS: Record<PaletteGroupId, string> = {
  pinned: 'Pinned',
  recent: 'Recent',
  trace: 'Trace',
  actions: 'Actions',
  services: 'Services',
  endpoints: 'Endpoints',
  operations: 'Operations',
  pages: 'Pages',
  identity: 'Problem / identity',
  search: 'Search',
};

/** Bir satırın grubu: açık `group` ezmesi > boş sorguda ★/Recent > kind. */
export function groupOf(r: PaletteResult): PaletteGroupId {
  if (r.group) return r.group;
  switch (r.kind) {
    case 'service': return 'services';
    case 'endpoint': return 'endpoints';
    case 'operation': return 'operations';
    case 'trace': return 'trace';
    case 'action': return 'actions';
    default: return 'pages';
  }
}

export interface PaletteResultGroup {
  /** Benzersiz (aynı grup ikinci kez görünürse `-2` eki). */
  id: string;
  group: PaletteGroupId;
  label: string;
  rows: PaletteResult[];
}

/** Sıralı sonuçları ardışık gruplara böler; sıra korunur. */
export function groupPaletteResults(results: PaletteResult[]): PaletteResultGroup[] {
  const out: PaletteResultGroup[] = [];
  const seen = new Map<PaletteGroupId, number>();
  for (const r of results) {
    const g = groupOf(r);
    const last = out[out.length - 1];
    if (last && last.group === g) { last.rows.push(r); continue; }
    const n = (seen.get(g) ?? 0) + 1;
    seen.set(g, n);
    out.push({ id: n === 1 ? g : `${g}-${n}`, group: g, label: GROUP_LABELS[g], rows: [r] });
  }
  return out;
}
