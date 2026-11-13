// inboxRowText.test.ts — v0.10.1084 (operatör: "tekilleştir, Exceptions'taki
// format güzel"): Problems satırının başlık / etiket / Occurrences metni.
import { describe, it, expect } from 'vitest';
import type { InboxItem } from './types';
import { rowSentence, inboxRowHeadline, inboxRowLabel, inboxOccurrencesLabel, inboxKindLabel, inboxCountSentence, openForTR } from './inboxRowText';

const base = {
  source: 's', priority: 'P1' as const, priorityReason: 'r', severity: 'critical', service: 'svc',
  startedAt: 1, lastSeen: 2, status: 'open',
};
const SENT = 'couchbase orders (cb-node-1) veritabanında hata oranı %100.0 (eşik %5), 3 çağıran servis etkilendi';

describe('inboxRowText', () => {
  it('rowSentence: yaşam döngüsü eki kırpılır', () => {
    expect(rowSentence(`${SENT} · auto-resolved: source silent`)).toBe(SENT);
    expect(rowSentence(`${SENT} · resolved: system excluded (db-health)`)).toBe(SENT);
    expect(rowSentence(undefined)).toBe('');
  });

  it('incident: başlık birincil problemin cümlesi, etiket incident adı, Occurrences "2 problem"', () => {
    const inc: InboxItem = { ...base, id: 'incident:i1', kind: 'incident', title: 'db:couchbase@orders — DB health · couchbase',
      description: SENT, incident: { id: 'i1', severity: 'critical', status: 'open', problemCount: 2 } };
    expect(inboxRowHeadline(inc)).toBe(SENT);
    expect(inboxRowLabel(inc)).toBe('db:couchbase@orders — DB health · couchbase');
    expect(inboxOccurrencesLabel(inc)).toBe('2 problem');
    // Bağlı problem bilinmiyor → "—"; özet yok → başlık adın kendisi, etiket tekrar etmez.
    const bare: InboxItem = { ...inc, description: '', incident: { id: 'i1', severity: 'critical', status: 'open' } };
    expect(inboxOccurrencesLabel(bare)).toBeNull();
    expect(inboxRowHeadline(bare)).toBe(bare.title);
    expect(inboxRowLabel(bare)).toBeNull();
  });

  it('problem: cümle başlık, kural adı etiket; exception: tip + oluşum; anomali: başlık', () => {
    const p: InboxItem = { ...base, id: 'problem:p1', kind: 'problem', title: 'DB health · couchbase', description: SENT,
      problem: { id: 'p1', ruleId: 'db-health:couchbase@cb-node-1/orders', metric: 'db.error_pct', value: 100, threshold: 5 } };
    expect(inboxRowHeadline(p)).toBe(SENT);
    expect(inboxRowLabel(p)).toBe('DB health · couchbase');
    expect(inboxOccurrencesLabel(p)).toBeNull();
    const e: InboxItem = { ...base, id: 'exception:f', kind: 'exception', title: 'java.net.SocketTimeoutException', description: '',
      exception: { fingerprint: 'f', type: 'java.net.SocketTimeoutException', message: 'Read timed out', occurrences: 1234 } };
    expect(inboxRowHeadline(e)).toBe('java.net.SocketTimeoutException');
    expect(inboxRowHeadline({ ...e, title: '' })).toBe('java.net.SocketTimeoutException'); // tip yedek
    expect(inboxRowLabel(e)).toBeNull();
    expect(inboxOccurrencesLabel(e)).toBe((1234).toLocaleString());
    const a: InboxItem = { ...base, id: 'anomaly:a', kind: 'anomaly', title: 'trace_op · POST /pay', description: 'x',
      anomaly: { id: 'a', kind: 'trace_op', pattern: 'POST /pay', peakRatio: 4, currentRatio: 2 } };
    expect(inboxRowHeadline(a)).toBe('trace_op · POST /pay');
    expect(inboxOccurrencesLabel(a)).toBeNull();
  });
});

describe('inboxKindLabel (v0.10.2121, Tür kolonu)', () => {
  it.each([
    ['problem', 'Alarm'], ['exception', 'Exception'], ['httperror', 'HTTP hatası'],
    ['anomaly', 'Anomali'], ['incident', 'Incident'], ['yeni', 'Kaynak X'],
  ])('%s → %s', (kind, want) => {
    expect(inboxKindLabel({ kind, source: 'Kaynak X' } as Pick<InboxItem, 'kind' | 'source'>)).toBe(want);
  });
});

describe('inboxCountSentence (v0.10.2122, S2 üst sayım cümlesi)', () => {
  const now = 1_800_000_000e9;
  const DAY = 86_400e9;
  it('sayılar + en eski', () => {
    expect(inboxCountSentence({ P1: 3, P2: 10, P3: 24, exception: 30 }, now - 4 * DAY - 3600e9, now))
      .toBe('37 açık problem: 3 P1, 10 P2, 24 P3. En eskisi 4 gündür açık.');
  });
  it('en eski bilinmiyorsa yalnız sayılar', () => {
    expect(inboxCountSentence({ P1: 1 }, 0, now)).toBe('1 açık problem: 1 P1, 0 P2, 0 P3.');
    expect(inboxCountSentence({ P1: 1 }, undefined, now)).toBe('1 açık problem: 1 P1, 0 P2, 0 P3.');
  });
  it('boş küme ve sayısız yanıt', () => {
    expect(inboxCountSentence({ P1: 0, P2: 0, P3: 0 }, now - DAY, now)).toBe('Açık problem yok.');
    expect(inboxCountSentence(undefined, now - DAY, now)).toBeNull();
  });
  it('binlik ayıraç', () => {
    expect(inboxCountSentence({ P1: 1000, P2: 234, P3: 0 }, 0, now)).toBe('1.234 açık problem: 1.000 P1, 234 P2, 0 P3.');
  });
  it.each([
    [30e9, '1 dakikadır'], [59 * 60e9, '59 dakikadır'], [60 * 60e9, '1 saattir'],
    [23 * 3600e9 + 59 * 60e9, '23 saattir'], [DAY, '1 gündür'], [10 * DAY + 5, '10 gündür'],
  ])('openForTR(%d) → %s', (ns, want) => {
    expect(openForTR(ns)).toBe(want);
  });
});
