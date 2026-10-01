// v0.10.230 (Influx D5) — externalEvidence saf çekirdeği.
import { describe, it, expect } from 'vitest';
import { traceRows, evidenceCounts, pickSeries, labelKeys, labelValues, toChart, sevTone, endpointRows } from './externalEvidence';
import type { DeepEvidence } from '@/lib/types';

const deep: DeepEvidence = {
  external: {
    source: 'extsrc', query: 'fail_count', labels: { operation_code: 'OP1', ERR_CODE: 'E1' },
    current: 60, median: 5, mad: 1, z: 37, windowFromNs: 0, windowToNs: 1, rows: 4, invalidIds: 1, updatedNs: 1,
    spanSummary: [
      { traceId: 'b', startNs: 20, durationNs: 5, spans: 2, errorSpans: 1, rootService: 'w' },
      { traceId: 'c', startNs: 10, durationNs: 1, spans: 1, errorSpans: 0 },
    ],
  },
  traceIds: ['b', 'a'],
  affectedPods: [{ pod: 'p', count: 2, lastSeenNs: 1 }],
  logSignatures: [{ hash: 'h', template: 't', count: 3, severity: 'ERROR', sample: 's', traceCount: 2 }],
};

describe('traceRows', () => {
  it('liste sırasını korur, özeti olmayan id missing kalır, kırpılmış özet de gelir', () => {
    const rows = traceRows(deep);
    expect(rows.map(r => r.traceId)).toEqual(['b', 'a', 'c']);
    expect(rows[0].missing).toBeUndefined();
    expect(rows[0].rootService).toBe('w');
    expect(rows[1].missing).toBe(true);
  });
  it('kanıt yoksa boş', () => {
    expect(traceRows(undefined)).toEqual([]);
    expect(traceRows({})).toEqual([]);
  });
});

describe('evidenceCounts', () => {
  it('sayımları ve geçersiz id dürüstlüğünü taşır', () => {
    expect(evidenceCounts(deep)).toEqual({ traces: 2, withSpans: 2, pods: 1, signatures: 1, rows: 4, errors: 0, invalid: 1 });
    expect(evidenceCounts(undefined).traces).toBe(0);
    // v0.10.904 — ön-toplanmış Oracle satırları: hata sayısı (Adet) satırdan büyük.
    const agg = { ...deep, external: { ...deep.external!, rows: 3, errors: 9 } };
    expect(evidenceCounts(agg).errors).toBe(9);
  });
});

describe('pickSeries / labels / toChart', () => {
  it('groupKey sıralı birebir eşleşen seriyi seçer, yoksa null', () => {
    const series = [
      { groupKey: ['OP1', 'E2'], points: [] },
      { groupKey: ['OP1', 'E1'], points: [{ time: 2e9, value: 3 }, { time: 1e9, value: 1 }] },
    ];
    expect(pickSeries(series, ['OP1', 'E1'])?.points.length).toBe(2);
    expect(pickSeries(series, ['OP1'])).toBeNull();
    expect(pickSeries(null, ['OP1', 'E1'])).toBeNull();
    // filtreli (groupBy'sız) sorgu: tek seri, groupKey null/boş
    const single = [{ groupKey: null as unknown as string[], points: [{ time: 1e9, value: 2 }] }];
    expect(pickSeries(single, [])?.points.length).toBe(1);
  });
  it('etiket anahtarları alfabetik, değerler aynı sırada', () => {
    expect(labelKeys(deep.external!.labels)).toEqual(['ERR_CODE', 'operation_code']);
    expect(labelValues(deep.external!.labels)).toEqual(['E1', 'OP1']);
  });
  it('toChart ns→s ve zaman sırası; boş seri null', () => {
    const c = toChart({ groupKey: [], points: [{ time: 2e9, value: 3 }, { time: 1e9, value: 1 }] });
    expect(c).toEqual({ times: [1, 2], data: [1, 3] });
    expect(toChart({ groupKey: [], points: [] })).toBeNull();
    expect(toChart(null)).toBeNull();
  });
  it('sevTone', () => {
    expect(sevTone('ERROR')).toBe('b-err');
    expect(sevTone('warn')).toBe('b-warn');
    expect(sevTone('INFO')).toBe('b-gray');
  });
});

// v0.10.1004 — ilgili endpoint'ler: Oracle Problem'inin kanıt trace'lerinden
// çıkan endpoint'ler /endpoint sayfasına OLAY penceresiyle bağlanır; RPC
// endpoint'i entry=rpc taşır (sayfa satırı aynı popülasyonda arar).
describe('endpointRows (v0.10.1004)', () => {
  const ext = {
    source: 'oracle-prod', query: 'q', current: 1, median: 0, mad: 0, z: 0, rows: 3, updatedNs: 1,
    windowFromNs: 1_790_000_000_000_000_000, windowToNs: 1_790_000_600_000_000_000,
    endpoints: [
      { service: 'eft-svc', path: '/api/eft/confirm', traces: 4, errorTraces: 3 },
      { service: 'eft-svc', path: 'EftService/Status', rpc: true, traces: 1, errorTraces: 0 },
    ],
  };
  it('her endpoint için /endpoint linki: servis + yol + olay penceresi (30 dk önce, 10 dk sonra)', () => {
    const rows = endpointRows({ external: ext } as never);
    expect(rows).toHaveLength(2);
    const q = new URLSearchParams(rows[0].href.slice(rows[0].href.indexOf('?') + 1));
    expect(rows[0].href.startsWith('/endpoint?')).toBe(true);
    expect(q.get('service')).toBe('eft-svc');
    expect(q.get('path')).toBe('/api/eft/confirm');
    expect(q.get('entry')).toBeNull();
    expect(q.get('range')).toBe(`custom:${1_790_000_000_000 - 30 * 60_000}-${1_790_000_600_000 + 10 * 60_000}`);
    const rpc = new URLSearchParams(rows[1].href.slice(rows[1].href.indexOf('?') + 1));
    expect(rpc.get('entry')).toBe('rpc');
    expect(rows[1].traces).toBe(1);
  });
  it('endpoint kanıtı yoksa boş; pencere bozuksa link pencere taşımaz', () => {
    expect(endpointRows(undefined)).toEqual([]);
    expect(endpointRows({ external: { ...ext, endpoints: [] } } as never)).toEqual([]);
    const noWin = endpointRows({ external: { ...ext, windowFromNs: 0, windowToNs: 0 } } as never);
    expect(new URLSearchParams(noWin[0].href.split('?')[1]).get('range')).toBeNull();
  });
});
