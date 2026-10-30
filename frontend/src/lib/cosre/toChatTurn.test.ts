// toChatTurn.test — v0.10.1656 (CoSRE v2 Faz 2b B3). Paylaşılan fixture'lar
// GERÇEK AI SDK'dan geçer (createCosreChat + sseFetch), son asistan mesajı v1
// ChatTurn'üne katlanır: adım çipleri parça sırasıyla, detaylar s<i> ile,
// araç çıktısı v1 step-result alanlarıyla, bütçe son data-budget.
import { describe, it, expect } from 'vitest';
import { cosreFixture, sseFetch } from '@/test/cosreSse';
import { createCosreChat } from './useCosreChat';
import { evidenceOf, parseBudget, stepIndex, toChatTurn } from './toChatTurn';

async function lastTurn(name: string) {
  const core = createCosreChat({ fetch: sseFetch(cosreFixture(name)).fetch });
  await core.chat.sendMessage({ text: 'q' });
  const m = core.chat.messages.at(-1)!;
  return toChatTurn(m, core.visibleParts(m));
}

describe('toChatTurn (B3: adımlar + bütçe)', () => {
  it('ui-stream-v1: 4 adım (2 etiket + 2 araç), detaylar ve bütçe', async () => {
    const t = await lastTurn('ui-stream-v1');
    expect(t.role).toBe('assistant');
    expect(t.text).toContain("orders-api p95 1830 ms'ye çıktı");
    expect(t.steps).toEqual([
      'araştırma bütçesi: 6 araç · 5 tur',
      'get_service_health',
      'query_metrics',
      expect.stringContaining('araştırma bütçesi doldu'),
    ]);
    expect(t.stepDetails?.map(d => [d.i, d.tool])).toEqual([[1, ''], [2, 'get_service_health'], [3, 'query_metrics'], [4, '']]);
    const [, health, metrics] = t.stepDetails!;
    expect(health).toMatchObject({ ok: true, preview: '{"p95_ms":1830}', bytes: 15, durationMs: 42, href: '/service?name=orders-api', truncated: false, args: '{"service":"orders-api"}' });
    expect(health.sources).toEqual([{ source: 'traces', state: 'ok' }]);
    expect(health.skipped).toBeUndefined();
    expect(metrics).toMatchObject({ ok: false, skipped: true, preview: 'araştırma bütçesi doldu', args: 'service=orders-api' });
    expect(t.budget).toEqual({ executed: 1, maxCalls: 2, roundsUsed: 1, maxRounds: 5, skipped: 1, exhausted: true });
    expect(t.pending).toBeUndefined();
  });

  it('closed: yalnız etiket adımı; araçsız detay', async () => {
    const t = await lastTurn('ui-stream-v1-closed');
    expect(t.steps).toEqual(['araştırma bütçesi: 6 araç · 5 tur']);
    expect(t.stepDetails).toEqual([{ i: 1, tool: '', label: 'araştırma bütçesi: 6 araç · 5 tur' }]);
    expect(t.budget).toBeUndefined();
  });

  it('akış sırasında araç girdisi: preview yok (çip "çalışıyor"), pending/error/stopped geçer', () => {
    const t = toChatTurn(
      { id: 'a', role: 'assistant', metadata: { partial: true }, parts: [
        { type: 'dynamic-tool', toolName: 'get_logs', toolCallId: 's7', state: 'input-available', input: { argsText: 'svc=a' } },
        { type: 'dynamic-tool', toolName: 'bad', toolCallId: 'x', state: 'input-available', input: {} },
      ] },
      [], { pending: true, error: 'boom' },
    );
    expect(t).toMatchObject({ text: '', pending: true, error: 'boom', stopped: true });
    expect(t.steps).toBeUndefined();
    const live = toChatTurn({ id: 'a', role: 'assistant', parts: [] }, [
      { type: 'dynamic-tool', toolName: 'get_logs', toolCallId: 's7', state: 'input-available', input: { argsText: 'svc=a' } },
      { type: 'dynamic-tool', toolName: 'bad', toolCallId: 'x', state: 'input-available', input: {} },
    ]);
    expect(live.steps).toEqual(['get_logs', 'bad']);
    expect(live.stepDetails).toEqual([{ i: 7, tool: 'get_logs', args: 'svc=a' }]);
  });

  it.each([
    ['s1', 1], ['s42', 42], ['s0', undefined], ['s01', undefined], ['t1', undefined], ['', undefined], [7, undefined],
  ])('stepIndex(%j) = %j', (id, want) => {
    expect(stepIndex(id)).toBe(want);
  });

  it('parseBudget: eksik sayısal alan → null; skipped varsayılanı 0', () => {
    expect(parseBudget({ executed: 1, maxCalls: 2, roundsUsed: 1 })).toBeNull();
    expect(parseBudget(null)).toBeNull();
    expect(parseBudget({ executed: 0, maxCalls: 6, roundsUsed: 0, maxRounds: 5 })).toEqual({ executed: 0, maxCalls: 6, roundsUsed: 0, maxRounds: 5, skipped: 0, exhausted: false });
  });

  // v0.10.1659 (B4) — sinyal anahtarı + kanıt çipleri.
  it('B4: exchangeId metadata\'dan; kısmi kanıt kaydı (kaynak/pencere yok) çip olmaz', async () => {
    const t = await lastTurn('ui-stream-v1');
    expect(t.exchangeId).toBe('golden-xid');
    expect(t.evidence).toBeUndefined(); // fixture: {state, tool} — v1 çipi kaynak + pencere ister
    const full = { tool: 'query_metrics', source: 'Thanos', query: 'up', queryKind: 'promql', window: { fromMs: 1, toMs: 2 }, durationMs: 3 };
    expect(evidenceOf([{ type: 'data-evidence-summary', id: 'evidence', data: { evidence: [full, { tool: 'x' }, null] } }])).toEqual({ evidence: [full] });
    expect(evidenceOf([{ type: 'data-evidence-summary', id: 'evidence', data: {} }])).toEqual({});
  });
});
