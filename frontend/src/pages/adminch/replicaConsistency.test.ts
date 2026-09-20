// replicaConsistency.test.ts — v0.10.791 kart saf yardımcıları + kablolama pini.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { runbook, shortZk, summarize, verdictLabel, verdictRank, verdictTone, canRepair, repairModeLabel, isInnerTable } from './replicaConsistency';
import type { CHReplicaShard, CHReplicaState, CHReplicaVerdict } from '@/lib/types';

const VERDICTS: CHReplicaVerdict[] = ['ok', 'single', 'unmapped', 'lagging', 'divergent', 'readonly', 'session_expired', 'missing_replica', 'not_replicated', 'no_replication'];

function rep(host: string, zkPath: string, over: Partial<CHReplicaState> = {}): CHReplicaState {
  return { host, shard: 1, replica: 1, zkPath, replicaName: host, totalReplicas: 2, activeReplicas: 2, readonly: false, sessionExpired: false, delayS: 0, queue: 0, rows: {}, totalRows: 0, ...over };
}
function shard(verdict: CHReplicaVerdict, replicas: CHReplicaState[], extra: Partial<CHReplicaShard> = {}): CHReplicaShard {
  return { shard: 1, replicas, verdict, hint: '', ...extra };
}

describe('replicaConsistency — saf', () => {
  it('her karar için ton ve etiket var; yapısal sorunlar kırmızı, gecikme sarı', () => {
    for (const v of VERDICTS) {
      expect(verdictLabel(v)).toBeTruthy();
      expect(['b-ok', 'b-warn', 'b-err', 'b-gray']).toContain(verdictTone(v));
    }
    expect(verdictTone('ok')).toBe('b-ok');
    expect(verdictTone('lagging')).toBe('b-warn');
    for (const v of ['divergent', 'readonly', 'session_expired', 'missing_replica', 'not_replicated', 'no_replication'] as const) expect(verdictTone(v)).toBe('b-err');
    // Sıra sunucuyla aynı: no_replication en kötü; v0.10.818 yapısal üçlü oturumun üstünde.
    expect(verdictRank('no_replication')).toBeGreaterThan(verdictRank('not_replicated'));
    expect(verdictRank('not_replicated')).toBeGreaterThan(verdictRank('missing_replica'));
    expect(verdictRank('missing_replica')).toBeGreaterThan(verdictRank('session_expired'));
    expect(verdictRank('lagging')).toBeGreaterThan(verdictRank('single'));
  });

  it('summarize: sorunlu = lagging ve üstü; en kötü karar başlıkta', () => {
    const s = summarize({ tables: [
      { table: 'a', shards: [], verdict: 'ok' }, { table: 'b', shards: [], verdict: 'single' },
      { table: 'c', shards: [], verdict: 'lagging' }, { table: 'd', shards: [], verdict: 'no_replication' },
    ] });
    expect(s).toMatchObject({ tables: 4, bad: 2, worst: 'no_replication', tone: 'b-err' });
    expect(s.text).toBe('2/4 tablo sorunlu · replikasyon yok');
    expect(summarize({ tables: [{ table: 'a', shards: [], verdict: 'ok' }] }).text).toBe('1 tablo tutarlı');
    // v0.10.818 — eksik replika sorunlu; tek replika "tutarlı" değil (sarı, yedeklilik yok).
    const m = summarize({ tables: [{ table: 'a', shards: [], verdict: 'missing_replica' }, { table: 'b', shards: [], verdict: 'ok' }] });
    expect(m).toMatchObject({ bad: 1, worst: 'missing_replica', tone: 'b-err' });
    expect(m.text).toBe('1/2 tablo sorunlu · eksik replika');
    const s1 = summarize({ tables: [{ table: 'a', shards: [], verdict: 'single' }, { table: 'b', shards: [], verdict: 'ok' }] });
    expect(s1.text).toBe('1/2 tablo tek replika · yedeklilik yok');
    expect(s1.tone).toBe('b-warn');
    expect(summarize({ tables: [] }).text).toBe('MergeTree tablo yok');
    // v0.10.792 — eşlenemeyen tablo "tutarlı" sayılmaz: karar yok, sarı.
    const u = summarize({ tables: [{ table: 'a', shards: [], verdict: 'unmapped' }, { table: 'b', shards: [], verdict: 'ok' }] });
    expect(u.text).toBe('1/2 tablo eşlenemedi · karar yok');
    expect(u.tone).toBe('b-warn');
  });

  it('shortZk: son üç parça', () => {
    expect(shortZk('/clickhouse/tables/1/spans')).toBe('…/tables/1/spans');
    expect(shortZk('/a/b')).toBe('/a/b');
  });

  it('runbook: replikasyon yok → makro doğrulama + eş yolunda _fix tablo + ATTACH + EXCHANGE', () => {
    const rb = runbook('c1', 'db', 'spans_local', shard('no_replication', [rep('h1', '/t/h1/spans'), rep('h2', '/t/h2/spans')]));
    expect(rb).toContain("clusterAllReplicas('c1', system.macros)");
    expect(rb).toContain("ENGINE = ReplicatedMergeTree('/t/h1/spans', '{replica}')");
    expect(rb).toContain('ATTACH PARTITION ID');
    expect(rb).toContain('EXCHANGE TABLES `db`.`spans_local` AND `db`.`spans_local_fix`');
    expect(rb).toContain('DBA');
  });

  it('runbook: ıraksama → eksik replikada SYNC + detached; readonly → RESTORE; ok → boş', () => {
    const d = runbook('c1', 'db', 't', shard('divergent', [rep('h1', '/p', { totalRows: 100 }), rep('h2', '/p', { totalRows: 40 })], { divergentPartition: '2026-09-18', divergencePct: 60 }));
    expect(d).toContain('(h2) koş');
    expect(d).toContain('SYSTEM SYNC REPLICA `db`.`t`');
    expect(d).toContain('system.detached_parts');
    const r = runbook('c1', 'db', 't', shard('readonly', [rep('h1', '/p', { readonly: true }), rep('h2', '/p')]));
    expect(r).toContain('h1 readonly');
    expect(r).toContain('SYSTEM RESTORE REPLICA `db`.`t`');
    expect(runbook('c1', 'db', 't', shard('ok', [rep('h1', '/p')]))).toBe('');
    // v0.10.818 — eksik replika: is_local + makro kontrolü, eşin motor AİLESİYLE CREATE (inceleme: düz
    // ReplicatedMergeTree, ReplacingMergeTree(version) eş yola katılamaz); düz tabloda canlı tabloya
    // dokunmadan _fix + ATTACH + EXCHANGE.
    const peer = rep('h2', '/t/02/t', { engine: 'ReplicatedReplacingMergeTree', replicaName: 'node2' });
    const mr = runbook('c1', 'db', 't', shard('missing_replica', [peer], { missing: [{ host: 'h1' }] }));
    expect(mr).toContain('countIf(is_local)');
    expect(mr).toContain("macro IN ('shard','replica')");
    expect(mr).toContain('(node2) FARKLI');
    expect(mr).toContain("ENGINE = ReplicatedReplacingMergeTree('/t/02/t', '{replica}', version)");
    expect(mr).not.toContain('RENAME TABLE');
    expect(mr).not.toContain('_fix');
    const nr = runbook('c1', 'db', 't', shard('not_replicated', [peer], { missing: [{ host: 'h1', engine: 'MergeTree' }] }));
    expect(nr).toContain('CREATE TABLE `db`.`t_fix`');
    expect(nr).toContain("ATTACH PARTITION ID '<partition_id>' FROM `db`.`t`");
    expect(nr).toContain('EXCHANGE TABLES `db`.`t` AND `db`.`t_fix`');
    expect(nr.indexOf('CREATE TABLE `db`.`t_fix`')).toBeLessThan(nr.indexOf('ATTACH PARTITION'));
    expect(nr).not.toContain('RENAME TABLE');
    // Eş motoru bilinmiyorsa aile yer tutucu; Replicated ama kayıtsız host düz sayılmaz.
    const un = runbook('c1', 'db', 't', shard('missing_replica', [rep('h2', '/t/02/t')], { missing: [{ host: 'h1', engine: 'ReplicatedMergeTree' }] }));
    expect(un).toContain("<SHOW CREATE'teki Replicated* motor>");
    expect(un).not.toContain('_fix');
    // Düz-MergeTree-her-yerde tablo: replicas boş → runbook patlamaz.
    expect(() => runbook('c1', 'db', 't', shard('not_replicated', [], { missing: [{ host: 'h1', engine: 'MergeTree' }] }))).not.toThrow();
  });
});

