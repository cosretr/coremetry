// traceAiContext.test.ts — v0.10.944 (CoSRE Faz A): trace → CoSRE çekmecesi
// bağlam devrinin SAF çekirdeği. Pinlenen sözleşme:
//   - odak span: seçili span varsa o, yoksa kök; kaynak alanları TEK çözücüden
//     (resolveResource) — `k8s.cluster.name` `cluster`dan, v0.10.944
//     `deployment.environment.name` `deployment.environment`tan önce gelir;
//   - pencere trace'in tamamı; sohbet penceresi ±5 dk, geleceğe taşmaz;
//   - `page` Go aynasının alan adlarıyla (traceId/spanId/timeRange custom);
//   - anlık görüntü ≤2 KB; kayıtlı görüntüden şerit geri kurulur;
//   - depo yalnız kendi trace'inin bağlamını verir.
import { describe, it, expect, afterEach } from 'vitest';
import {
  TRACE_CHAT_PAD_MS, capPageContext, clearTraceAiContext, formatTraceWindow, getTraceAiContext,
  publishTraceAiContext, traceChatContext, traceChatWindow, traceContextFromPage, traceContextToPage,
  type TraceCtxSpan,
} from './traceAiContext';

const T0 = 1_790_000_000_000_000_000; // ns
const TRACE = '0af7651916cd43dd8448eb211c80319c';

const span = (over: Partial<TraceCtxSpan>): TraceCtxSpan => ({
  spanId: 'a', parentSpanId: '', name: 'GET /checkout', serviceName: 'checkout',
  startTime: T0, endTime: T0 + 120e6, resourceAttributes: {}, attributes: {}, ...over,
});

const SPANS: TraceCtxSpan[] = [
  span({
    spanId: 'root0001', name: 'POST /checkout', serviceName: 'checkout', startTime: T0, endTime: T0 + 900e6,
    resourceAttributes: {
      'deployment.environment': 'prod', 'k8s.cluster.name': 'cluster-a', cluster: 'eski-anahtar',
      'k8s.namespace.name': 'shop', 'k8s.pod.name': 'checkout-7d9f-abc', 'service.version': '1.4.2',
    },
  }),
  span({
    spanId: 'pay00002', parentSpanId: 'root0001', name: 'charge', serviceName: 'payments',
    startTime: T0 + 100e6, endTime: T0 + 1_200e6,
    resourceAttributes: { 'deployment.environment.name': 'uat', 'k8s.namespace.name': 'billing', 'k8s.pod.name': 'payments-0' },
  }),
  span({ spanId: 'inv00003', parentSpanId: 'root0001', name: 'reserve', serviceName: 'inventory', startTime: T0 + 50e6, endTime: T0 + 80e6 }),
];

