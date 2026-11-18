// @vitest-environment jsdom
//
// EvidenceParts.test — v0.10.2363 (investigation-design S5). Kart başına çizim
// testi altın fixture'ın (testdata/cosre/ui-stream-v1-evidence) GERÇEK
// yükleriyle; link hedefleri; tembel ithalat pini (kartlar v2 sohbet chunk'ına
// statik girmez); MessageList üstünden gerçek SDK akışı.
import { describe, it, expect, afterEach, beforeAll, vi } from 'vitest';
import { act, type ReactElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { COSRE_REPO_ROOT, cosreFixture, sseFetch } from '@/test/cosreSse';
import { createCosreChat } from '@/lib/cosre/useCosreChat';
import { evidencePartsOf, type EvidencePart } from '@/lib/cosre/evidenceParts';
import { MessageList } from '../../conversation/MessageList';
import { ExceptionPartCard } from './ExceptionPartCard';
import { TracePartCard } from './TracePartCard';
import { LogsPartCard } from './LogsPartCard';
import { AnomalyPartCard } from './AnomalyPartCard';

const events = readFileSync(join(COSRE_REPO_ROOT, 'testdata', 'cosre', 'ui-stream-v1-evidence.events.jsonl'), 'utf8')
  .split('\n').filter(Boolean).map(l => JSON.parse(l) as { kind: string; payload: { id: string; type: string; seq: number; final: boolean; payload: unknown } });
const parts = evidencePartsOf(events.filter(e => e.kind === 'block')
  .map(e => ({ type: `data-block-${e.payload.type}`, id: e.payload.id, data: { seq: e.payload.seq, final: e.payload.final, payload: e.payload.payload } })));
const part = <T extends EvidencePart['type']>(t: T) => parts.find(p => p.type === t) as Extract<EvidencePart, { type: T }>;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(async () => { await act(async () => { root?.unmount(); }); host?.remove(); host = null; root = null; });

async function draw(el: ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter>{el}</MemoryRouter>); });
  return host;
}
const hrefs = (el: Element, sel: string) => [...el.querySelectorAll(sel)].map(a => a.getAttribute('href'));

describe('evidencePartsOf', () => {
  it('fixture: dört blok seq sırasıyla; href\'siz / bilinmeyen atlanır', () => {
    expect(parts.map(p => p.type)).toEqual(['exception', 'trace', 'logs', 'anomaly']);
    expect(evidencePartsOf([
      { type: 'data-block-trace', data: { payload: { traceId: 'x', durationMs: 1, spanCount: 1 } } },
      { type: 'data-block-table', data: { payload: { href: '/x' } } },
    ])).toEqual([]);
  });
});

describe('kanıt kartları × altın yük', () => {
  it('exception: tip, sayı, ilk çerçeveler, örnek trace + sayfa linki', async () => {
    const el = await draw(<ExceptionPartCard p={part('exception').payload} />);
    expect(el.textContent).toContain('java.net.SocketTimeoutException');
    expect(el.textContent).toContain('412 kez');
    expect(el.querySelector('pre')?.textContent).toContain('BankClient.call(BankClient.java:42)');
    expect(hrefs(el, '[data-part-href]')).toEqual(['/problems?exc=fp-pay-timeout']);
    expect(hrefs(el, 'a.mono')[0]).toContain('4bf92f3577b34da6a3ce929d0e0e4736');
  });

  it('trace: servis / operasyon, süre, span sayısı, hatalı span vurgulu + link', async () => {
    const el = await draw(<TracePartCard p={part('trace').payload} />);
    expect(el.textContent).toContain('POST /api/checkout');
    expect(el.textContent).toContain('1,2 sn');
    expect(el.textContent).toContain('38 span');
    const err = el.querySelector('.wf-evidence[data-error-span="00f067aa0ba902b7"]');
    expect(err?.textContent).toContain('POST /api/payments');
    expect(err?.textContent).toContain('Read timed out');
    expect(hrefs(el, '[data-part-href]')).toEqual(['/trace/4bf92f3577b34da6a3ce929d0e0e4736']);
  });

  it('logs: sorgu, toplam, satır + satırın trace linki', async () => {
    const el = await draw(<LogsPartCard p={part('logs').payload} />);
    expect(el.textContent).toContain('severity>=ERROR service=payments');
    expect(el.textContent).toContain('37 kayıt');
    expect(el.querySelectorAll('[role="listitem"]')).toHaveLength(1);
    expect(el.textContent).toContain('timeout calling bank');
    expect(hrefs(el, '[data-log-trace]')[0]).toContain('4bf92f3577b34da6a3ce929d0e0e4736');
    expect(hrefs(el, '[data-part-href]')).toEqual(['/logs?service=payments']);
  });

  it('anomaly: dedektör adı, gözlenen / taban (oran yok), örnek trace + link', async () => {
    const el = await draw(<AnomalyPartCard p={part('anomaly').payload} />);
    expect(el.textContent).toContain('latency');
    expect(el.textContent).toContain('POST /api/payments');
    expect(el.textContent).toContain('870');
    expect(el.textContent).toContain('120');
    expect(el.textContent).not.toMatch(/×|%/);
    expect(hrefs(el, '[data-part-href]')).toEqual(['/anomalies/ml/ml-1']);
  });
});

describe('tembel ithalat pini', () => {
  const src = (f: string) => readFileSync(resolve(__dirname, f), 'utf8');
  it('kartlar yalnız lazy(import()) ile; MessageList kartları statik içe aktarmaz', () => {
    const d = src('EvidenceParts.tsx');
    for (const c of ['ExceptionPartCard', 'TracePartCard', 'LogsPartCard', 'AnomalyPartCard']) {
      expect(d).toContain(`lazy(() => import('./${c}')`);
      expect(d).not.toMatch(new RegExp(`^import .*'\\./${c}'`, 'm'));
    }
    expect(src('../../conversation/MessageList.tsx')).not.toMatch(/PartCard/);
  });
});

describe('MessageList × ui-stream-v1-evidence (gerçek SDK)', () => {
  it('dört kart balonun altında tembel çizilir', async () => {
    const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-evidence')).fetch });
    await core.chat.sendMessage({ text: 'payments neden hata veriyor?' });
    const el = await draw(<MessageList messages={core.chat.messages} visibleParts={core.visibleParts} status="ready" />);
    await vi.waitFor(() => expect(el.querySelectorAll('[data-cosre-evidence-part]')).toHaveLength(4));
    expect([...el.querySelectorAll('[data-cosre-evidence-part]')].map(n => n.getAttribute('data-cosre-evidence-part')))
      .toEqual(['exception', 'trace', 'logs', 'anomaly']);
  });
});
