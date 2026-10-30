// cosreSse — v0.10.1650 (CoSRE v2 Faz 2b B2, plan §5.1). Paylaşılan
// testdata/cosre/*.sse baytlarını çerçeve çerçeve yollayan sahte fetch;
// useCosreChat.test.tsx'ten çıkarıldı (yalnız test). `pauseAfter` doğru dönen
// çerçeveden sonra `release()`e dek bekler; abort akışı keser.
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

export const COSRE_REPO_ROOT = resolve(__dirname, '../../..');

export const cosreFixture = (name: string): string =>
  readFileSync(join(COSRE_REPO_ROOT, 'testdata', 'cosre', `${name}.sse`), 'utf8');

export function sseFetch(body: string, pauseAfter?: (frame: string) => boolean) {
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