describe('runbook — v0.10.824 MV iç tablosu', () => {
  const peer = rep('ch-04', '/t/01/inner', { engine: 'ReplicatedAggregatingMergeTree', replicaName: 'ch-04' });
  const INNER = '.inner_id.11111111-1111-1111-1111-111111111111';
  it('iç tabloda tablo merdiveni YOK; MV düzeyi onarım ve MV onarımı kartı', () => {
    const rb = runbook('c1', 'db', INNER, shard('not_replicated', [peer], { missing: [{ host: 'ch-03', engine: 'AggregatingMergeTree' }] }), 'service_summary_5m');
    expect(rb).toContain('MV İÇ TABLOSU');
    // v0.10.825 — kart adı "MV onarımı"; runbook oraya yollar (sarkan · düz · eksik).
    expect(rb).toContain('MV onarımı kartı');
    expect(rb).not.toContain('Sarkan MV onarımı');
    expect(rb).toContain('DROP TABLE `db`.`service_summary_5m` SYNC;');
    expect(rb).toContain('CREATE MATERIALIZED VIEW `db`.`service_summary_5m`');
    // MV geri doldurmaz: yeniden kurulan view'a YALNIZ yeni yazımlar akar.
    expect(rb).toContain('yalnız yeni yazımlarla dolar (geçmiş pencere için MV backfill gerekir)');
    expect(rb).not.toContain("ham spans'ten yeniden dolar");
    // 818 merdiveni BURADA yanlış: EXCHANGE uuid'yi taşımaz (kapanış satırı bunu
    // açıklar, ama çalıştırılacak tek bir takas/ATTACH ifadesi kalmaz).
    expect(rb).not.toContain('_fix');
    expect(rb).not.toContain('EXCHANGE TABLES');
    expect(rb).not.toContain('ATTACH PARTITION');
    expect(rb).not.toContain('RENAME');
    expect(rb).toContain("İç tabloya doğrudan dokunma: EXCHANGE uuid'yi taşımaz, MV eski tabloya yazmaya devam eder. DBA gözetiminde, önce test ortamında.");
  });
  it('view bilinmiyorsa başlıkta yer tutucu + arama SQL\'i; biliniyorsa arama yok', () => {
    const missing = shard('missing_replica', [peer], { missing: [{ host: 'ch-03' }] });
    const unknown = runbook('c1', 'db', INNER, missing);
    expect(unknown).toContain("<MV adı: system.tables'ta TO INNER UUID ile bul>");
    expect(unknown).toContain("create_table_query LIKE '%11111111-1111-1111-1111-111111111111%'");
    expect(unknown).toContain('ch-03 · iç tablo YOK');
    const known = runbook('c1', 'db', INNER, missing, 'db_summary_5m');
    expect(known).toContain('-- MV: db_summary_5m');
    expect(known).not.toContain('create_table_query LIKE');
    expect(known).toContain('DROP TABLE `db`.`db_summary_5m` SYNC;');
  });
  it('replikasyon yok: eksik host yerine ZK yolları ve makro sorgusu; yine merdiven yok', () => {
    const nr = runbook('c1', 'db', INNER, shard('no_replication', [rep('ch-01', '/t/a/x'), rep('ch-02', '/t/b/x')]), 'spanmetrics_1m');
    expect(nr).toContain('FARKLI ZooKeeper yolunda');
    expect(nr).toContain('--   ch-01: /t/a/x');
    expect(nr).toContain("clusterAllReplicas('c1', system.macros)");
    expect(nr).not.toContain('_fix');
    expect(nr).not.toContain('EXCHANGE TABLES');
  });
  it('yapısal olmayan kararlarda iç tablo genel runbook\'ta kalır (SYNC/RESTORE uuid değiştirmez)', () => {
    const d = runbook('c1', 'db', INNER, shard('divergent', [rep('ch-01', '/p', { totalRows: 100 }), rep('ch-02', '/p', { totalRows: 40 })], { divergentPartition: 'p', divergencePct: 60 }));
    expect(d).toContain('SYSTEM SYNC REPLICA');
    expect(d).not.toContain('MV İÇ TABLOSU');
    expect(runbook('c1', 'db', INNER, shard('ok', [peer]))).toBe('');
    expect(isInnerTable(INNER)).toBe(true);
    expect(isInnerTable('spans_local')).toBe(false);
  });
});

