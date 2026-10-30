// @vitest-environment jsdom
//
// v0.10.1274 — CoSRE adım 5: sayfa bağlamı + env araçlara.
//
//   (1) bağlam şeridi gönderilecek kapsamı gösterir (Servis · Env · pencere ·
//       Trace); boş alan öğe üretmez;
//   (2) ✕ öğeyi bu konuşmanın isteğinden düşürür — düz alan VE sayfa ikizi
//       (sunucu page.service'ten servisi geri kurmasın);
//   (3) istek gövdesi: useChatThread → api.copilotChat argümanları;
//   (4) env Topbar'ın global seçicisinden (useUrlEnv), "Tüm ortamlar" = '';
//   (5) ekran kapsamı künyesi (ctxApplied) kanıt çipinde görünür.
// Adlar sentetik.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useState } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { api } from '@/lib/api';
import type { PageContext } from '@/lib/types';
import { resolveEnv } from '@/lib/useUrlEnv';
import { useChatThread } from './useChatThread';
import { ChatContextBar } from './ChatContextBar';
import {
  applyContextExclusions, contextBarItems, rangeLabel, shouldResetExclusions,
  type ChatRequestContext, type ContextKey,
} from './chatContextScope';
import { ctxAppliedLabel, evidenceChipText } from './evidenceChip';

const TRACE = 'abcdef0123456789abcdef0123456789';
const page: PageContext = { page: 'service', path: '/service', service: 'checkout', env: 'prod', operation: 'GET /x', traceId: TRACE, timeRange: { preset: '1h' } as PageContext['timeRange'] };
const ctx: ChatRequestContext = { service: 'checkout', operation: 'GET /x', env: 'prod', rangeS: 3600, trace: TRACE, page };

describe('contextBarItems', () => {
  it('dört öğe, sıra sabit; boş alan öğe üretmez', () => {
    const items = contextBarItems(ctx);
    expect(items.map(i => i.key)).toEqual(['service', 'env', 'range', 'trace']);
    expect(items.map(i => i.label)).toEqual(['Servis: checkout · GET /x', 'Env: prod', 'Son 1 saat', 'Trace: abcdef01…']);
    expect(contextBarItems({ env: '', service: '', rangeS: 0 })).toEqual([]);
  });
  it('rangeLabel birimleri', () => {
    expect(rangeLabel(21600)).toBe('Son 6 saat');
    expect(rangeLabel(1800)).toBe('Son 30 dakika');
    expect(rangeLabel(172800)).toBe('Son 2 gün');
    expect(rangeLabel(3600, Date.UTC(2026, 9, 16, 4, 0))).toMatch(/^1 saat · \d\d:\d\d'de biter$/);
  });
});

describe('applyContextExclusions', () => {
  it('çıkarma yoksa aynı nesne', () => {
    expect(applyContextExclusions(ctx, new Set())).toBe(ctx);
  });
  it('servis çıkarılınca operation ve sayfa ikizi de düşer', () => {
    const out = applyContextExclusions(ctx, new Set<ContextKey>(['service']));
    expect(out.service).toBe('');
    expect(out.operation).toBe('');
    expect(out.page?.service).toBeUndefined();
    expect(out.page?.operation).toBeUndefined();
    expect(out.env).toBe('prod');
    expect(page.service).toBe('checkout'); // girdi değişmez
  });
  it('env / pencere / trace çıkarımı', () => {
    const out = applyContextExclusions(ctx, new Set<ContextKey>(['env', 'range', 'trace']));
    expect(out.env).toBe('');
    expect(out.rangeS).toBeUndefined();
    expect(out.toMs).toBeUndefined();
    expect(out.trace).toBe('');
    expect(out.page?.env).toBeUndefined();
    expect(out.page?.timeRange).toBeUndefined();
    expect(out.page?.traceId).toBeUndefined();
    expect(contextBarItems(out).map(i => i.key)).toEqual(['service']);
  });
  it('çıkarmalar konuşmaya bağlı: ilk kimlik oluşumu sıfırlamaz, geçiş sıfırlar', () => {
    expect(shouldResetExclusions(undefined, 'c1')).toBe(false);
    expect(shouldResetExclusions('', 'c1')).toBe(false);
    expect(shouldResetExclusions('c1', 'c1')).toBe(false);
    expect(shouldResetExclusions('c1', 'c2')).toBe(true);
    expect(shouldResetExclusions('c1', undefined)).toBe(true);
  });
});

describe('ChatContextBar + istek gövdesi', () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.restoreAllMocks();
  });

  it('çizer; ✕ öğeyi kaldırır ve sonraki istek onu taşımaz', async () => {
    const calls: unknown[][] = [];
    vi.spyOn(api, 'copilotChat').mockImplementation(async (...args: unknown[]) => {
      calls.push(args);
      const onEvent = args[1] as (e: { kind: 'done'; ok: boolean }) => void;
      onEvent({ kind: 'done', ok: true });
    });
    let thread: ReturnType<typeof useChatThread> | null = null;
    let setEx: (s: ReadonlySet<ContextKey>) => void = () => {};
    function Harness() {
      const [ex, setExState] = useState<ReadonlySet<ContextKey>>(() => new Set());
      setEx = setExState;
      const req = applyContextExclusions(ctx, ex);
      thread = useChatThread({ ...req });
      return <ChatContextBar items={contextBarItems(req)} onRemove={k => setExState(p => new Set(p).add(k))} />;
    }
    act(() => root.render(<Harness />));
    expect(host.textContent).toContain('Servis: checkout');
    expect(host.textContent).toContain('Env: prod');
    expect(host.textContent).toContain('Son 1 saat');
    expect(host.textContent).toContain('Trace: abcdef01…');
    expect(host.querySelectorAll('.cm-context-bar button[aria-label$="bağlamını bu konuşmadan çıkar"]').length).toBe(4);

    await act(async () => { await thread!.send('hata oranı ne?'); });
    // copilotChat(messages, onEvent, signal, service, operation, explain, subject, rangeS, trace, env, toMs, profile, conv, page, pinnedPage, …)
    expect(calls[0][3]).toBe('checkout');
    expect(calls[0][9]).toBe('prod');
    expect(calls[0][7]).toBe(3600);
    expect(calls[0][8]).toBe(TRACE);
    expect((calls[0][13] as PageContext).env).toBe('prod');

    const removeEnv = host.querySelector<HTMLButtonElement>('button[aria-label="Env: prod bağlamını bu konuşmadan çıkar"]');
    expect(removeEnv).not.toBeNull();
    act(() => removeEnv!.click());
    expect(host.textContent).not.toContain('Env: prod');
    expect(host.textContent).toContain('Servis: checkout');

    await act(async () => { await thread!.send('ya şimdi?'); });
    expect(calls[1][9]).toBeUndefined(); // env gövdeye girmez
    expect((calls[1][13] as PageContext).env).toBeUndefined();
    expect(calls[1][3]).toBe('checkout');

    act(() => setEx(new Set<ContextKey>(['service', 'env', 'range', 'trace'])));
    expect(host.querySelector('.cm-context-bar')).toBeNull(); // öğe yoksa şerit yok
  });
});

