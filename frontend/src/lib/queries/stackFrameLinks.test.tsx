// @vitest-environment jsdom
//
// stackFrameLinks.test.tsx — v0.10.1048 (operatör: "Exception sayfasındaki
// dosya bağlantıları hâlâ daldan açılıyor; kod incelemesi artık sürümden
// okuyor. İkisi aynı yere baksın.").
//
// Exception detayının kablolaması (representativeStack → useStackFrameLinks)
// GERÇEK istek gövdesiyle (fetch stub) ve main.tsx'le AYNI varsayılanlı bir
// QueryClient'la (küresel keepPreviousData) doğrulanır:
//   • gösterilen örneğin sürümü gider, en yeni örneğinki değil;
//   • sürüm yoksa gövde bugünküyle bayt bayt aynı;
//   • sürüm anahtarda: aynı stack + başka sürüm = ayrı istek;
//   • temsilî örnek değişince önceki örneğin linkleri hiçbir render'da
//     görünmez (placeholderData: undefined).
//
// ZAMANA BAĞLI DEĞİL (ilk sürüm tam takımda yarışıyordu): cevaplar
// testin AÇIKÇA çözdüğü ertelenmiş söz; gerçek Response yok (gövde akışı
// birkaç makro-görev sürebiliyordu), sahte nesnenin json()'u mikro-görev.
// Veri gelişi zamanlayıcıyla değil olayla beklenir: QueryCache'in o anahtar
// için 'success' bildirimi. notifyManager zamanlayıcısı EŞZAMANLI — aksi
// hâlde gözlemci → React bildirimi kendi setTimeout(0)'ında kalır ve act
// ondan önce kapanabilirdi. Her test taze QueryClient; sonra temizlik.
// Kalıp: createRoot + act (repo @testing-library kullanmıyor).
import { describe, it, expect, vi, afterEach, beforeAll, afterAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import {
  QueryClient, QueryClientProvider, keepPreviousData, notifyManager, defaultScheduler,
} from '@tanstack/react-query';
import type { ExceptionSample, StackFramesResult } from '@/lib/types';
import { useStackFrameLinks } from './devops';
import { representativeStack } from '@/features/anomalies/stackSource';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

beforeAll(() => { notifyManager.setScheduler(cb => cb()); });
afterAll(() => { notifyManager.setScheduler(defaultScheduler); });

const bodies: string[] = [];
// Etiket (stack|sürüm) → o isteğin cevabını veren fonksiyon. Cevap ancak
// test respond() çağırınca gelir.
const pending = new Map<string, () => void>();

const tagOf = (stack: string, version: string | undefined) => `${stack}|${version ?? ''}`;

function frames(tag: string): StackFramesResult {
  return {
    configured: true, branch: 'release', revisionWarning: 'w',
    frames: [{ lineIndex: 1, class: tag, method: 'm', file: 'a/B.java', line: 1, isApp: true, tier: 1, url: `https://scm.example.invalid/${encodeURIComponent(tag)}` }],
  };
}

function stubFetch() {
  vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) => {
    const body = String(init?.body);
    bodies.push(body);
    const b = JSON.parse(body) as { stack: string; version?: string };
    const tag = tagOf(b.stack, b.version);
    return new Promise<Response>((resolve, reject) => {
      const res = frames(tag);
      pending.set(tag, () => resolve({
        status: 200, ok: true,
        headers: { get: (k: string) => (k.toLowerCase() === 'content-type' ? 'application/json' : null) },
        json: () => Promise.resolve(res),
        text: () => Promise.resolve(JSON.stringify(res)),
      } as unknown as Response));
      // Söküm (unmount) isteği iptal eder: request()'in finally'si 60 sn'lik
      // zaman aşımı sayacını temizlesin, askıda söz kalmasın.
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true });
    });
  }));
}

type Seen = { tag: string | undefined; placeholder: boolean };
const history: Seen[] = [];

// ProblemDetail'in kablolamasıyla birebir: stack + sürüm tek yardımcıdan,
// stack kanonik metne çevrilip sorguya gider.
function Probe({ samples }: { samples: ExceptionSample[] }) {
  const { stack, version } = representativeStack(samples);
  const norm = stack.replace(/\r\n/g, '\n').trimEnd();
  const q = useStackFrameLinks({ service: 'card-service', stack: norm, version, enabled: !!norm });
  history.push({ tag: q.data?.frames[0]?.class, placeholder: q.isPlaceholderData });
  return null;
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient | null = null;
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  qc?.clear(); qc = null;
  bodies.length = 0;
  history.length = 0;
  pending.clear();
  vi.unstubAllGlobals();
});

// Taze istemci, main.tsx'teki yer tutucu kuralıyla.
function client() {
  qc = new QueryClient({ defaultOptions: { queries: { placeholderData: keepPreviousData, retry: false } } });
  return qc;
}

