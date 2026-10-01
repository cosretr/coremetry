import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  dbSubjectId, dbProblemSubjects, problemOverlapsWindow, dbProblemsForWindow, problemDurationText, rowProblemSummary,
  DB_NAME_SENTINEL, DB_CAPACITY_RULE_PREFIX, dbProblemForm, dbProblemSubjectForms, keepProblemForms, dbSeverityBadgeClass, dbSeverityRank,
} from './databaseProblems';
import type { DBProblemCount, DBProblemForms, Problem } from '@/lib/types';

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
    // v0.10.1027 — okumalar biçim farkında aday kümesinden (dbProblemSubjectForms);
    // pin bilinçli güncellendi: eski `subjects[0] ?? ''` dizgi listesiydi.
    expect(sec).toContain("useProblems({ status: 'all', service: subjects[0]?.id ?? '', limit: 100 }, { enabled: subjects.length > 0 })");
    expect(sec).toContain("{ enabled: subjects.length > 1 }");
    expect(sec).toContain('<DataTableState dt={dt} {...state} />');
    expect(sec).toContain('timeRangeToNs(range)');
  });
});

// ── v0.10.1027 — /databases listesinde satır başına açık problem işareti ────
// (Databases dilim 4). Özne dizgisi `db:<sys>@<X>` X'in instance mı db.name mi
// olduğunu söylemez; biçim kuraldan türer ve iki yüzey aynı kuralı uygular.

const GO = (rel: string) => readFileSync(resolve(__dirname, '../../../../', rel), 'utf8');
const c = (open: number, topSeverity: DBProblemCount['topSeverity']): DBProblemCount => ({ open, topSeverity });

describe('biçim yüklemi (dbProblemForm) — Go chstore ikizi', () => {
  it('kapasite kuralı instance, gerisi dbName', () => {
    expect(dbProblemForm('db-capacity:oracle-tablespace')).toBe('instance');
    expect(dbProblemForm('db-slow-stmt')).toBe('dbName');
    expect(dbProblemForm('3f2a9c1e-target-rule')).toBe('dbName');
    expect(dbProblemForm('')).toBe('dbName');
    expect(dbProblemForm(undefined)).toBe('dbName');
  });
  it('önek Go sabitiyle aynı ve üretici onu kullanıyor', () => {
    expect(DB_CAPACITY_RULE_PREFIX).toBe('db-capacity:');
    expect(GO('internal/chstore/problem.go')).toContain(`RuleDBCapacityPrefix = "${DB_CAPACITY_RULE_PREFIX}"`);
    const cap = GO('internal/evaluator/db_capacity.go');
    expect(cap).toContain('return chstore.RuleDBCapacityPrefix + checkID');
    expect(GO('internal/evaluator/db_slow_statement.go')).toContain('const dbSlowStmtRuleID = "db-slow-stmt"');
  });
});

describe('aday kümesi (dbProblemSubjectForms) — liste ve detayın TEK kümesi', () => {
  it('instance ve dbName ayrı biçimlerle; aynıysa tek kimlik iki biçim', () => {
    expect(dbProblemSubjectForms({ system: 'Oracle', instance: 'core-db', dbName: 'CORE' })).toEqual([
      { id: 'db:oracle@core-db', forms: ['instance'] },
      { id: 'db:oracle@CORE', forms: ['dbName'] },
    ]);
    expect(dbProblemSubjectForms({ system: 'oracle', instance: 'CORE', dbName: 'CORE' })).toEqual([
      { id: 'db:oracle@CORE', forms: ['instance', 'dbName'] },
    ]);
  });
  it("'default' nöbetçisi sorulmaz (iki yüzeyde de) — dbProblemSubjects aynı kuralı taşır", () => {
    expect(dbProblemSubjectForms({ system: 'oracle', instance: 'core-db', dbName: DB_NAME_SENTINEL }))
      .toEqual([{ id: 'db:oracle@core-db', forms: ['instance'] }]);
    expect(dbProblemSubjects({ system: 'oracle', instance: 'core-db', dbName: 'default' })).toEqual(['db:oracle@core-db']);
    expect(dbProblemSubjects({ system: 'oracle', instance: 'core-db', dbName: ' default ' })).toEqual(['db:oracle@core-db']);
  });
  it('receiver kimliği yalnız instance biçimi', () => {
    expect(dbProblemSubjectForms({ system: 'oracle', instance: 'core-db', dbName: 'CORE', source: 'receiver' }))
      .toEqual([{ id: 'db:oracle@core-db', forms: ['instance'] }]);
  });
  it("nöbetçi değeri MV'lerin coalesce yedeğiyle aynı", () => {
    expect(GO('internal/chstore/store.go')).toContain("coalesce(nullIf(attr_values[indexOf(attr_keys, 'db.name')], ''), 'default') AS db_name");
  });
});

