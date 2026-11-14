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
    expect(src).toContain('{...rowActivation(() => setICluster(sel ? \'\' : r.cluster))}');
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
