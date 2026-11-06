// logDetail — v0.10.1878 (Log Search Faz 3 dilim 3.1).
import { describe, it, expect } from 'vitest';
import { plainLogSentence, severityStatus, severityLabel, detailFields, technicalFields, neighborRow, firstLine } from './logDetail';
import type { LogRow } from '@/lib/types';

const row = (p: Partial<LogRow> = {}): LogRow => ({
  id: 7, timestamp: 1759494800000000000, severity: 17, severityText: 'ERROR',
  body: 'java.lang.OutOfMemoryError: Java heap space\n  at com.acme.orders.cart.CartSerializer.write(CartSerializer.java:118)',
  serviceName: 'orders-api', traceId: '9c1d07aa5b3e4f0a8d217c66f201e41f', spanId: '5f1c22e09ab47d10',
  attributes: { CHANNEL_CODE: 'MOBILE', 'kubernetes.pod_name': 'orders-api-7d9f6-x2k4p' },
  resourceAttributes: { 'host.name': 'node-a-17', 'k8s.namespace.name': 'acme-prod' },
  ...p,
});

describe('plainLogSentence', () => {
  it('kim · ne zaman · seviye · ilk satır · trace', () => {
    expect(plainLogSentence(row(), '14:35:12.482')).toBe(
      "orders-api (pod orders-api-7d9f6-x2k4p) 14:35:12.482'de ERROR seviyesinde bir kayıt yazdı: “java.lang.OutOfMemoryError: Java heap space” Kayıt bir trace'e bağlı.");
  });
  it('servis / gövde / trace yoksa uydurmaz', () => {
    expect(plainLogSentence(row({ serviceName: '', body: '', traceId: '', attributes: {}, severityText: '', severity: 9 }), '10:00:00.000'))
      .toBe("Bilinmeyen servis 10:00:00.000'de INFO seviyesinde bir kayıt yazdı.");
  });
  it('ilk satır kırpılır', () => {
    expect(firstLine('x'.repeat(400))).toHaveLength(160);
    expect(firstLine('\n\n  a  \nb')).toBe('a');
  });
});

describe('seviye', () => {
  it.each([[21, 'critical', 'FATAL'], [17, 'critical', 'ERROR'], [13, 'warn', 'WARN'], [9, 'info', 'INFO'], [5, 'unknown', 'DEBUG'], [0, 'unknown', 'OTHER']] as const)(
    '%i → %s / %s', (sev, st, lbl) => {
      expect(severityStatus(sev)).toBe(st);
      expect(severityLabel({ severity: sev, severityText: '' })).toBe(lbl);
    });
});

describe('alanlar', () => {
  it('çekirdek önce, öznitelikler sıralı; kaynak teknik kanıtta', () => {
    expect(detailFields(row()).map(f => f.key)).toEqual(['service.name', 'level', 'trace.id', 'span.id', 'CHANNEL_CODE', 'kubernetes.pod_name']);
    expect(technicalFields(row({ docRef: 'app-orders-api.acme-prod-2026.11.05/abc' }))).toEqual([
      { key: '_index', value: 'app-orders-api.acme-prod-2026.11.05' }, { key: '_id', value: 'abc' },
      { key: 'host.name', value: 'node-a-17' }, { key: 'k8s.namespace.name', value: 'acme-prod' },
    ]);
  });
  it('süzgeç anahtar ya da değerde', () => {
    expect(detailFields(row(), 'mobile').map(f => f.key)).toEqual(['CHANNEL_CODE']);
    expect(detailFields(row(), 'POD').map(f => f.key)).toEqual(['kubernetes.pod_name']);
  });
  it('komşu satır', () => {
    const rows = [row({ id: 1 }), row({ id: 2 }), row({ id: 3 })];
    expect(neighborRow(rows, 2, 1)?.id).toBe(3);
    expect(neighborRow(rows, 1, -1)).toBeNull();
    expect(neighborRow(rows, 9, 1)).toBeNull();
  });
});
