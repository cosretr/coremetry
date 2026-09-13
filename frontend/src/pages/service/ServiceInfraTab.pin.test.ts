// ServiceInfraTab.pin.test.ts — v0.10.718 (servis sekmeleri etüdü, Infra
// dilim 1; mockup 3b03fe22 şerh 1-4). Kaynak pinleri: çipler → tablo
// (useDataTable, satır = kapsam, ?icluster aynen, Topbar ?cluster eşleşince
// kapsam), KPI StatTile şeridi, hata yerinde (ChartSlot), HAProxy 3 sütun,
// bölüm başlığı atomu; Kafka paneli aynı atom.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const infra = readFileSync(resolve(__dirname, 'ServiceInfraTab.tsx'), 'utf8');
const kafka = readFileSync(resolve(__dirname, 'ServiceKafkaClientsPanel.tsx'), 'utf8');
const css = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8');

describe('Infra dilim 1 — Clusters tablosu', () => {
  it('çipler gitti, tablo geldi (useDataTable, kalıcı anahtar), satır seçimi ?icluster', () => {
    expect(infra).not.toContain('<Chip');
    expect(infra).toContain("storageKey: 'svc-infra-clusters'");
    expect(infra).toContain("next.set('icluster', c)");
    expect(infra).toContain("rowActivation(() => setICluster(sel ? '' : r.cluster))");
    expect(infra).toContain("sel ? 'row-selected' : ''");
  });
  it("Topbar ?cluster= yalnız Thanos adıyla birebir eşleşince kapsam; rozet kaynağı söyler", () => {
    expect(infra).toContain("const effCluster = icluster || (clustersWithPods.includes(topbarCluster) ? topbarCluster : '')");
    expect(infra).toContain("kapsam: {effCluster}{icluster ? '' : ' · Topbar'}");
  });
  it('cluster hücresi entity linki; Traces → yalnız span cluster adı da görülüyorsa', () => {
    expect(infra).toContain("entityHref({ type: 'cluster', id: r.cluster, name: r.cluster, clusterId: r.cluster }, { range })");
    expect(infra).toContain('spanClusters.includes(r.cluster) && (');
  });
  it('başlık atomu: eşleşme + tazelik + ↻; kesik/hatalı cluster rozetleri', () => {
    expect(infra).toContain('<SectionHead id="infra-clusters" title="Clusters" source="Thanos · kube-state"');
    expect(infra).toContain('cluster tarandı');
    expect(infra).toContain('aria-label="Pod envanterini yenile"');
    expect(infra).toContain('cluster kesildi');
    expect(infra).toContain('cluster yanıt vermedi');
  });
});

describe('Infra dilim 1 — KPI, grafik hatası, HAProxy', () => {
  it('dört StatTile, limit oranı dürüst (bilinmiyorsa yazar), restart bilinmiyorsa —', () => {
    expect((infra.match(/<StatTile /g) ?? []).length).toBe(4);
    expect(infra).toContain("'limit bilinmiyor'");
    expect(infra).toContain("kpi.restarts == null ? '—'");
    expect(css).toContain('.stat-grid {');
    expect(css).toContain('.stat-sub {');
  });
  it('grafik hatası yerinde: ChartSlot beş grafiği sarar', () => {
    expect((infra.match(/<ChartSlot q=/g) ?? []).length).toBe(5);
    expect(infra).toContain('title="Grafik okunamadı"');
  });
  it('HAProxy üç sütun; bölüm başlıkları atom', () => {
    expect(infra).toContain('<div className="grid-3" style={{ display: \'grid\', gap: 14 }}>');
    expect(infra).toContain('<SectionHead id="infra-haproxy" title="Router / HAProxy"');
    expect(infra).toContain('<SectionHead id="infra-resources" title="Kaynak kullanımı" source="Thanos · deploy-trend"');
  });
  it('Kafka paneli aynı başlık atomu; mount sırası korunur (kafka.pin ile birlikte)', () => {
    expect(kafka).toContain('<SectionHead id="infra-kafka" title="Kafka client"');
    expect(kafka).not.toContain('<h3');
    expect(css).toContain('.sec-head .badge { text-transform: none;');
  });
});

// v0.10.719 — Infra dilim 2: grafikler yalnız toplam, kapsam tümü → cluster
// başına seri (useQueries), tek cluster → limit çizgisi; HAProxy aynı;
// Kafka 4 birincil + "tümü ▸" (uyarılı ikincil katlanmaz).
describe('Infra dilim 2 — cluster başına seri, limit çizgisi, Kafka 4+5', () => {
  it('By pod kipi Infra\'dan kalktı; pod başına Pods\'a link', () => {
    expect(infra).not.toContain('byLabel="By pod"');
    expect(infra).not.toContain('cpuByPod');
    expect(infra).toContain('Pod başına → Pods');
  });
  it('kapsam: hedefler = seçili cluster ya da hepsi; her hedef ayrı sorgu, birleşik seri', () => {
    expect(infra).toContain('const targets = effCluster ? [effCluster] : clustersWithPods;');
    expect(infra).toContain("useQueries({ queries: trendQueries('cpu') })");
    expect(infra).toContain("useQueries({ queries: hapQueries('latency') })");
    expect(infra).toContain('mergeClusterSeries(targets, cpuQs.map(q => q.data?.series))');
    expect(infra).toContain("api.clusterDeployTrend(c, effNs, effDeploy, metric, false, cFrom, cTo, trendMdp)");
  });
  it('tek cluster → limit çizgisi (envanter limitleri), kısmi hata rozeti', () => {
    expect(infra).toContain('thresholds={single ? limitThreshold(kpi.cpuLimitCores, fmtCores) : undefined}');
    expect(infra).toContain('thresholds={single ? limitThreshold(kpi.memLimitBytes, fmtBytes) : undefined}');
    expect(infra).toContain('cluster okunamadı');
    expect(infra).toContain('isError: qs.length > 0 && qs.every(q => q.isError)');
  });
  it('Kafka: 4 birincil + tümü ▸; ikincil uyarı taşıyorsa açık başlar', () => {
    expect(kafka).toContain('const KAFKA_PRIMARY_TILES = 4;');
    expect(kafka).toContain('const expanded = showAll || restFlagged;');
    expect(kafka).toContain('tümü (${rest.length}) ▸');
  });
});
