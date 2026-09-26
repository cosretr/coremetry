// @vitest-environment jsdom
//
// v0.10.948 (CoSRE Faz B) — "CoSRE'ye sor" ilk cevabının ODAK SPAN'i.
// Sunucu `?span=<16hex>` okur ve incelemeyi seçili span'in servisine odaklar;
// bağlam şeridi (Faz A) ve takip soruları (B2) zaten o servisi kullanıyor.
// Kablo çekmecede: trace öznesinde span, kabuğun traceCtx'inden (canlı yayın
// ?? kayıtlı görüntü) gelir — traceCtx.spanId yalnız operatör bir span
// SEÇMİŞSE dolu. Seçim yoksa istek span'siz (kök) kalır.
//
// NEDEN GERÇEK MOUNT: kablo bir prop dalı (subject.kind × traceCtx) ve
// CopilotExplain'in run()'ı; kaynak pini "4. argüman geçiyor"u değil, yalnız
// bir metnin varlığını kanıtlardı. Adlar sentetik (payments, prod).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { api } from '@/lib/api';
import type { ExplainStreamOpts } from '@/lib/api';
import type { ExplainTraceAnswer } from '@/lib/types';
import type { TraceAiContext } from '@/lib/traceAiContext';
import type { AISubject } from '@/lib/aiSubject';
import { AIDrawerBody } from './AIDrawerBody';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const SPAN = 'b7ad6b7169203331';
const CTX: TraceAiContext = {
  traceId: TRACE, spanId: SPAN, spanName: 'charge', service: 'payments', env: 'prod',
  fromNs: 1_790_000_000_000 * 1e6, toNs: 1_790_000_001_200 * 1e6,
};

let host: HTMLDivElement;
let root: Root;
let seen: { id: string; spanId?: string }[];

beforeEach(() => {
  window.history.replaceState({}, '', '/');
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  __resetCopilotEnabledCache();
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  seen = [];
  // cevap hiç gelmez: yalnız isteğin argümanları ölçülür (sohbet mount olmaz)
  vi.spyOn(api, 'copilotExplainTrace').mockImplementation(
    (id: string, _code?: boolean, _opts?: ExplainStreamOpts, spanId?: string) => {
      seen.push({ id, spanId });
      return new Promise<ExplainTraceAnswer>(() => {});
    });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function mount(subject: AISubject, traceCtx?: TraceAiContext | null) {
  await act(async () => {
    root.render(<MemoryRouter><AIDrawerBody subject={subject} onClose={() => {}} traceCtx={traceCtx} /></MemoryRouter>);
  });
  await act(async () => { await Promise.resolve(); });
}

describe('AIDrawerBody — trace öznesinin odak span\'i (v0.10.948)', () => {
  it('seçili span (traceCtx.spanId) copilotExplainTrace\'e 4. argüman olarak gider', async () => {
    await mount({ kind: 'trace', id: TRACE }, CTX);
    expect(seen).toEqual([{ id: TRACE, spanId: SPAN }]);
  });

  it('seçim yoksa (spanId yok / bağlam yok) span GİTMEZ — kök incelenir', async () => {
    await mount({ kind: 'trace', id: TRACE }, { ...CTX, spanId: undefined });
    await act(async () => { root.unmount(); });
    root = createRoot(host);
    __resetCopilotEnabledCache();
    await mount({ kind: 'trace', id: TRACE }, null);
    expect(seen).toEqual([{ id: TRACE, spanId: undefined }, { id: TRACE, spanId: undefined }]);
  });

  it('BAŞKA trace\'in bağlamı span taşımaz (bayat görüntü sızmaz)', async () => {
    await mount({ kind: 'trace', id: TRACE }, { ...CTX, traceId: 'f'.repeat(32) });
    expect(seen).toEqual([{ id: TRACE, spanId: undefined }]);
  });

  // v0.10.948 — yenileme / paylaşılan `/trace?id=X&span=Y&ai=trace` linki:
  // config span'lerden önce çözülür, auto-koşu traceCtx yayınlanmadan atar.
  // Odak o ana kadar URL'deki span (ilk cevap ile takipler aynı servis).
  it('bağlam henüz yok → URL\'deki seçili span gider (yenileme / paylaşılan link)', async () => {
    window.history.replaceState({}, '', `/trace?id=${TRACE}&span=${SPAN}&ai=trace`);
    await mount({ kind: 'trace', id: TRACE }, null);
    expect(seen).toEqual([{ id: TRACE, spanId: SPAN }]);
  });

  it('bağlam yayınlandıysa URL değil bağlam kazanır (açık seçimsizlik → kök)', async () => {
    window.history.replaceState({}, '', `/trace?id=${TRACE}&span=${SPAN}`);
    await mount({ kind: 'trace', id: TRACE }, { ...CTX, spanId: undefined });
    expect(seen).toEqual([{ id: TRACE, spanId: undefined }]);
  });

  it('URL başka trace\'in span\'ini taşıyorsa gitmez', async () => {
    window.history.replaceState({}, '', `/trace?id=${'f'.repeat(32)}&span=${SPAN}`);
    await mount({ kind: 'trace', id: TRACE }, null);
    expect(seen).toEqual([{ id: TRACE, spanId: undefined }]);
  });
});
