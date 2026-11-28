// @vitest-environment jsdom
//
// inlineFlow.test — v0.10.2491 (CoSRE sohbet dilim 1). Kayıtlı olay dizisi
// (testdata/cosre/ui-stream-v1-narration, Go çeviricisinin altını) GERÇEK AI
// SDK üstünden sürülür ve MessageList ile çizilir:
//   • sıra: bağlam cümlesi → anlatım → "2 araç çalıştı ›" → anlatım →
//     "1 araç çalıştı ›" → özet satırı → cevap;
//   • grup açılınca araç başına ad + amaç satırı (toolPurpose);
//   • arşiv gidiş-dönüşü aynı sırayı geri getirir;
//   • anlatımsız akış (block kipi) bugünkü StepList'i çizer, grup yok.
import { describe, it, expect, vi, afterEach, beforeAll, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { cosreFixture, sseFetch } from '@/test/cosreSse';
import { createCosreChat } from '@/lib/cosre/useCosreChat';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import { fromPersistMessages, toPersistMessages } from '@/lib/cosre/cosrePersist';
import { toolPurpose } from '@/lib/cosre/toolPurpose';
import { MessageList, type CosreChatStatus } from './MessageList';
import { setUserLang } from '@/lib/i18n';

beforeEach(() => {
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => { mem.set(k, String(v)); }, removeItem: (k: string) => { mem.delete(k); } });
  setUserLang('tr');
});
afterEach(() => { setUserLang(null); vi.unstubAllGlobals(); });

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

const visible = (m: CosreUIMessage) => m.parts;

async function draw(messages: readonly CosreUIMessage[], status: CosreChatStatus = 'ready') {
  if (!host) {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  }
  await act(async () => {
    root!.render(<MemoryRouter><MessageList messages={messages} visibleParts={visible} status={status} /></MemoryRouter>);
  });
  return host!;
}

/** Asistan mesajının görünür satırları, DOM sırasıyla (bağlam, anlatım, grup başlığı, özet, cevap). */
function order(el: Element): string[] {
  const sel = '[data-cosre-flow-context], [data-cosre-narration], [data-cosre-flow-toggle], [data-cosre-flow-summary], .cm-msg-ai';
  return [...el.querySelectorAll(sel)].map(n => {
    if (n.hasAttribute('data-cosre-flow-context')) return `ctx:${n.textContent}`;
    if (n.hasAttribute('data-cosre-narration')) return `say:${n.textContent}`;
    if (n.hasAttribute('data-cosre-flow-toggle')) return `group:${n.textContent?.trim()}`;
    if (n.hasAttribute('data-cosre-flow-summary')) return 'summary';
    return 'answer';
  });
}

async function narrationChat() {
  const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-narration')).fetch });
  await core.chat.sendMessage({ text: 'checkout neden hata veriyor?' });
  return core.chat.messages;
}

const WANT = [
  'ctx:checkout servis sayfasındasınız, son 1 saate bakıyorsunuz.',
  'say:Servisin durumuna bakıyorum…',
  'group:2 araç çalıştı',
  'say:Loglara bakıyorum…',
  'group:1 araç çalıştı',
  'summary',
  'answer',
];

