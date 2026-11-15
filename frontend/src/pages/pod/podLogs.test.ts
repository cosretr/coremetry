import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { levelCounts, POD_LOG_MIN_SEV } from './podLogs';
import type { LogRow } from '@/lib/types';

// v0.10.910 — pod sayfası "Loglar" bölümü: seviye sayıları sayfadaki
// satırlardan; bölüm varsayılan kapalı (yalnız açıkken sorgu).
// v0.10.2233 (Entity S3): pod kapsamı entity=pod:… (backend alan süzgeci,
// iki backend), gövde araması değil.
describe('podLogs', () => {
  it('seviye sayıları: metin önce, yoksa OTel numarası', () => {
    const row = (severityText: string, severity: number) => ({ severityText, severity }) as LogRow;
    expect(levelCounts([row('ERROR', 0), row('FATAL', 0), row('', 17), row('WARN', 0), row('', 13), row('INFO', 9), row('', 5)]))
      .toEqual({ error: 3, warn: 2 });
  });
  it('seviye tabanları OTel', () => {
    expect(POD_LOG_MIN_SEV).toEqual({ all: undefined, error: 17, warn: 13 });
  });
  it('bölüm yalnız açıkken sorgular (ES disiplini) ve pod sayfasına bağlı', () => {
    const sec = readFileSync(resolve(__dirname, 'PodLogsSection.tsx'), 'utf8');
    expect(sec).toMatch(/enabled: open && !!entityId/);
    expect(sec).toMatch(/entity: entityId/);
    expect(sec).not.toMatch(/kubernetes\.pod_name/);
    expect(sec).toMatch(/refetchOnWindowFocus: false/);
    const page = readFileSync(resolve(__dirname, '..', 'Pod.tsx'), 'utf8');
    expect(page).toMatch(/<PodLogsSection /);
  });
});
