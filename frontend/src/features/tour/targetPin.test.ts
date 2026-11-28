// targetPin.test.ts — v0.10.1334 rehberli tur çapa pini. Bir refactor
// Services tablosundan `data-tour="services-table"`ı düşürürse tur SESSİZCE
// zaman aşımına düşer (kart "veri henüz yok" der, halka hiç çizilmez) ve
// dört kapının hiçbiri bunu görmez. Bu dosya kaynak düzeyinde pinler:
//   1. her AÇIK adımın hedef/yedek çapası TOUR_ANCHORS'ta ve sahibi dosyada
//      literal olarak duruyor;
//   2. src'de sahipsiz data-tour yok (her literal TOUR_ANCHORS'ta), dinamik
//      `data-tour={…}` yalnız izinli dosyada;
//   3. TOUR_ANCHORS'ta ölü giriş yok (her çapa sahibinde gerçekten var).
// v0.10.1336 (R2) — açık adımlar beş; çapa denetimi her adımın HER KARESİNE
// (alt adım) uygulanır; geçirgen bileşenler (Combobox, FilterQueryBox) çapa
// kimliğini çağırandan alır (dinamik data-tour izinli), kimlik literal olarak
// sahibinde (Traces.tsx) durur.
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve, relative } from 'node:path';
import { TOUR_ANCHORS, TOUR_STEPS, stepFrames, type TourCtx } from './steps';

const SRC = resolve(__dirname, '../..');
const rel = (p: string) => relative(SRC, p).split('\\').join('/');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    if (e === 'node_modules') continue;
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(e) && !/\.test\.tsx?$/.test(e)) out.push(p);
  }
  return out;
}

// Zengin bağlam: veri var, copilot açık → hiçbir adım kapanmaz.
const RICH: TourCtx = {
  service: 'svc', range: null, serviceCount: 10, spans5m: 100, environments: 1, copilotEnabled: true, returnTo: '/',
};
const ENABLED = TOUR_STEPS.filter(s => s.requires(RICH) !== 'not_yet');
const anchorsOf = (sel: string | null | undefined) =>
  [...(sel ?? '').matchAll(/\[data-tour="([^"]+)"\]/g)].map(m => m[1]);
const read = (file: string) => readFileSync(join(SRC, file), 'utf8');
const hasLiteral = (src: string, id: string) =>
  src.includes(`data-tour="${id}"`) || src.includes(`'${id}'`) || src.includes(`"${id}"`);

// Dinamik `data-tour={…}` (harita / geçirgen prop üzerinden) taşıyabilen dosyalar.
// v0.10.2488 — HealthDot çağıranın çapasını (data-tour) geçirir; çapa literal'i Services.tsx'te.
const DYNAMIC_OK = new Set(['pages/Services.tsx', 'components/coremetry/HealthDot.tsx', 'components/Combobox.tsx', 'components/FilterQueryBox.tsx']);
const FRAMES = ENABLED.flatMap(s => stepFrames(s).map(f => ({ id: `${s.id}/${f.key}`, target: f.target, fallbackTarget: f.fallbackTarget })));

describe('tour çapaları (v0.10.1334)', () => {
  it('R2: beş adımın hepsi açık (v0.10.1336)', () => {
    expect(ENABLED.map(s => s.id)).toEqual(['welcome', 'services', 'operations', 'traces', 'cosre']);
    expect(TOUR_STEPS.every(s => s.requires(RICH) === null)).toBe(true);
  });

  it('her açık karenin hedefi [data-tour="…"] ile başlıyor ya da null', () => {
    for (const s of FRAMES) {
      if (s.target === null) continue;
      expect(s.target.startsWith('[data-tour="'), `${s.id}: ${s.target}`).toBe(true);
      if (s.fallbackTarget) expect(s.fallbackTarget.startsWith('[data-tour="'), `${s.id} yedek`).toBe(true);
    }
  });

  it('her açık karenin çapası sahibi dosyada literal olarak duruyor', () => {
    for (const s of FRAMES) {
      for (const id of [...anchorsOf(s.target), ...anchorsOf(s.fallbackTarget)]) {
        const owner = TOUR_ANCHORS[id];
        expect(owner, `${s.id}: "${id}" TOUR_ANCHORS'ta yok`).toBeTruthy();
        expect(hasLiteral(read(owner), id), `${owner} içinde data-tour="${id}" yok`).toBe(true);
      }
    }
  });

  it('TOUR_ANCHORS\'ta ölü giriş yok', () => {
    for (const [id, owner] of Object.entries(TOUR_ANCHORS)) {
      expect(hasLiteral(read(owner), id), `${owner} içinde "${id}" yok — ölü çapa`).toBe(true);
    }
  });

  it('her açık karenin hedef/yedek çapası TOUR_ANCHORS\'ta (R2 çapaları dahil)', () => {
    const used = new Set(FRAMES.flatMap(f => [...anchorsOf(f.target), ...anchorsOf(f.fallbackTarget)]));
    for (const id of ['svc-nav', 'svc-crumbs', 'svc-tab-operations', 'ops-impact-col', 'ops-normalized', 'ops-table',
      'traces-active-chips', 'traces-quick-chips', 'traces-min-ms', 'traces-service', 'traces-table',
      'cosre-fab', 'cosre-drawer', 'cosre-composer']) {
      expect(used.has(id), id).toBe(true);
    }
  });

  it('src\'de sahipsiz data-tour yok; dinamik kullanım yalnız izinli dosyada', () => {
    const orphans: string[] = [];
    const dynamic: string[] = [];
    for (const f of walk(SRC)) {
      const src = readFileSync(f, 'utf8');
      for (const m of src.matchAll(/data-tour="([^"]+)"/g)) {
        // Seçici dizgeleri (steps.ts) de bu kalıba uyar; onlar beyan, çapa değil.
        if (rel(f) === 'features/tour/steps.ts') continue;
        if (TOUR_ANCHORS[m[1]] !== rel(f)) orphans.push(`${rel(f)}: ${m[1]}`);
      }
      if (/data-tour=\{/.test(src) && !DYNAMIC_OK.has(rel(f))) dynamic.push(rel(f));
    }
    expect(orphans).toEqual([]);
    expect(dynamic).toEqual([]);
  });
});
