// Log Search Faz 6 dilim 6.7 — servis "Log özeti": istek yalnız tıkla (ES yük
// kuralı), arama / seviye gitmez (özet servisin tüm loglarıdır), servis Logs
// sekmesine bağlı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'ServiceLogSummary.tsx'), 'utf8');
const compact = readFileSync(resolve(__dirname, 'CompactLogSearch.tsx'), 'utf8');

describe('ServiceLogSummary', () => {
  it('istek yalnız run() içinde', () => {
    expect(src).not.toContain('useQuery');
    expect(src.match(/api\.explainServiceLogs\(/g)).toHaveLength(1);
    expect(src).toMatch(/const run = \(\) => \{[\s\S]*api\.explainServiceLogs\(/);
  });
  it('yalnız servis + env + pencere gider', () => {
    expect(src).toContain('api.explainServiceLogs({ service: p.service, env: p.env || undefined, from: p.from, to: p.to }');
  });
  it('servis Logs sekmesinde', () => {
    expect(compact).toContain('<ServiceLogSummaryButton ctl={summary} />');
    expect(compact).toContain('<ServiceLogSummaryResult ctl={summary}');
  });
});
