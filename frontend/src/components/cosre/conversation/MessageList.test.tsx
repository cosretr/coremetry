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

  // v0.10.1656 (B3) — adım açılırı v1'inki: akarken açık, bitince kapalı özet.
  it('ui-stream-v1 (B3): "Nasıl cevapladım · 4 adım" kapalı + bütçe özeti + doldu rozeti; açınca 2 araç satırı', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1')).fetch });
    await core.chat.sendMessage({ text: 'q' });
    const el = await draw(core);
    const how = el.querySelector<HTMLButtonElement>('.cm-how__btn')!;
    expect(how.textContent).toContain('Nasıl cevapladım · 4 adım');
    expect(how.getAttribute('aria-expanded')).toBe('false');
    expect(el.querySelector('[data-cosre-budget]')?.textContent).toBe('1/2 çağrı · 1/5 tur');
    expect(el.querySelector('[data-cosre-budget-exhausted]')?.textContent).toBe('bütçe doldu');
    await act(async () => { how.click(); });
    const sum = el.querySelector('.cm-steps-sum')?.textContent ?? '';
    expect(sum).toContain('2 araç');
    expect(sum).toContain('1 yürütülmedi');
    expect(el.textContent).toContain('get_service_health');
    expect(el.textContent).toContain('query_metrics');
  });

  it('akış ortası (B3): adım açılırı AÇIK ve "sürüyor"; araç çipi çalışıyor', async () => {
    const f = sseFetch(cosreFixture('ui-stream-v1'), fr => fr.includes('"toolCallId":"s2","toolName":"get_service_health","type":"tool-input-available"'));
    const core = createCosreChat({ fetch: f.fetch });
    const p = core.chat.sendMessage({ text: 'q' });
    await vi.waitFor(() => expect(core.chat.messages.at(-1)?.parts.some(x => x.type === 'dynamic-tool')).toBe(true));
    const el = await draw(core, 'streaming');
    const how = el.querySelector('.cm-how__btn')!;
    expect(how.getAttribute('aria-expanded')).toBe('true');
    expect(how.textContent).toContain('sürüyor');
    expect(el.textContent).toContain('get_service_health · çalışıyor…');
    f.release();
    await p;
  });

  it('closed (B3): etiket adımı çipte görünür', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-closed')).fetch });
    await core.chat.sendMessage({ text: 'q' });
    const el = await draw(core);
    const how = el.querySelector<HTMLButtonElement>('.cm-how__btn')!;
    expect(how.textContent).toContain('1 adım');
    await act(async () => { how.click(); });
    expect(el.querySelector('.cm-how__body')?.textContent).toContain('araştırma bütçesi: 6 araç · 5 tur');
  });

  // v0.10.1659 (B4) — kanıt başına İlgili/İlgisiz + mesaj notu → /api/ai/signal.
  it('ui-stream-v1 (B4): İlgili/İlgisiz s2 sinyali (iki durumlu), Not 512 bayta kırpılır; 👍/👎 yok', async () => {
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
      await act(async () => { btn('Nasıl cevapladım').click(); });
      await act(async () => { btn('get_service_health').click(); });
      const rel = el.querySelector('[data-cosre-relevance="s2"]')!;
      expect(rel).not.toBeNull();
      const [yes, no] = [...rel.querySelectorAll('button')];
      await act(async () => { yes.click(); });
      await act(async () => { yes.click(); }); // aynı durum: tekrar gönderilmez
      expect(yes.getAttribute('aria-pressed')).toBe('true');
      await act(async () => { no.click(); });
      expect(no.getAttribute('aria-pressed')).toBe('true');
      // v1'in kendi örtük sinyali (çip açma = evidence_click) ayrı; burada B4 türleri.
      const b4 = () => calls.filter(c => (c as { kind: string }).kind !== 'evidence_click');
      expect(calls).toContainEqual({ exchangeId: 'golden-xid', kind: 'evidence_click' });
      expect(b4()).toEqual([
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
      const note = b4()[2] as { kind: string; detail: string };
      expect(note.kind).toBe('note');
      expect(new TextEncoder().encode(note.detail).length).toBe(512);
      expect(el.querySelector('[data-cosre-note-sent]')).not.toBeNull();
      expect(el.textContent).not.toMatch(/👍|👎/);
    } finally {
      spy.mockRestore();
    }
  });

  it('B4: akış sürerken (exchangeId yok) İlgili/İlgisiz devre dışı', async () => {
    await act(async () => {
      if (!host) { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); }
      root!.render(<MemoryRouter><MessageList status="streaming" visibleParts={m => m.parts} messages={[
        { id: 'a', role: 'assistant', parts: [
          { type: 'dynamic-tool', toolName: 'get_logs', toolCallId: 's3', state: 'output-available', input: { argsText: 'a' }, output: { ok: true, preview: '{}' } },
          { type: 'text', text: 'akıyor' },
        ] },
      ]} /></MemoryRouter>);
    });
    const chip = [...host!.querySelectorAll('button')].find(b => b.textContent?.includes('get_logs'))!;
    await act(async () => { chip.click(); });
    const btns = [...host!.querySelectorAll('[data-cosre-relevance="s3"] button')];
    expect(btns).toHaveLength(2);
    expect(btns.every(b => (b as HTMLButtonElement).disabled)).toBe(true);
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
    await act(async () => {
      root!.render(<MemoryRouter><MessageList status="ready" visibleParts={m => m.parts} messages={[
        { id: 'a', role: 'assistant', parts: [{ type: 'data-unknown', data: {} }, { type: 'dynamic-tool', toolName: 'x', toolCallId: 's1', state: 'input-available', input: {} }, { type: 'text', text: 'tamam' }] },
      ]} /></MemoryRouter>);
    });
    expect(bodyText(host!.querySelector('.cm-msg-ai')!)).toBe('tamam');
  });
});
