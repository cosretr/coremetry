// copilotChatContext.test — v0.10.1648 (CoSRE v2 Faz 2b B2a). api.copilotChat'in
// `context` gövdesi lib/copilotChatContext'e çıkarıldı; v1 gövdesi bayt bayt
// aynı kalmalı (anahtar SIRASI dahil — JSON.stringify sırayı korur) ve v2 aynı
// kurucuyu kullanır. Çıkarma öncesi gövde şekli burada sabitlenir.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { api } from './api';
import { buildCopilotChatContext, type CopilotChatContextFields } from './copilotChatContext';
import type { PageContext } from './types';

afterEach(() => { vi.unstubAllGlobals(); });

const page: PageContext = { page: 'service', path: '/service', service: 'orders-api' };
const FULL: CopilotChatContextFields = {
  service: 'orders-api', operation: 'GET /orders', explain: 'açıklama', subject: 'trace:abc',
  rangeS: 3600, trace: 'abc', env: 'prod', toMs: 1_700_000_000_000, profile: 'fast',
  conversation: 'c1', page, pinnedPage: page, wikiRefs: ['/wiki/a'],
  scope: { services: ['orders-api'] }, command: 'trace', deep: true,
  attempt: 'retry', explainAsk: { kind: 'trace', id: 'abc', src: 'test' }, code: true, codeLocked: true,
};

/** api.copilotChat'in gerçek gövdesi (sahte fetch, boş akış). */
async function v1Body(f: CopilotChatContextFields): Promise<{ messages: unknown; context?: Record<string, unknown> }> {
  let body = '';
  vi.stubGlobal('fetch', vi.fn(async (_u: unknown, init?: RequestInit) => {
    body = String(init?.body);
    return new Response(new ReadableStream({ start(c) { c.close(); } }), { status: 200 });
  }));
  await api.copilotChat([{ role: 'user', text: 'q' }], () => {}, undefined,
    f.service, f.operation, f.explain, f.subject, f.rangeS, f.trace, f.env, f.toMs, f.profile,
    f.conversation, f.page, f.pinnedPage, f.wikiRefs, f.scope, f.command, f.deep, f.attempt,
    f.explainAsk, f.code, f.codeLocked);
  return JSON.parse(body);
}

describe('buildCopilotChatContext ≡ api.copilotChat gövdesi', () => {
  it.each<[string, CopilotChatContextFields]>([
    ['boş (yalnız dilim)', {}],
    ['tüm alanlar', FULL],
    ['sıfır/boş değerler düşer', { rangeS: 0, toMs: 0, wikiRefs: [], deep: false, code: false, service: '' }],
    ['kapsam + komut', { scope: FULL.scope, command: FULL.command }],
    ['yalnız sayfa + derin', { page, deep: true }],
  ])('%s', async (_n, f) => {
    const b = await v1Body(f);
    const want = buildCopilotChatContext(f);
    expect(b.context).toEqual(want);
    expect(JSON.stringify(b)).toBe(JSON.stringify(want ? { messages: [{ role: 'user', text: 'q' }], context: want } : { messages: [{ role: 'user', text: 'q' }] }));
  });

  it('anahtar sırası sabit (çıkarma öncesi gövde)', () => {
    expect(Object.keys(buildCopilotChatContext(FULL)!).filter(k => k !== 'tz' && k !== 'tzOffsetMin')).toEqual([
      'service', 'operation', 'explain', 'subject', 'rangeS', 'trace', 'env', 'toMs', 'profile', 'conversation',
      'page', 'pinnedPage', 'wikiRefs', 'scope', 'command', 'deep', 'attempt', 'explainAsk', 'code', 'codeLocked',
    ]);
  });
});