describe('rowProblemSummary — liste işareti', () => {
  const subjects: Record<string, DBProblemForms> = {
    'db:oracle@core-db': { instance: c(2, 'warning') },                     // kapasite
    'db:oracle@CORE': { dbName: c(1, 'critical') },                         // yavaş ifade
    'db:oracle@default': { dbName: c(5, 'critical') },                      // nöbetçi kova
    'db:postgresql@pg-1': { instance: c(3, 'info') },
    'db:oracle@ZERO': { instance: c(0, 'critical') },                       // savunma: sıfır sayı işaret üretmez
    'db:oracle@BOTH': { instance: c(1, 'info'), dbName: c(2, 'warning') },  // aynı dizgi, iki biçim
  };
  const cases: Array<[string, Parameters<typeof rowProblemSummary>[0], DBProblemCount | null]> = [
    ['yalnız instance biçimi', { system: 'oracle', instance: 'core-db', dbName: 'OTHER' }, c(2, 'warning')],
    ['yalnız dbName biçimi', { system: 'oracle', instance: 'other-host', dbName: 'CORE' }, c(1, 'critical')],
    ['ikisi birden: toplanır, önem en ağırı', { system: 'oracle', instance: 'core-db', dbName: 'CORE' }, c(3, 'critical')],
    ['instance == dbName: iki biçim bir kez sayılır', { system: 'oracle', instance: 'BOTH', dbName: 'BOTH' }, c(3, 'warning')],
    ["'default' nöbetçisi eşleşmez", { system: 'oracle', instance: 'other-host', dbName: 'default' }, null],
    ["'default' nöbetçisi instance biçimini engellemez", { system: 'oracle', instance: 'core-db', dbName: 'default' }, c(2, 'warning')],
    ['receiver satırı: yalnız instance biçimi', { system: 'oracle', instance: 'core-db', dbName: 'CORE', source: 'receiver' }, c(2, 'warning')],
    ['receiver satırı dbName biçimiyle eşleşmez', { system: 'oracle', instance: 'other-host', dbName: 'CORE', source: 'receiver' }, null],
    ['system büyük/küçük harf duyarsız', { system: 'PostgreSQL', instance: 'pg-1' }, c(3, 'info')],
    ['hiçbir şey eşleşmez → null', { system: 'mysql', instance: 'orders-db', dbName: 'orders' }, null],
    ['sıfır sayılı özet → null', { system: 'oracle', instance: 'ZERO' }, null],
    ['instance yok, dbName yok → null', { system: 'oracle' }, null],
  ];
  for (const [name, row, want] of cases) {
    it(name, () => { expect(rowProblemSummary(row, subjects)).toEqual(want); });
  }
  it('okuma yok (yükleniyor / hata) → null', () => {
    expect(rowProblemSummary({ system: 'oracle', instance: 'core-db' }, undefined)).toBeNull();
    expect(rowProblemSummary({ system: 'oracle', instance: 'core-db' }, null)).toBeNull();
  });
});

