import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, dirname, relative, sep } from 'node:path';
import { stripTsComments } from './zLayers.test';

// uiImportRatchet — v0.10.1614 (shadcn Faz 1 R6, plan §3.1).
//
// components/ui DONDURULDU (DECISIONS 2026-10-30): yeni dosya yok, yeni
// içe aktaran yok; Faz 2 dilimleri atomları sayfa sayfa siler. Sayılan şey
// atom BAŞINA içe aktaran DOSYA sayısı (CEILINGS) + başlık toplamı
// (dosya×atom çifti). Neden dosya, ifade değil: tek barrel importunu ikiye
// bölen bir düzenleme göç olmadan ifade sayısını oynatır. Neden atom başına:
// yalnız toplam tavanı `+5 Button / −5 Badge`i geçirir. CEILINGS'te olmayan
// atomun tavanı 0 (ui/ donmuş). Tavanlar yalnız AŞAĞI iner; göç eden commit
// düşürür.
//
// Popülasyon: src/**/*.{ts,tsx}, testler ve components/ui/** HARİÇ
// (atomların birbirini içe aktarması benimseme değil). `import type` sayılır
// (tip importu da dosyayı atoma bağlar). Barrel (`components/ui`,
// `components/ui/index`) adları atom DOSYASINA, barrel'ın KENDİ
// `export … from './X'` satırları test anında ayrıştırılarak eşlenir — elle
// harita yok, barrel düzenlemesi mandalı kaydıramaz. DataTable/* tek atom.
const SRC = resolve(__dirname, '..');
const UI = join(SRC, 'components', 'ui');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(p) && !/\.test\.tsx?$/.test(p)) out.push(p);
  }
  return out;
}
const files = walk(SRC).filter(p => !p.startsWith(UI + sep));

/** './Button' → 'Button'; './DataTable/VirtualTable' → 'DataTable'. */
const atomOf = (subpath: string) => subpath.replace(/^\.\//, '').split('/')[0].replace(/\.(tsx?|js)$/, '');

/** Barrel: dışa aktarılan ad → atom dosyası. */
export function parseBarrel(src: string): Map<string, string> {
  const map = new Map<string, string>();
  for (const m of stripTsComments(src).matchAll(/export\s+(?:type\s+)?\{([^}]*)\}\s*from\s*['"]([^'"]+)['"]/g)) {
    for (const raw of m[1].split(',')) {
      const name = raw.trim().replace(/^type\s+/, '').split(/\s+as\s+/).pop()!.trim();
      if (name) map.set(name, atomOf(m[2]));
    }
  }
  return map;
}
const BARREL = parseBarrel(readFileSync(join(UI, 'index.ts'), 'utf8'));

