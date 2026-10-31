// stepView.test — CoSRE v2 araç seti dilim 5: parça → adım kartı görünümü.
// Her kind (evidence / text / skipped / error / running), önizleme satırları
// (page_url varsa tıklanır, yoksa href yok), durdurma satırı, kapsam rozeti
// (7 neden) ve kapsam değişimi notu.
import { describe, expect, it } from 'vitest';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import { SCOPE_REASONS, STOP_REASONS } from '@/lib/cosre/protocol';
import { errorMessage, inputsLine, previewFor, queryKindLabel, stepsView, stopBadges, stopLine } from './stepView';
import { latestScope, scopeChangeNotes, scopeOf, scopeView } from './scopeView';

type Part = CosreUIMessage['parts'][number];
const P = (x: Record<string, unknown>) => x as unknown as Part;

const tool = (i: number, name: string, args: Record<string, unknown>, output?: Record<string, unknown>, state = output ? 'output-available' : 'input-available') =>
  P({ type: 'dynamic-tool', toolCallId: `s${i}`, toolName: name, state, input: { argsText: JSON.stringify(args), args }, ...(output ? { output } : {}) });
const result = (i: number, data: Record<string, unknown>) => P({ type: 'data-tool-result', id: `s${i}`, data });

const epBody = {
  tool: 'endpoint_metrics', service: 'orders-api', mode: 'top', window_s: 3600, source: 'vm', has_more: false,
  page_url: '/endpoints?service=orders-api&src=metric&range=custom%3A1-2',
  endpoints: [
    { route: '/v1/orders', rps: 212, err5xx_pct: 4.8, last: 200, peak: 300 },
    { route: '/v1/orders/{id}', rps: 640, err5xx_pct: 0.1, last: 1, peak: 1 },
    { route: '/v1/cart', rps: 318, last: 1, peak: 1 },
    { route: '/v1/pay', rps: 3, last: 1, peak: 1 },
  ],
};

