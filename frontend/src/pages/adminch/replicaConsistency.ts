/**
 * replicaConsistency — "Replika tutarlılığı" kartının saf yarısı (v0.10.791).
 * Kararlar sunucuda (chstore.replicaVerdict); burada rozet tonu, etiket,
 * özet ve kararın runbook metni. Runbook operatörün kopyalayıp DBA ile
 * koşacağı SQL: ürün ZK yolu uyuşmazlığını kendi düzeltmez (veri taşıma).
 */
import type { CHReplicaConsistencyResponse, CHReplicaMissingHost, CHReplicaShard, CHReplicaVerdict } from '@/lib/types';

export type ReplicaTone = 'b-ok' | 'b-warn' | 'b-err' | 'b-gray';

export function verdictTone(v: CHReplicaVerdict): ReplicaTone {
  switch (v) {
    case 'ok': return 'b-ok';
    case 'single':
    case 'unmapped': return 'b-gray';
    case 'lagging': return 'b-warn';
    default: return 'b-err'; // divergent · readonly · session_expired · missing_replica · not_replicated · no_replication
  }
}

const LABEL: Record<CHReplicaVerdict, string> = {
  ok: 'tutarlı', single: 'tek replika', unmapped: 'eşlenemedi', lagging: 'geride',
  divergent: 'ıraksama', readonly: 'readonly', session_expired: 'oturum düşmüş',
  missing_replica: 'eksik replika', not_replicated: 'replike değil', no_replication: 'replikasyon yok', // v0.10.818
};
export const verdictLabel = (v: CHReplicaVerdict): string => LABEL[v];

// Sıralama: sunucudaki replicaVerdictRank ile aynı (kötü önce listelenir).
const RANK: Record<CHReplicaVerdict, number> = {
  ok: 0, single: 1, unmapped: 2, lagging: 3, divergent: 4, readonly: 5, session_expired: 6,
  missing_replica: 7, not_replicated: 8, no_replication: 9, // v0.10.818
};
export const verdictRank = (v: CHReplicaVerdict) => RANK[v];

export interface ReplicaSummary { tables: number; bad: number; single: number; worst: CHReplicaVerdict; tone: ReplicaTone; text: string }

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
  // v0.10.818 — "tek replika" da tutarlı değildir: yedeklilik yok, ıraksama
  // ölçülemez. (2×2 kümede eksik eş artık missing_replica/not_replicated → sorunlu;
  // single yalnız gerçekten tek-replikalı shard'larda kalır.)
  const single = r.tables.filter(t => t.verdict === 'single').length;
  const tone: ReplicaTone = (unmapped > 0 || single > 0) && bad === 0 ? 'b-warn' : verdictTone(worst);
  const text = r.tables.length === 0 ? 'MergeTree tablo yok'
    : bad > 0 ? `${bad}/${r.tables.length} tablo sorunlu · ${verdictLabel(worst)}`
    : unmapped > 0 ? `${unmapped}/${r.tables.length} tablo eşlenemedi · karar yok${single > 0 ? ` · ${single} tek replika` : ''}`
    : single > 0 ? `${single}/${r.tables.length} tablo tek replika · yedeklilik yok`
    : `${r.tables.length} tablo tutarlı`;
  return { tables: r.tables.length, bad, single, worst, tone, text };
}

/**
 * v0.10.820 — "Onar" görünürlüğü: yalnız eksik/replike-olmayan kararda, o host'ta
 * tablo yok ya da düz (Replicated-ama-kayıtsız host onarılmaz, yeniden ölçülür),
 * shard'da sağlam Replicated eş var (kaynak DDL), MV iç tablosu değil.
 */
export function canRepair(table: string, sh: CHReplicaShard, m: CHReplicaMissingHost): boolean {
  if (table.startsWith('.inner') || table.endsWith('_fix')) return false; // `_fix` sihirbazın geçici tablosu: sahibinin satırında Temizle
  if (sh.verdict !== 'missing_replica' && sh.verdict !== 'not_replicated') return false;
  if (m.engine && m.engine.startsWith('Replicated')) return false;
  return (sh.replicas ?? []).some(r => (r.engine ?? '').startsWith('Replicated') && !r.readonly && !r.sessionExpired);
}

