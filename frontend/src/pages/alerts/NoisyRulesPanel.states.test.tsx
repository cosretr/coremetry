// @vitest-environment jsdom
//
// NoisyRulesPanel.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR:
//   • Rapor okunamayınca hata (aynı anlam: "öneriler yok değil, alınamadı" +
//     aynı ↻ Retry = load) panelin statik tablosunun İÇİNDE, başlık dururken
//     basılır — eskiden tablo yerine çıplak QueryErrorInline dönüyordu.
//     Satırdan türeyen başlık parçaları ("N rules…", AI anlatımı) hata
//     hâlinde yok ("0 rules" demez).
//   • Yükleniyor ve "hiç gürültülü kural yok" self-hide AYNEN (öneri paneli,
//     sayfa gövdesi değil).
//   • Toplu Apply sonrası tazeleme düşerse eski öneri listesi SESSİZCE
//     ekranda kalmaz (eskiden `.catch(() => {})`): hata satırı gelir.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { AlertRule, NoisyRule } from '@/lib/types';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';

const h = vi.hoisted(() => ({
  noisy: vi.fn<(since: string, limit: number) => Promise<{ rules: unknown[] }>>(),
  mutate: vi.fn(() => Promise.resolve()),
}));
vi.mock('@/lib/api', () => ({
  api: {
    alertTuningNoisyRules: (since: string, limit: number) => h.noisy(since, limit),
    explainAlertNoise: () => Promise.resolve({ explanation: '', exchangeId: '' }),
  },
}));
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    useUpdateAlertRule: () => ({ mutateAsync: h.mutate }),
    useDisableAlertRule: () => ({ mutateAsync: h.mutate }),
  };
});

import { NoisyRulesPanel } from './NoisyRulesPanel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOISY: NoisyRule = {
  ruleId: 'r1', ruleName: 'checkout p99', severity: 'warning', openCount: 14, medianDurSec: 90,
  lastFiredNs: 1.7e18, totalDurSec: 1200, suggestion: 'raise for: 5m', suggestedForSec: 300,
  currentForSec: 0, currentMinSamples: 0, currentCooldownSec: 0,
};
const RULES: AlertRule[] = [{
  id: 'r1', name: 'checkout p99', service: 'checkout', metric: 'p99', comparator: '>', threshold: 500,
  windowSec: 300, severity: 'warning', enabled: true, builtIn: false, forSec: 0, minSamples: 0, cooldownSec: 0, createdAt: 0,
}];

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(rules: AlertRule[] = RULES): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<ConfirmProvider><NoisyRulesPanel rules={rules} onEditFromSuggestion={() => {}} /></ConfirmProvider>);
  });
  await flush();
  return host;
}
const flush = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.noisy.mockReset();
  h.mutate.mockClear();
});

const stateRow = (el: HTMLElement, kind: string) => el.querySelector<HTMLTableRowElement>(`tbody tr[data-dt-state="${kind}"]`);
const dataRows = (el: HTMLElement) => Array.from(el.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));
const button = (el: HTMLElement, text: RegExp) =>
  Array.from(el.querySelectorAll('button')).find(b => text.test(b.textContent ?? ''));