describe('inline akış (data-narration)', () => {
  it('sıra: bağlam → anlatım → grup → … → özet → cevap; grup açılınca amaç satırları', async () => {
    const el = await draw(await narrationChat());
    expect(order(el)).toEqual(WANT);
    expect(el.querySelector('[data-cosre-steps]')).toBeNull(); // eski blok yok
    // Özet satırı "Nasıl cevapladım" bilgisini taşır: çağrı sayısı + durma satırı.
    const sum = el.querySelector('[data-cosre-flow-summary]')!;
    expect(sum.getAttribute('data-cosre-stop')).toBe('stop');
    expect(sum.textContent).toContain('3 çağrı');
    // Katlı: amaç satırı kapalıyken yok, açılınca araç başına bir.
    expect(el.querySelectorAll('[data-cosre-step-purpose]')).toHaveLength(0);
    const toggles = [...el.querySelectorAll<HTMLButtonElement>('[data-cosre-flow-toggle]')];
    await act(async () => { toggles.forEach(b => b.click()); });
    const purposes = [...el.querySelectorAll('[data-cosre-step-purpose]')].map(n => n.textContent);
    expect(purposes).toEqual(['checkout sağlığı okunuyor', "Açık Problem'lere bakılıyor", 'checkout logları aranıyor']);
  });

  it('akarken çalışan grup "çalışıyor…" der', async () => {
    const msgs = await narrationChat();
    const last = msgs[msgs.length - 1];
    // Son aracın sonucu henüz gelmemiş hâl: s3'ün çıktı parçaları atılır.
    const parts = last.parts.flatMap(p => {
      if (p.type === 'dynamic-tool' && (p as { toolCallId: string }).toolCallId === 's3') return [{ ...p, state: 'input-available', output: undefined } as typeof p];
      if (p.type === 'data-tool-result' && (p as { id?: string }).id === 's3') return [];
      if (p.type === 'text' || p.type === 'data-stop') return [];
      return [p];
    });
    const el = await draw([...msgs.slice(0, -1), { ...last, parts }], 'streaming');
    const running = el.querySelectorAll('[data-cosre-flow-running]');
    expect(running).toHaveLength(1);
    expect(running[0].closest('[data-cosre-flow-group]')?.getAttribute('data-cosre-flow-group')).toBe('n2');
  });

  it('arşiv gidiş-dönüşü aynı sırayı geri getirir', async () => {
    const saved = toPersistMessages(await narrationChat());
    const a = saved.find(m => m.role === 'assistant')!;
    expect(a.flow).toEqual([
      { text: 'Servisin durumuna bakıyorum…', context: 'checkout servis sayfasındasınız, son 1 saate bakıyorsunuz.', steps: [
        { id: 's1', tool: 'get_service_health', purpose: 'checkout sağlığı okunuyor' },
        { id: 's2', tool: 'list_problems', purpose: "Açık Problem'lere bakılıyor" },
      ] },
      { text: 'Loglara bakıyorum…', steps: [{ id: 's3', tool: 'search_logs', purpose: 'checkout logları aranıyor' }] },
    ]);
    const restored = fromPersistMessages(JSON.parse(JSON.stringify(saved)), 'c1');
    const el = await draw(restored);
    expect(order(el)).toEqual(WANT);
    const toggles = [...el.querySelectorAll<HTMLButtonElement>('[data-cosre-flow-toggle]')];
    await act(async () => { toggles.forEach(b => b.click()); });
    expect([...el.querySelectorAll('[data-cosre-step-purpose]')].map(n => n.textContent)).toEqual(['checkout sağlığı okunuyor', "Açık Problem'lere bakılıyor", 'checkout logları aranıyor']);
  });

  it('anlatımsız akış (block kipi): bugünkü StepList, grup yok, arşivde flow yok', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-status')).fetch });
    await core.chat.sendMessage({ text: 'q' });
    const el = await draw(core.chat.messages);
    expect(el.querySelector('[data-cosre-steps]')).not.toBeNull();
    expect(el.querySelector('[data-cosre-flow-group], [data-cosre-flow-context], [data-cosre-flow-summary]')).toBeNull();
    expect(toPersistMessages(core.chat.messages).some(m => 'flow' in m)).toBe(false);
  });
});

describe('toolPurpose', () => {
  it.each([
    ['list_problems', {}, "Açık Problem'lere bakılıyor"],
    ['get_service_health', { service: 'orders' }, 'orders sağlığı okunuyor'],
    ['find_traces', { services: ['a', 'b'] }, "2 servis trace'leri aranıyor"],
    ['explain_trace', { trace_id: '0123456789abcdef' }, "01234567… trace'i açılıyor"],
    ['ext_tool', {}, 'ext tool çalıştırılıyor'],
    ['ext_tool', { service: 'orders' }, 'orders için ext tool'],
  ])('%s %j → %s', (tool, args, want) => {
    expect(toolPurpose(tool, args)).toBe(want);
  });
  it('EN katalog', () => {
    expect(toolPurpose('get_service_health', { service: 'orders' }, 'en')).toBe('Reading orders health');
    expect(toolPurpose('list_problems', undefined, 'en')).toBe('Checking open Problems');
  });
});
