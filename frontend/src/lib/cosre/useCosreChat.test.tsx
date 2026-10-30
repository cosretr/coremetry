// @vitest-environment jsdom
//
// useCosreChat.test — v0.10.1618 (CoSRE v2 Faz 2a): GERÇEK `ai` istemcisi
// (Chat + DefaultChatTransport + processUIMessageStream) paylaşılan
// testdata/cosre/*.sse baytlarıyla, sahte fetch üstünden sürülür (emsal:
// scratchpad aiparse/reducer2.mjs, finish-paths.mjs). Kanıtlanan:
//   normal / çift reset: final mesajda yalnız düzeltilmiş metin;
//   akış sırasında: taslak message.parts'ta DURUR, visibleParts gizler;
//   error / closed: onFinish isError → metadata.error, kayıt yok;
//   stop: isAbort → metadata.partial, reset edilen taslak yine silinir;
//   idempotans: onError + onFinish ikisi de temizler, sonuç tek temizlik;
//   kalıcılık: chat.messages'tan (temizlik sonrası), sunucu kimlikleri.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { act, useEffect } from 'react';
import type { ChatMessage } from '@/lib/types';
import { COSRE_V2_CHAT_API, createCosreChat, useCosreChat } from './useCosreChat';
import type { CosreUIMessage } from './resetReducer';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ROOT = resolve(__dirname, '../../../..');
const sse = (name: string) => readFileSync(join(ROOT, 'testdata', 'cosre', `${name}.sse`), 'utf8');
const MID = '01M62F9N000000000000000000';
const texts = (m: CosreUIMessage | undefined) => (m?.parts ?? []).flatMap(p => (p.type === 'text' ? [p.text] : []));

/** SSE gövdesini çerçeve çerçeve yollayan sahte fetch; `pauseAfter` doğru
 *  dönen çerçeveden sonra `release()`e dek bekler; abort akışı keser. */
function sseFetch(body: string, pauseAfter?: (frame: string) => boolean) {
  const calls: Array<{ url: string; body: unknown }> = [];
  let release = () => {};
  const gate = new Promise<void>(r => { release = r; });
  const fetch = (async (url: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(url), body: JSON.parse(String(init?.body)) });
    const signal = init?.signal;
    return new Response(new ReadableStream<Uint8Array>({
      async start(c) {
        signal?.addEventListener('abort', () => {
          try { c.error(new DOMException('aborted', 'AbortError')); } catch { /* kapalı */ }
        });
        const enc = new TextEncoder();
        for (const f of body.split('\n\n').filter(Boolean)) {
          if (signal?.aborted) return;
          c.enqueue(enc.encode(`${f}\n\n`));
          if (pauseAfter?.(f)) await gate;
        }
        if (!signal?.aborted) c.close();
      },
    }), { headers: { 'content-type': 'text/event-stream', 'x-vercel-ai-ui-message-stream': 'v1' } });
  }) as typeof globalThis.fetch;
  return { fetch, calls, release: () => release() };
}

it('repo kökü bulunur', () => {
  expect(existsSync(join(ROOT, 'go.mod'))).toBe(true);
});

