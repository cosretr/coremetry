// v0.10.1547 — operatör: "Paneller açık olsun ama series kısmı kapalı gelsin;
// çok fazla series dönen var, panel aşağıya uzuyor; Grafana'daki gibi olsun".
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

const read = (p: string) => readFileSync(new URL(p, import.meta.url), 'utf8');

describe('lejant: kaydırılabilir tablo + Kafka panellerinde kapalı', () => {
  it('lejant tablosu sabit yükseklikli kaydırma kabında', () => {
    expect(read('./PanelLegend.tsx')).toContain('<div className="cp-legend-scroll">');
    // v0.10.1611 — kural katmansız chart.css'e taşındı (shadcn Faz 1 R4, §12 Q4).
    const css = read('../../styles/chart.css');
    expect(css).toMatch(/\.cp-legend-scroll \{ overflow: auto; max-height: \d+px; \}/);
    expect(css).toContain('.cp-legend-scroll thead th { position: sticky; top: 0;');
  });
  it('Kafka panelleri seri lejantını kapalı açar', () => {
    expect(read('../../pages/service/ServiceMessagingTab.tsx')).toContain('legendOpen={false}');
    expect(read('../../features/dependencies/KafkaTopicMetrics.tsx')).toContain('legendOpen={false}');
    expect(read('../../pages/service/httpMetricsViz.tsx')).toContain('legendDefaultOpen={legendOpen}');
  });
});
