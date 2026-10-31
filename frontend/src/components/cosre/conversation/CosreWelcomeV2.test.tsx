// @vitest-environment jsdom
//
// CosreWelcomeV2.test — CoSRE v2 karşılaması (mockup B, operatör onayı
// 2026-10-31): selam + açılış cümlesi, takım bilinmiyorsa sor, sekmeler ve
// v1 ile aynı gönder / doldur anlamları, composer takım çipi.
import { describe, it, expect, beforeAll, afterEach, vi } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import '@/test/radixJsdom';
import { CosreTeamScopeChip, CosreWelcomeV2 } from './CosreWelcomeV2';
import type { CosreTeamScope } from './useCosreTeamScope';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.stubGlobal('fetch', vi.fn(async () => { throw new Error('offline'); }));
});
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

async function mount(node: ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<MemoryRouter><QueryClientProvider client={qc}>{node}</QueryClientProvider></MemoryRouter>);
  });
  return host;
}
const scopeOf = (team: string, unknown = false): CosreTeamScope => ({ team, picked: '', unknown, choose: vi.fn(), clear: vi.fn() });
const tabsOf = (el: HTMLElement) => [...el.querySelectorAll<HTMLElement>('[data-welcome-tab]')].map(b => b.dataset.welcomeTab);
const chip = (el: HTMLElement, id: string) => el.querySelector<HTMLButtonElement>(`[data-welcome-suggestion="${id}"]`)!;

describe('CosreWelcomeV2', () => {
  it('takım biliniyor: Durum sekmesi açık, çip takım sorusunu HEMEN gönderir; hata hapı ve satır içi seçici yok', async () => {
    const onAsk = vi.fn(), onPrefill = vi.fn();
    const el = await mount(<CosreWelcomeV2 firstName="Ada" scope={scopeOf('payments-core')} wiki ctxStarter={null}
      briefing={null} onPrefill={onPrefill} onAsk={onAsk} onAskExplain={vi.fn()} />);
    expect(el.querySelector('.cosre-welcome__hello')?.textContent).toBe('Merhaba Ada.');
    expect(el.textContent).toContain('Sana nasıl yardımcı olabilirim?');
    expect(tabsOf(el)).toEqual(['status', 'investigate', 'find', 'learn']);
    expect(el.querySelector('[data-briefing]')).toBeNull();
    expect(el.querySelector('input')).toBeNull();
    await act(async () => { chip(el, 'team-services').click(); });
    expect(onAsk).toHaveBeenCalledWith('payments-core takımının servisleri nasıl?');
    expect(onPrefill).not.toHaveBeenCalled();
  });

  it('Bul sekmesinin şablonu composer\'ı DOLDURUR (göndermez), imleç | yerinde', async () => {
    const onAsk = vi.fn(), onPrefill = vi.fn();
    const el = await mount(<CosreWelcomeV2 scope={scopeOf('')} wiki={false} ctxStarter={null}
      briefing={null} onPrefill={onPrefill} onAsk={onAsk} onAskExplain={vi.fn()} />);
    expect(tabsOf(el)).toEqual(['investigate', 'find']);
    // Radix Tabs: sekmeler pointerdown/keydown ile değişir; içerik DOM'da yalnız etkin sekmede.
    const trigger = el.querySelector<HTMLElement>('[data-welcome-tab="find"]')!;
    await act(async () => { trigger.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, button: 0 })); });
    await act(async () => { trigger.focus(); trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    await act(async () => { chip(el, 'fill-path').click(); });
    expect(onPrefill).toHaveBeenCalledWith(" hatalı trace'lerini getir", 0);
    expect(onAsk).not.toHaveBeenCalled();
  });

  it('takım bilinmiyor: "hangi takımdasın?" + sunucu-aramalı takım Command\'ı; takım soruları gizli', async () => {
    const el = await mount(<CosreWelcomeV2 scope={scopeOf('', true)} wiki={false} ctxStarter={null}
      briefing={null} onPrefill={vi.fn()} onAsk={vi.fn()} onAskExplain={vi.fn()} />);
    expect(el.querySelector('[data-briefing="unknownTeam"]')).not.toBeNull();
    expect(el.textContent).toContain('Ekibin tanımlı değil — hangi takımdasın?');
    expect(el.querySelector('[data-search-command="team"] input')?.getAttribute('placeholder')).toBe('Takım ara…');
    expect(tabsOf(el)).not.toContain('status');
  });

  it('composer takım çipi: takım varken ✕ kapsamı kaldırır; yokken "Takım seç"', async () => {
    const s = scopeOf('payments-core');
    let el = await mount(<CosreTeamScopeChip scope={s} />);
    expect(el.querySelector('[data-team-scope]')?.textContent).toContain('payments-core');
    await act(async () => { el.querySelector<HTMLButtonElement>('[aria-label="Takım kapsamını kaldır"]')!.click(); });
    expect(s.clear).toHaveBeenCalledTimes(1);
    await act(async () => { root!.unmount(); });
    host!.remove();
    el = await mount(<CosreTeamScopeChip scope={scopeOf('')} />);
    expect(el.querySelector('[data-team-scope]')?.textContent).toBe('Takım seç');
  });
});