// R8(1) — BİÇİM ÇAKIŞMASI: span satırlarının instance'ı çoğu kez genel bir ad
// ("postgres"); aynı adlı bir veritabanının yavaş ifadesi o satırları
// işaretlememeli. Ayna: kapasite problemi db.name'i aynı olan satırı işaretlememeli.
describe('biçim çakışması — liste ve detay aynı cevabı verir', () => {
  const rows = [
    { system: 'postgresql', instance: 'postgres', dbName: 'orders' },  // instance genel ad
    { system: 'postgresql', instance: 'pg-2', dbName: 'postgres' },    // db.name "postgres"
    { system: 'postgresql', instance: 'postgres', dbName: 'billing' }, // instance genel ad
  ];
  const probs = (ruleId: string): Problem[] => [
    { id: 'x', ruleId, ruleName: ruleId, severity: 'critical', service: 'db:postgresql@postgres', status: 'open', description: '', startedAt: 1 } as unknown as Problem,
  ];
  const detailKeeps = (ref: (typeof rows)[number], list: Problem[]) =>
    dbProblemSubjectForms(ref).some(q => q.id === 'db:postgresql@postgres' && keepProblemForms(list, q.forms).length > 0);

  it('dbName biçimli problem yalnız db.name\'i "postgres" olan satırı işaretler', () => {
    const subjects = { 'db:postgresql@postgres': { dbName: c(1, 'critical') } };
    expect(rows.map(r => rowProblemSummary(r, subjects)?.open ?? 0)).toEqual([0, 1, 0]);
    expect(rows.map(r => detailKeeps(r, probs('db-slow-stmt')))).toEqual([false, true, false]);
  });
  it('kapasite problemi yalnız instance\'ı "postgres" olan satırları işaretler (ayna)', () => {
    const subjects = { 'db:postgresql@postgres': { instance: c(1, 'warning') } };
    expect(rows.map(r => rowProblemSummary(r, subjects)?.open ?? 0)).toEqual([1, 0, 1]);
    expect(rows.map(r => detailKeeps(r, probs('db-capacity:postgres-connections')))).toEqual([true, false, true]);
  });
  it('keepProblemForms: instance kimliğinden yalnız kapasite, dbName kimliğinden yalnız kapasite dışı', () => {
    const list = [...probs('db-capacity:oracle-sessions'), ...probs('db-slow-stmt'), ...probs('rule-x')];
    expect(keepProblemForms(list, ['instance']).map(p => p.ruleId)).toEqual(['db-capacity:oracle-sessions']);
    expect(keepProblemForms(list, ['dbName']).map(p => p.ruleId)).toEqual(['db-slow-stmt', 'rule-x']);
    expect(keepProblemForms(list, ['instance', 'dbName']).length).toBe(3);
    expect(keepProblemForms(undefined, ['instance'])).toEqual([]);
    expect(keepProblemForms(list, undefined)).toEqual([]);
  });
});

// R3 — BİLİNEN AÇIK (bu dilimde düzeltilmedi): PostgreSQL kapasite denetimi
// motoru "POSTGRES" yazar → özne `db:postgres@<instance>`; satırlar
// "postgresql" taşır. İKİ yüzey de bugün bu problemi kaçırır — AYNI biçimde.
// Düzeltme üreticide (özne kimliği kanonikleştirme) ayrı bir değişiklik; o gün
// bu pin ve Go ikizi (internal/evaluator/db_problem_form_test.go) birlikte
// güncellenir. Tek taraflı bir düzeltme (ör. yalnız listeye takma ad) burada kırar.
describe('Postgres takma adı — bugünkü davranış iki yüzeyde AYNI', () => {
  it('üretici "POSTGRES", satırlar "postgresql"', () => {
    expect(GO('internal/evaluator/db_capacity.go')).toContain('dbsys: "POSTGRES", probe: "postgresql.backends"');
    expect(GO('internal/chstore/dependencies.go')).toContain('{"postgresql.", "postgresql"}');
    expect(dbSubjectId('POSTGRES', 'pg-1')).toBe('db:postgres@pg-1');
  });
  it('liste işareti ve detay aday kümesi db:postgres@… öznesini sormaz', () => {
    const subjects = { 'db:postgres@pg-1': { instance: c(1, 'critical') } };
    for (const ref of [
      { system: 'postgresql', instance: 'pg-1', source: 'receiver' },
      { system: 'postgresql', instance: 'pg-1', dbName: 'orders' },
    ]) {
      expect(rowProblemSummary(ref, subjects)).toBeNull();
      expect(dbProblemSubjects(ref)).not.toContain('db:postgres@pg-1');
    }
  });
});

