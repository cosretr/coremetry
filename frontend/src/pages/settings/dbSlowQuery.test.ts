// dbSlowQuery.test.ts — v0.10.325 kaynak pini: Anomaly sekmesinde yavaş SQL
// bölümü, EscalationSection'dan sonra; istemci iki uç; tip alanları spec ile.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('DB yavaş sorgu ayarları', () => {
  const tab = readFileSync(resolve(__dirname, 'AnomalyTab.tsx'), 'utf8');
  const api = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
  const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
  it('bölüm monte, istemci ve tip mevcut', () => {
    expect(tab.indexOf('<DBSlowQuerySection />')).toBeGreaterThan(tab.indexOf('<EscalationSection />'));
    expect(tab).toContain('function DBSlowQuerySection()');
    expect(api).toContain('`/api/settings/db-slow-query`');
    for (const f of ['thresholdMs', 'criticalMs', 'minExecutions', 'forBuckets', 'cooldownSec']) {
      expect(types).toMatch(new RegExp(`export interface DBSlowQueryConfig \\{[^}]*${f}: number`));
    }
  });
  // v0.10.1073 — db-health vidaları aynı blobda, aynı bölümde; varsayılanlar Go ile aynı.
  it('db-health alanları yavaş ifadenin yanında; varsayılanlar Go DefaultDBHealth ile aynı', () => {
    expect(types).toMatch(/export interface DBSlowQueryConfig \{[^}]*health\?: DBHealthConfig/);
    for (const f of ['errorPct', 'p99Ms', 'p99RiseFactor', 'minCallerCalls', 'minCalls', 'minCallers', 'maxNewPerTick',
      'minErrorCount', 'errorRiseFactor', 'minCallerErrors']) { // v0.10.1083 — mutlak hata sayısı kolu
      expect(types).toMatch(new RegExp(`export interface DBHealthConfig \\{[^}]*${f}: number`));
      expect(tab).toContain(`hnum('${f}')`);
    }
    const sec = tab.slice(tab.indexOf('function DBSlowQuerySection()'));
    expect(sec.indexOf("hnum('errorPct')")).toBeGreaterThan(sec.indexOf("num('cooldownSec')"));
    const go = readFileSync(resolve(__dirname, '../../../../internal/chstore/db_slow_statement.go'), 'utf8');
    expect(go).toContain('DBHealthConfig{Enabled: &on, ErrorPct: 5, P99Ms: 2000, P99RiseFactor: 3, MinCallerCalls: 10, MinCalls: 100, MinCallers: 2, MaxNewPerTick: 20,\n\t\tMinErrorCount: 50, ErrorRiseFactor: 3, MinCallerErrors: 10}');
    expect(tab).toContain('{ enabled: true, errorPct: 5, p99Ms: 2000, p99RiseFactor: 3, minCallerCalls: 10, minCalls: 100, minCallers: 2, maxNewPerTick: 20, minErrorCount: 50, errorRiseFactor: 3, minCallerErrors: 10 }');
  });
});