describe('traceChatContext', () => {
  it('seçim yoksa KÖK span: servis/env/cluster/namespace/pod/sürüm + trace penceresi', () => {
    const c = traceChatContext(SPANS, { traceId: TRACE });
    expect(c).toEqual({
      traceId: TRACE, service: 'checkout', env: 'prod', cluster: 'cluster-a', namespace: 'shop',
      pod: 'checkout-7d9f-abc', version: '1.4.2', fromNs: T0, toNs: T0 + 1_200e6,
    });
    // Seçim yokken span alanları HİÇ yazılmaz (boş string modele boşluk sunar).
    expect(c).not.toHaveProperty('spanId');
  });

  it('seçili span ODAK olur: kendi servisi + kendi kaynağı (env alias zinciri dahil)', () => {
    const c = traceChatContext(SPANS, { traceId: TRACE, spanId: 'pay00002' });
    expect(c).toMatchObject({
      spanId: 'pay00002', spanName: 'charge', service: 'payments', env: 'uat',
      namespace: 'billing', pod: 'payments-0',
    });
    // Kökün cluster'ı ödünç ALINMAZ — payments'ın kaynağı cluster taşımıyor.
    expect(c?.cluster).toBeUndefined();
    // Pencere yine trace'in TAMAMI.
    expect(c?.fromNs).toBe(T0);
  });

  // v0.10.944 — iki env anahtarı birden varsa backend'in sırası kazanır
  // (internal/otlp/convert.go deploy_env: güncel `.name` önce, eski yazım
  // yedek). Ters sıra şeride ve çekmecenin her turuna deploy_env'de OLMAYAN
  // bir ortam koyuyordu; guided kademe ctxEnv'i doğrulamadan route.Env'e
  // kopyaladığı için env kapsamlı her demet boş dönüyordu. Tek çözücü
  // (resolveResource) düzeltilir — burada ikinci bir anahtar zinciri YOK.
  it('iki env anahtarı: deployment.environment.name kazanır (deploy_env ile aynı)', () => {
    const both = [span({
      spanId: 'root0001', serviceName: 'checkout',
      resourceAttributes: { 'deployment.environment.name': 'prod', 'deployment.environment': 'production' },
    })];
    const c = traceChatContext(both, { traceId: TRACE });
    if (!c) throw new Error('bağlam kurulamadı');
    expect(c.env).toBe('prod');
    expect(traceContextToPage(c).env).toBe('prod');
  });

  it('bilinmeyen span seçimi köke düşer; boş liste / kimliksiz → null', () => {
    expect(traceChatContext(SPANS, { traceId: TRACE, spanId: 'yok' })?.service).toBe('checkout');
    expect(traceChatContext([], { traceId: TRACE })).toBeNull();
    expect(traceChatContext(undefined, { traceId: TRACE })).toBeNull();
    expect(traceChatContext(SPANS, { traceId: '' })).toBeNull();
  });

  it('ebeveyni trace dışında kalan (yetim) span da kök adayıdır; en erken olan seçilir', () => {
    const orphan = [
      span({ spanId: 'x', parentSpanId: 'disarida', serviceName: 'inventory', startTime: T0 + 5e6 }),
      span({ spanId: 'y', parentSpanId: 'disarida', serviceName: 'payments', startTime: T0 + 1e6 }),
    ];
    expect(traceChatContext(orphan, { traceId: TRACE })?.service).toBe('payments');
  });
});

describe('traceContextToPage + traceChatWindow', () => {
  const ctx = traceChatContext(SPANS, { traceId: TRACE, spanId: 'pay00002' });
  if (!ctx) throw new Error('bağlam kurulamadı');

  it('page: Go aynasının alanları; timeRange trace penceresi (pad\'siz, mutlak)', () => {
    const p = traceContextToPage(ctx);
    expect(p).toEqual({
      page: 'trace', path: '/trace', traceId: TRACE, spanId: 'pay00002', service: 'payments',
      env: 'uat', namespace: 'billing', pod: 'payments-0',
      timeRange: { preset: 'custom', fromMs: T0 / 1e6, toMs: (T0 + 1_200e6) / 1e6 },
    });
  });

  it('ms altı trace sıfır genişliğe çökmez (en az 1 ms)', () => {
    const p = traceContextToPage({ traceId: TRACE, service: 'checkout', fromNs: T0 + 1, toNs: T0 + 2 });
    expect(p.timeRange?.toMs).toBeGreaterThan(p.timeRange?.fromMs ?? Infinity);
  });

  it('sohbet penceresi ±5 dk; bitiş şimdiyi aşmaz', () => {
    const farFuture = T0 / 1e6 + 86_400_000;
    const w = traceChatWindow(ctx, farFuture);
    expect(w?.toMs).toBe((T0 + 1_200e6) / 1e6 + TRACE_CHAT_PAD_MS);
    expect(w?.rangeS).toBe(Math.ceil((1_200 + 2 * TRACE_CHAT_PAD_MS) / 1000));

    const justAfter = (T0 + 1_200e6) / 1e6 + 1_000; // trace 1 s önce bitti
    const c = traceChatWindow(ctx, justAfter);
    expect(c?.toMs).toBe(justAfter);
    expect(c?.rangeS).toBe(Math.ceil((justAfter - (T0 / 1e6 - TRACE_CHAT_PAD_MS)) / 1000));
  });

  it('pencere bilinmiyorsa null (sunucu kendi varsayılanında kalır)', () => {
    expect(traceChatWindow({ fromNs: 0, toNs: 0 }, Date.now())).toBeNull();
  });
});