describe('stepsView — kind eşlemesi', () => {
  it('evidence: kaynak / seri / env etiketi, sorgu, page_url, tıklanır önizleme', () => {
    const v = stepsView([
      tool(1, 'endpoint_metrics', { service: 'orders-api' }, { ok: true, preview: JSON.stringify(epBody), durationMs: 1100 }),
      result(1, { kind: 'evidence', tool: 'endpoint_metrics', ok: true, source: 'VictoriaMetrics', query_kind: 'promql', query: 'topk(10, sum by (http_route) (rate(x[5m])))', series: 4, env: 'prod', duration_ms: 1100, page_url: epBody.page_url }),
    ]);
    const s = v.steps[0];
    expect(s.kind).toBe('evidence');
    expect(s.title).toBe('Endpoint metrikleri');
    expect(s.subtitle).toBe('orders-api, en yoğun 10 rota');
    expect(s.tags.map(t => t.label)).toEqual(['endpoint_metrics', 'VictoriaMetrics', '4 seri', 'env: prod']);
    expect(s.query).toContain('topk(10');
    expect(queryKindLabel(s.queryKind)).toBe('PromQL');
    expect(s.pageUrl).toBe(epBody.page_url);
    expect(s.pageLabel).toContain("Endpoints'te aç");
    expect(s.preview?.rows).toHaveLength(3);
    expect(s.preview?.more).toBe(1);
    expect(s.preview?.columns.map(c => c.label)).toEqual(['Rota', 'istek/sn', '5xx']); // top kipinde gecikme yok
    const r0 = s.preview!.rows[0];
    expect(r0.cells[2]).toMatchObject({ text: '%4,8', bad: true });
    expect(s.preview!.rows[1].cells[2].bad).toBe(false);
    expect(s.preview!.rows[2].cells[2].text).toBe('—');
    expect(r0.href).toContain('search=%2Fv1%2Forders');
    expect(r0.href).toContain('src=metric');
    expect(v.totalMs).toBe(1100);
  });

  it('page_url yoksa önizleme satırları tıklanmaz (href yok)', () => {
    const pv = previewFor('endpoint_metrics', epBody as unknown as Record<string, unknown>, undefined);
    expect(pv?.rows.every(r => r.href === undefined)).toBe(true);
    const tr = previewFor('find_traces', { traces: [{ id: 'abc', root_op: 'GET /x', ms: 1830, error: true }] }, '/traces?x=1');
    expect(tr?.rows[0]).toMatchObject({ href: '/trace/abc' });
    expect(tr?.rows[0].cells[1].text).toBe('1,8 sn');
    expect(tr?.rows[0].cells[2]).toMatchObject({ text: 'hata', bad: true });
    const svc = previewFor('list_my_services', { services: [{ service: 'payments-core', error_rate_pct: 0.3, p99_ms: 84 }] }, '/services');
    expect(svc?.rows[0].cells.map(c => c.text)).toEqual(['payments-core', '%0,3', '84 ms']);
    expect(svc?.rows[0].href).toContain('payments-core');
    expect(previewFor('unknown_tool', { rows: [1] }, '/x')).toBeUndefined();
  });

  it('route kipi p95 sütununu taşır; kısaltılmış gövde "daha fazlası" der', () => {
    const pv = previewFor('endpoint_metrics', { mode: 'route', truncated: true, endpoints: [{ route: '/v1/orders', method: 'POST', rps: 1, err5xx_pct: 0, p95_ms: 1900 }] }, undefined);
    expect(pv?.columns.map(c => c.label)).toEqual(['Rota', 'istek/sn', '5xx', 'p95']);
    expect(pv?.rows[0].cells[0].text).toBe('POST /v1/orders');
    expect(pv?.rows[0].cells[3].text).toBe('1,9 sn');
    expect(pv?.moreUnknown).toBe(true);
  });

  it('skipped: gerekçe (sunucu nedeni → cümle), önizleme yok', () => {
    const v = stepsView([
      tool(2, 'search_logs', { words: 'timeout' }, { ok: false, skipped: true, reason: 'call_cap' }),
      result(2, { kind: 'skipped', tool: 'search_logs', ok: false, reason: 'call_cap' }),
      tool(3, 'endpoint_metrics', { service: 'orders-api' }, { ok: false, skipped: true, reason: 'servisin VM ailesi gRPC' }),
      result(3, { kind: 'skipped', tool: 'endpoint_metrics', ok: false, reason: 'servisin VM ailesi gRPC' }),
    ]);
    expect(v.steps.map(s => s.kind)).toEqual(['skipped', 'skipped']);
    expect(v.steps[0].reason).toBe('çağrı bütçesi doldu');
    expect(v.steps[0].subtitle).toBe('çalıştırılmadı');
    expect(v.steps[1].reason).toBe('servisin VM ailesi gRPC');
    expect(v.steps[1].preview).toBeUndefined();
  });

  it('error: ToolErrorJSON ayrıştırılır; output-error de hata kartı', () => {
    const v = stepsView([
      tool(4, 'endpoint_metrics', { service: 'orders-api' }, { ok: false, preview: '{"error":"timeout","retryable":true,"hint":"tekrar dene","detail":"VictoriaMetrics sorgusu BAŞARISIZ"}' }),
      result(4, { kind: 'error', tool: 'endpoint_metrics', ok: false, text: '{"error":"timeout","retryable":true,"hint":"tekrar dene","detail":"VictoriaMetrics sorgusu BAŞARISIZ"}' }),
      P({ type: 'dynamic-tool', toolCallId: 's5', toolName: 'find_traces', state: 'output-error', input: {}, errorText: 'boom' }),
    ]);
    expect(v.steps[0]).toMatchObject({ kind: 'error', errorText: 'VictoriaMetrics sorgusu BAŞARISIZ' });
    expect(v.steps[1]).toMatchObject({ kind: 'error', errorText: 'boom' });
    expect(errorMessage('düz metin')).toBe('düz metin');
  });

  it('text + running; bilinmeyen araç adı insanlaştırılır; biçimsiz id atlanır', () => {
    const v = stepsView([
      tool(6, 'get_service_health', { service: 'payments-core' }, { ok: true, preview: 'ok' }),
      result(6, { kind: 'text', tool: 'get_service_health', ok: true, text: 'ok' }),
      tool(7, 'some_new_tool', {}),
      P({ type: 'dynamic-tool', toolCallId: 'x9', toolName: 'bad', state: 'input-available', input: {} }),
    ]);
    expect(v.steps.map(s => [s.kind, s.title])).toEqual([['text', 'Servis sağlığı'], ['running', 'some new tool']]);
    expect(v.steps[0].subtitle).toBe('payments-core');
  });

  it('data-tool-result olmadan (arşiv) çıktıdan kind türetilir', () => {
    const v = stepsView([tool(8, 'search_logs', {}, { ok: false, skipped: true, reason: 'repeated' })]);
    expect(v.steps[0]).toMatchObject({ kind: 'skipped', reason: 'aynı çağrı bu konuşmada zaten yapıldı' });
  });

  it('data-stop: tek satır, 8 nedenin hepsinin etiketi var', () => {
    const v = stepsView([P({ type: 'data-stop', id: 'stop', data: { reason: 'call_cap', calls: 4, rounds: 2, elapsed_ms: 4200 } })]);
    expect(v.stop).toEqual({ reason: 'call_cap', calls: 4, rounds: 2, elapsed_ms: 4200 });
    expect(stopLine(v.stop!)).toBe('çağrı tavanı · 4 çağrı · 2 tur · 4,2 sn');
    for (const r of STOP_REASONS) expect(stopLine({ reason: r, calls: 0, rounds: 0, elapsed_ms: 10 })).not.toMatch(/^undefined/);
  });
});

