// v0.10.1900 (Log Search 3.10) — servis Logs sekmesinden taşınan seviye bantları
// + kompakt kipin "Logs'ta aç" linki ve KQL terimi.
import { describe, it, expect } from 'vitest';
import { levelBandOf, levelBandOfName, parseLevelBand } from './logLevelBand';
import { compactLogsHref, kqlTerm } from '@/components/coremetry/logs/CompactLogSearch';
import type { LogRow } from './types';

const row = (severityText: string, severity: number) => ({ severityText, severity } as LogRow);

describe('logLevelBand', () => {
  it.each([
    ['ERROR', 0, 'error'], ['FATAL', 0, 'error'], ['CRITICAL', 0, 'error'], ['WARNING', 0, 'warn'],
    ['INFO', 0, 'info'], ['TRACE', 0, 'debug'], ['', 17, 'error'], ['', 13, 'warn'], ['', 9, 'info'], ['', 5, 'debug'],
  ] as const)('%s/%i → %s', (t, s, want) => {
    expect(levelBandOf(row(t, s))).toBe(want);
  });
  it('seri adı ve URL', () => {
    expect(levelBandOfName('Error')).toBe('error');
    expect(levelBandOfName('other')).toBe('debug');
    expect(parseLevelBand('warn')).toBe('warn');
    expect(parseLevelBand('bogus')).toBe('all');
    expect(parseLevelBand(null)).toBe('all');
  });
});

describe('compactLogsHref / kqlTerm', () => {
  it('servis + arama + seviye tabanı + pencere + env + dil + kolonlar', () => {
    const href = compactLogsHref({ service: 'orders-api', search: 'timeout', lvl: 'warn', rangeParam: '1h', env: 'prod', lucene: true, cols: ['message', 'pod'] });
    const p = new URLSearchParams(href.split('?')[1]);
    expect(href.startsWith('/logs?')).toBe(true);
    expect(Object.fromEntries(p)).toMatchObject({ service: 'orders-api', q: 'timeout', severity: '13', range: '1h', env: 'prod', lang: 'lucene', cols: 'message,pod' });
  });
  it('varsayılanlar paramsız', () => {
    const p = new URLSearchParams(compactLogsHref({ service: 'orders-api', search: '', lvl: 'all', rangeParam: '', lucene: false, cols: ['cluster', 'message', 'service', 'pod'] }).split('?')[1]);
    expect([...p.keys()]).toEqual(['service']);
  });
  it('KQL değeri tırnaklı + kaçışlı', () => {
    expect(kqlTerm('k8s.pod', 'a"b\\c')).toBe('k8s.pod:"a\\"b\\\\c"');
  });
});
