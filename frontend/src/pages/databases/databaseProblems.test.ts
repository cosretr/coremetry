import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { dbSubjectId, dbProblemSubjects, problemOverlapsWindow, dbProblemsForWindow, problemDurationText } from './databaseProblems';
import type { Problem } from '@/lib/types';

// v0.10.1019 — /database sayfası kendi problemlerini gösterir (operatör:
// Databases için Dynatrace baz; "o database ile ilgili veriler gelebilir").

const S = 1e9;
const prob = (id: string, status: string, startedAt: number, resolvedAt?: number): Problem =>
  ({ id, ruleId: 'r', ruleName: id, severity: 'warning', service: 'db:oracle@x', status, description: '', startedAt, resolvedAt }) as unknown as Problem;

describe('özne kimliği', () => {
  it('chstore.DBSubjectID ile aynı biçim: system küçük harf, kırpılmış; eksikse boş', () => {
    expect(dbSubjectId('Oracle', ' core-db ')).toBe('db:oracle@core-db');
    expect(dbSubjectId('', 'core-db')).toBe('');
    expect(dbSubjectId('oracle', '  ')).toBe('');
    const go = readFileSync(resolve(__dirname, '../../../../internal/chstore/identity.go'), 'utf8');
    expect(go).toContain('system = strings.ToLower(strings.TrimSpace(system))');
    expect(go).toContain('return dbNodePrefix() + system + "@" + instance');
  });
  it('sayfa kimliği iki özne üretir (instance: kapasite · dbName: yavaş ifade / hedefli kural); tekrarsız', () => {
    expect(dbProblemSubjects({ system: 'oracle', instance: 'core-db', dbName: 'CORE' })).toEqual(['db:oracle@core-db', 'db:oracle@CORE']);
    expect(dbProblemSubjects({ system: 'oracle', instance: 'core-db' })).toEqual(['db:oracle@core-db']);
    expect(dbProblemSubjects({ system: 'oracle', instance: 'CORE', dbName: 'CORE' })).toEqual(['db:oracle@CORE']);
    const cap = readFileSync(resolve(__dirname, '../../../../internal/evaluator/db_capacity.go'), 'utf8');
    expect(cap).toContain('chstore.DBSubjectID(dbsys, instance)');
    const slow = readFileSync(resolve(__dirname, '../../../../internal/evaluator/db_slow_statement.go'), 'utf8');
    expect(slow).toContain('chstore.DBSubjectID(k.DBSystem, k.DBName)');
  });
});

describe('pencere + sıralama', () => {
  const from = 1000 * S, to = 2000 * S;
  it('açık problem pencere bitmeden başladıysa hep görünür; çözülmüş yalnız pencereyle kesişirse', () => {
    expect(problemOverlapsWindow(prob('a', 'open', 10 * S), from, to)).toBe(true);
    expect(problemOverlapsWindow(prob('b', 'open', 2500 * S), from, to)).toBe(false);
    expect(problemOverlapsWindow(prob('c', 'resolved', 10 * S, 900 * S), from, to)).toBe(false);
    expect(problemOverlapsWindow(prob('d', 'resolved', 10 * S, 1500 * S), from, to)).toBe(true);
    expect(problemOverlapsWindow(prob('e', 'acknowledged', 10 * S), from, to)).toBe(true);
  });
  it('iki liste birleşir, kimlikle tekrarsız; açıklar önce, sonra en yeni', () => {
    const got = dbProblemsForWindow([
      [prob('old-open', 'open', 100 * S), prob('res', 'resolved', 1200 * S, 1300 * S)],
      [prob('new-open', 'open', 1800 * S), prob('old-open', 'open', 100 * S), prob('gone', 'resolved', 1 * S, 2 * S)],
      undefined,
    ], from, to);
    expect(got.map(p => p.id)).toEqual(['new-open', 'old-open', 'res']);
  });
  it('süre: açıkta şimdiye, çözülmüşte çözülme anına dek', () => {
    expect(problemDurationText(prob('a', 'open', 0), 45 * S)).toBe('45 sn');
    expect(problemDurationText(prob('a', 'open', 0), 600 * S)).toBe('10 dk');
    expect(problemDurationText(prob('a', 'resolved', 0, 7200 * S), 999999 * S)).toBe('2.0 sa');
    expect(problemDurationText(prob('a', 'open', 0), 3 * 86400 * S)).toBe('3.0 gün');
  });
});

describe('kablolama', () => {
  it('sayfa bölümü çiziyor; bölüm kesin-özne okumasını ve tablo standardını kullanıyor', () => {
    const page = readFileSync(resolve(__dirname, '../DatabaseDetail.tsx'), 'utf8');
    expect(page).toContain('<DatabaseProblemsSection refObj={refObj} range={range} />');
    const sec = readFileSync(resolve(__dirname, './DatabaseProblemsSection.tsx'), 'utf8');
    expect(sec).toContain("useProblems({ status: 'all', service: subjects[0] ?? '', limit: 100 }, { enabled: subjects.length > 0 })");
    expect(sec).toContain("{ enabled: subjects.length > 1 }");
    expect(sec).toContain('<DataTableState dt={dt} {...state} />');
    expect(sec).toContain('timeRangeToNs(range)');
  });
});
