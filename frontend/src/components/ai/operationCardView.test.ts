import { describe, it, expect } from 'vitest';
import {
  deltaLabel, errRate, fmtMs, opAlternativesLine, opEndpointExceptionRows, opEndpointExceptionsNote, opEndpointHref, opEndpointLabel,
  opExceptionRows, opExceptionsNote, worse,
} from './operationCardView';
import type { ChatOperationException, ChatOperationPayload } from '@/lib/types'; // v0.10.1331
import { operationBlocks } from '@/lib/chatBlocks';

describe('operasyon kartı (CoSRE operasyon tespiti Faz 3)', () => {
  it('fark etiketi', () => {
    expect(deltaLabel(140, 100)).toBe('+%40');
    expect(deltaLabel(88, 100)).toBe('−%12');
    expect(deltaLabel(5, 0)).toBe('yeni');
    expect(deltaLabel(0, 0)).toBe('');
  });
  it('yalnız sapan değer renklenir', () => {
    expect(worse('err', 0.05, 0.01)).toBe(true);
    expect(worse('err', 0.015, 0.01)).toBe(false);
    expect(worse('p99', 900, 600)).toBe(true);
    expect(worse('p99', 700, 600)).toBe(false);
  });
  it('oran ve süre', () => {
    expect(errRate({ calls: 200, errors: 20, p99Ms: 0, measured: true })).toBe(0.1);
    expect(errRate({ calls: 0, errors: 0, p99Ms: 0, measured: false })).toBe(0);
    expect(fmtMs(1500)).toBe('1.50 s');
    expect(fmtMs(42.4)).toBe('42 ms');
  });
  it('geçersiz blok yükü düşer', () => {
    const ok = { operation: 'X', tracesHref: '/traces', current: { calls: 1, errors: 0, p99Ms: 1, measured: true } };
    expect(operationBlocks([
      { id: 'a', type: 'operation', seq: 1, final: true, payload: ok },
      { id: 'b', type: 'operation', seq: 2, final: true, payload: { operation: 'Y' } },
      { id: 'c', type: 'chart', seq: 3, final: true, payload: ok },
    ])).toHaveLength(1);
  });
});

// v0.10.1331 — "Teknik hatalar": operasyonun Exceptions grupları.

describe('operasyon kartı — Exceptions grupları (v0.10.1331)', () => {
  const now = Date.UTC(2026, 9, 18, 10, 0, 0);
  const grp = (o: Partial<ChatOperationException>): ChatOperationException => ({
    fingerprint: 'ora:0123456789abcdef', type: 'COR-30518', message: 'OP_X', state: 'new', oracle: true, code: 'COR-30518', operation: 'OP_X',
    hourKnown: true, lastHour: 900, prevHour: 200, occurrences: 12345, firstSeen: 0, lastSeen: (now - 4 * 60_000) * 1e6,
    href: '/problems?exc=ora%3A0123456789abcdef', explainHref: '/problems?exc=ora%3A0123456789abcdef&ai=exception', ...o,
  });
  const op = (ex: ChatOperationPayload['exceptions']): ChatOperationPayload => ({
    operation: 'OP_X', matchKind: 'exact', rangeS: 3600, tracesHref: '/traces',
    current: { calls: 1, errors: 0, p99Ms: 1, measured: true }, previous: { calls: 1, errors: 0, p99Ms: 1, measured: true },
    traces: null, oracleErrors: null, exceptions: ex,
  });
  it('Oracle grubu: kod, saatlik sayı, patlama, son görülme, güvenli link', () => {
    const [r] = opExceptionRows(op({ groups: [grp({ priority: 'P1' })], lookbackS: 86400 }), now);
    expect(r.code).toBe('COR-30518');
    expect(r.count).toBe('son 1 sa 900 · önce 200');
    expect(r.burst).toBe(true);
    expect(r.lastSeen).toBe('4 dk önce');
    expect(r.priority).toBe('P1');
    expect(r.href).toBe('/problems?exc=ora%3A0123456789abcdef');
  });
  it('span grubu toplam sayıyla; güvensiz href düşer', () => {
    const [r] = opExceptionRows(op({ groups: [grp({ oracle: false, code: undefined, type: 'java.io.IOException', href: '//evil.com/x', explainHref: 'javascript:alert(1)' })], lookbackS: 86400 }), now);
    expect(r.code).toBe('java.io.IOException');
    expect(r.count).toBe('toplam 12.3K');
    expect(r.burst).toBe(false);
    expect(r.href).toBeNull();
    expect(r.explainHref).toBeNull();
  });
  it('okunamadı ≠ grup yok; fazlası notta; bakılmadıysa bölüm yok', () => {
    expect(opExceptionsNote(op({ groups: null, error: 'timeout', lookbackS: 86400 }))).toBe('Exceptions okunamadı');
    expect(opExceptionsNote(op({ groups: [], lookbackS: 86400 }))).toMatch(/grup yok/);
    expect(opExceptionsNote(op({ groups: [grp({})], more: 15, moreAtMin: true, lookbackS: 86400 }))).toBe('+en az 15 grup daha');
    expect(opExceptionsNote(op({ groups: [grp({})], lookbackS: 86400 }))).toBeNull();
    expect(opExceptionsNote(op(undefined))).toBeNull();
    expect(opExceptionRows(op(undefined))).toEqual([]);
  });
});