describe('canRepair / repairModeLabel — v0.10.820 Replika onarımı', () => {
  const peer = rep('h2', '/t/02/t', { engine: 'ReplicatedReplacingMergeTree', replicaName: 'node2' });
  it('yalnız eksik/replike-olmayan kararda, düz/yok host için, sağlam Replicated eş varken', () => {
    expect(canRepair('t', shard('missing_replica', [peer], { missing: [{ host: 'h1' }] }), { host: 'h1' })).toBe(true);
    expect(canRepair('t', shard('not_replicated', [peer], { missing: [{ host: 'h1', engine: 'MergeTree' }] }), { host: 'h1', engine: 'MergeTree' })).toBe(true);
    // Replicated-ama-kayıtsız host onarılmaz (yeniden ölç); MV iç tablosu hariç; başka karar hariç.
    expect(canRepair('t', shard('missing_replica', [peer]), { host: 'h1', engine: 'ReplicatedMergeTree' })).toBe(false);
    expect(canRepair('.inner_id.abc', shard('missing_replica', [peer]), { host: 'h1' })).toBe(false);
    expect(canRepair('t_fix', shard('not_replicated', [peer], { missing: [{ host: 'h1', engine: 'MergeTree' }] }), { host: 'h1', engine: 'MergeTree' })).toBe(false); // sihirbazın geçici tablosu
    expect(canRepair('t', shard('single', [peer]), { host: 'h1' })).toBe(false);
    expect(canRepair('t', shard('no_replication', [peer]), { host: 'h1' })).toBe(false);
    // Eş motoru bilinmiyor / readonly / yok → kaynak DDL yok.
    expect(canRepair('t', shard('missing_replica', [rep('h2', '/t/02/t')]), { host: 'h1' })).toBe(false);
    expect(canRepair('t', shard('missing_replica', [rep('h2', '/t/02/t', { engine: 'ReplicatedMergeTree', readonly: true })]), { host: 'h1' })).toBe(false);
    expect(canRepair('t', shard('missing_replica', []), { host: 'h1' })).toBe(false);
  });
  it('mod etiketi iki kipi ayırır', () => {
    expect(repairModeLabel('plain')).toContain('EXCHANGE');
    expect(repairModeLabel('missing')).toContain('klon');
  });
});