describe('global env seçicisi → sohbet', () => {
  it('"Tüm ortamlar" (açık boş ?env=) yapışkan seçimi diriltmez → env gönderilmez', () => {
    expect(resolveEnv('', 'uat')).toBe('');
    expect(resolveEnv('prod', 'uat')).toBe('prod');
    expect(resolveEnv(null, 'uat')).toBe('uat');
  });
  it('CopilotChat env\'i Topbar ile AYNI kaynaktan (useUrlEnv) okur ve şerit + istek aynı nesneden', () => {
    const src = (readFileSync(resolve(__dirname, '../CopilotChat.tsx'), 'utf8') + readFileSync(resolve(__dirname, './cosreAsk.ts'), 'utf8')).replace(/^\s*\/\/.*$/gm, '');
    expect(src).toContain('const [env] = useUrlEnv();');
    expect(src).toMatch(/env: pinnedLegacy \? \(pinnedLegacy\.env \?\? ''\) : env,/);
    expect(src).toContain('const reqCtx = useMemo(() => applyContextExclusions(screenReqCtx, ctxExcluded)');
    expect(src).toContain('const baseCtxItems = useMemo(() => contextBarItems(reqCtx), [reqCtx]);'); // v0.10.1400 — + açıklama öğesi (ctxItems)
    expect(src).toMatch(/useChatThread\(\{\s*\.\.\.reqCtx,/);
    expect(src).toContain('<ChatContextBar items={ctxItems}');
  });
});

describe('ctxApplied künyesi', () => {
  it('kanıt çipi ekrandan uygulanan kapsamı söyler; yoksa parça eklemez', () => {
    expect(ctxAppliedLabel(undefined)).toBe('');
    expect(ctxAppliedLabel({ env: 'prod', rangeS: 3600 })).toMatch(/^ekrandan: env prod, 1/);
    expect(ctxAppliedLabel({ envKept: 'uat', screenEnv: 'prod' })).toBe('env uat (ekran prod)');
    expect(ctxAppliedLabel({ envNotApplied: 'x', screenEnv: 'prod' })).toBe('env uygulanmadı');
    const base = { tool: 'query_logs', source: 'ClickHouse', query: 'q', queryKind: 'summary', window: { fromMs: 0, toMs: 0 }, durationMs: 5 };
    expect(evidenceChipText(base)).not.toContain('ekrandan');
    expect(evidenceChipText({ ...base, ctxApplied: { env: 'prod' } })).toContain('ekrandan: env prod');
  });
});