export const repairModeLabel = (mode: string): string =>
  mode === 'plain' ? 'düz tablo → Replicated: `_fix` kur, partition\'ları ATTACH et, EXCHANGE ile değiştir' : 'tablo yok → eşten klon: aynı ZK yolunda CREATE, parçalar eşten çekilir';

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
    case 'missing_replica':
    case 'not_replicated': {
      // v0.10.818 — eksik host'ta tabloyu eş ZK yolunda AYNI motor ailesiyle
      // (ReplicatedReplacingMergeTree(version) / ReplicatedAggregatingMergeTree…)
      // kur (ON CLUSTER'sız); düz tablo varsa canlı tabloyu ÖNCE kenara almadan
      // `_fix` kur, ATTACH ile taşı, EXCHANGE ile değiştir (inceleme 2026-09-19).
      const missingHosts = (sh.missing ?? []).filter(m => !m.engine || !m.engine.startsWith('Replicated'));
      const miss = missingHosts.map(m => `${m.host}${m.engine ? ` (engine=${m.engine})` : ' (tablo yok)'}`).join(', ') || '<host>';
      const peerRep = (sh.replicas ?? [])[0];
      const peer = peerRep?.zkPath ?? '/clickhouse/tables/{shard}/' + table;
      const peerHost = peerRep?.host ?? '<eş host>';
      const peerReplica = peerRep?.replicaName ?? '<eş replica adı>';
      const engineFamily = peerRep?.engine ?? "<SHOW CREATE'teki Replicated* motor>";
      const plain = missingHosts.some(m => !!m.engine);
      const engineLine = `ENGINE = ${engineFamily}('${peer}', '{replica}'${engineFamily.includes('Replacing') ? ', version' : ''}) …;  -- SHOW CREATE'teki aile + argümanlar AYNEN (farklı motor eş yola katılamaz)`;
      return [
        `-- ${table} · shard ${sh.shard}: ${sh.verdict === 'not_replicated' ? 'tablo Replicated değil' : 'tablo yok'} → ${miss}`,
        `-- 0) Host kendini küme tanımında görüyor mu? Satır yok ya da 0 ise ON CLUSTER DDL bu host'ta uygulanmaz (küme tanımını host adıyla düzelt):`,
        `SELECT hostName(), countIf(is_local) FROM clusterAllReplicas('${cluster}', system.clusters) WHERE cluster = '${cluster}' GROUP BY 1;`,
        `-- 1) Makrolar: eksik host'un {shard} değeri eşle AYNI, {replica} değeri eşinkinden (${peerReplica}) FARKLI olmalı; aynıysa CREATE 'Replica already exists' der:`,
        `SELECT hostName(), macro, substitution FROM clusterAllReplicas('${cluster}', system.macros) WHERE macro IN ('shard','replica') ORDER BY 1, 2;`,
        `-- 2) Sağlam eşte (${peerHost}) tanımı al:`,
        `SHOW CREATE TABLE ${q(table)};`,
        ...(plain ? [
          `-- 3) EKSİK host'ta aynı ZK yolunda Replicated tabloyu _fix adıyla kur (ON CLUSTER'sız; canlı tabloya dokunma):`,
          `CREATE TABLE ${q(table + '_fix')} (…SHOW CREATE'teki kolonlar…) ${engineLine}`,
          `-- 4) Yerel veriyi taşı (partition başına; eşe replikasyonla gider):`,
          `SELECT DISTINCT partition_id FROM system.parts WHERE database = '${db}' AND table = '${table}' AND active;`,
          `ALTER TABLE ${q(table + '_fix')} ATTACH PARTITION ID '<partition_id>' FROM ${q(table)};`,
          `-- 5) Değiştir:`,
          `EXCHANGE TABLES ${q(table)} AND ${q(table + '_fix')};`,
        ] : [
          `-- 3) EKSİK host'ta aynı ZK yolunda Replicated tabloyu (ON CLUSTER'sız) oluştur; parçalar eşten çekilir:`,
          `CREATE TABLE ${q(table)} (…SHOW CREATE'teki kolonlar…) ${engineLine}`,
        ]),
        `-- 6) Doğrula:`,
        `SYSTEM SYNC REPLICA ${q(table)};`,
        `SELECT hostName(), total_replicas, active_replicas FROM clusterAllReplicas('${cluster}', system.replicas) WHERE database = '${db}' AND table = '${table}';`,
        ...(plain ? [`-- DROP TABLE ${q(table + '_fix')} SYNC;  -- EXCHANGE sonrası eski düz tablo _fix adında; sayımlar eşitlenince`] : []),
        `-- Bu prosedür veri taşır: DBA gözetiminde, önce test ortamında.`,
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
