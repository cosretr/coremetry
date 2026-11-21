// navLayer — wave 3 (komut paleti = ana gezinme katmanı; Datadog Quick Nav /
// Grafana komut paleti emsali). SAF satır üreticileri; gövde
// (CommandPaletteBody) mevcut sonuç listesine ekler, sıra:
//   trace kimliği (varsa, en üstte — results.ts) → zaman aralığı eylemleri →
//   kayıtlı görünümler → mevcut sonuçlar → pod'lar → kısayollar → "CoSRE'ye sor".
// Metinler çağıranın `t`'siyle (i18n); veri sunucu aramasından (eager katalog yok).
import { PRESET_SECONDS } from '@/lib/utils';
import type { EntityRecord, SavedView } from '@/lib/types';
import type { Shortcut } from '@/lib/keyboard';
import { entityHref } from '@/lib/entityHref';
import type { PaletteResult } from './pages';

type T = (key: string) => string;
const fill = (s: string, vars: Record<string, string>) =>
  s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? vars[k] : m));

/** saved_views.page → rota. Listede olmayan sayfa (sistem / değerlendirme) palette çıkmaz. */
export const SAVED_VIEW_ROUTES: Record<string, string> = {
  traces: '/traces', logs: '/logs', problems: '/problems', anomalies: '/anomalies',
  messaging: '/messaging', endpoints: '/endpoints', slowqueries: '/databases/slow-queries',
  databases: '/databases', inbox: '/inbox', explore: '/explore',
};

/** Boş sorguda gösterilen kayıtlı görünüm sayısı (sabitli önce — sunucu sırası). */
export const PALETTE_VIEW_EMPTY_MAX = 5;
export const PALETTE_VIEW_MATCH_MAX = 8;

export function savedViewRows(views: readonly SavedView[], query: string, t: T): PaletteResult[] {
  const q = query.trim().toLowerCase();
  const rows: PaletteResult[] = [];
  for (const v of views) {
    const route = SAVED_VIEW_ROUTES[v.page];
    if (!route) continue;
    if (q && !v.name.toLowerCase().includes(q) && !v.page.includes(q)) continue;
    rows.push({
      kind: 'page', group: 'views', label: v.name,
      hint: fill(t('palette.view.hint'), { page: v.page }),
      to: v.queryString ? `${route}?${v.queryString}` : route,
    });
    if (rows.length >= (q ? PALETTE_VIEW_MATCH_MAX : PALETTE_VIEW_EMPTY_MAX)) break;
  }
  return rows;
}

const RANGE_WORDS = ['range', 'time', 'last', 'zaman', 'aralık', 'aralik', 'son '];
export const PALETTE_RANGE_PRESETS = ['15m', '1h', '3h', '6h', '24h', '7d'] as const;

/** Sorgu bir zaman aralığı niyeti mi: anahtar kelime → hazır listesi; "1h" gibi hazır ad → o tek satır. */
export function rangeRows(query: string, t: T): PaletteResult[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const presets: string[] = q in PRESET_SECONDS ? [q]
    : RANGE_WORDS.some(w => w.startsWith(q) || q.startsWith(w.trim())) && q.length >= 2 ? [...PALETTE_RANGE_PRESETS]
      : [];
  return presets.map(p => ({
    kind: 'action' as const, group: 'range' as const,
    label: fill(t('palette.range'), { r: t(`range.${p}`) }),
    hint: t('palette.range.hint'),
    rangePreset: p,
  }));
}

export function podRows(pods: readonly { rec: EntityRecord; clusterName: string }[], t: T): PaletteResult[] {
  return pods.map(({ rec, clusterName }) => ({
    kind: 'page' as const, group: 'pods' as const,
    label: rec.namespace ? `${rec.namespace}/${rec.name}` : rec.name,
    hint: fill(t('palette.pod.hint'), { cluster: clusterName }),
    to: entityHref(rec, { clusterName }),
  }));
}

/** Kısayol listesi: "?" → hepsi; başka sorgu → etiketi/tuşu eşleşenler (≤6). */
export function shortcutRows(all: readonly Shortcut[], query: string): PaletteResult[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const seen = new Set<string>();
  const rows: PaletteResult[] = [];
  for (const s of all) {
    if (seen.has(s.keys)) continue;
    seen.add(s.keys);
    if (q !== '?' && !(q.length >= 3 && s.label.toLowerCase().includes(q))) continue;
    rows.push({ kind: 'action', group: 'shortcuts', label: s.label, hint: s.keys.replace(/\bmod\b/, '⌘/Ctrl'), shortcut: true });
  }
  return q === '?' ? rows : rows.slice(0, 6);
}

/** "CoSRE'ye sor: …" — yazılan metin, copilot açıkken; kimlik/tek karakter değil. */
export function askRow(query: string, copilotEnabled: boolean, t: T): PaletteResult[] {
  const q = query.trim();
  if (!copilotEnabled || q.length < 3 || q === '?') return [];
  return [{ kind: 'action', group: 'ask', label: fill(t('palette.ask'), { q }), hint: t('palette.ask.hint'), askCosre: q }];
}
