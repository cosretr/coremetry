// replicaConsistency.test.ts — v0.10.791 kart saf yardımcıları + kablolama pini.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { runbook, shortZk, summarize, verdictLabel, verdictRank, verdictTone, canRepair, canSeedFirstReplica, catalogLabel, leavesFixTable, repairModeLabel, repairRequestMode, isInnerTable } from './replicaConsistency';
import type { CHReplicaShard, CHReplicaState, CHReplicaVerdict } from '@/lib/types';

const VERDICTS: CHReplicaVerdict[] = ['ok', 'removed', 'unmanaged', 'single', 'unmapped', 'lagging', 'divergent', 'readonly', 'session_expired', 'missing_replica', 'not_replicated', 'no_replication'];

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
    // v0.10.832 — ad VIEW'ın uuid'sidir: MV'yi system.tables.uuid ile bulunur.
    // Eski reçete "TO INNER UUID ile bul" + `create_table_query LIKE '%uuid%'`
    // diyordu; ikisi de yanlış (o uuid ada girmez ve varsayılan ayarda metinde
    // hiç görünmez, yani arama HİÇBİR ZAMAN eşleşmezdi).
    expect(unknown).toContain("<MV adı: adın uuid'si VIEW'ın uuid'sidir — system.tables.uuid ile bul>");
    expect(unknown).toContain("engine = 'MaterializedView' AND toString(uuid) = '11111111-1111-1111-1111-111111111111'");
    expect(unknown).not.toContain('create_table_query LIKE');
    expect(unknown).toContain('ch-03 · iç tablo YOK');
    const known = runbook('c1', 'db', INNER, missing, 'db_summary_5m');
    expect(known).toContain('-- MV: db_summary_5m');
    expect(known).not.toContain('toString(uuid) =');
    expect(known).toContain('DROP TABLE `db`.`db_summary_5m` SYNC;');
  });

  // v0.10.832 — metin iki uuid'yi AYIRIR ve nesne uuid'sini görmek için
  // ayarın O SORGUDA açılması gerektiğini söyler.
  it("iki uuid ayrı: ad = view uuid'si, eşleşme = system.tables.uuid == TO INNER UUID", () => {
    const rb = runbook('c1', 'db', INNER, shard('not_replicated', [peer], { missing: [{ host: 'ch-03', engine: 'AggregatingMergeTree' }] }), 'db_summary_5m');
    expect(rb).toContain("İki uuid AYRIDIR");
    expect(rb).toContain("system.tables.uuid ('.inner_id.11111111-1111-1111-1111-111111111111' satırında) == MV'nin TO INNER UUID'si");
    expect(rb).toContain('SETTINGS show_table_uuid_in_table_create_query_if_not_nil = 1');
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
    expect(canRepair({ table: 't' }, shard('missing_replica', [peer], { missing: [{ host: 'h1' }] }), { host: 'h1' })).toBe(true);
    expect(canRepair({ table: 't' }, shard('not_replicated', [peer], { missing: [{ host: 'h1', engine: 'MergeTree' }] }), { host: 'h1', engine: 'MergeTree' })).toBe(true);
    // Replicated-ama-kayıtsız host onarılmaz (yeniden ölç); MV iç tablosu hariç; başka karar hariç.
    expect(canRepair({ table: 't' }, shard('missing_replica', [peer]), { host: 'h1', engine: 'ReplicatedMergeTree' })).toBe(false);
    expect(canRepair({ table: '.inner_id.abc' }, shard('missing_replica', [peer]), { host: 'h1' })).toBe(false);
    expect(canRepair({ table: 't_fix' }, shard('not_replicated', [peer], { missing: [{ host: 'h1', engine: 'MergeTree' }] }), { host: 'h1', engine: 'MergeTree' })).toBe(false); // sihirbazın geçici tablosu
    expect(canRepair({ table: 't' }, shard('single', [peer]), { host: 'h1' })).toBe(false);
    expect(canRepair({ table: 't' }, shard('no_replication', [peer]), { host: 'h1' })).toBe(false);
    // Eş motoru bilinmiyor / readonly / yok → kaynak DDL yok.
    expect(canRepair({ table: 't' }, shard('missing_replica', [rep('h2', '/t/02/t')]), { host: 'h1' })).toBe(false);
    expect(canRepair({ table: 't' }, shard('missing_replica', [rep('h2', '/t/02/t', { engine: 'ReplicatedMergeTree', readonly: true })]), { host: 'h1' })).toBe(false);
    expect(canRepair({ table: 't' }, shard('missing_replica', []), { host: 'h1' })).toBe(false);
  });
  it('mod etiketi kipleri ayırır', () => {
    expect(repairModeLabel('plain')).toContain('EXCHANGE');
    expect(repairModeLabel('missing')).toContain('klon');
    // v0.10.829 — seed etiketi iki şeyi SÖYLEMEK zorunda: 1/1 (yedeklilik yok)
    // ve ötekilerin sonra "Onar" ile katılması.
    expect(repairModeLabel('seed')).toContain('1/1');
    expect(repairModeLabel('seed')).toContain('Onar');
  });
});

