import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1224 — operatör: Infrastructure'da cluster adına basınca cluster
// sayfasına gidiyordu; ad satır tıkının parçası (pod'ları açar), detay ↗'de.
const src = readFileSync(resolve(__dirname, 'ServiceInfraTab.tsx'), 'utf8');

describe('Infra cluster satırı', () => {
  it('ad hücresi tıkı yutmaz, detay linki ↗', () => {
    expect(src).not.toMatch(/<td className="mono" onClick=\{e => e\.stopPropagation\(\)\}>\s*<Link to=\{entityHref\(\{ type: 'cluster'/);
    expect(src).toMatch(/title="Cluster detayı" aria-label=\{`\$\{r\.cluster\} cluster detayı`\}>↗<\/Link>/);
  });
});

// S0 karakterizasyon (docs/frontend/service-tabs-plan.md §5, K7/K8): satır
// tıkı kapsamı seçer + pod'ları satırın altında açar (?icluster, replace),
// ?jpod= pod'un cluster'ını açar, pod satırı tıkı genişletir / ad bağlantısı
// pod sayfasına gider. S1–S5 bunlara dokunmaz; S6 birleşik tabloda korur.
const pods = readFileSync(resolve(__dirname, 'ServicePodsTable.tsx'), 'utf8');

describe('Infra satır davranışı (S0)', () => {
  it('?icluster satır seçimi, replace:true; seçili satırın altında pod envanteri', () => {
    expect(src).toContain("const icluster = params.get('icluster') ?? ''");
    expect(src).toMatch(/if \(c\) next\.set\('icluster', c\); else next\.delete\('icluster'\);\s*return next;\s*\}, \{ replace: true \}\)/);
    expect(src).toContain("const toggle = () => setICluster(sel ? '' : r.cluster);");
    expect(src).toContain('{...(canOpen ? rowActivation(toggle) : {})}');
    expect(src).toContain('<ServicePodInventory service={service} range={range} cluster={r.cluster} />');
  });
  it('?jpod= pod\'un cluster\'ını açar', () => {
    expect(src).toContain("const jpod = params.get('jpod') ?? ''");
    expect(src).toContain('const effCluster = icluster || jpodCluster ||');
    expect(pods).toContain("const jpod = (params.get('jpod') ?? '').trim();");
  });
  it('pod satırı: tık genişletir, yalnız ad bağlantısı pod sayfasına gider (v0.10.1210)', () => {
    expect(pods).toContain('const onRow = expandable ? () => setOpenKey(open ? null : r.key)');
    expect(pods).toContain("onClick={e => { if ((e.target as HTMLElement).closest('a')) e.stopPropagation(); }}");
    expect(pods).toContain('<Link to={podHref(r)} className="row-link">{r.pod}</Link>');
  });
});

// v0.10.2145 (S6, mockup operatör onayı 2026-11-13) — "Nerede çalışıyor" tek
// tablo: kipler / ikinci pod listesi yok, eski ?psrc / ?pview okunmaz.
const inv = readFileSync(resolve(__dirname, 'ServicePodInventory.tsx'), 'utf8');
const merge = readFileSync(resolve(__dirname, 'podsMerge.ts'), 'utf8');
const code = (s: string) => s.replace(/^\s*\/\/.*$/gm, '').replace(/\{\/\*[\s\S]*?\*\/\}/g, '');

describe('Infra "Nerede çalışıyor" (S6)', () => {
  it('?psrc / ?pview hiçbir yerde okunmaz, codec\'leri silindi (dolgu yok)', () => {
    for (const f of [src, inv, pods, merge]) {
      const c = code(f);
      expect(c).not.toMatch(/['"]psrc['"]|['"]pview['"]/);
      expect(c).not.toMatch(/parsePodSource|parsePodView|filterBySource|groupByCluster/);
    }
  });
  it('kip düğmeleri, `runs in:` şeridi ve alttaki ikinci pod listesi yok', () => {
    expect(code(inv)).not.toMatch(/SegmentedControl|ChainStrip|runs in:|excludeClusters|SectionHead/);
    expect(code(src)).not.toMatch(/excludeClusters|#pods-sec/);
  });
  it('tek tablo: birleşik satırlar (whereRows), Thanos dışı satır cümleyle, kaynak notu sayfa altında', () => {
    expect(src).toContain('const where = useMemo(() => whereRows(clusterRows, merged, podErrors),');
    expect(src).toContain("storageKey: 'svc-infra-where', columns: WHERE_COLS, rows: where,");
    expect(src).toContain("const WHERE_DEFAULT_HIDDEN = ['ns', 'restarts', 'src', 'act'];");
    // "Tüm kolonlar" tercihi tarayıcı başına (S3/S4 ile aynı desen).
    expect(src).toContain("setRaw(STORAGE_KEYS.svcInfraAllCols, v ? '1' : '0')");
    expect(src).toContain('const note = whereNote(r, thanosState);');
    // Kaynak notu Kafka panelinden SONRA (sayfanın altı).
    expect(src.indexOf('<InfraSourceNote')).toBeGreaterThan(src.indexOf('<ServiceKafkaClientsPanel'));
  });
  it('grafik kapsamı yalnız Thanos\'un pod gördüğü cluster (Thanos dışı satır seçimi Thanos\'a sorulmaz)', () => {
    expect(src).toContain("const scopeCluster = clustersWithPods.includes(scopeWant) ? scopeWant : '';");
    expect(src).toContain('const targets = scopeCluster ? [scopeCluster] : clustersWithPods;');
  });
  it('pod tablosu: Kaynak / Cluster / Node / Workload / Net varsayılan gizli', () => {
    expect(inv).toContain("const POD_DEFAULT_HIDDEN = ['src', 'cluster', 'node', 'workload', 'net'];");
  });
});
