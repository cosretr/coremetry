// Log Search Faz 6 dilim 6.3 — "Bu logları açıkla" satır seçimi + ES yük
// kuralı (2026-11-06): istek yalnız tıkla, sayfa açılışında değil.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { EXPLAIN_LOGS_MAX_ROWS, explainRowsOf } from './logsExplain';
import type { LogRow } from './types';

const row = (id: number, over: Partial<LogRow> = {}): LogRow => ({
  id, timestamp: 1762941971204000000 + id, severity: 17, severityText: '', body: `b${id}`, serviceName: 'svc',
  traceId: '', spanId: '', attributes: {}, resourceAttributes: {}, ...over,
});

describe('explainRowsOf', () => {
  it('span-event / Oracle / boş gövde düşer, tekrar tek kez', () => {
    const r1 = row(1, { traceId: '0af7651916cd43dd8448eb211c80319c' });
    const out = explainRowsOf([r1, r1, row(2, { origin: 'span-event' }), row(3, { origin: 'oracle' }), row(4, { body: '' })]);
    expect(out).toEqual([{ tsNs: String(r1.timestamp), service: 'svc', severity: 'ERROR', body: 'b1', traceId: '0af7651916cd43dd8448eb211c80319c' }]);
  });
  it(`en çok ${EXPLAIN_LOGS_MAX_ROWS} satır`, () => {
    expect(explainRowsOf(Array.from({ length: 30 }, (_, i) => row(i)))).toHaveLength(EXPLAIN_LOGS_MAX_ROWS);
  });
});

describe('LogsExplain ES yük kuralı', () => {
  const src = readFileSync(resolve(__dirname, '../pages/logs/LogsExplain.tsx'), 'utf8');
  it('istek yalnız run() içinde (useQuery / mount effect yok)', () => {
    expect(src).not.toContain('useQuery');
    expect(src.match(/api\.explainLogs\(/g)).toHaveLength(1);
    expect(src).toMatch(/const run = \(\) => \{[\s\S]*api\.explainLogs\(/);
  });
});
