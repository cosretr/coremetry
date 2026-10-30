// @vitest-environment jsdom
//
// BriefingFeed.test — CoSRE v2 karşılaması (mockup B, operatör onayı
// 2026-10-31). Sunumsal brifing yuvasının üç hâli + veri yokken boş yuva.
// Fixture'lar nötr (payments-core, orders-api).
import { describe, it, expect, beforeAll, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { axeViolations } from '@/test/axe';
import { BriefingFeed, type BriefingFeedLine, type BriefingView } from './BriefingFeed';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

async function mount(view: BriefingView | null, onAskLine = vi.fn()) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(
      <MemoryRouter>
        <BriefingFeed view={view} onAskLine={onAskLine}
          teamPicker={<div data-testid="team-picker">picker</div>} />
      </MemoryRouter>);
  });
  return { el: host, onAskLine };
}

const RED: BriefingFeedLine = {
  id: 'l1', kind: 'RED', severity: 'critical',
  parts: [{ text: 'orders-api', tone: 'subject' }, { text: ' hata oranı %0,4 → ' }, { text: '%2,1', tone: 'num' }],
  ask: 'orders-api hata oranı neden arttı?',
  pivot: { label: 'Servis', href: '/service?name=orders-api' },
};
const LOG: BriefingFeedLine = {
  id: 'l2', kind: 'LOG', severity: 'warn',
  parts: [{ text: 'orders-api', tone: 'subject' }, { text: ' ERROR 38 → ' }, { text: '412', tone: 'num' }],
  ask: 'orders-api ERROR logları neden arttı?',
};

describe('BriefingFeed', () => {
  it('view null → hiçbir şey çizilmez (veri kaynağı yok)', async () => {
    const { el } = await mount(null);
    expect(el.innerHTML).toBe('');
  });

  it('findings: eyebrow sayıyla, satır başına nokta + nötr kalem + renk YALNIZ sayıda + pivot; tık composer\'a doldurur', async () => {
    const { el, onAskLine } = await mount({ state: 'findings', windowLabel: 'Son 3 sa', agoLabel: '4 dk önce', lines: [RED, LOG] });
    expect(el.querySelector('[data-briefing]')?.getAttribute('data-briefing')).toBe('findings');
    expect(el.textContent).toContain('Brifing · Son 3 sa · 2 bulgu');
    expect(el.textContent).toContain('4 dk önce');
    const lines = el.querySelectorAll('[data-briefing-line]');
    expect(lines).toHaveLength(2);
    expect(lines[0].querySelector('[data-dot]')?.getAttribute('data-dot')).toBe('critical');
    expect(lines[1].querySelector('[data-dot]')?.getAttribute('data-dot')).toBe('warn');
    // Kalem etiketi nötr; renk yalnız sapan sayıda.
    const kalem = [...lines[0].querySelectorAll('span')].find(s => s.textContent === 'RED')!;
    expect(kalem.className).toContain('text-muted-foreground');
    const colored = [...lines[0].querySelectorAll('[class*="text-status-"]')].map(n => n.textContent);
    expect(colored).toEqual(['%2,1']);
    expect([...lines[1].querySelectorAll('[class*="text-status-"]')].map(n => n.textContent)).toEqual(['412']);
    // Pivot gerçek bağlantı; pivotu olmayan satırda link yok.
    expect(lines[0].querySelector('a')?.getAttribute('href')).toBe('/service?name=orders-api');
    expect(lines[1].querySelector('a')).toBeNull();
    // Geri bildirim düğmesi yok (operatör 2026-10-31).
    expect(el.querySelector('[aria-label="Faydalı"], [aria-label="Gürültü"]')).toBeNull();
    await act(async () => { lines[0].querySelector<HTMLButtonElement>('button')!.click(); });
    expect(onAskLine).toHaveBeenCalledWith(RED);
    expect(await axeViolations(el)).toEqual([]);
  });

  it('silence: tek ok-noktalı satır, takım ve izlenen servis sayısı', async () => {
    const { el } = await mount({ state: 'silence', windowLabel: 'Son 3 sa', agoLabel: '4 dk önce', team: 'payments-core', serviceCount: 12 });
    expect(el.querySelector('[data-briefing]')?.getAttribute('data-briefing')).toBe('silence');
    expect(el.textContent).toContain('Brifing · Son 3 sa');
    expect(el.textContent).not.toContain('bulgu');
    expect(el.querySelectorAll('[data-dot]')).toHaveLength(1);
    expect(el.querySelector('[data-dot]')?.getAttribute('data-dot')).toBe('ok');
    expect(el.textContent).toContain('payments-core');
    expect(el.textContent).toContain('dikkat çeken bir şey yok');
    expect(el.textContent).toContain('12 servis izlendi');
    expect(el.querySelector('[data-briefing-line]')).toBeNull();
    expect(await axeViolations(el)).toEqual([]);
  });

  it('unknownTeam: "Ekibin tanımlı değil — hangi takımdasın?" + çağıranın takım seçicisi', async () => {
    const { el } = await mount({ state: 'unknownTeam', windowLabel: 'Son 3 sa' });
    expect(el.querySelector('[data-briefing]')?.getAttribute('data-briefing')).toBe('unknownTeam');
    expect(el.textContent).toContain('Ekibin tanımlı değil — hangi takımdasın?');
    expect(el.textContent).toContain('Brifing · Son 3 sa');
    expect(el.querySelector('[data-testid="team-picker"]')).not.toBeNull();
    expect(el.querySelector('[data-briefing-line]')).toBeNull();
  });
});
