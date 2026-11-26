// @vitest-environment jsdom
//
// ProblemRepeatBlock.render — v0.10.2281 (entity grafı S3): çip nötr (renk
// sınıfı yok) ve ipucu gölge ölçümü söyler; "Tekrar" bloğu beş satır +
// en çok 5 önceki bölüm.
import { describe, it, expect } from 'vitest';
import type { JSX } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { tf } from '@/lib/i18n';
import { frequentReportLines, type FrequentVerdict, type GraphT } from '@/lib/problemGraph';
import { FrequentChip, RepeatBlock } from './ProblemRepeatBlock';

function mount(el: JSX.Element) {
  const div = document.createElement('div');
  div.innerHTML = renderToStaticMarkup(el);
  return div;
}
const tr: GraphT = (k, v) => tf(k, 'tr', v);
const en: GraphT = (k, v) => tf(k, 'en', v);
const H = 3_600_000_000_000;

const v: FrequentVerdict = {
  signature: 'db:orders-db|latency|', entity: 'db:orders-db', name: 'orders-db', family: 'latency', viaOrigin: true,
  prior24h: 3, prior7d: 6, days7d: 5, frequent: true, kind: 'hold', priority: 'P1', maxPriority: 'P1', magnitude: 7, maxMagnitude: 7.6,
  episodes: Array.from({ length: 6 }, (_, i) => ({ id: `e${i}`, startedAt: i * H, resolvedAt: i * H + 26 * 60e9, priority: 'P1' })),
};

describe('ProblemRepeatBlock', () => {
  it('chip is neutral and says it is a shadow measurement', () => {
    const d = mount(<FrequentChip f={v} t={tr} />);
    const chip = d.querySelector('[data-testid="pg-frequent-chip"]')!;
    expect(chip.textContent).toBe('sık tekrar eden');
    expect(chip.getAttribute('title')).toContain('Bildirimler değişmedi');
    expect(chip.className.split(' ').filter(c => !c.includes(':'))).not.toContainEqual(expect.stringMatching(/err|warn|destructive|red|amber|bg-/));
    expect(mount(<FrequentChip f={v} t={en} />).textContent).toBe('frequent');
  });
  it('v0.10.2342 (S5 on): same word, tooltip says the opening is held', () => {
    const chip = mount(<FrequentChip f={v} t={tr} enforce />).querySelector('[data-testid="pg-frequent-chip"]')!;
    expect(chip.textContent).toBe('sık tekrar eden');
    expect(chip.getAttribute('title')).toContain('Açılış bildirimi tutulur');
    expect(chip.getAttribute('title')).not.toContain('Gölge');
  });
  it('repeat block: kind, windows, severity, rule line, at most 5 episodes', () => {
    const d = mount(<RepeatBlock v={v} t={tr} when={ns => `d${ns / H}`} />);
    const txt = d.textContent ?? '';
    expect(txt).toContain('orders-db · gecikme · olası kökene göre');
    expect(txt).toContain('bu 4. bölüm (eşik: 3 önceki bölüm)');
    expect(txt).toContain('5 ayrı günde görüldü');
    expect(txt).toContain('artmadı: bildirim önceliği P1');
    expect(txt).toContain('açılış bildirimi gitmezdi');
    const eps = d.querySelectorAll('[data-testid="pg-repeat-episodes"] li');
    expect(eps).toHaveLength(5);
    expect(eps[0].textContent).toBe('d0 · 26 dk sürdü · P1');
  });
  it('severity increased: would still notify', () => {
    const txt = mount(<RepeatBlock v={{ ...v, kind: 'severity', bypass: 'impact_2x' }} t={tr} when={String} />).textContent ?? '';
    expect(txt).toContain('arttı (büyüklük en büyüğün en az 2 katı)');
    expect(txt).toContain('yine giderdi');
  });
  it('admin report lines', () => {
    const l = frequentReportLines({ frequent: 4, wouldHold: 3, severityUp: 1, heldBecameP1: 0, atThreshold: { '2': 5, '3': 4 },
      topSignatures: [{ signature: 's', name: 'orders-db', family: 'latency', held: 2 }], reads: { passes: 9, reads: 2, rows: 4100, maxRows: 2100, capped: 0, errors: 0 } });
    expect(l[0]).toContain('tutulurdu 3 · şiddet arttığı için yine giderdi 1 · eşik 24 sa 2/3/5: 5/4/0');
    expect(l[1]).toBe("Tutulacakken sonradan P1'e çıkan (hedef 0): 0");
    expect(l[3]).toContain('4100 satır');
    expect(frequentReportLines(undefined)).toEqual([]);
  });
});
