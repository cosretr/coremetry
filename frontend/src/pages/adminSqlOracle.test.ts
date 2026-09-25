// adminSqlOracle.test.ts — v0.10.742 (operatör: "Oracle database için de SQL
// console olsa güzel olur"). Üçüncü backend: seçici düğmesi, kaynak seçici
// (Settings → Oracle listesi, tarayıcıda hatırlanır), Oracle örnekleri, run
// yolu, dürüst alt yazı; istemci ucu /api/admin/sql/oracle.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const page = readFileSync(resolve(__dirname, 'AdminSql.tsx'), 'utf8');
const api = readFileSync(resolve(__dirname, '../lib/api.ts'), 'utf8');
const storage = readFileSync(resolve(__dirname, '../lib/storage.ts'), 'utf8');

describe('Admin SQL — Oracle backend (v0.10.742)', () => {
  it('üç backend; Oracle düğmesi ve kaynak seçici', () => {
    expect(page).toContain("type Backend = 'clickhouse' | 'elasticsearch' | 'oracle';");
    expect(page).toContain('value={backend} onChange={switchBackend}'); // v0.10.915 SegmentedControl
    expect(page).toContain("{ value: 'oracle', label: 'Oracle'");
    expect(page).toContain('aria-label="Oracle kaynağı"');
    expect(page).toContain("enabled: isAdmin && backend === 'oracle'"); // yalnız sekme açıkken çekilir
  });
  it('run yolu kaynak ister ve Oracle ucuna gider; örnekler ve alt yazı backend\'e göre', () => {
    expect(page).toContain("if (backend === 'oracle' && !oracleSource) {");
    expect(page).toContain('await api.oracleSqlQuery(oracleSource, q)');
    expect(page).toContain("backend === 'oracle' ? ORACLE_SAMPLES : SAMPLES");
    expect(page).toContain("read-only (SELECT/WITH, alt sorguya sarılı) · kaynağın zaman aşımı · 10k row cap");
  });
  it('istemci ucu + kalıcı seçim anahtarı; örnekler kurum adı taşımaz', () => {
    expect(api).toContain("`/api/admin/sql/oracle`");
    expect(api).toContain('body: JSON.stringify({ sourceId, query })');
    expect(storage).toContain("sqlOracleSource:  'coremetry-sql-oracle-source'");
    const samples = page.slice(page.indexOf('const ORACLE_SAMPLES'), page.indexOf('];', page.indexOf('const ORACLE_SAMPLES')));
    expect(samples).toContain('<OWNER>');
    // Kurum adı muhafızı repo-genelinde (internal/api/no_customer_identifiers_test.go);
    // burada olumsuz bir desen yazmak muhafızı kendi kendine ısırtır (v0.10.744).
    expect(samples).toContain('<TABLE>');
  });
});
