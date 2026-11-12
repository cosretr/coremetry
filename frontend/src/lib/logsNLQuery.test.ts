// Log Search Faz 6 dilim 6.6 — doğal dilden sorgu: öneri yalnız taslağa gider,
// sorgu kullanıcı Search'e basmadan çalışmaz.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { applyNLToDraft, nlSeverityLabel } from './logsNLQuery';

describe('nlSeverityLabel', () => {
  it.each([[0, ''], [5, 'DEBUG'], [9, 'INFO'], [13, 'WARN'], [17, 'ERROR'], [21, 'FATAL']])('%i → %s', (n, want) => {
    expect(nlSeverityLabel(n)).toBe(want);
  });
});

describe('applyNLToDraft', () => {
  const draft = { service: 'a', search: 'old', severity: 9 };
  it('arama değişir, seviye önerisi varsa uygulanır', () => {
    expect(applyNLToDraft(draft, 'level:error', 17)).toEqual({ service: 'a', search: 'level:error', severity: 17 });
  });
  it('seviye önerisi yoksa taslağınki kalır', () => {
    expect(applyNLToDraft(draft, 'timeout', 0).severity).toBe(9);
  });
});

describe('Sorgu çalıştırılmaz (kabul: Ara’ya basmadan çalışmaz)', () => {
  const page = readFileSync(resolve(__dirname, '../pages/Logs.tsx'), 'utf8');
  const row = readFileSync(resolve(__dirname, '../pages/logs/LogsNLQuery.tsx'), 'utf8');
  it('öneri yalnız setDraft ile taslağa konur; apply / setFilter çağrılmaz', () => {
    const m = page.match(/onUse=\{\(q, sev\) => ([^}]*)\}/);
    expect(m?.[1]).toContain('setDraft(');
    expect(m?.[1]).not.toMatch(/apply\(|setFilter\(|writeUrl\(/);
  });
  it('istek yalnız run() içinde; uç sorgu çalıştırmaz', () => {
    expect(row).not.toContain('useQuery');
    expect(row.match(/api\.logsNLQuery\(/g)).toHaveLength(1);
  });
});