describe('replicaConsistency — kablolama pini', () => {
  const page = readFileSync(resolve(__dirname, '../AdminClickhouse.tsx'), 'utf8');
  it('panel Admin → ClickHouse sayfasında mount edilir ve Ölç ile okur', () => {
    expect(page).toContain('<ReplicaConsistencyPanel />');
    expect(page).toContain('api.chReplicaConsistency(');
    expect(page).toContain("from './adminch/replicaConsistency'");
  });
  // v0.10.820 — Onar: plan → Modal → Uygula (onay kutusu) → Temizle; istemci + tip + rota literalleri.
  it('Onar akışı kablolu: plan/apply/cleanup istemcileri, Modal, onay kutusu; api.ts rotaları; types', () => {
    for (const s of ['api.chReplicaRepairPlan(', 'api.chReplicaRepairApply(', 'api.chReplicaRepairCleanup(', 'canRepair(', 'Replika onarımı —', 'Uygula (DDL koşar)', 'DBA gözetiminde', '_fix temizliği', 'p.fixExists', 'r.verifyError', '(plan.steps ?? [])']) {
      expect(page).toContain(s);
    }
    const apiSrc = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
    for (const r of ['/api/admin/clickhouse/replica-consistency/repair/plan', '/api/admin/clickhouse/replica-consistency/repair/apply', '/api/admin/clickhouse/replica-consistency/repair/cleanup']) {
      expect(apiSrc).toContain(r);
    }
    expect(apiSrc).toContain('confirm: true');
    const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(types).toContain('export interface CHReplicaRepairPlan');
    expect(types).toContain('export interface CHReplicaRepairResult');
  });
  // v0.10.824 — iç tablo satırı hangi MV'nin hedefi olduğunu söyler ve runbook
  // view adını ALIR (almazsa DROP/CREATE satırları yer tutucuyla kalır).
  it('iç tablo satırı MV adını gösterir ve runbook view alır', () => {
    expect(page).toContain('MV iç tablosu · ');
    expect(page).toContain('view çözülemedi');
    expect(page).toContain('t.table, sh, t.view)');
    const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(types).toContain('view?: string; inner?: boolean');
  });
});