// Render + efektler tek act'te: sorgu fetch'i efektte EŞZAMANLI başlar, yani
// act dönünce gövde kaydedilmiştir; cevap henüz YOK.
async function render(c: QueryClient, samples: ExceptionSample[]) {
  await act(async () => {
    if (!root) {
      host = document.createElement('div');
      document.body.appendChild(host);
      root = createRoot(host);
    }
    root.render(<QueryClientProvider client={c}><Probe samples={samples} /></QueryClientProvider>);
  });
}

// Cevabı ver ve o anahtarın önbelleğe YAZILDIĞI olayı bekle (zamanlayıcı yok).
// Abonelik cevaptan ÖNCE kurulur — olay kaçmaz. act kapanırken React,
// eşzamanlı bildirilen güncellemeyi çizer.
async function respond(c: QueryClient, tag: string) {
  const fire = pending.get(tag);
  expect(fire, `bekleyen istek yok: ${tag}`).toBeDefined();
  const landed = new Promise<void>(resolve => {
    const unsub = c.getQueryCache().subscribe(ev => {
      const k = ev.query.queryKey as unknown[];
      if (ev.type === 'updated' && ev.action.type === 'success' && tagOf(String(k[3]), String(k[4])) === tag) {
        unsub();
        resolve();
      }
    });
  });
  await act(async () => { fire!(); await landed; });
}

const sample = (p: Partial<ExceptionSample>): ExceptionSample => ({
  traceId: 't', spanId: 's', time: 0, message: 'm', stacktrace: '', spanName: 'op', statusMsg: '', ...p,
});
const last = () => history[history.length - 1];

describe('exception detayı frame linkleri — gösterilen örneğin sürümü (v0.10.1048)', () => {
  it('gövde gösterilen (stack\'li) örneğin sürümünü taşır, en yeni örneğinkini değil', async () => {
    stubFetch();
    const c = client();
    await render(c, [
      sample({ traceId: 'newest', stacktrace: '', runningVersion: '2.0.0' }),
      sample({ traceId: 'older', stacktrace: 'S-old\r\n', runningVersion: '1.4.2' }),
    ]);
    expect(bodies).toHaveLength(1);
    expect(JSON.parse(bodies[0])).toEqual({ service: 'card-service', stack: 'S-old', version: '1.4.2' });
    await respond(c, 'S-old|1.4.2');
    expect(last()).toEqual({ tag: 'S-old|1.4.2', placeholder: false });
  });

  it('sürüm yoksa gövde bugünküyle bayt bayt aynı (version alanı hiç yok)', async () => {
    stubFetch();
    await render(client(), [sample({ stacktrace: 'S1' })]);
    expect(bodies).toEqual([JSON.stringify({ service: 'card-service', stack: 'S1' })]);
  });

  it('sürüm anahtarda: aynı stack + başka sürüm → ayrı istek ve o sürümün künyesi', async () => {
    stubFetch();
    const c = client();
    await render(c, [sample({ stacktrace: 'S', runningVersion: '1.4.1' })]);
    await respond(c, 'S|1.4.1');
    expect(last().tag).toBe('S|1.4.1');
    await render(c, [sample({ stacktrace: 'S', runningVersion: '1.4.2' })]);
    expect(bodies.map(b => JSON.parse(b).version)).toEqual(['1.4.1', '1.4.2']);
    // 1.4.2 cevabı gelmeden: 1.4.1'in künyesi bu sürümün altında gösterilmez.
    expect(last()).toEqual({ tag: undefined, placeholder: false });
    await respond(c, 'S|1.4.2');
    expect(last()).toEqual({ tag: 'S|1.4.2', placeholder: false });
  });

  it('temsilî örnek değişince A\'nın linkleri B\'nin yüklemesi boyunca HİÇ görünmez', async () => {
    stubFetch();
    const c = client();
    await render(c, [sample({ traceId: 'A', stacktrace: 'S-A', runningVersion: '1.4.1' })]);
    await respond(c, 'S-A|1.4.1');
    expect(last()).toEqual({ tag: 'S-A|1.4.1', placeholder: false });

    // Örnek listesi tazelendi: daha yeni bir örnek temsilî oldu. B'nin cevabı
    // test respond() diyene dek gelmez — aradaki her render denetlenir.
    const mark = history.length;
    await render(c, [
      sample({ traceId: 'B', stacktrace: 'S-B', runningVersion: '1.4.2' }),
      sample({ traceId: 'A', stacktrace: 'S-A', runningVersion: '1.4.1' }),
    ]);
    expect(pending.has('S-B|1.4.2')).toBe(true);
    const loading = history.slice(mark);
    expect(loading.length).toBeGreaterThan(0);
    for (const h of loading) expect(h).toEqual({ tag: undefined, placeholder: false });

    await respond(c, 'S-B|1.4.2');
    expect(last()).toEqual({ tag: 'S-B|1.4.2', placeholder: false });
    for (const h of history.slice(mark)) {
      expect(h.tag === undefined || h.tag === 'S-B|1.4.2').toBe(true);
      expect(h.placeholder).toBe(false);
    }
  });
});