// v0.10.1343 (operator-reported: "POST /ACMWEB/application/expressOrderPreValidate
// neden yavaş?") — endpoint hedefi: başlık, /endpoint linki, endpoint exception
// satırları (güvensiz href düşer), ≥3× kuralının alternatif satırı.
describe('operasyon kartı — endpoint hedefi (v0.10.1343)', () => {
  const now = Date.UTC(2026, 9, 18, 10, 0, 0);
  const base: ChatOperationPayload = {
    operation: 'POST /ACMWEB/application/expressOrderPreValidate', matchKind: 'endpoint_exact', rangeS: 3600, tracesHref: '/traces',
    services: ['acmweb'], current: { calls: 1, errors: 0, p99Ms: 1, measured: true }, previous: { calls: 1, errors: 0, p99Ms: 1, measured: true },
    traces: null, oracleErrors: null,
  };
  it('başlık "FİİL rota · servis"; endpoint dışı operasyonda null', () => {
    expect(opEndpointLabel({ ...base, route: '/ACMWEB/application/expressOrderPreValidate', method: 'POST' }))
      .toBe('POST /ACMWEB/application/expressOrderPreValidate · acmweb');
    expect(opEndpointLabel({ ...base, method: 'POST' })).toBe('POST /ACMWEB/application/expressOrderPreValidate · acmweb');
    expect(opEndpointLabel({ ...base, operation: 'GET /orders', route: '/orders', services: [] })).toBe('/orders');
    expect(opEndpointLabel(base)).toBeNull();
  });
  it('endpoint sayfası linki yalnız güvenli yolsa', () => {
    expect(opEndpointHref({ ...base, endpointHref: '/endpoint?path=%2Fx&service=acmweb' })).toBe('/endpoint?path=%2Fx&service=acmweb');
    expect(opEndpointHref({ ...base, endpointHref: '//evil.com/endpoint' })).toBeNull();
    expect(opEndpointHref({ ...base, endpointHref: 'javascript:alert(1)' })).toBeNull();
    expect(opEndpointHref(base)).toBeNull();
  });
  it('endpoint exception satırları: tip, toplam, son görülme, güvensiz href düşer, ≤5', () => {
    const ex = (o: Partial<NonNullable<ChatOperationPayload['endpointExceptions']>[number]>) => ({
      type: 'java.net.SocketTimeoutException', message: 'Read timed out', count: 1234, lastSeenNs: (now - 4 * 60_000) * 1e6, href: '/problems?exc=f1', ...o,
    });
    const rows = opEndpointExceptionRows({ ...base, endpointExceptions: [ex({}), ex({ type: 'X', href: '//evil.com/x' }), ex({}), ex({}), ex({}), ex({})], endpointExceptionsChecked: true }, now);
    expect(rows).toHaveLength(5);
    expect(rows[0]).toMatchObject({ code: 'java.net.SocketTimeoutException', count: 'toplam 1.2K', lastSeen: '4 dk önce', href: '/problems?exc=f1', explainHref: null, title: 'Read timed out', burst: false });
    expect(rows[1].href).toBeNull();
    expect(opEndpointExceptionRows(base)).toEqual([]);
  });
  it('okunamadı ≠ yok ≠ bakılmadı', () => {
    expect(opEndpointExceptionsNote({ ...base, endpointExceptionsError: 'timeout', endpointExceptionsChecked: true })).toMatch(/okunamadı/);
    expect(opEndpointExceptionsNote({ ...base, endpointExceptionsChecked: true })).toMatch(/exception yok/);
    expect(opEndpointExceptionsNote(base)).toBeNull();
    expect(opEndpointExceptionsNote({ ...base, endpointExceptionsChecked: true, endpointExceptions: [{ type: 'X', count: 1, lastSeenNs: 0, href: '' }] })).toBeNull();
  });
  it('alternatif servis satırı', () => {
    expect(opAlternativesLine({ ...base, alternatives: [{ service: 'acmweb-gw', op: '/x', calls: 100 }] })).toBe('Ayrıca şu servislerde de var: acmweb-gw (100 çağrı)');
    expect(opAlternativesLine(base)).toBeNull();
  });
});
