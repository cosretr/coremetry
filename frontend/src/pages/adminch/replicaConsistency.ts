/**
 * replicaConsistency — "Replika tutarlılığı" kartının saf yarısı (v0.10.791).
 * Kararlar sunucuda (chstore.replicaVerdict); burada rozet tonu, etiket,
 * özet ve kararın runbook metni. Runbook operatörün kopyalayıp DBA ile
 * koşacağı SQL: ürün ZK yolu uyuşmazlığını kendi düzeltmez (veri taşıma).
 */
import type { CHReplicaConsistencyResponse, CHReplicaShard, CHReplicaVerdict } from '@/lib/types';

export type ReplicaTone = 'b-ok' | 'b-warn' | 'b-err' | 'b-gray';

export function verdictTone(v: CHReplicaVerdict): ReplicaTone {
  switch (v) {
    case 'ok': return 'b-ok';
    case 'single':
    case 'unmapped': return 'b-gray';
    case 'lagging': return 'b-warn';
    default: return 'b-err'; // divergent · readonly · session_expired · no_replication
  }
}

const LABEL: Record<CHReplicaVerdict, string> = {
  ok: 'tutarlı', single: 'tek replika', unmapped: 'eşlenemedi', lagging: 'geride',
  divergent: 'ıraksama', readonly: 'readonly', session_expired: 'oturum düşmüş', no_replication: 'replikasyon yok',
};
export const verdictLabel = (v: CHReplicaVerdict): string => LABEL[v];

// Sıralama: sunucudaki replicaVerdictRank ile aynı (kötü önce listelenir).
const RANK: Record<CHReplicaVerdict, number> = {
  ok: 0, single: 1, unmapped: 2, lagging: 3, divergent: 4, readonly: 5, session_expired: 6, no_replication: 7,
};
export const verdictRank = (v: CHReplicaVerdict) => RANK[v];

export interface ReplicaSummary { tables: number; bad: number; worst: CHReplicaVerdict; tone: ReplicaTone; text: string }

/** Kart başlığı: kaç tablo, kaçı sorunlu, en kötü karar. */
export function summarize(r: Pick<CHReplicaConsistencyResponse, 'tables'>): ReplicaSummary {
  let worst: CHReplicaVerdict = 'ok';
  let bad = 0;
  for (const t of r.tables) {
    if (RANK[t.verdict] > RANK[worst]) worst = t.verdict;
    if (RANK[t.verdict] >= RANK.lagging) bad++;
  }
  // v0.10.792 — eşlenemeyen tablo "tutarlı" DEĞİLDİR: karar verilememiştir.
  // 791 test ortamında (IP'li küme tanımı) 89 tablo "eşlenemedi" iken başlık
  // "tutarlı" dedi; yanlış güven, yanlış rozetten kötü.
  const unmapped = r.tables.filter(t => t.verdict === 'unmapped').length;
  const tone: ReplicaTone = unmapped > 0 && bad === 0 ? 'b-warn' : verdictTone(worst);
  const text = r.tables.length === 0 ? 'Replicated tablo yok'
    : bad > 0 ? `${bad}/${r.tables.length} tablo sorunlu · ${verdictLabel(worst)}`
    : unmapped > 0 ? `${unmapped}/${r.tables.length} tablo eşlenemedi · karar yok`
    : `${r.tables.length} tablo tutarlı`;
  return { tables: r.tables.length, bad, worst, tone, text };
}

/** ZK yolunun son üç parçası — tabloda okunur; tamamı title'da. */
export function shortZk(path: string): string {
  const parts = path.split('/').filter(Boolean);
  return parts.length > 3 ? '…/' + parts.slice(-3).join('/') : path;
}

/** Kararın runbook'u; ok/single/unmapped için boş. */
export function runbook(cluster: string, db: string, table: string, sh: CHReplicaShard): string {
  const q = (s: string) => `\`${db}\`.\`${s}\``;
  const hosts = sh.replicas.map(r => r.host).join(', ');
  switch (sh.verdict) {
    case 'no_replication': {
      const paths = sh.replicas.map(r => `--   ${r.host}: ${r.zkPath}`).join('\n');
      const peer = sh.replicas[0]?.zkPath ?? '/clickhouse/tables/{shard}/' + table;
      return [
        `-- ${table} · shard ${sh.shard}: replikalar birbirini replike etmiyor (${hosts})`,
        paths,
        `-- 1) Makroları doğrula — aynı shard'ın hostları AYNI {shard}, FARKLI {replica} taşımalı:`,
        `SELECT hostName(), macro, substitution FROM clusterAllReplicas('${cluster}', system.macros) ORDER BY 1, 2;`,
        `-- 2) ETKİLENEN host'ta (ON CLUSTER'sız), eşin ZK yolunu paylaşan yeni tablo:`,
        `SHOW CREATE TABLE ${q(table)};`,
        `CREATE TABLE ${q(table + '_fix')} AS ${q(table)} ENGINE = ReplicatedMergeTree('${peer}', '{replica}');`,
        `-- 3) Yerel parçaları taşı (partition başına; eş replikaya replikasyonla gider):`,
        `SELECT DISTINCT partition_id FROM system.parts WHERE database = '${db}' AND table = '${table}' AND active;`,
        `ALTER TABLE ${q(table + '_fix')} ATTACH PARTITION ID '<partition_id>' FROM ${q(table)};`,
        `-- 4) Değiştir, doğrula, eskiyi düşür:`,
        `EXCHANGE TABLES ${q(table)} AND ${q(table + '_fix')};`,
        `-- DROP TABLE ${q(table + '_fix')} SYNC;  -- sayımlar eşitlenince`,
        `-- Bu prosedür veri taşır: DBA gözetiminde, önce test ortamında.`,
      ].join('\n');
    }
    case 'divergent': {
      const low = [...sh.replicas].sort((a, b) => a.totalRows - b.totalRows)[0];
      return [
        `-- ${table} · shard ${sh.shard}: replikalar farklı satır tutuyor (${sh.divergentPartition ?? '?'}: %${(sh.divergencePct ?? 0).toFixed(1)})`,
        `-- Eksik replikada (${low?.host ?? '<host>'}) koş:`,
        `SYSTEM SYNC REPLICA ${q(table)};`,
        `SELECT name, reason FROM system.detached_parts WHERE database = '${db}' AND table = '${table}';`,
        `-- detached parça varsa: ALTER TABLE ${q(table)} ATTACH PART '<name>';`,
        `-- kuyruk erimiyorsa: SYSTEM RESTART REPLICA ${q(table)};`,
      ].join('\n');
    }
    case 'readonly':
    case 'session_expired': {
      const bad = sh.replicas.find(r => r.readonly || r.sessionExpired);
      return [
        `-- ${table} · shard ${sh.shard}: ${bad?.host ?? '<host>'} ${sh.verdict === 'readonly' ? 'readonly' : 'oturumu düşmüş'}`,
        `SELECT is_readonly, is_session_expired, zookeeper_exception, last_queue_update_exception FROM system.replicas WHERE database = '${db}' AND table = '${table}';`,
        `-- Keeper oturumu sağlıklı ama readonly ise (meta kayıp):`,
        `SYSTEM RESTORE REPLICA ${q(table)};`,
        `-- oturum düşmüşse Keeper bağlantısını düzelt; oturum kendiliğinden yenilenir.`,
      ].join('\n');
    }
    default:
      return '';
  }
}
