// @vitest-environment jsdom
//
// MessageList.test — v0.10.1650 (CoSRE v2 Faz 2b B2, plan §5.1). Paylaşılan
// testdata/cosre fixture'ları GERÇEK AI SDK (createCosreChat + sahte fetch)
// üstünden sürülür, son durum MessageList ile çizilir:
//   • ui-stream-v1 / reset / reset2: DOM'da yalnız son metin, taslak YOK
//   • akış ortası: taslak message.parts'ta durur, DOM'da yok (visibleParts)
//   • error: akan metin kalır, altında ⚠ satırı (role=alert)
//   • stop: yarım metin + v1'in "yarım kaldı" notu
//   • bilinmeyen / B3–B5 parçaları çizilmez, çökme yok; 👍/👎 yok
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { cosreFixture, sseFetch } from '@/test/cosreSse';
import { createCosreChat, type CosreChatCore } from '@/lib/cosre/useCosreChat';
import { MessageList, type CosreChatStatus } from './MessageList';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

async function draw(core: CosreChatCore, status?: CosreChatStatus) {
  if (!host) {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  }
  const st = status ?? (core.chat.status as CosreChatStatus);
  await act(async () => {
    root!.render(<MemoryRouter><MessageList messages={core.chat.messages} visibleParts={core.visibleParts} status={st} error={core.chat.error} /></MemoryRouter>);
  });
  return host!;
}

describe('MessageList × paylaşılan fixture (gerçek SDK)', () => {
  it('ui-stream-v1: yalnız t3, t1/t2 taslakları DOM\'da yok; kullanıcı sorusu çizilir', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1')).fetch });
    await core.chat.sendMessage({ text: 'orders-api neden yavaş?' });
    const el = await draw(core);
    expect(el.querySelector('.cm-msg--user')?.textContent).toContain('orders-api neden yavaş?');
    const ai = el.querySelectorAll('.cm-msg--ai .cm-msg-ai');
    expect(ai).toHaveLength(1);
    expect(ai[0].textContent).toContain("orders-api p95 1830 ms'ye çıktı");
    expect(el.textContent).not.toContain('yükseldi');
    expect(el.querySelector('.cm-msg-ai b')?.textContent).toBe('Bulgu'); // renderMessage motoru (markdown)
    expect(el.textContent).not.toMatch(/👍|👎/);
    expect(el.querySelector('[role="alert"]')).toBeNull();
  });

  it.each([
    ['ui-stream-v1-reset', 'Düzeltilmiş tam metin.'],
    ['ui-stream-v1-reset2', 'Son tam metin.'],
  ])('%s: yalnız düzeltilmiş metin', async (name, want) => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture(name)).fetch });
    await core.chat.sendMessage({ text: 'q' });
    const el = await draw(core);
    const ai = [...el.querySelectorAll('.cm-msg--ai .cm-msg-ai')].map(n => n.textContent?.trim());
    expect(ai).toEqual([want]);
    expect(el.textContent).not.toContain('taslak');
  });

  it('akış ortası (reset2, t1 reset sonrası): taslak parts\'ta, DOM\'da yok; imleç son metinde', async () => {
    const f = sseFetch(cosreFixture('ui-stream-v1-reset2'), fr => fr.includes('"delta":"İkinci ","id":"t2"'));
    const core = createCosreChat({ fetch: f.fetch });
    const p = core.chat.sendMessage({ text: 'q' });
    await vi.waitFor(() => expect(core.chat.messages.at(-1)?.parts.filter(x => x.type === 'text')).toHaveLength(2));
    const el = await draw(core, 'streaming');
    expect(JSON.stringify(core.chat.messages.at(-1)!.parts)).toContain('Birinci taslak');
    expect(el.textContent).not.toContain('Birinci taslak');
    expect(el.textContent).toContain('İkinci');
    f.release();
    await p;
    const done = await draw(core);
    expect([...done.querySelectorAll('.cm-msg-ai')].map(n => n.textContent?.trim())).toEqual(['Son tam metin.']);
  });

  it('error: akan metin kalır, altında ⚠ satırı; durdurma notu yok', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-error')).fetch });
    await core.chat.sendMessage({ text: 'q' });
    const el = await draw(core);
    expect(core.chat.status).toBe('error');
    const alert = el.querySelector('[role="alert"]');
    expect(alert?.textContent).toMatch(/^⚠ /);
    expect(alert?.getAttribute('title') ?? alert?.textContent).toContain('3 dakika');
    expect(el.querySelector('[data-cosre-partial]')).toBeNull();
  });

  it('stop: yarım metin + "yarım kaldı" notu (v1 metni)', async () => {
    const f = sseFetch(cosreFixture('ui-stream-v1-reset'), fr => fr.includes('"delta":"Düzeltilmiş tam metin."'));
    const core = createCosreChat({ fetch: f.fetch });
    const p = core.chat.sendMessage({ text: 'q' });
    await vi.waitFor(() => expect(core.chat.messages.at(-1)?.parts.filter(x => x.type === 'text')).toHaveLength(2));
    await core.chat.stop();
    await p;
    const el = await draw(core);
    expect([...el.querySelectorAll('.cm-msg-ai')].map(n => n.textContent?.trim())).toEqual(['Düzeltilmiş tam metin.']);
    expect(el.querySelector('[data-cosre-partial]')?.textContent).toBe('⏹ Durduruldu — bu cevap yarım kaldı.');
  });

  it('gönderildi, ilk parça yok → bekleyiş göstergesi; bilinmeyen parça çökertmez', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-reset')).fetch });
    core.chat.messages = [{ id: 'u', role: 'user', parts: [{ type: 'text', text: 'q' }] }];
    const el = await draw(core, 'submitted');
    expect(el.querySelector('[data-cosre-thinking]')).not.toBeNull();
    await act(async () => {
      root!.render(<MemoryRouter><MessageList status="ready" visibleParts={m => m.parts} messages={[
        { id: 'a', role: 'assistant', parts: [{ type: 'data-unknown', data: {} }, { type: 'dynamic-tool', toolName: 'x', toolCallId: 's1', state: 'input-available', input: {} }, { type: 'text', text: 'tamam' }] },
      ]} /></MemoryRouter>);
    });
    expect(host!.querySelector('.cm-msg-ai')?.textContent?.trim()).toBe('tamam');
  });
});
