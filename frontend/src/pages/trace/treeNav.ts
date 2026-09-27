// treeNav.ts — v0.10.968 — Trace › Metrics pod tablosunun treegrid klavyesi (SAF).
//
// v0.10.968 — Tablo `role="treegrid"`: gezici tabindex (tek Tab durağı) ve
// tbody'de TEK devredilmiş onKeyDown. Karar burada, DOM'suz ve tablo-güdümlü
// testli (treeNav.test.ts); bileşen yalnız sonucu uygular. WAI-ARIA treegrid
// satır kipi: ↑ ↓ Home End odak; → kapalı grubu açar, açık gruptan ilk
// çocuğa iner; ← açık grubu kapatır, çocuktan ebeveynine çıkar; Enter/Boşluk
// pod'u seçer, grubu açar/kapatır, "N pod daha"yı genişletir.

export interface TreeNavRow {
  kind: 'group' | 'pod' | 'more' | 'nopod' | 'nopod-svc';
  level: number;
  open?: boolean;
}

export type TreeNavResult =
  | { kind: 'focus'; index: number }
  | { kind: 'toggle'; index: number; open: boolean }
  | { kind: 'activate'; index: number }
  | null;

const expandable = (r: TreeNavRow) => r.kind === 'group' || r.kind === 'nopod';

/** v0.10.968 — (satırlar, odaklı indeks, tuş) → yapılacak iş; null = tuş bizim değil. */
export function treeNav(rows: readonly TreeNavRow[], index: number, key: string): TreeNavResult {
  const n = rows.length;
  if (n === 0) return null;
  const i = Math.min(Math.max(index, 0), n - 1);
  const r = rows[i];
  switch (key) {
    case 'ArrowDown': return { kind: 'focus', index: Math.min(n - 1, i + 1) };
    case 'ArrowUp': return { kind: 'focus', index: Math.max(0, i - 1) };
    case 'Home': return { kind: 'focus', index: 0 };
    case 'End': return { kind: 'focus', index: n - 1 };
    case 'ArrowRight': {
      if (!expandable(r)) return { kind: 'focus', index: i };
      if (!r.open) return { kind: 'toggle', index: i, open: true };
      const child = rows[i + 1];
      return child && child.level > r.level ? { kind: 'focus', index: i + 1 } : { kind: 'focus', index: i };
    }
    case 'ArrowLeft': {
      if (expandable(r) && r.open) return { kind: 'toggle', index: i, open: false };
      if (r.level > 1) {
        for (let j = i - 1; j >= 0; j--) if (rows[j].level < r.level) return { kind: 'focus', index: j };
      }
      return { kind: 'focus', index: i };
    }
    case 'Enter':
    case ' ': {
      if (expandable(r)) return { kind: 'toggle', index: i, open: !r.open };
      if (r.kind === 'pod' || r.kind === 'more') return { kind: 'activate', index: i };
      return null;
    }
    default: return null;
  }
}
