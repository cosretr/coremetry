// @vitest-environment jsdom
//
// SummaryLinkCard.test — v0.10.2101 (docs/cosre/experience-audit.md D4).
// Sözleşme: data-links title + summary taşıyorsa balonun altında kart (tek
// cümle + ≤3 iç link) ve o linkler balonda çip olarak ÇİZİLMEZ; title yoksa
// bugünkü "Aç →" çipleri aynen. Biçimsiz kart yükü çiplere düşer.
import { describe, it, expect, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { parseDataLinksCard } from '@/lib/cosre/protocol';
import type { ChatTurn } from '@/lib/types';
import { MessageList, type CosreChatMessage } from './MessageList';
import { bubbleTurnForCard, linkCardOf } from './linkCard';

const LINKS = [
  { href: '/services/orders-api', label: 'Servis' },
  { href: '/problems?service=orders-api', label: 'Problemler' },
];
const CARD = { title: 'orders-api', summary: 'Sağlık durumu Dikkat; hata oranı %1,2, p99 340 ms, 2 açık problem.', links: LINKS };

describe('parseDataLinksCard', () => {
  it.each([
    [{ type: 'data-links', data: { ...CARD } }, CARD],
    [{ type: 'data-links', data: { links: LINKS } }, null],
    [{ type: 'data-links', data: { ...CARD, title: '' } }, null],
    [{ type: 'data-links', data: { ...CARD, summary: 7 } }, null],
    [{ type: 'data-links', data: { ...CARD, links: [] } }, null],
    [{ type: 'data-links', data: { ...CARD, links: [...LINKS, { href: '/a', label: 'a' }, { href: '/b', label: 'b' }] } }, null],
    [{ type: 'data-links', data: { ...CARD, links: [{ href: 'https://evil.example', label: 'x' }] } }, null],
    [{ type: 'data-links', data: { ...CARD, links: [{ href: '//evil.example', label: 'x' }] } }, null],
    [{ type: 'data-links', data: { ...CARD, links: [{ href: '/x', label: '' }] } }, null],
    [{ type: 'data-pick', data: { ...CARD } }, null],
  ])('%j → %j', (p, want) => {
    expect(parseDataLinksCard(p)).toEqual(want);
  });
});

describe('linkCardOf / bubbleTurnForCard', () => {
  it('son data-links belirler', () => {
    expect(linkCardOf([{ type: 'data-links', data: { ...CARD } }])).toEqual(CARD);
    expect(linkCardOf([{ type: 'text' }, { type: 'data-links', data: { links: LINKS } }])).toBeNull();
  });
  it('kart linkleri çipten çıkar, satır içi izin listesine girer; diğer linkler kalır', () => {
    const turn: ChatTurn = { role: 'assistant', text: 'x', links: [...LINKS, { href: '/traces', label: 'Trace' }], allowedLinks: ['/a'] };
    const got = bubbleTurnForCard(turn, CARD);
    expect(got.links).toEqual([{ href: '/traces', label: 'Trace' }]);
    expect(got.allowedLinks).toEqual(['/a', ...LINKS.map(l => l.href)]);
    expect(bubbleTurnForCard({ role: 'assistant', text: 'x', links: LINKS }, CARD).links).toBeUndefined();
    expect(bubbleTurnForCard(turn, null)).toBe(turn);
  });
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

function msg(data: Record<string, unknown>): CosreChatMessage {
  return { id: 'a1', role: 'assistant', parts: [
    { type: 'text', text: 'Cevap metni.', state: 'done' },
    { type: 'data-links', id: 'links', data },
  ] } as CosreChatMessage;
}

async function draw(m: CosreChatMessage) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<MemoryRouter><MessageList messages={[m]} visibleParts={x => x.parts} status="ready" /></MemoryRouter>);
  });
  return host;
}

describe('MessageList: kart ↔ çip', () => {
  it('title varsa kart çizilir, aynı linkler çip satırında yok', async () => {
    const el = await draw(msg({ ...CARD }));
    const card = el.querySelector('[data-cosre-linkcard]');
    expect(card?.textContent).toContain('orders-api');
    expect(card?.textContent).toContain(CARD.summary);
    expect([...card!.querySelectorAll('a')].map(a => a.getAttribute('href'))).toEqual(LINKS.map(l => l.href));
    expect(el.querySelector('.ai-links')).toBeNull();
  });
  it('title yoksa bugünkü çipler, kart yok', async () => {
    const el = await draw(msg({ links: LINKS }));
    expect(el.querySelector('[data-cosre-linkcard]')).toBeNull();
    expect([...el.querySelectorAll('.ai-links a')].map(a => a.getAttribute('href'))).toEqual(LINKS.map(l => l.href));
  });
});
