import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import type { ChatEvidenceChartPayload } from '@/lib/types';
import { evidenceChartGroups, evidenceHopLabel, evidenceRegions } from './evidenceChip';

// CoSRE faz D — compare_baseline: satır içi grafik ARACIN yükünden çizilir.
// Girdi Go altınıdır (internal/mcptools/testdata/compare_baseline_golden.json):
// sunucunun ürettiği yük ile FE'nin çizdiği noktalar aynı sayılar.
const golden = JSON.parse(readFileSync(resolve(__dirname, '../../../../internal/mcptools/testdata/compare_baseline_golden.json'), 'utf8'));
const chart = golden.data.blocks.find((b: { type: string }) => b.type === 'chart').payload as ChatEvidenceChartPayload;

describe('compare_baseline grafik bloğu', () => {
  it('noktalar araç yükünden birebir (ms → ns), model metni yok', () => {
    const groups = evidenceChartGroups(chart);
    expect(groups).toHaveLength(1);
    expect(groups[0].unit).toBe('ms');
    const pts = groups[0].items[0].series[0].points;
    expect(pts.map(p => [p.time / 1e6, p.value])).toEqual(chart.series[0].points);
  });

  it('inceleme penceresi gölgeli, taban soluk', () => {
    const r = evidenceRegions(chart.regions);
    expect(r.map(x => x.color)).toEqual(['var(--text3)', 'var(--warn)']);
    expect(r[1].fromSec * 1000).toBe(Date.parse(golden.window.from_iso));
    expect(r[1].toSec * 1000).toBe(Date.parse(golden.window.to_iso));
  });

  it('kanıt halkaları "Trace ›" öneki almaz', () => {
    expect(evidenceHopLabel('baseline')).not.toContain('Trace');
    expect(evidenceHopLabel('baseline_chart')).not.toContain('Trace');
    expect(evidenceHopLabel('pods')).toBe('Trace › Pod');
  });
});