/** Bir kaynağın bağlandığı ui atomları (bilinmeyen barrel adı → '?ad'). */
export function atomsImported(src: string, file: string): Set<string> {
  const out = new Set<string>();
  for (const m of stripTsComments(src).matchAll(/import\s+(type\s+)?([\s\S]*?)\s+from\s+['"]([^'"]+)['"]/g)) {
    const spec = m[3];
    let sub: string | null = null;
    if (spec === '@/components/ui' || spec.startsWith('@/components/ui/')) sub = spec.slice('@/components/ui'.length);
    else if (spec.startsWith('.')) {
      const abs = resolve(dirname(file), spec);
      if (abs === UI || abs.startsWith(UI + sep)) sub = abs.slice(UI.length).split(sep).join('/');
    }
    if (sub === null) continue;
    sub = sub.replace(/^\//, '');
    if (sub === '' || sub === 'index') {
      const names = /\{([\s\S]*)\}/.exec(m[2])?.[1] ?? '';
      for (const raw of names.split(',')) {
        const name = raw.trim().replace(/^type\s+/, '').split(/\s+as\s+/)[0].trim();
        if (name) out.add(BARREL.get(name) ?? `?${name}`);
      }
      if (!/\{/.test(m[2])) out.add('?barrel-default-or-namespace');
    } else out.add(atomOf(sub));
  }
  return out;
}

// Taban: R6 commit'inde ölçüldü — 342 dosya, 969 çift (plan §3.1 tablosu
// birebir). R8: 969 → 902 (PageShell 51 + PageControls 17 ayrıldı,
// DisclosureButton +1 taşınan PageControls'ten). R9: 902 → 904 (coremetry/
// DataTable sarmalayıcısının iki dosyası ui/DataTable'ı içe aktarıyor).
const CEILINGS: Record<string, number> = {
  Button: 206, // Faz 2 dilim 1 R7: 209 → 207 (Remote Cluster); etkileşim P2: 207 → 206 (⌘K)
  // v0.10.1617 (R9): 126 → 128 — coremetry/DataTable dondurulmuş API'nin Faz 1
  // gövdesi ui/DataTable'ı SARIYOR (plan §6: "thin wrapper over today's
  // code"): DataTable.tsx (gövde) + types.ts (bugünkü tiplerin yeniden dışa
  // aktarımı). Benimseme değil, halef; gövde Traces diliminde değişince düşer.
  DataTable: 127, // Faz 2 dilim 1 R7: 128 → 127 (SpanClusterValuesPanel)
  Badge: 60, // R7: 63 → 61; CoSRE 2b B1: 61 → 60 (CosreV2StatusLine → coremetry Field)
  IconButton: 50,
  Field: 36, // R7: 37 → 36
  Chip: 36,
  ConfirmDialog: 35,
  SegmentedControl: 35,
  // v0.10.1616 (R8): 31 → 32 — ARTIŞ değil TAŞIMA: coremetry/PageControls
  // (ui/'den değişmeden taşındı, karar k) dar ekran "Filtreler (N)"
  // düğmesini hâlâ DisclosureButton ile çiziyor; ui/ içindeyken sayılmıyordu.
  // Düğmeyi shadcn'e çevirmek görünür değişiklik olur — PageControls'ü
  // geçiren dilimin işi; o commit bunu 31'e indirir.
  DisclosureButton: 32,
  LinkButton: 29, // R7: 31 → 30; etkileşim P2: 30 → 29 (⌘K)
  Modal: 23,
  Stack: 21, // R7: 23 → 21
  Card: 19,
  TabStrip: 17,
  Drawer: 15,
  Menu: 14,
  StatTile: 13,
  SafeHref: 12,
  Popover: 11,
  SearchField: 11,
  PanelTitle: 10,
  PriorityBadge: 8,
  ButtonGroup: 7,
  Tooltip: 6,
  OptionRow: 4,
  EnterHint: 3,
  MethodBadge: 3,
  SectionUnavailable: 3,
  ColumnsPicker: 2,
  FacetMultiSelect: 2,
  PickerPopover: 2,
  useFocusTrap: 1, // etkileşim P2: 2 → 1 (⌘K → Radix Dialog)
  IdentityChip: 1,
  RouteSkeleton: 1,
  TileButton: 1,
  VirtualList: 1,
};

// 38 girdi (DataTable/ dizini ve index.ts dahil, testler hariç). v0.10.1616
// (R8): PageShell + PageControls coremetry/'e taşındı (45 → 43). Faz 2
// dilim 1 R4: KeyValue, SectionHead, ActionRow coremetry/'e taşındı (43 → 40).
// CoSRE v2 Faz 2b B1: saf drawerWidth + emptyAreaClose lib/'e taşındı (40 →
// 38) — v2 Sheet'i ui/'den hiçbir şey içe aktaramaz (tavanı olmayan atom = 0).
const FROZEN_UI_ENTRIES = [
  'Badge.tsx',
  'Button.tsx',
  'ButtonGroup.tsx',
  'Card.tsx',
  'Chip.tsx',
  'ColumnsPicker.tsx',
  'ConfirmDialog.tsx',
  'DataTable',
  'DisclosureButton.tsx',
  'Drawer.tsx',
  'EnterHint.tsx',
  'FacetMultiSelect.tsx',
  'Field.tsx',
  'IconButton.tsx',
  'IdentityChip.tsx',
  'LinkButton.tsx',
  'Menu.tsx',
  'MethodBadge.tsx',
  'Modal.tsx',
  'OptionRow.tsx',
  'PanelTitle.tsx',
  'PickerPopover.tsx',
  'Popover.tsx',
  'PriorityBadge.tsx',
  'RouteSkeleton.tsx',
  'SafeHref.tsx',
  'SearchField.tsx',
  'SectionUnavailable.tsx',
  'SegmentedControl.tsx',
  'Stack.tsx',
  'StatTile.tsx',
  'TabStrip.tsx',
  'TileButton.tsx',
  'Tooltip.tsx',
  'VirtualList.tsx',
  'buttonGroupContext.ts',
  'index.ts',
  'useFocusTrap.ts',
];

function measure() {
  const perAtom = new Map<string, Set<string>>();
  const importers = new Set<string>();
  for (const p of files) {
    const atoms = atomsImported(readFileSync(p, 'utf8'), p);
    for (const a of atoms) {
      if (!perAtom.has(a)) perAtom.set(a, new Set());
      perAtom.get(a)!.add(relative(SRC, p));
      importers.add(p);
    }
  }
  return { perAtom, importers };
}

describe('components/ui içe aktarma mandalı', () => {
  const { perAtom, importers } = measure();
  const pairs = [...perAtom.values()].reduce((a, s) => a + s.size, 0);

  it('tarama kaynak buluyor ve her barrel adı bir atoma çözülüyor', () => {
    expect(files.length).toBeGreaterThan(400);
    expect(BARREL.size).toBeGreaterThan(40);
    expect([...perAtom.keys()].filter(a => a.startsWith('?')), 'barrel\'da olmayan ad').toEqual([]);
  });

  it('atom başına içe aktaran dosya sayısı tavanı aşmaz (listede olmayan = 0)', () => {
    const over: string[] = [];
    for (const [atom, set] of perAtom) {
      const cap = CEILINGS[atom] ?? 0;
      if (set.size > cap) over.push(`${atom}: ${set.size} > ${cap} — yeni içe aktaran yerine coremetry/ ya da shadcn/ kullan (ui/ donmuş)`);
    }
    expect(over).toEqual([]);
  });

  it('başlık toplamı (dosya×atom) tavanların toplamını aşmaz', () => {
    const cap = Object.values(CEILINGS).reduce((a, n) => a + n, 0);
    expect(pairs).toBeLessThanOrEqual(cap);
    expect(importers.size).toBeGreaterThan(0);
  });

  it('components/ui girdi listesi donmuş: yeni dosya yok', () => {
    const now = readdirSync(UI).filter(e => !/\.test\.tsx?$/.test(e)).sort();
    expect(now).toEqual([...FROZEN_UI_ENTRIES].sort());
  });

  it('ayrıştırıcı: barrel, doğrudan yol, göreli yol, type importu, takma ad', () => {
    const f = join(SRC, 'pages', 'X.tsx');
    const a = (s: string) => [...atomsImported(s, f)].sort();
    expect(a("import { Button, Field as LabeledInput, type Tone } from '@/components/ui';")).toEqual(['Badge', 'Button', 'Field']);
    expect(a("import type { ColumnDef } from '../components/ui/DataTable/types';")).toEqual(['DataTable']);
    expect(a("import { Modal } from '@/components/ui/Modal';")).toEqual(['Modal']);
    expect(a("import { x } from '@/components/uix';")).toEqual([]);
    expect(a("import {\n  Chip,\n  Drawer,\n} from '../components/ui';")).toEqual(['Chip', 'Drawer']);
  });
});
