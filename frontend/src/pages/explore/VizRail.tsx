import { EXPLORE_VIZ, type ExploreViz } from './model';
import { SegmentedControl } from '@/components/ui'; // v0.10.914 dilim 2 (buton bütünlüğü)

// VizRail — the builder's viz mode picker.
// line / area / bars render on TimeSeriesPanel; stat / toplist render the
// per-series summary (SummaryViz); table is the GroupTable alone; heatmap
// keeps the LatencyHeatmap path (query A).

const VIZ_META: Record<ExploreViz, { icon: string; label: string; hint: string }> = {
  line:    { icon: '∿', label: 'Line',    hint: 'One line per series' },
  area:    { icon: '◪', label: 'Area',    hint: 'Filled line — good for rates' },
  bars:    { icon: '▮', label: 'Bars',    hint: 'Per-bucket bars — good for counts' },
  stacked: { icon: '▟', label: 'Stacked', hint: 'Cum-sum stacked bands — composition over time (DE5)' },
  stat:    { icon: '#', label: 'Stat',    hint: 'Big current value per series' },
  toplist: { icon: '≣', label: 'Top list', hint: 'Series ranked by current value' },
  pie:     { icon: '◔', label: 'Pie',     hint: 'Share of current value per series — donut per query (DE5)' },
  table:   { icon: '▤', label: 'Table',   hint: 'Per-series breakdown table only' },
  heatmap: { icon: '▦', label: 'Heatmap', hint: 'Latency density (time × log-duration) — uses query A' },
};

export function VizRail({ value, onChange, disabled }: {
  value: ExploreViz;
  onChange: (v: ExploreViz) => void;
  /** v0.10.393 — viz → sebep; kapalı düğme sebebi title'da söyler (model.vizDisabledFor). */
  disabled?: Partial<Record<ExploreViz, string>>;
}) {
  return (
    <SegmentedControl aria-label="Görselleştirme" value={value} onChange={onChange}
      options={EXPLORE_VIZ.map(v => ({
        value: v, label: `${VIZ_META[v].icon} ${VIZ_META[v].label}`,
        title: disabled?.[v] ?? VIZ_META[v].hint,
        disabled: !!disabled?.[v] && value !== v,
      }))} />
  );
}