describe('kalıcı anlık görüntü', () => {
  it('kayıtlı görüntüden şerit geri kurulur (span adı yok, kimlik var)', () => {
    const ctx = traceChatContext(SPANS, { traceId: TRACE, spanId: 'pay00002' });
    if (!ctx) throw new Error('bağlam kurulamadı');
    const back = traceContextFromPage(traceContextToPage(ctx));
    expect(back).toMatchObject({ traceId: TRACE, spanId: 'pay00002', service: 'payments', env: 'uat', fromNs: T0 });
    expect(back?.spanName).toBeUndefined();
    expect(traceContextFromPage({ page: 'home', path: '/' })).toBeNull();
    expect(traceContextFromPage(null)).toBeNull();
  });

  it('≤2 KB: sığan olduğu gibi; taşan önce filtre/arama bırakır, sonra alan kırpar', () => {
    const small = { page: 'trace' as const, path: '/trace', traceId: TRACE };
    expect(capPageContext(small)).toEqual(small);
    const big = {
      page: 'traces' as const, path: '/traces', traceId: TRACE, search: 'x'.repeat(3000),
      activeFilters: [{ k: 'service', op: '=', v: ['checkout'.repeat(50)] }],
    };
    const c = capPageContext(big);
    expect(c?.search).toBeUndefined();
    expect(c?.activeFilters).toBeUndefined();
    expect(c?.traceId).toBe(TRACE);
    const huge = { page: 'trace' as const, path: '/trace', service: 'ş'.repeat(3000) };
    const h = capPageContext(huge);
    expect([...(h?.service ?? '')].length).toBe(200);
    expect(new TextEncoder().encode(JSON.stringify(h)).length).toBeLessThanOrEqual(2048);
    expect(capPageContext(null)).toBeNull();
  });
});

describe('formatTraceWindow — tr-TR, saat dilimi etiketli', () => {
  // 2026-09-21T14:13:20Z = 1790000000 s
  it('UTC: aynı gün → bitişte yalnız saat; milisaniye görünür', () => {
    const w = formatTraceWindow(T0, T0 + 1_200e6, 'UTC');
    expect(w).toEqual({ text: '21.09.2026 14:13:20.000 → 14:13:21.200', tzLabel: 'UTC' });
  });
  it('Europe/Istanbul (+03:00) yerel saati gösterir ve etiketi taşır', () => {
    const w = formatTraceWindow(T0, T0 + 1_200e6, 'Europe/Istanbul');
    expect(w?.text).toBe('21.09.2026 17:13:20.000 → 17:13:21.200');
    expect(w?.tzLabel).toBe('Europe/Istanbul');
  });
  it('gün aşımı iki tarihi de yazar; tanınmayan dilim UTC\'ye düşer ve bunu söyler', () => {
    const w = formatTraceWindow(T0, T0 + 13 * 3600e9, 'UTC');
    expect(w?.text).toBe('21.09.2026 14:13:20.000 → 22.09.2026 03:13:20.000');
    expect(formatTraceWindow(T0, T0 + 1e6, 'Yok/Boyle_Bir_Dilim')?.tzLabel).toBe('UTC');
    expect(formatTraceWindow(0, 0, 'UTC')).toBeNull();
  });
});

describe('depo', () => {
  afterEach(() => publishTraceAiContext(null));

  it('yalnız kendi trace\'inin bağlamı okunur; ayrılan sayfa yalnız KENDİ yuvasını boşaltır', () => {
    const ctx = traceChatContext(SPANS, { traceId: TRACE });
    publishTraceAiContext(ctx);
    expect(getTraceAiContext(TRACE)).toBe(ctx);
    expect(getTraceAiContext('baska')).toBeNull();
    clearTraceAiContext('baska');
    expect(getTraceAiContext(TRACE)).toBe(ctx);
    clearTraceAiContext(TRACE);
    expect(getTraceAiContext(TRACE)).toBeNull();
  });

  it('eşit içerikli yeniden yayın kimliği DEĞİŞTİRMEZ (dinleyici uyanmaz)', () => {
    const a = traceChatContext(SPANS, { traceId: TRACE });
    const b = traceChatContext(SPANS, { traceId: TRACE });
    publishTraceAiContext(a);
    publishTraceAiContext(b);
    expect(getTraceAiContext(TRACE)).toBe(a);
  });
});
