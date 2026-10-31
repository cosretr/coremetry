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

/** Cevap gövdesi: v1 balonunun adım açılırı ve notları hariç metin. */
function bodyText(n: Element): string {
  const c = n.cloneNode(true) as Element;
  c.querySelectorAll('.cm-how, .cm-phase-trail, .cm-msg-note').forEach(x => x.remove());
  return c.textContent?.trim() ?? '';
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

  it('error: akan metin kalır, altında v1 ⚠ satırı; durdurma notu yok', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-error')).fetch });
    await core.chat.sendMessage({ text: 'q' });
    const el = await draw(core);
    expect(core.chat.status).toBe('error');
    // v0.10.1656 (B3) — hata v1 ChatBubble'ın satırı (balon içinde ⚠ + title = ham).
    const line = [...el.querySelectorAll('.cm-msg-ai span')].find(n => n.textContent?.startsWith('⚠ '));
    expect(line).toBeTruthy();
    expect(line!.getAttribute('title') ?? line!.textContent).toContain('3 dakika');
    expect(el.textContent).not.toContain('Durduruldu');
  });

  it('stop: yarım metin + "yarım kaldı" notu (v1 metni)', async () => {
    const f = sseFetch(cosreFixture('ui-stream-v1-reset'), fr => fr.includes('"delta":"Düzeltilmiş tam metin."'));
    const core = createCosreChat({ fetch: f.fetch });
    const p = core.chat.sendMessage({ text: 'q' });
    await vi.waitFor(() => expect(core.chat.messages.at(-1)?.parts.filter(x => x.type === 'text')).toHaveLength(2));
    await core.chat.stop();
    await p;
    const el = await draw(core);
    expect([...el.querySelectorAll('.cm-msg-ai')].map(bodyText)).toEqual(['Düzeltilmiş tam metin.']);
    expect(el.querySelector('.cm-msg-ai .cm-msg-note')?.textContent?.trim()).toBe('⏹ Durduruldu — bu cevap yarım kaldı.');
  });

  // v0.10.1710 — v2'de adımlar YALNIZ kartlarda: balonda "Nasıl cevapladım" yok.
  it('ui-stream-v1 (1710): balonda adım listesi yok (çip / şeffaflık paneli / bütçe özeti); kartlar + data-stop satırı var', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1')).fetch });
    await core.chat.sendMessage({ text: 'q' });
    const el = await draw(core);
    const bubble = el.querySelector('.cm-msg-ai')!;
    expect(bubble.querySelector('.cm-how, .cm-steps, .cm-steps-sum')).toBeNull();
    expect(el.textContent).not.toContain('Nasıl cevapladım');
    expect(el.querySelector('[data-cosre-budget]')).toBeNull();
    const list = el.querySelector('[data-cosre-steps]')!;
    expect(list.getAttribute('data-open')).toBeNull(); // bitti → kapalı özet
    await act(async () => { list.querySelector<HTMLButtonElement>('button')!.click(); });
    expect([...el.querySelectorAll('[data-cosre-step]')].map(n => n.getAttribute('data-cosre-step'))).toEqual(['s2', 's3']);
    expect(el.querySelector('[data-cosre-stop]')?.textContent).toContain('çağrı tavanı · 2 çağrı · 1 tur');
  });

  it('akış ortası (1710): adım kartları açık, araç kartı çalışıyor; balonda adım açılırı yok', async () => {
    const f = sseFetch(cosreFixture('ui-stream-v1'), fr => fr.includes('"toolCallId":"s2","toolName":"get_service_health","type":"tool-input-available"'));
    const core = createCosreChat({ fetch: f.fetch });
    const p = core.chat.sendMessage({ text: 'q' });
    await vi.waitFor(() => expect(core.chat.messages.at(-1)?.parts.some(x => x.type === 'dynamic-tool')).toBe(true));
    const el = await draw(core, 'streaming');
    expect(el.querySelector('.cm-how')).toBeNull();
    expect(el.querySelector('[data-cosre-steps]')?.getAttribute('data-open')).toBe('true');
    expect(el.querySelector('[data-cosre-step="s2"]')?.getAttribute('data-kind')).toBe('running');
    f.release();
    await p;
  });

  // v0.10.1729 (görünüm turu A) — AYNI cevap: akarken adımlar açık, bitince tek
  // çerçeveli özet satırına kapanır (kullanıcı dokunmadıysa).
  it('görünüm A (1729): adımlar akarken açık → cevap bitince kapalı', async () => {
    const f = sseFetch(cosreFixture('ui-stream-v1'), fr => fr.includes('"toolCallId":"s2","toolName":"get_service_health","type":"tool-input-available"'));
    const core = createCosreChat({ fetch: f.fetch });
    const p = core.chat.sendMessage({ text: 'q' });
    await vi.waitFor(() => expect(core.chat.messages.at(-1)?.parts.some(x => x.type === 'dynamic-tool')).toBe(true));
    let el = await draw(core, 'streaming');
    expect(el.querySelector('[data-cosre-steps]')?.getAttribute('data-open')).toBe('true');
    f.release();
    await p;
    el = await draw(core, 'ready');
    const list = el.querySelector('[data-cosre-steps]')!;
    expect(list.getAttribute('data-open')).toBeNull();
    expect(list.className).toContain('border-border');
  });

  // v0.10.1659 (B4) → v0.10.1710: İlgili / İlgisiz kartın altında; sinyal yükü aynı.
  it('ui-stream-v1 (B4, kartta): İlgili/İlgisiz s2 sinyali (iki durumlu), Not 512 bayta kırpılır; 👍/👎 yok', async () => {
    const calls: unknown[] = [];
    const spy = vi.spyOn(globalThis, 'fetch').mockImplementation(async (_u, init) => {
      calls.push(JSON.parse(String(init?.body)));
      return new Response('{}');
    });
    try {
      const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1')).fetch });
      await core.chat.sendMessage({ text: 'q' });
      const el = await draw(core);
      const btn = (t: string) => [...el.querySelectorAll('button')].find(b => b.textContent?.includes(t))!;
      await act(async () => { el.querySelector<HTMLButtonElement>('[data-cosre-steps] button')!.click(); });
      await act(async () => { el.querySelector<HTMLButtonElement>('[data-cosre-step="s2"] button')!.click(); });
      const card = el.querySelector('[data-cosre-step="s2"]')!;
      expect(card.querySelector('[data-cosre-inputs]')?.textContent).toBe('Girdiler: service=orders-api');
      const rel = card.querySelector('[data-cosre-relevance="s2"]')!;
      expect(rel).not.toBeNull();
      const [yes, no] = [...rel.querySelectorAll('button')];
      await act(async () => { yes.click(); });
      await act(async () => { yes.click(); }); // aynı durum: tekrar gönderilmez
      expect(yes.getAttribute('aria-pressed')).toBe('true');
      await act(async () => { no.click(); });
      expect(no.getAttribute('aria-pressed')).toBe('true');
      expect(calls).toEqual([
        { exchangeId: 'golden-xid', kind: 'evidence_relevant', detail: 's2' },
        { exchangeId: 'golden-xid', kind: 'evidence_irrelevant', detail: 's2' },
      ]);
      await act(async () => { btn('✎ Not').click(); });
      const ta = el.querySelector<HTMLTextAreaElement>('[data-cosre-note] textarea')!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(ta, 'ş'.repeat(300));
        ta.dispatchEvent(new Event('input', { bubbles: true }));
      });
      await act(async () => { btn('Kaydet').click(); });
      const note = calls[2] as { kind: string; detail: string };
      expect(note.kind).toBe('note');
      expect(new TextEncoder().encode(note.detail).length).toBe(512);
      expect(el.querySelector('[data-cosre-note-sent]')).not.toBeNull();
      expect(el.textContent).not.toMatch(/👍|👎/);
    } finally {
      spy.mockRestore();
    }
  });

  it('B4 (kartta): akış sürerken (exchangeId yok) İlgili/İlgisiz devre dışı; Ham çıktı kapalı', async () => {
    await act(async () => {
      if (!host) { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); }
      root!.render(<MemoryRouter><MessageList status="streaming" visibleParts={m => m.parts} messages={[
        { id: 'a', role: 'assistant', parts: [
          { type: 'dynamic-tool', toolName: 'get_logs', toolCallId: 's3', state: 'output-available', input: { argsText: 'a' }, output: { ok: true, preview: '{"n":1}' } },
          { type: 'text', text: 'akıyor' },
        ] },
      ]} /></MemoryRouter>);
    });
    await act(async () => { host!.querySelector<HTMLButtonElement>('[data-cosre-step="s3"] button')!.click(); });
    const btns = [...host!.querySelectorAll('[data-cosre-relevance="s3"] button')];
    expect(btns).toHaveLength(2);
    expect(btns.every(b => (b as HTMLButtonElement).disabled)).toBe(true);
    const raw = host!.querySelector('[data-cosre-step="s3"] [data-cosre-raw]')!;
    expect(raw.querySelector('pre')).toBeNull();
    await act(async () => { raw.querySelector<HTMLButtonElement>('button')!.click(); });
    expect(raw.querySelector('pre')?.textContent).toBe('{"n":1}');
  });

  it('1710: bütçe aşımı (süre tavanı) rozeti data-stop satırında, balonda değil', async () => {
    await act(async () => {
      if (!host) { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); }
      root!.render(<MemoryRouter><MessageList status="error" error={new Error('yanıt süre tavanına dayandı (180 sn)')} visibleParts={m => m.parts} messages={[
        { id: 'a', role: 'assistant', parts: [
          { type: 'dynamic-tool', toolName: 'find_traces', toolCallId: 's2', state: 'output-available', input: { args: { service: 'orders-api' } }, output: { ok: true, preview: '{}' } },
          { type: 'data-stop', id: 'stop', data: { reason: 'deadline', calls: 1, rounds: 1, elapsed_ms: 180000 } },
          { type: 'text', text: 'yarım' },
        ] },
      ]} /></MemoryRouter>);
    });
    await act(async () => { host!.querySelector<HTMLButtonElement>('[data-cosre-steps] button')!.click(); });
    const stop = host!.querySelector('[data-cosre-stop]')!;
    expect([...stop.querySelectorAll('[data-cosre-stop-badge]')].map(b => b.textContent)).toEqual(['⏱ bütçe aşıldı']);
    expect(host!.querySelector('.cm-msg-ai')?.textContent).not.toContain('bütçe aşıldı');
  });

  // v0.10.1660 (B5) — veri blokları / linkler / kaynaklar / takipler v1 çizicileriyle.
  it('ui-stream-v1 (B5): Aç → tek link (blok + data-links birleşir), Kaynaklar 2, takip çipi onAsk', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1')).fetch });
    await core.chat.sendMessage({ text: 'q' });
    const asked: string[] = [];
    host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
    await act(async () => {
      root!.render(<MemoryRouter><MessageList messages={core.chat.messages} visibleParts={core.visibleParts} status="ready"
        onAsk={t => asked.push(t)} /></MemoryRouter>);
    });
    const el = host;
    const links = [...el.querySelectorAll('.ai-links a')].map(a => a.getAttribute('href'));
    expect(links).toEqual(['/service?name=orders-api']);
    const src = el.querySelector('.cm-sources, [aria-label="Kaynaklar"]') ?? [...el.querySelectorAll('*')].find(n => n.textContent?.startsWith('Kaynaklar'));
    expect(src?.textContent).toContain('wiki-42'); // v1 SourceList belge adını çizer (etiket = "Kaynak n" sunucuda)
    expect(src?.textContent).toContain('orders-runbook.md');
    const fu = [...el.querySelectorAll('[data-cosre-followups] button')];
    expect(fu.map(b => b.textContent?.trim())).toEqual(['↳ orders-api loglarına bak']); // v1: kural-türevli takip varsa öneriler gizli
    await act(async () => { (fu[0] as HTMLButtonElement).click(); });
    expect(asked).toEqual(['orders-api loglarına bak']);
  });

  it('B5: akarken ve hata turunda takip çipi yok; hatada Yeniden dene onRetry', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-error')).fetch });
    await core.chat.sendMessage({ text: 'q' });
    let retried = 0;
    host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
    await act(async () => {
      root!.render(<MemoryRouter><MessageList messages={core.chat.messages} visibleParts={core.visibleParts} status="error"
        error={core.chat.error} onAsk={() => {}} onRetry={() => { retried++; }} /></MemoryRouter>);
    });
    expect(host.querySelector('[data-cosre-followups]')).toBeNull();
    const btn = [...host.querySelectorAll('button')].find(b => b.textContent?.includes('Yeniden dene'))!;
    await act(async () => { btn.click(); });
    expect(retried).toBe(1);
  });

  it('gönderildi, ilk parça yok → bekleyiş göstergesi; bilinmeyen parça çökertmez', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-reset')).fetch });
    core.chat.messages = [{ id: 'u', role: 'user', parts: [{ type: 'text', text: 'q' }] }];
    const el = await draw(core, 'submitted');
    expect(el.querySelector('[data-cosre-thinking]')).not.toBeNull();
    // v0.10.1722 (operatör): bekleyiş satırı ortalı — sınıf .cosre-root altında align-items:center.
    expect(el.querySelector('[data-cosre-thinking]')!.classList.contains('cm-wait-row')).toBe(true);
    await act(async () => {
      root!.render(<MemoryRouter><MessageList status="ready" visibleParts={m => m.parts} messages={[
        { id: 'a', role: 'assistant', parts: [{ type: 'data-unknown', data: {} }, { type: 'dynamic-tool', toolName: 'x', toolCallId: 's1', state: 'input-available', input: {} }, { type: 'text', text: 'tamam' }] },
      ]} /></MemoryRouter>);
    });
    expect(bodyText(host!.querySelector('.cm-msg-ai')!)).toBe('tamam');
  });
});

// araç seti dilim 5 (G4) — adım kartları cevabın ÜSTÜNDE, gerçek SDK + fixture.
describe('MessageList × adım kartları (dilim 5)', () => {
  it('ui-stream-v1: liste balondan önce; bitince katlı, kart türleri ve durdurma satırı', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1')).fetch });
    await core.chat.sendMessage({ text: 'orders-api neden yavaş?' });
    const el = await draw(core, 'ready');
    const list = el.querySelector('[data-cosre-steps]')!;
    const bubble = el.querySelector('.cm-msg-ai')!;
    expect(list).not.toBeNull();
    expect(list.compareDocumentPosition(bubble) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(list.querySelectorAll('[data-cosre-step]')).toHaveLength(0); // bitti → katlı
    await act(async () => { (list.querySelector('button') as HTMLElement).click(); });
    expect([...list.querySelectorAll('[data-cosre-step]')].map(s => s.getAttribute('data-kind'))).toEqual(['text', 'skipped']);
    expect(list.querySelector('[data-cosre-stop="call_cap"]')?.textContent).toContain('çağrı tavanı · 2 çağrı · 1 tur');
  });
});