const scopePart = (data: Record<string, unknown>) => P({ type: 'data-scope', id: 'scope', data });
const msg = (id: string, role: 'user' | 'assistant', parts: Part[]) => ({ id, role, parts }) as CosreUIMessage;

describe('scopeView — 7 neden', () => {
  it('her neden etiket + açıklama + ton taşır; yalnız team kapsamlı', () => {
    for (const reason of SCOPE_REASONS) {
      const v = scopeView({ reason, unscoped: reason !== 'team', count: reason === 'team' ? 2 : 0, team: 'acme-orders', ...(reason === 'team' ? { services: ['orders-api', 'payments-core'] } : {}) });
      expect(v.label).not.toBe('');
      expect(v.text).not.toBe('');
      expect(v.tone).toBe(reason === 'team' ? 'ok' : reason === 'error' ? 'err' : 'warn');
    }
    const t = scopeView({ reason: 'team', unscoped: false, count: 32, team: 'acme-orders', services: ['orders-api', 'payments-core'], trimmed: 12 });
    expect(t.label).toBe('Kapsam: acme-orders · 32 servis');
    expect(t.services).toEqual(['orders-api', 'payments-core']);
    expect(t.more).toBe(30);
    expect(scopeView({ reason: 'error', unscoped: true, count: 0 }).text).toContain('okunamadı ≠');
  });

  it('rozet son kapsamı okur; değişim notu yalnız farklı kapsamda', () => {
    const team = scopePart({ reason: 'team', unscoped: false, count: 1, team: 'acme-orders', services: ['orders-api'] });
    const esc = scopePart({ reason: 'escape', unscoped: true, count: 0 });
    const msgs = [
      msg('u1', 'user', []), msg('a1', 'assistant', [team]),
      msg('u2', 'user', []), msg('a2', 'assistant', [team]),
      msg('u3', 'user', []), msg('a3', 'assistant', [esc]),
      msg('u4', 'user', []), msg('a4', 'assistant', []),
    ];
    expect(latestScope(msgs)?.reason).toBe('escape');
    const notes = scopeChangeNotes(msgs);
    expect([...notes.keys()]).toEqual(['a3']);
    expect(notes.get('a3')).toBe('Kapsam değişti: Tüm servisler · bu soru');
    expect(scopeOf([P({ type: 'data-scope', data: { reason: 'team', unscoped: true, count: 1 } })])).toBeNull(); // çelişki
    expect(latestScope([msg('u', 'user', [])])).toBeNull();
  });
});

// v0.10.1710 — mockup A: Girdiler satırı, Ham çıktı kaynağı, data-stop rozetleri.
describe('stepView 1710: girdiler / ham çıktı / durdurma rozetleri', () => {
  it.each([
    [{ service: 'orders-api', window_s: 3600 }, undefined, 'service=orders-api · window_s=3600'],
    [{ services: ['a', 'b'] }, undefined, 'services=["a","b"]'],
    [{}, 'service=orders-api', 'service=orders-api'],
    [{}, '{}', undefined],
    [{}, undefined, undefined],
  ])('inputsLine(%j, %j) → %j', (args, text, want) => {
    expect(inputsLine(args, text)).toBe(want);
  });

  it('kart görünümü girdileri ve ham önizlemeyi taşır; önizleme yoksa raw yok', () => {
    const v = stepsView([tool(2, 'find_traces', { service: 'x' }, { ok: true, preview: '{"traces":[]}' }), tool(3, 'search_logs', {})]);
    expect(v.steps[0].inputs).toBe('service=x');
    expect(v.steps[0].raw).toBe('{"traces":[]}');
    expect(v.steps[1].inputs).toBeUndefined();
    expect(v.steps[1].raw).toBeUndefined();
  });

  const d = (i: number, origin?: string) => ({ i, tool: 't', preview: '{}', ...(origin ? { origin } : {}) });
  it.each([
    ['rozet yok', { stepDetails: [d(1)] }, []],
    ['ön-yükleme: tüm araç adımları guided', { stepDetails: [d(1, 'guided'), d(2, 'guided')] }, ['ön-yükleme']],
    ['niyet: intent + guided', { stepDetails: [d(1, 'intent'), d(2, 'guided')] }, ['niyet']],
    ['etiket adımı (tool boş) sayılmaz', { stepDetails: [d(1, 'guided'), { i: 2, tool: '', label: 'x' }] }, ['ön-yükleme']],
    ['süre tavanı hatası', { stepDetails: [d(1)], error: 'yanıt süre tavanına dayandı' }, ['⏱ bütçe aşıldı']],
    ['başka hata rozet değil', { error: 'bağlantı koptu' }, []],
  ])('stopBadges: %s', (_n, turn, want) => {
    expect(stopBadges(turn).map(b => b.label)).toEqual(want);
  });
});