describe('önem tonu (öncelik değil)', () => {
  it('critical err, warning warn, gerisi nötr; sıra critical > warning > info', () => {
    expect(dbSeverityBadgeClass('critical')).toBe('badge b-err');
    expect(dbSeverityBadgeClass('warning')).toBe('badge b-warn');
    expect(dbSeverityBadgeClass('info')).toBe('badge b-gray');
    expect(dbSeverityBadgeClass('fatal')).toBe('badge b-gray');
    expect(dbSeverityBadgeClass(undefined)).toBe('badge b-gray');
    expect(dbSeverityRank('critical')).toBeGreaterThan(dbSeverityRank('warning'));
    expect(dbSeverityRank('warning')).toBeGreaterThan(dbSeverityRank('info'));
    expect(dbSeverityRank('')).toBe(dbSeverityRank('info'));
  });
  it('detay kartı Önem sütunu, PriorityBadge yok; biçim süzgeci okumalarda', () => {
    const sec = readFileSync(resolve(__dirname, './DatabaseProblemsSection.tsx'), 'utf8');
    expect(sec).toContain("{ id: 'severity', label: 'Önem', width: 96, sortValue: p => dbSeverityRank(p.severity) }");
    expect(sec).toContain('<span className={dbSeverityBadgeClass(p.severity)}>');
    expect(sec).not.toContain('PriorityBadge');
    expect(sec).toContain('keepProblemForms(first.data?.items, subjects[0]?.forms)');
    expect(sec).toContain('keepProblemForms(second.data?.items, subjects[1]?.forms)');
  });
});

describe('kablolama — /databases listesi (v0.10.1027)', () => {
  const page = readFileSync(resolve(__dirname, '../Databases.tsx'), 'utf8');
  it('özet satıra matcher üzerinden, toRow içinde açık kopyayla gelir', () => {
    expect(page).toContain("import { rowProblemSummary } from '@/pages/databases/databaseProblems';");
    expect(page).toContain('const prob = rowProblemSummary(d, problemSubjects);');
    expect(page).toMatch(/openProblems: prob\?\.open,/);
    expect(page).toMatch(/topSeverity: prob\?\.topSeverity,/);
    expect(page).toContain('}, [problemSubjects]);');
    expect(page).toContain('useMemo(() => spanRows.map(toRow), [spanRows, toRow])');
  });
  it('sorgu anahtarı statik (pencere / env yok), polling yok', () => {
    const i = page.indexOf('const problemsQ = useQuery({');
    expect(i).toBeGreaterThan(-1);
    const call = page.slice(i, page.indexOf('});', i));
    expect(call).toContain("queryKey: ['database-problems']");
    expect(call).toContain('api.databaseProblems(signal)');
    expect(call).not.toMatch(/refetchInterval/);
  });
  it('iki db mount noktası da işareti satır tıkıyla AYNI adrese bağlar', () => {
    expect(page.match(/rowHref=\{databasePageHref\}/g)?.length).toBe(2);
    expect(page).toContain('const openDatabasePage = (r: DepRow) => navigate(databasePageHref(r));');
  });
});
