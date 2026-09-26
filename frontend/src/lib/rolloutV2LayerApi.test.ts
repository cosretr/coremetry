// rolloutV2LayerApi.test.ts — v0.10.960 (Rollouts v2 P1.8, inceleme F2):
// 0015 sihirbazının üç istemci yöntemi GERÇEKTEN doğru uca, doğru gövdeyle,
// doğru zaman aşımıyla gidiyor mu. Kaynak taraması değil — fetch taklidiyle
// api.ts'ten çıkan istek (metricSourceApi.test.ts deseni).
//
//   preflight-0015  GET, ?cluster= yalnız verilince; varsayılan 60 s
//   apply-0015      POST {cluster}; 360 s = sunucunun ön kontrol bütçesi
//                   (45 s) + DDL bütçesi (5 dk) — istemci SUNUCUDAN SONRA
//                   pes etmeli, yoksa operatör "zaman aşımı" görür ama DDL
//                   koşmaya devam eder (rollupApply yorumu)
//   rollback-0015   POST {cluster, confirm:true}; 200 s (sunucu 3 dk). confirm
//                   olmadan sunucu 400 döner (admin_rollout_layer.go)
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

type Seen = { url: string; method: string; body: unknown };

/** Her fetch çağrısını yakalar, 200 {} ile cevaplar. */
function captureFetch(): Seen[] {
  const seen: Seen[] = [];
  vi.stubGlobal('fetch', (url: unknown, init?: RequestInit) => {
    seen.push({
      url: String(url),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
    });
    return Promise.resolve(new Response('{}', {
      status: 200, headers: { 'content-type': 'application/json' },
    }));
  });
  return seen;
}

/** Hiç cevaplamayan sunucu; abort'u kaydeder. */
function hangingFetch(): { aborted: () => boolean } {
  let aborted = false;
  vi.stubGlobal('fetch', (_url: unknown, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
    init?.signal?.addEventListener('abort', () => {
      aborted = true;
      reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
    });
  }));
  return { aborted: () => aborted };
}

describe('rolloutV2Layer* istemci uçları (v0.10.960)', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('ön kontrol: küme yoksa parametresiz GET (sunucu önerilen kümeyi probe eder)', async () => {
    const seen = captureFetch();
    await api.rolloutV2LayerPreflight();
    await api.rolloutV2LayerPreflight('');
    expect(seen).toEqual([
      { url: '/api/admin/rollout-layer/preflight-0015', method: 'GET', body: undefined },
      { url: '/api/admin/rollout-layer/preflight-0015', method: 'GET', body: undefined },
    ]);
  });

  it('ön kontrol: istenen küme ?cluster= ile, kodlanmış', async () => {
    const seen = captureFetch();
    await api.rolloutV2LayerPreflight('uptrace_all');
    await api.rolloutV2LayerPreflight('a&b');
    expect(seen.map(s => s.url)).toEqual([
      '/api/admin/rollout-layer/preflight-0015?cluster=uptrace_all',
      '/api/admin/rollout-layer/preflight-0015?cluster=a%26b',
    ]);
    expect(seen.every(s => s.method === 'GET')).toBe(true);
  });

  it('uygula: POST apply-0015 {cluster} — 0012 ucuna ya da withMV gövdesine gitmez', async () => {
    const seen = captureFetch();
    await api.rolloutV2LayerApply('uptrace_all');
    expect(seen).toEqual([
      { url: '/api/admin/rollout-layer/apply-0015', method: 'POST', body: { cluster: 'uptrace_all' } },
    ]);
  });

  it('geri al: POST rollback-0015 {cluster, confirm:true}', async () => {
    const seen = captureFetch();
    await api.rolloutV2LayerRollback('uptrace_all');
    expect(seen).toEqual([
      { url: '/api/admin/rollout-layer/rollback-0015', method: 'POST', body: { cluster: 'uptrace_all', confirm: true } },
    ]);
  });

  it('ön kontrol varsayılan 60 s tavanında kalır', async () => {
    vi.useFakeTimers();
    const f = hangingFetch();
    const p = api.rolloutV2LayerPreflight('uptrace_all');
    const assertion = expect(p).rejects.toThrow(/timed out after 60s/);
    await vi.advanceTimersByTimeAsync(59_000);
    expect(f.aborted()).toBe(false);
    await vi.advanceTimersByTimeAsync(2_000);
    await assertion;
    expect(f.aborted()).toBe(true);
  });

  it('uygula 360 s bekler (45 s ön kontrol + 5 dk DDL), sonra keser', async () => {
    vi.useFakeTimers();
    const f = hangingFetch();
    const p = api.rolloutV2LayerApply('uptrace_all');
    const assertion = expect(p).rejects.toThrow(/timed out after 360s/);
    await vi.advanceTimersByTimeAsync(345_000); // sunucu bütçesinin (45 s + 300 s) sonu
    expect(f.aborted()).toBe(false);
    await vi.advanceTimersByTimeAsync(16_000);
    await assertion;
  });

  it('geri al 200 s bekler (sunucu 3 dk), sonra keser', async () => {
    vi.useFakeTimers();
    const f = hangingFetch();
    const p = api.rolloutV2LayerRollback('uptrace_all');
    const assertion = expect(p).rejects.toThrow(/timed out after 200s/);
    await vi.advanceTimersByTimeAsync(180_000);
    expect(f.aborted()).toBe(false);
    await vi.advanceTimersByTimeAsync(21_000);
    await assertion;
  });
});
