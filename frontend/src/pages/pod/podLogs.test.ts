import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { podLogSearch, levelCounts, POD_LOG_MIN_SEV } from './podLogs';
import type { LogRow } from '@/lib/types';

// v0.10.910 — pod sayfası "Loglar" bölümü: pod pili /logs "Loglar ↗" ile aynı
// alan; serbest metin eklenir; seviye sayıları sayfadaki satırlardan;
// bölüm varsayılan kapalı (yalnız açıkken sorgu).
describe('podLogs', () => {
  it('pod pili kubernetes.pod_name + serbest metin', () => {
    const q = podLogSearch('bsa-mobile-login-prod-7b99-l4bg5', '');
    expect(q).toContain('kubernetes.pod_name');
    expect(q).toContain('bsa-mobile-login-prod-7b99-l4bg5');
    const q2 = podLogSearch('p-1', ' connection refused ');
    expect(q2).toContain('kubernetes.pod_name');
    expect(q2).toContain('connection refused');
  });
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
    expect(sec).toMatch(/enabled: open && !!pod/);
    expect(sec).toMatch(/refetchOnWindowFocus: false/);
    const page = readFileSync(resolve(__dirname, '..', 'Pod.tsx'), 'utf8');
    expect(page).toMatch(/<PodLogsSection /);
  });
});