describe('NoisyRulesPanel — hata tablonun içinde (P-2), self-hide aynen', () => {
  it('yükleniyor: panel sessiz (null)', async () => {
    h.noisy.mockReturnValue(new Promise(() => {}));
    const el = await mount();
    expect(el.innerHTML).toBe('');
  });

  it('gürültülü kural yok: panel kendini gizler', async () => {
    h.noisy.mockResolvedValue({ rules: [{ ...NOISY, suggestion: '' }] });
    const el = await mount();
    expect(el.innerHTML).toBe('');
  });

  it('okuma hatası: başlık + tablo başlığı durur, tek hata satırı colSpan = 7 <th>, Retry load\'u yeniden çağırır', async () => {
    h.noisy.mockRejectedValue(new Error('HTTP 500'));
    const el = await mount();
    expect(el.textContent).toContain('Noisy rules (last 24h)');
    expect(el.querySelectorAll('thead th')).toHaveLength(7);
    const row = stateRow(el, 'error');
    expect(row, 'hata tablonun içinde değil').not.toBeNull();
    expect(el.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(row!.querySelector('td')!.colSpan).toBe(7);
    expect(row!.textContent).toContain('Gürültülü kural raporu okunamadı — ayar önerileri yok değil, alınamadı.');
    // Satırdan türeyen başlık parçaları hata hâlinde yok.
    expect(el.textContent).not.toMatch(/could be tightened/);
    expect(button(el, /Gürültüyü anlat/)).toBeUndefined();
    // Retry = aynı load: yükleniyor (panel sessiz) → yeni okuma.
    h.noisy.mockResolvedValue({ rules: [NOISY] });
    const retry = button(row!, /Retry/);
    expect(retry).toBeDefined();
    await act(async () => { retry!.click(); });
    await flush();
    expect(h.noisy).toHaveBeenCalledTimes(2);
    expect(stateRow(el, 'error')).toBeNull();
    expect(dataRows(el)).toHaveLength(1);
  });

  it('satırlar: durum satırı yok, sayı ve AI düğmesi başlıkta', async () => {
    h.noisy.mockResolvedValue({ rules: [NOISY] });
    const el = await mount();
    expect(el.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(dataRows(el)).toHaveLength(1);
    expect(el.textContent).toContain('1 rule could be tightened');
    expect(button(el, /Gürültüyü anlat/)).toBeDefined();
  });

  it('toplu Apply sonrası tazeleme düştü: eski öneriler sessizce kalmaz, hata satırı gelir', async () => {
    h.noisy.mockResolvedValueOnce({ rules: [NOISY] });
    const el = await mount();
    expect(dataRows(el)).toHaveLength(1);
    const box = el.querySelector<HTMLInputElement>('tbody input[type="checkbox"]')!;
    await act(async () => { box.click(); });
    h.noisy.mockRejectedValueOnce(new Error('HTTP 503'));
    const apply = button(el, /^Apply 1 suggestion$/);
    expect(apply).toBeDefined();
    await act(async () => { apply!.click(); });
    await flush();
    expect(h.mutate).toHaveBeenCalledTimes(1);
    expect(stateRow(el, 'error'), 'düşen tazeleme bayat öneri listesinin arkasında kayboldu').not.toBeNull();
    expect(dataRows(el)).toHaveLength(0);
  });
});

// v0.10.1069 — yerleşik kurallar varsayılan kapalı. 24 saatlik rapor (5 dk
// önbellek) kapatılan yerleşikleri hâlâ en gürültülü sayabilir; panel zaten
// kapalı kurala "Disable" önermemeli. Her şey kapalıyken panel kendini gizler
// (boş tablo / "0 rules" yok); açık kuralın satırı kalır.
describe('NoisyRulesPanel — kapalı kurallar (v0.10.1069)', () => {
  const BUILTIN_OFF: AlertRule = {
    id: 'builtin-warn-http-p99-3s', name: 'HTTP P99 latency >3s (sustained 10 min)', service: '', metric: 'http_p99_ms',
    comparator: '>', threshold: 3000, windowSec: 600, severity: 'warning', enabled: false, builtIn: true, createdAt: 0,
  };
  const NOISY_BUILTIN: NoisyRule = { ...NOISY, ruleId: BUILTIN_OFF.id, ruleName: BUILTIN_OFF.name, openCount: 340 };

  it('her şey kapalı: panel gizli, boş tablo yok', async () => {
    h.noisy.mockResolvedValue({ rules: [NOISY_BUILTIN] });
    const el = await mount([BUILTIN_OFF]);
    expect(el.innerHTML).toBe('');
  });

  it('karışık: kapalı yerleşik düşer, açık kural kalır', async () => {
    h.noisy.mockResolvedValue({ rules: [NOISY_BUILTIN, NOISY] });
    const el = await mount([BUILTIN_OFF, ...RULES]);
    expect(dataRows(el)).toHaveLength(1);
    expect(el.textContent).toContain('checkout p99');
    expect(el.textContent).not.toContain(BUILTIN_OFF.name);
    expect(el.textContent).toContain('1 rule could be tightened');
  });
});