describe('createCosreChat × paylaşılan fixture', () => {
  it('normal (reset): final yalnız düzeltilmiş metin; sunucu messageId; istek gövdesi v1 {role,text}', async () => {
    const f = sseFetch(sse('ui-stream-v1-reset'));
    const persisted: ChatMessage[][] = [];
    const { chat } = createCosreChat({ fetch: f.fetch, onPersist: m => persisted.push(m) });
    await chat.sendMessage({ text: 'q' });
    const last = chat.messages.at(-1)!;
    expect(last.id).toBe(MID);
    expect(texts(last)).toEqual(['Düzeltilmiş tam metin.']);
    expect(last.metadata?.resetApplied).toEqual([0]);
    expect(last.metadata?.partial).toBeUndefined();
    expect(f.calls).toEqual([{ url: COSRE_V2_CHAT_API, body: { messages: [{ role: 'user', text: 'q' }] } }]);
    expect(persisted).toEqual([[{ role: 'user', text: 'q' }, { role: 'assistant', text: 'Düzeltilmiş tam metin.' }]]);
  });

  it('çift reset (reset2): yalnız "Son tam metin." — t1 sonrası t2 doğru çözülür', async () => {
    const persisted: ChatMessage[][] = [];
    const { chat } = createCosreChat({ fetch: sseFetch(sse('ui-stream-v1-reset2')).fetch, onPersist: m => persisted.push(m) });
    await chat.sendMessage({ text: 'q' });
    expect(texts(chat.messages.at(-1))).toEqual(['Son tam metin.']);
    expect(chat.messages.at(-1)!.metadata?.resetApplied).toEqual([0, 1]);
    expect(persisted.at(-1)!.at(-1)).toEqual({ role: 'assistant', text: 'Son tam metin.' });
  });

  it('akış sırasında: taslak message.parts\'ta durur (onData mesajı değiştirmez), visibleParts gizler', async () => {
    const f = sseFetch(sse('ui-stream-v1-reset2'), fr => fr.includes('"delta":"İkinci ","id":"t2"'));
    const core = createCosreChat({ fetch: f.fetch });
    const p = core.chat.sendMessage({ text: 'q' });
    await vi.waitFor(() => expect(texts(core.chat.messages.at(-1))).toEqual(['Birinci taslak', 'İkinci ']));
    const shown = core.visibleParts(core.chat.messages.at(-1)!).flatMap(x => (x.type === 'text' ? [x.text] : []));
    expect(shown).toEqual(['İkinci ']);
    f.release();
    await p;
    expect(texts(core.chat.messages.at(-1))).toEqual(['Son tam metin.']);
  });

  it.each(['ui-stream-v1-error', 'ui-stream-v1-closed'])('%s: isError → metadata.error, status error, kayıt yok', async name => {
    const onPersist = vi.fn();
    const { chat } = createCosreChat({ fetch: sseFetch(sse(name)).fetch, onPersist });
    await chat.sendMessage({ text: 'q' });
    expect(chat.status).toBe('error');
    expect(chat.messages.at(-1)!.metadata?.error).toBe(true);
    expect(chat.messages.at(-1)!.parts.some(x => x.type === 'data-error')).toBe(true);
    expect(onPersist).not.toHaveBeenCalled();
  });

  it('stop: isAbort → partial:true; reset edilen t1 silinir, t2 kalır; kayıt yarım metinle', async () => {
    const f = sseFetch(sse('ui-stream-v1-reset'), fr => fr.includes('"delta":"Düzeltilmiş tam metin."'));
    const persisted: ChatMessage[][] = [];
    const { chat } = createCosreChat({ fetch: f.fetch, onPersist: m => persisted.push(m) });
    const p = chat.sendMessage({ text: 'q' });
    await vi.waitFor(() => expect(texts(chat.messages.at(-1))).toHaveLength(2));
    await chat.stop();
    await p;
    const last = chat.messages.at(-1)!;
    expect(last.metadata?.partial).toBe(true);
    expect(texts(last)).toEqual(['Düzeltilmiş tam metin.']);
    expect(persisted.at(-1)!.at(-1)).toEqual({ role: 'assistant', text: 'Düzeltilmiş tam metin.' });
  });

  it('idempotans: reset + error chunk → onError ve onFinish ikisi de temizler, t2 SİLİNMEZ', async () => {
    // reset fixture'ı t2 bitince hata parçasıyla kesilir (sentetik, aynı baytlar).
    const body = sse('ui-stream-v1-reset').split('data: {"type":"finish-step"}')[0]
      + 'data: {"errorText":"x","type":"error"}\n\ndata: [DONE]\n\n';
    const { chat } = createCosreChat({ fetch: sseFetch(body).fetch });
    await chat.sendMessage({ text: 'q' });
    expect(chat.status).toBe('error');
    expect(texts(chat.messages.at(-1))).toEqual(['Düzeltilmiş tam metin.']);
    expect(chat.messages.at(-1)!.metadata).toMatchObject({ resetApplied: [0], error: true });
  });

  it('ui-stream-v1 (tam senaryo): exchangeId metadata\'dan, codeRead, yalnız t3', async () => {
    const persisted: ChatMessage[][] = [];
    const { chat } = createCosreChat({ fetch: sseFetch(sse('ui-stream-v1')).fetch, onPersist: m => persisted.push(m) });
    await chat.sendMessage({ text: 'q' });
    const a = persisted.at(-1)!.at(-1)!;
    expect(a.role).toBe('assistant');
    expect((a.text ?? '').startsWith("**Bulgu** — orders-api p95 1830 ms'ye çıktı")).toBe(true);
    expect(a.exchangeId).toBe('golden-xid');
    expect(a.codeRead).toBe(true);
  });

  it('ikinci tur: durdurulan cevap geçmişte yarım notuyla, hatalı tur elenmiş gider', async () => {
    const stop = sseFetch(sse('ui-stream-v1-reset'), fr => fr.includes('"delta":"Düzeltilmiş tam metin."'));
    let fetch = stop.fetch;
    const calls: unknown[] = [];
    const { chat } = createCosreChat({ fetch: ((u, i) => { calls.push(JSON.parse(String(i?.body))); return fetch(u, i); }) as typeof globalThis.fetch });
    const p = chat.sendMessage({ text: 'q1' });
    await vi.waitFor(() => expect(texts(chat.messages.at(-1))).toHaveLength(2));
    await chat.stop();
    await p;
    // Her tur ayrı sunucu ULID'i: aynı id'li ikinci akış SDK'da öncekinin
    // YERİNE yazılır (findLastIndex(id)) — fixture'lar aynı id'yi paylaşıyor.
    fetch = sseFetch(sse('ui-stream-v1-error').replace(MID, '01M62F9N000000000000000001')).fetch;
    await chat.sendMessage({ text: 'q2' });
    fetch = sseFetch(sse('ui-stream-v1-reset2').replace(MID, '01M62F9N000000000000000002')).fetch;
    await chat.sendMessage({ text: 'q3' });
    expect(calls[2]).toEqual({ messages: [
      { role: 'user', text: 'q1' },
      { role: 'assistant', text: 'Düzeltilmiş tam metin.\n\n[Bu cevap operatör tarafından YARIDA DURDURULDU — tamamlanmadı.]' },
      { role: 'user', text: 'q2' },
      { role: 'user', text: 'q3' },
    ] });
    expect(texts(chat.messages.at(-1))).toEqual(['Son tam metin.']);
    expect(texts(chat.messages[1])).toEqual(['Düzeltilmiş tam metin.']);
  });
});

describe('useCosreChat (useChat kablosu)', () => {
  let host: HTMLDivElement;
  let root: Root;
  afterEach(() => { act(() => root.unmount()); host.remove(); });

  it('kanca render eder, sendMessage → mesajlar state\'e yansır, final temiz', async () => {
    const f = sseFetch(sse('ui-stream-v1-reset2'));
    let api: ReturnType<typeof useCosreChat> | undefined;
    const seen: string[][] = [];
    function Probe() {
      const c = useCosreChat({ fetch: f.fetch });
      api = c;
      useEffect(() => { seen.push(texts(c.messages.at(-1))); }, [c.messages]);
      return null;
    }
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => root.render(<Probe />));
    await act(async () => { await api!.sendMessage({ text: 'q' }); });
    expect(api!.status).toBe('ready');
    expect(texts(api!.messages.at(-1))).toEqual(['Son tam metin.']);
    expect(seen.at(-1)).toEqual(['Son tam metin.']);
  });
});
