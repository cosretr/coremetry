// v0.10.1742 (operatör onayı) — "Bu trace'i açıkla" Kanıt satırı log okumasının
// sonucunu söyler: N log satırı · log yok · log okunamadı (neden). Okunamama
// "log yok" diye yutulmaz; künye sohbet turundan (explainEvidence) gelir.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { ExplainBody } from './ExplainBody';
import { explainEvidence } from './explainTurn';
import { traceLogEvidence } from '@/lib/explainLogEvidence';
import type { ChatExplainMeta } from '@/lib/types';

describe('traceLogEvidence', () => {
  it.each([
    [{ state: 'rows', rows: 12 } as const, '12 log satırı'],
    [{ state: 'rows', rows: 0 } as const, ''],
    [{ state: 'none' } as const, 'log yok'],
    [{ state: 'failed', reason: 'timeout' } as const, 'log okunamadı (zaman aşımı)'],
    [{ state: 'failed', reason: 'error' } as const, 'log okunamadı (hata)'],
    [{ state: 'failed', reason: 'nostore' } as const, 'log okunamadı (log deposu yok)'],
    [{ state: 'failed' } as const, 'log okunamadı (hata)'],
    [undefined, ''],
  ])('%j → %s', (o, want) => {
    expect(traceLogEvidence(o)).toBe(want);
  });
});

describe('Kanıt satırı', () => {
  const body = (logs?: ChatExplainMeta['logs'], spans = 3, oracle = 1) =>
    renderToStaticMarkup(<ExplainBody text="**Kök Neden**\n- a." busy={false} evidence={{ spans, traces: 0, oracle, ...(logs ? { logs } : {}) }} />);

  it('üç hâl span / Oracle parçasından sonra', () => {
    expect(body({ state: 'rows', rows: 7 })).toContain('Kanıt: 3 span · 1 Oracle satırı · 7 log satırı');
    expect(body({ state: 'none' })).toContain('Kanıt: 3 span · 1 Oracle satırı · log yok');
    expect(body({ state: 'failed', reason: 'timeout' })).toContain('Kanıt: 3 span · 1 Oracle satırı · log okunamadı (zaman aşımı)');
  });
  it('künye yoksa satır bugünkü gibi', () => {
    expect(body()).toContain('Kanıt: 3 span · 1 Oracle satırı');
    expect(body()).not.toContain('log');
  });
  it('yalnız log sonucu da satırı çizer', () => {
    expect(body({ state: 'failed', reason: 'nostore' }, 0, 0)).toContain('Kanıt: log okunamadı (log deposu yok)');
  });
  it('sohbet künyesinden geçer', () => {
    const meta = { kind: 'trace', id: 'a'.repeat(32), oracleRows: 1, evidenceSpanIds: ['b'.repeat(16)], logs: { state: 'none' } } as ChatExplainMeta;
    expect(explainEvidence(meta)?.logs).toEqual({ state: 'none' });
    expect(explainEvidence({ kind: 'trace', id: 'a'.repeat(32) } as ChatExplainMeta)).not.toHaveProperty('logs');
  });
});
