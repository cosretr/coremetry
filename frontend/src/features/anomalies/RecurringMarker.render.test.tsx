// @vitest-environment jsdom
//
// RecurringMarker.render — v0.10.1049, yinelenen anomali ayrımı.
//
// Operatör: "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık
// her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor."
//
// NE ÇİVİLİYOR: yinelenmemiş satırda HİÇBİR düğüm yok (sarmalayıcı da);
// yinelenen satırda nötr (.b-gray) tek kelime "yinelenen", sayı + ilk tarih
// ipucunda; iki tüketici (Problems kuyruğu, /anomalies geçmişi) işareti bu
// bileşenden alıyor.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { RecurringMarker } from './RecurringMarker';

let root: Root | null = null;
let host: HTMLDivElement | null = null;

function render(node: React.ReactNode): HTMLDivElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  act(() => {
    root = createRoot(host!);
    root.render(node);
  });
  return host;
}

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null;
  host = null;
});

describe('RecurringMarker', () => {
  it.each<[string, { episodeCount?: number; firstStartedAt?: number; line?: boolean }]>([
    ['sayaç yok', {}],
    ['sayaç 1', { episodeCount: 1, firstStartedAt: 1_700_000_000e9 }],
    ['sayaç yok, satır kipi', { line: true }],
  ])('%s → hiçbir şey çizilmez', (_n, props) => {
    const el = render(<RecurringMarker {...props} />);
    expect(el.innerHTML).toBe('');
  });

  it('sayaç 3 → nötr "yinelenen", ipucunda sayı ve ilk tarih', () => {
    const el = render(<RecurringMarker episodeCount={3} firstStartedAt={1_700_000_000e9} />);
    const b = el.querySelector('[data-recurring]') as HTMLElement;
    expect(b).not.toBeNull();
    expect(b.textContent).toBe('yinelenen');
    expect(b.className).toBe('badge b-gray');
    expect(b.getAttribute('data-recurring')).toBe('3');
    expect(b.title).toMatch(/^Yinelenen anomali: bu 3\. kez, ilk kez \d{2}\.\d{2}\.\d{4} \d{2}:\d{2}\./);
  });

  // v0.10.1054 — kuralın bastırdığı deploy atılmaz: rozet aynı (renk yok),
  // ipucunun yeni satırında nötr deploy metni.
  it('priorDeploy → ipucunda "deploy <sürüm> N dk önce — öncesinde de görülüyordu", rozet aynı', () => {
    const el = render(<RecurringMarker episodeCount={8} priorDeploy={{ version: 'v2.0.0', timeUnixNs: 1, ageSeconds: 600 }} />);
    const b = el.querySelector('[data-recurring]') as HTMLElement;
    expect(b.className).toBe('badge b-gray');
    expect(b.textContent).toBe('yinelenen');
    expect(b.title).toBe('Yinelenen anomali: bu 8. kez. Sayaç kaydın ömrüyle sınırlı (son tetiklenmeden 30 gün sonra kayıt düşer).\n' +
      'deploy v2.0.0 10 dk önce — öncesinde de görülüyordu');
  });

  it('satır kipi → damga altındaki yaş satırı kalıbında', () => {
    const el = render(<RecurringMarker episodeCount={2} line />);
    expect(el.firstElementChild?.className).toBe('ib-when__ago');
    expect(el.querySelector('[data-recurring]')?.getAttribute('title'))
      .toBe('Yinelenen anomali: bu 2. kez. Sayaç kaydın ömrüyle sınırlı (son tetiklenmeden 30 gün sonra kayıt düşer).');
  });

  it('iki tüketici işareti bu bileşenden alıyor', () => {
    const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');
    expect(read('../../pages/Inbox.tsx')).toContain(
      '<RecurringMarker episodeCount={it.anomaly.episodeCount} firstStartedAt={it.anomaly.firstStartedAt} />');
    expect(read('./streams.tsx')).toContain(
      '<RecurringMarker episodeCount={e.episodeCount} firstStartedAt={e.firstStartedAt} priorDeploy={e.priorDeploy} line />');
  });
});
