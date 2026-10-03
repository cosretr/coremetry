// dbHealthPivots.test.ts — v0.10.1073 (operatör: "Dün akşam CRM database'inde
// sorun oldu ama problemlerde P1 gelmedi"). db-health kural id'si ↔ Go ikizi,
// Databases liste/detay biçim yüklemi, Problem detayının pivotları, ve
// detayın bu öneki gerçekten kullandığı (kaynak pini).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { DB_HEALTH_RULE_PREFIX, parseDbHealthRuleId } from '@/lib/problemSubject';
import { dbProblemForm } from '@/pages/databases/databaseProblems';
import { dbHealthPivots } from './dbHealthPivots';

const GO = (rel: string) => readFileSync(resolve(__dirname, '../../../../', rel), 'utf8');
const HOUR = 3600e9;
const T0 = 1_791_015_960 * 1e9;
const win = { fromNs: T0 - HOUR, toNs: T0 + 10 * 60e9 };

describe('parseDbHealthRuleId — Go chstore.ParseDBHealthRuleID ikizi', () => {
  it.each([
    ['db-health:oracle@db-host-01/crm-db', { system: 'oracle', instance: 'db-host-01', dbName: 'crm-db' }],
    ['db-health:postgresql@pg.example.internal/default', { system: 'postgresql', instance: 'pg.example.internal', dbName: 'default' }],
    // İLK '@', SON '/': instance '/' taşırsa instance'ta kalır.
    ['db-health:mysql@a/b/orders', { system: 'mysql', instance: 'a/b', dbName: 'orders' }],
  ])('%s', (id, want) => {
    expect(parseDbHealthRuleId(id)).toEqual(want);
  });
  it.each(['', 'db-health:', 'db-health:@h/d', 'db-health:oracle@h', 'db-health:oracle@h/', 'db-slow-stmt', 'db-capacity:oracle-sessions'])(
    '%s → null', id => { expect(parseDbHealthRuleId(id)).toBeNull(); });
  it('null / undefined güvenli', () => {
    expect(parseDbHealthRuleId(undefined)).toBeNull();
    expect(parseDbHealthRuleId(null)).toBeNull();
  });
  it('önek Go sabitiyle aynı, üretici onu kullanıyor', () => {
    expect(DB_HEALTH_RULE_PREFIX).toBe('db-health:');
    expect(GO('internal/chstore/problem.go')).toContain(`RuleDBHealthPrefix = "${DB_HEALTH_RULE_PREFIX}"`);
    expect(GO('internal/chstore/db_health.go')).toContain('return RuleDBHealthPrefix + strings.ToLower(strings.TrimSpace(system)) + "@" + instance + "/" + dbName');
  });
});

describe('dbProblemForm — db-health (Go DBProblemSubjectForm ikizi)', () => {
  it('gerçek db.name → dbName biçimi; nöbetçi → instance biçimi', () => {
    expect(dbProblemForm('db-health:oracle@db-host-01/crm-db')).toBe('dbName');
    expect(dbProblemForm('db-health:oracle@db-host-01/default')).toBe('instance');
    expect(dbProblemForm('db-capacity:oracle-tablespace')).toBe('instance');
    expect(dbProblemForm('db-slow-stmt')).toBe('dbName');
  });
});

describe('dbHealthPivots — Problem detayı pivotları', () => {
  it('başka kural → null (bugünkü "pivot yok" cümlesi sürer)', () => {
    expect(dbHealthPivots('db-capacity:oracle-sessions', win)).toBeNull();
    expect(dbHealthPivots('builtin-error-rate-15pct', win)).toBeNull();
  });
  it('Veritabanı sayfası: üçlü + span kaynağı + problem penceresi', () => {
    const p = dbHealthPivots('db-health:oracle@db-host-01/crm-db', win)!;
    const u = new URL(p.databaseHref, 'http://x');
    expect(u.pathname).toBe('/database');
    expect(u.searchParams.get('system')).toBe('oracle');
    expect(u.searchParams.get('instance')).toBe('db-host-01');
    expect(u.searchParams.get('name')).toBe('crm-db');
    expect(u.searchParams.get('source')).toBeNull();
    expect(u.searchParams.get('range')).toMatch(/^custom:\d+-\d+$/);
  });
  it("Trace'ler: db.system + db.name + instance zinciri, rootOnly kapalı; hatalı varyant hasError", () => {
    const p = dbHealthPivots('db-health:oracle@db-host-01/crm-db', win)!;
    const t = new URL(p.tracesHref, 'http://x');
    expect(t.pathname).toBe('/traces');
    expect(t.searchParams.get('rootOnly')).toBe('false');
    expect(t.searchParams.get('hasError')).toBeNull();
    const fg = t.searchParams.get('filterGroup') ?? '';
    expect(fg).toContain('db.system');
    expect(fg).toContain('crm-db');
    expect(fg).toContain('db-host-01');
    expect(new URL(p.errorTracesHref, 'http://x').searchParams.get('hasError')).toBe('true');
  });
  it("'default' db.name trace süzgecine girmez ama detay kimliğinde kalır (liste satırıyla aynı)", () => {
    const p = dbHealthPivots('db-health:oracle@db-host-01/default', win)!;
    expect(new URL(p.databaseHref, 'http://x').searchParams.get('name')).toBe('default');
    expect(new URL(p.tracesHref, 'http://x').searchParams.get('filterGroup') ?? '').not.toContain('default');
  });
  it('ProblemDetail pivotları db-health için çiziyor (kaynak pini)', () => {
    const src = readFileSync(resolve(__dirname, 'ProblemDetail.tsx'), 'utf8');
    expect(src).toContain('dbHealthPivots(problem.ruleId, probWindow)');
    expect(src).toContain('label="◫ Veritabanı sayfası"');
    expect(src).toContain(`label="⋮ Trace'ler"`);
    // v0.10.1083 — "Hata kırılımı" pivotu (ORA kodu → exception → mesaj kartı).
    expect(src).toContain('to={dbHealthLinks.errorsHref} label="⚠ Hata kırılımı"');
  });
  // v0.10.1083 — operatör: "Oracle hataları da problemse hâlâ düşmüyor". Hata
  // sayısı kolunun açtığı Problem'den Databases detayının "Errors on this
  // database" kartına (/api/databases/errors) tek tık.
  it('Hata kırılımı: Databases detayı + aynı pencere + #db-errors çapası; sayfa çapayı taşıyor', () => {
    const p = dbHealthPivots('db-health:oracle@db-host-01/crm-db', win)!;
    const u = new URL(p.errorsHref, 'http://x');
    expect(u.pathname).toBe('/database');
    expect(u.hash).toBe('#db-errors');
    expect(u.searchParams.get('name')).toBe('crm-db');
    expect(u.searchParams.get('range')).toBe(new URL(p.databaseHref, 'http://x').searchParams.get('range'));
    const page = readFileSync(resolve(__dirname, '../../pages/DatabaseDetail.tsx'), 'utf8');
    expect(page).toContain('<div id={DATABASE_ERRORS_ANCHOR} style={{ marginTop: 12 }}>\n              <DatabaseErrorsSection');
    expect(page).toContain('scrollIntoView');
  });
});
