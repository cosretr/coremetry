// v0.10.784 — inboxItemHref + attentionRows (servis dikkat şeridi).
import { describe, it, expect } from 'vitest';
import { attentionRows, inboxItemHref, isSloProblem } from './inboxHref';
import type { InboxItem } from './types';

function item(over: Partial<InboxItem>): InboxItem {
  return {
    id: 'x', kind: 'problem', source: 'Alert rule', priority: 'P2', priorityReason: '',
    severity: 'warning', service: 'shop-payment', title: 't', description: '',
    startedAt: 1, lastSeen: 2, status: 'open', ...over,
  };
}

const prob = (id: string, ruleId = 'r1', priority: InboxItem['priority'] = 'P2') =>
  item({ id: `problem:${id}`, kind: 'problem', priority, problem: { id, ruleId, metric: 'error_rate', value: 9, threshold: 1 } });
const exc = (fp: string, kind: 'exception' | 'httperror' = 'exception') =>
  item({ id: `${kind}:${fp}`, kind, exception: { fingerprint: fp, type: 'T', message: 'm', occurrences: 3 } });
const anom = (id: string) =>
  item({ id: `anomaly:${id}`, kind: 'anomaly', anomaly: { id, kind: 'k', pattern: 'p', peakRatio: 2, currentRatio: 1 } });
const inc = (id: string) =>
  item({ id: `incident:${id}`, kind: 'incident', incident: { id, severity: 'sev2', status: 'open' } });

describe('inboxItemHref', () => {
  it('routes every kind to its detail surface', () => {
    expect(inboxItemHref(prob('a b'))).toBe('/problems?problem=a%20b');
    expect(inboxItemHref(exc('fp/1'))).toBe('/problems?exc=fp%2F1');
    expect(inboxItemHref(exc('fp2', 'httperror'))).toBe('/problems?exc=fp2');
    expect(inboxItemHref(anom('e1'))).toBe('/anomalies?event=e1');
    expect(inboxItemHref(inc('i1'))).toBe('/incident?id=i1');
  });
  it('returns null when the payload lacks the native id', () => {
    expect(inboxItemHref(item({ kind: 'problem' }))).toBeNull();
    expect(inboxItemHref(item({ kind: 'exception' }))).toBeNull();
  });
});

describe('isSloProblem', () => {
  it('matches the slo: rule-id prefix only on problems', () => {
    expect(isSloProblem(prob('p', 'slo:abc:critical'))).toBe(true);
    expect(isSloProblem(prob('p', 'error_rate_high'))).toBe(false);
    expect(isSloProblem(exc('f'))).toBe(false);
  });
});

describe('attentionRows', () => {
  it('drops SLO problems into the footnote count and keeps server order', () => {
    const r = attentionRows([prob('p1', 'r', 'P1'), prob('s1', 'slo:a:critical'), exc('f1'), prob('s2', 'slo:a:warning')]);
    expect(r.rows.map(x => x.id)).toEqual(['problem:p1', 'exception:f1']);
    expect(r.sloCount).toBe(2);
    expect(r.hidden).toBe(0);
  });
  it('excludes anomalies unless asked', () => {
    expect(attentionRows([anom('a'), exc('f')]).rows.map(x => x.id)).toEqual(['exception:f']);
    expect(attentionRows([anom('a'), exc('f')], { includeAnomalies: true }).rows.map(x => x.id))
      .toEqual(['anomaly:a', 'exception:f']);
  });
  it('caps and reports the remainder', () => {
    const many = Array.from({ length: 8 }, (_, i) => exc(`f${i}`));
    const r = attentionRows(many);
    expect(r.rows).toHaveLength(5);
    expect(r.hidden).toBe(3);
  });
  it('skips rows without a destination', () => {
    expect(attentionRows([item({ kind: 'problem' })]).rows).toHaveLength(0);
  });
});