// v0.10.829 — "İlk replikayı kur": shard'da HİÇ Replicated replika yokken
// (operatör vakası: events · shard 1, iki host da düz ReplacingMergeTree)
// düz tablosu olan host shard'ın İLK replikası olur.
describe('canSeedFirstReplica / leavesFixTable — v0.10.829 ilk replika', () => {
  const plain = { host: 'h1', engine: 'ReplacingMergeTree' };
  const peer = rep('h2', '/t/01/t', { engine: 'ReplicatedMergeTree' });
  it('operatör vakası: Replicated replika yok + düz tablo → düğme çıkar', () => {
    expect(canSeedFirstReplica({ table: 'events' }, shard('not_replicated', [], { missing: [plain, { host: 'h2', engine: 'ReplacingMergeTree' }] }), plain)).toBe(true);
    expect(canSeedFirstReplica({ table: 'events' }, shard('missing_replica', [], { missing: [plain] }), plain)).toBe(true);
  });
  it('shard\'da Replicated replika VARSA çıkmaz — orada doğru eylem "Onar"', () => {
    expect(canSeedFirstReplica({ table: 'events' }, shard('not_replicated', [peer], { missing: [plain] }), plain)).toBe(false);
    // İki düğme aynı satırda asla birlikte çıkmaz (koşullar birbirini dışlar).
    const withPeer = shard('not_replicated', [peer], { missing: [plain] });
    const noPeer = shard('not_replicated', [], { missing: [plain] });
    expect(canRepair({ table: 'events' }, withPeer, plain) && canSeedFirstReplica({ table: 'events' }, withPeer, plain)).toBe(false);
    expect(canRepair({ table: 'events' }, noPeer, plain) && canSeedFirstReplica({ table: 'events' }, noPeer, plain)).toBe(false);
  });
  // v0.10.829 boş-shard dalı — operatör: span_links_reverse · shard 2, İKİ host da "tablo yok".
  it('shard\'ın hepsi "tablo yok" → düğme çıkar (boş Replicated tablo kurulur)', () => {
    const empty = { host: 'h9' }; // plain h1'de: AYRI host olmalı, yoksa "başka host" kuralı ısırmaz
    expect(canSeedFirstReplica({ table: 'span_links_reverse' }, shard('missing_replica', [], { missing: [empty, { host: 'h2' }] }), empty)).toBe(true);
    // Ama shard'da düz tablo taşıyan BAŞKA host varsa: ilk replika ORADA kurulur.
    expect(canSeedFirstReplica({ table: 'span_links_reverse' }, shard('not_replicated', [], { missing: [empty, plain] }), empty)).toBe(false);
    // O düz host'un kendi satırında düğme ÇIKAR (tohum verisi onda).
    expect(canSeedFirstReplica({ table: 'span_links_reverse' }, shard('not_replicated', [], { missing: [empty, plain] }), plain)).toBe(true);
    // Replicated replika varsa boş host için de çıkmaz (Onar eşe katar).
    expect(canSeedFirstReplica({ table: 'span_links_reverse' }, shard('missing_replica', [peer], { missing: [empty] }), empty)).toBe(false);
  });
  it('kapsam dışı satırlarda çıkmaz', () => {
    // Replicated ama kayıtsız → yeniden ölç.
    expect(canSeedFirstReplica({ table: 'events' }, shard('missing_replica', []), { host: 'h1', engine: 'ReplicatedMergeTree' })).toBe(false);
    // MV iç tablosu ve sihirbazın `_fix` tablosu kapsam dışı.
    expect(canSeedFirstReplica({ table: '.inner_id.abc' }, shard('not_replicated', []), plain)).toBe(false);
    expect(canSeedFirstReplica({ table: 'events_fix' }, shard('not_replicated', []), plain)).toBe(false);
    // Yapısal olmayan kararlar (ıraksama / readonly / tek replika) başka hastalık.
    for (const v of ['divergent', 'readonly', 'session_expired', 'single', 'lagging', 'no_replication', 'ok'] as const) {
      expect(canSeedFirstReplica({ table: 'events' }, shard(v, []), plain)).toBe(false);
    }
  });
  it('leavesFixTable: EXCHANGE\'li kipler `_fix` bırakır, boş dal bırakmaz', () => {
    expect(leavesFixTable('plain')).toBe(true);
    expect(leavesFixTable('seed')).toBe(true);
    expect(leavesFixTable('seed_empty')).toBe(false); // `_fix` hiç kurulmaz
    expect(leavesFixTable('missing')).toBe(false);
    expect(leavesFixTable('')).toBe(false);
  });
  it('repairRequestMode: seed_empty bir PLAN kipi, istek yine "seed"', () => {
    expect(repairRequestMode('seed')).toBe('seed');
    expect(repairRequestMode('seed_empty')).toBe('seed');
    expect(repairRequestMode('plain')).toBeUndefined();
    expect(repairRequestMode('missing')).toBeUndefined();
  });
  it('mod etiketi boş dalın veri taşımadığını söyler', () => {
    const l = repairModeLabel('seed_empty');
    expect(l).toContain('VERİ TAŞINMAZ');
    expect(l).toContain('Onar');
    expect(l).not.toContain('ATTACH edilir');
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
    for (const s of ['api.chReplicaRepairPlan(', 'api.chReplicaRepairApply(', 'api.chReplicaRepairCleanup(', 'canRepair(', "'Replika onarımı'", 'Uygula (DDL koşar)', 'DBA gözetiminde', '_fix temizliği', 'p.fixExists', 'r.verifyError', '(plan.steps ?? [])']) {
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
  // v0.10.829 — "İlk replikayı kur" satır düğmesi + kip tek koda bağlı:
  // AYNI üç uç, kip gövdede (ikinci rota yok), `_fix` kararı leavesFixTable'dan.
  it('İlk replikayı kur kablolu: düğme, seed kipi, tek rota, mod etiketi', () => {
    for (const s of ['canSeedFirstReplica(', 'İlk replikayı kur', "openPlan(t.table, sh.shard, m.host, 'seed')", "'İlk replika kurulumu'", 'leavesFixTable(', "repairRequestMode(mode)"]) {
      expect(page).toContain(s);
    }
    // Düğme tehlikeli: danger varyantı (accent "Onar"dan ayrılır).
    // v0.10.829 incelemesi (5): ALTERNASYON YOK — `/…|İlk replikayı kur/`
    // yazımında çıplak literal her zaman eşleşiyordu, yani pin boştu.
    // Düğmenin JSX dilimi kesilir ve varyant ORADA aranır.
    const seedBtn = page.slice(page.indexOf('canSeedFirstReplica(t, sh, m) && ('), page.indexOf('İlk replikayı kur</Button>'));
    expect(seedBtn.length).toBeGreaterThan(0);
    expect(seedBtn).toContain('variant="danger"');
    expect(seedBtn).not.toContain('variant="accent"');
    const apiSrc = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
    expect(apiSrc).toContain('mode?: import(\'./types\').CHReplicaRepairMode');
    expect(apiSrc).toContain('...(mode ? { mode } : {})');
    // Seed kipi YENİ rota açmaz: üç repair rotası, hepsi bu kadar.
    expect((apiSrc.match(/\/api\/admin\/clickhouse\/replica-consistency\/repair\//g) ?? []).length).toBe(3);
    const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(types).toContain("mode: 'missing' | 'plain' | 'seed' | 'seed_empty'");
    expect(types).toContain('export type CHReplicaRepairMode');
    // v0.10.829 boş dal: modal metni veri taşımadığını söyler, `_fix` geçmez.
    expect(page).toContain("plan.mode === 'seed_empty'");
    expect(page).toContain('Veri TAŞINMAZ');
  });
  // v0.10.824 — iç tablo satırı hangi MV'nin hedefi olduğunu söyler ve runbook
  // view adını ALIR (almazsa DROP/CREATE satırları yer tutucuyla kalır).
  // v0.10.830 — metin saf gövdeye (innerViewLabel) taşındı; sayfa onu çağırır.
  it('iç tablo satırı MV adını gösterir ve runbook view alır', () => {
    const helper = readFileSync(resolve(__dirname, 'replicaConsistency.ts'), 'utf8');
    expect(helper).toContain('MV iç tablosu · ');
    expect(helper).toContain('view çözülemedi');
    expect(page).toContain('{innerViewLabel(t)}');
    expect(page).toContain('t.table, sh, t.view, t.catalog)'); // v0.10.872 — katalog dışı satır runbook almaz
    const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(types).toContain('view?: string; inner?: boolean');
  });

  // ── v0.10.846 — KALDIRILMIŞ tablonun kalıntısı ────────────────────────
  //
  // Operatör-bildirimli (prod, 2026-09-20, ekran görüntüsü): kart
  // "1/90 tablo sorunlu · eksik replika" dedi; tek sorunlu tablo `feedbacks`
  // ve shard 1'in İKİ host'unda da yoktu ("İlk replikayı kur" düğmesiyle),
  // shard 2'de 2/2 Replicated duruyordu.
  //
  // `feedbacks` ürünün v0.8.240'ta KALDIRDIĞI bir tablo. Eski boot DROP'u
  // ON CLUSTER taşımıyordu (adaptDDL DROP'u yeniden yazmaz), o yüzden yalnız
  // koordinatör host'ta koştu: bir shard temizlendi, öteki shard'da tablo
  // kaldı. "Eksik replika" sanılan şey YARIM KALMIŞ SİLME'ydi; düğme zaten
  // çalışamıyordu (kanonik tanım yok) — yani YANILTICIydı.
  it('kart satırı katalog etiketini çizer ve düğme kapılarına TABLOYU verir', () => {
    // Kapı `t.table` değil `t` alır: katalog sınıfı TABLO düzeyinde yaşıyor.
    expect(page).toContain('{catalogLabel(t)}');
    expect(page).toContain('{t.catalog && (');
    expect(page).toContain('canRepair(t, sh, m)');
    expect(page).toContain('canSeedFirstReplica(t, sh, m)');
    const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(types).toContain("export type CHReplicaCatalog = 'removed' | 'unmanaged';");
    expect(types).toContain('catalog?: CHReplicaCatalog; removedSince?: string');
  });
});

// v0.10.846 — katalog kararlarının DAVRANIŞI (ton, etiket, özet, düğme kapısı).
describe('katalog kararları — v0.10.846', () => {
  const plain = { host: 'ch-01', engine: 'ReplacingMergeTree' };
  const peer = rep('ch-03', '/clickhouse/tables/2/feedbacks', { engine: 'ReplicatedMergeTree' });

  it('kaldırılmış ve yönetilmeyen kararlar GRİ, `lagging` eşiğinin altında', () => {
    expect(verdictTone('removed')).toBe('b-gray');
    expect(verdictTone('unmanaged')).toBe('b-gray');
    expect(verdictLabel('removed')).toBe('kaldırıldı');
    expect(verdictLabel('unmanaged')).toBe('katalog dışı');
    expect(verdictRank('removed')).toBeLessThan(verdictRank('lagging'));
    expect(verdictRank('unmanaged')).toBeLessThan(verdictRank('lagging'));
    expect(verdictRank('removed')).toBeGreaterThan(verdictRank('ok'));
  });

  it('özet kalıntıyı "sorunlu" saymaz ama "tutarlı" da demez', () => {
    // Prod şekli: 89 ölçülmüş tablo + 1 kaldırılmış kalıntı (ekran: 1/90).
    const tables = Array.from({ length: 89 }, (_, i) => ({ table: `t${i}`, shards: [], verdict: 'ok' as const }));
    const s = summarize({ tables: [...tables, { table: 'feedbacks', shards: [], verdict: 'removed', catalog: 'removed', removedSince: 'v0.8.240' }] });
    expect(s.bad).toBe(0);
    expect(s.removed).toBe(1);
    expect(s.tone).toBe('b-gray');
    expect(s.text).toBe('89 tablo tutarlı · 1 kalıntı (ürün kaldırdı)');
    expect(s.text).not.toContain('sorunlu');
  });

  // v0.10.846 incelemesi — sayaç KARARA değil `catalog` ALANINA bakmalı:
  // kapsaması susturulmuş ama replikaları sağlıklı bir tablonun kararı `ok`
  // olur ve karara bakan sayaç onu "tutarlı" sayardı (ölçülmemiş bir şeyi
  // ölçülmüş göstermek).
  it('kapsaması susturulmuş ama kararı ok olan tablo "tutarlı" sayılmaz', () => {
    const s = summarize({ tables: [
      { table: 'a', shards: [], verdict: 'ok' },
      { table: 'musteri_deneme', shards: [], verdict: 'ok', catalog: 'unmanaged' },
    ] });
    expect(s.unmanaged).toBe(1);
    expect(s.text).toBe('1 tablo tutarlı · 1 katalog dışı');
  });

  // İKİSİ FARKLI ŞEY: `removed` geçici (bir sonraki boot siler) ve rozeti
  // boyar; `unmanaged` kalıcı (operatörün kendi tablosu) ve kartı sonsuza
  // dek griye kilitlememeli.
  it('kalıcı katalog-dışı rozeti kilitlemez, geçici kalıntı boyar', () => {
    const only = summarize({ tables: [
      { table: 'a', shards: [], verdict: 'ok' },
      { table: 'musteri_deneme', shards: [], verdict: 'ok', catalog: 'unmanaged' },
    ] });
    expect(only.tone).toBe('b-ok');
    const withRemoved = summarize({ tables: [
      { table: 'a', shards: [], verdict: 'ok' },
      { table: 'feedbacks', shards: [], verdict: 'removed', catalog: 'removed', removedSince: 'v0.8.240' },
    ] });
    expect(withRemoved.tone).toBe('b-gray');
    const both = summarize({ tables: [
      { table: 'a', shards: [], verdict: 'ok' },
      { table: 'feedbacks', shards: [], verdict: 'removed', catalog: 'removed', removedSince: 'v0.8.240' },
      { table: 'musteri_deneme', shards: [], verdict: 'ok', catalog: 'unmanaged' },
    ] });
    expect(both.text).toBe('1 tablo tutarlı · 1 kalıntı (ürün kaldırdı) · 1 katalog dışı');
  });

  // v0.10.846 incelemesi — düğme ile sunucu aynı kararı vermeli: sihirbaz
  // kanonik tanım bulamadığında (`<ürün>_old` göç yedeği) sunucu zaten
  // reddediyordu, FE ise ada bakıp düğmeyi çiziyordu.
  it('kanonik tanımı olmayan satırda "İlk replikayı kur" çizilmez', () => {
    const missing = shard('missing_replica', [], { missing: [plain] });
    expect(canSeedFirstReplica({ table: 'problems_old', seedable: false }, missing, plain)).toBe(false);
    expect(canSeedFirstReplica({ table: 'problems', seedable: true }, missing, plain)).toBe(true);
  });

  it('etiket DURUM bildirir, eylem önermez', () => {
    expect(catalogLabel({ catalog: 'removed', removedSince: 'v0.8.240' }))
      .toBe('ürün bu tabloyu KALDIRDI (v0.8.240) — kalıntı; bir sonraki boot küme genelinde temizler');
    expect(catalogLabel({ catalog: 'unmanaged' })).toContain('Coremetry yönetmiyor');
    expect(catalogLabel({})).toBe('');
  });

  it('kaldırılmış/yönetilmeyen satırda ONAR ve İLK REPLİKAYI KUR çizilmez', () => {
    // KONTROL satırları: aynı shard şekli katalog bilgisi OLMADAN düğmeyi
    // çizer — yani kapı adın kendisinde değil, tablonun katalog sınıfında.
    const missing = shard('missing_replica', [], { missing: [plain] });
    expect(canSeedFirstReplica({ table: 'feedbacks' }, missing, plain)).toBe(true);
    expect(canSeedFirstReplica({ table: 'feedbacks', catalog: 'removed' }, missing, plain)).toBe(false);
    expect(canSeedFirstReplica({ table: 'musteri_deneme', catalog: 'unmanaged' }, missing, plain)).toBe(false);
    const withPeer = shard('not_replicated', [peer], { missing: [plain] });
    expect(canRepair({ table: 'feedbacks' }, withPeer, plain)).toBe(true);
    expect(canRepair({ table: 'feedbacks', catalog: 'removed' }, withPeer, plain)).toBe(false);
    expect(canRepair({ table: 'musteri_deneme', catalog: 'unmanaged' }, withPeer, plain)).toBe(false);
  });

  it('katalog kararında runbook YOK (kopyalanacak SQL yok)', () => {
    expect(runbook('shop_cluster', 'shop', 'feedbacks', shard('removed', []))).toBe('');
    expect(runbook('shop_cluster', 'shop', 'musteri_deneme', shard('unmanaged', []))).toBe('');
  });
});

// v0.10.872 (inceleme) — 846'nın katalog kararı özete ve runbook'a TAŞINDI:
// replikalı `unmanaged` tablo sunucudan gerçek kararla (divergent) gelir; özet
// onu "sorunlu" saymaz, başlığı boyamaz, runbook basmaz. Eski pin yalnız
// ulaşılamaz `default` kolunu (shard verdict = 'unmanaged') tutuyordu.
describe('katalog dışı satır — replikalı unmanaged (v0.10.872)', () => {
  const managedOk = { table: 'problems', shards: [], verdict: 'ok' as const };
  const unmanagedDiv = {
    table: 'musteri_deneme', verdict: 'divergent' as const, catalog: 'unmanaged' as const,
    shards: [shard('divergent', [{ host: 'ch-01' } as CHReplicaState, { host: 'ch-02' } as CHReplicaState])],
  };
  it('özet: sorunlu saymaz, en kötüyü belirlemez, boyamaz', () => {
    const s = summarize({ tables: [managedOk, unmanagedDiv] });
    expect(s.bad).toBe(0);
    expect(s.worst).toBe('ok');
    expect(s.tone).toBe('b-ok');
    expect(s.unmanaged).toBe(1);
  });
  it('runbook: divergent shard bile olsa katalog dışı satır SQL basmaz', () => {
    expect(runbook('shop_cluster', 'shop', 'musteri_deneme', unmanagedDiv.shards[0], undefined, 'unmanaged')).toBe('');
    expect(runbook('shop_cluster', 'shop', 'feedbacks', unmanagedDiv.shards[0], undefined, 'removed')).toBe('');
    // katalogsuz aynı shard runbook ALIR (kapı yalnız kataloğa bakar)
    expect(runbook('shop_cluster', 'shop', 'problems', unmanagedDiv.shards[0])).not.toBe('');
  });
});
