// @vitest-environment jsdom
//
// v0.10.1100 (operatör onaylı: Oracle hata grubu için AI açıklaması span yerine
// Oracle bağlamı) — AI panelinde Oracle hata grubu (`ora:` parmak izi):
//   • "Kodu da incele" çipi YOK (stack yok, kod okunacak satır yok) ve ilk
//     cevaptan sonra "Kodu da inceleyeyim mi?" sorusu da çıkmaz;
//   • yerine tek satır "Oracle · <kaynak> · <kod> · <operasyon>" (detay
//     panelinin aynı sorgusundan); bilgi yokken "Oracle hata grubu";
//   • istek kodsuz gider (includeCode=false), aynı uç (explain-exception).
// Span grubu değişmedi: çip yerinde, Oracle satırı yok. Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { CopilotExplain } from './CopilotExplain';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { api } from '@/lib/api';
import type { OracleGroupInfo } from '@/lib/types';

const ORA = 'ora:42c30d3ac1ddbf2c';

const INFO: OracleGroupInfo = {
  sourceId: 'o-11111111', sourceName: 'core-errlog', code: 'APP_ERR_042', operation: 'OP_TRANSFER',
  channels: [{ name: 'MOB', count: 60 }], services: [{ name: 'svc-payments', count: 9 }], serviceCount: 1, known: true,
};

let host: HTMLDivElement;
let root: Root;

async function mount(node: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>);
  });
  await act(async () => { await Promise.resolve(); });
}
const ctxLine = () => host.querySelector('[data-testid="oracle-explain-context"]');
const chip = () => Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Kodu da incele'));

beforeEach(() => {
  window.history.replaceState({}, '', '/');
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  __resetCopilotEnabledCache();
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

describe('AI paneli: Oracle hata grubu (v0.10.1100)', () => {
  it('kod çipi yerine Oracle bağlam satırı; istek kodsuz; cevap sonrası kod sorusu yok', async () => {
    const oracle = vi.spyOn(api, 'exceptionGroupOracle').mockResolvedValue(INFO);
    const explain = vi.spyOn(api, 'copilotExplainException').mockResolvedValue({ explanation: 'APP_ERR_042 MOB kanalında yoğun', exchangeId: 'x1' });
    await mount(<CopilotExplain kind="exception" id={ORA} auto />);
    await act(async () => { await Promise.resolve(); });

    expect(oracle).toHaveBeenCalledWith(ORA);
    expect(ctxLine()?.textContent).toBe('Oracle · core-errlog · APP_ERR_042 · OP_TRANSFER');
    expect(chip()).toBeUndefined();
    expect(explain).toHaveBeenCalledTimes(1);
    expect(explain.mock.calls[0][0]).toBe(ORA);
    expect(explain.mock.calls[0][1]).toBe(false);
    expect(host.textContent).toContain('APP_ERR_042 MOB kanalında yoğun');
    expect(host.textContent).not.toContain('Kodu da inceleyeyim');
  });

  it('bilgi okunamazsa jenerik satır (panel yine çizilir)', async () => {
    vi.spyOn(api, 'exceptionGroupOracle').mockRejectedValue(new Error('404'));
    vi.spyOn(api, 'copilotExplainException').mockResolvedValue({ explanation: 'ok', exchangeId: 'x1' });
    await mount(<CopilotExplain kind="exception" id={ORA} auto />);
    expect(ctxLine()?.textContent).toBe('Oracle hata grubu');
    expect(chip()).toBeUndefined();
  });

  it('span grubu değişmedi: çip var, Oracle satırı yok, Oracle sorgusu yok', async () => {
    const oracle = vi.spyOn(api, 'exceptionGroupOracle').mockResolvedValue(INFO);
    vi.spyOn(api, 'copilotExplainException').mockResolvedValue({ explanation: 'null referans', exchangeId: 'x1' });
    await mount(<CopilotExplain kind="exception" id="fp-checkout-1" auto />);
    expect(ctxLine()).toBeNull();
    expect(chip()).toBeDefined();
    expect(oracle).not.toHaveBeenCalled();
  });
});
