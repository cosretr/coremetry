// TopologyFlowGraph.perf.test.tsx — İSTEMCİ TARAFI yerleşim bütçesi
// (docs/perf/perf-budget-2026-08-28.md P4, v0.10.116). Tarayıcısız:
// react-dom/server ile render → useMemo yerleşimi + SVG üretimi ölçülür
// (boya hariç). Taban (2026-08-28, laptop): 50/148 → 5 ms · 100/496 →
// 13 ms · 300/3k → 189 ms · 500/10k → 940 ms · 500/20k (sunucu tavanı)
// → 1.906 ms. v0.10.133 komşuluk indeksi (lib/topoBfsLayout.ts) sonrası
// aynı makine: 300/3k → 83 ms · 500/10k → 199 ms · 500/20k → 336 ms
// (5×; kalan maliyet 20k kenarın SVG dizesi, yerleşim değil). Eşik
// tavanda 1.5 s (4× pay) — eski 4 s gevşek kalırdı; medyan of 3.
// v0.10.1012 — eşik makine hızına göre ölçeklenir (layoutBudgetMs).
import { describe, it, expect } from 'vitest';
import { renderToString } from 'react-dom/server';
import { TopologyFlowGraph } from './TopologyFlowGraph';
import type { ServiceMap } from '../lib/types';
import type React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
const qc = new QueryClient({ defaultOptions: { queries: { retry: false, enabled: false } } });
const wrap = (el: React.ReactElement) => <MemoryRouter><QueryClientProvider client={qc}>{el}</QueryClientProvider></MemoryRouter>;

function synth(nodes: number, edgesPerNode: number): ServiceMap {
  const ns = Array.from({ length: nodes }, (_, i) => ({ service: `svc-${i}`, spanCount: 1000 - (i % 997), errorRate: 0 }));
  const es: ServiceMap['edges'] = [];
  for (let i = 0; i < nodes; i++) {
    for (let k = 1; k <= edgesPerNode; k++) {
      const j = (i * 7 + k * 13) % nodes;
      if (j !== i) es.push({ caller: `svc-${i}`, callee: `svc-${j}`, traceCount: 10, spanCount: 50, errorCount: 1 });
    }
  }
  return { nodes: ns, edges: es, sampledFrom: 0, totalSpans: 0 } as ServiceMap;
}

// calibrate — v0.10.1012: makinenin HIZINI ölçen sabit iş (ms). Yerleşimle
// aynı sınıf yük: sayı sıralama + SVG benzeri dize üretimi, tek iş parçacığı.
// Bileşen kodundan BAĞIMSIZ — yerleşimdeki bir gerileme bunu yavaşlatmaz, o
// yüzden bütçeyi makineye göre ölçekleyen dürüst bir cetveldir.
function calibrate(): number {
  const runs: number[] = [];
  for (let r = 0; r < 5; r++) {
    const t0 = performance.now();
    const arr = Array.from({ length: 200_000 }, (_, i) => (i * 2654435761) % 1000003);
    arr.sort((a, b) => a - b);
    let s = '';
    for (let i = 0; i < 60_000; i++) s += `<path d="M${arr[i]} ${i}L${arr[i + 1]} ${i + 1}"/>`;
    if (s.length === 0) throw new Error('calibration optimised away');
    runs.push(performance.now() - t0);
  }
  runs.sort((a, b) => a - b);
  return runs[2];
}

// layoutBudgetMs — v0.10.1012: bütçe makineye göre ÖLÇEKLENİR.
//
// Mutlak 1500 ms bütçe bir laptopta tanımlanmıştı (yerleşim 336 ms, "4× pay,
// CI makinesi dahil"). CI koşucusu o laptoptan ~7× yavaş: aynı kod orada
// istikrarlı 1512–1514 ms (min 1512, max 1658) ölçüyor — gerileme yok (yerelde
// bugün 222 ms), yalnız sabit bütçe yavaş makinenin sınırında duruyordu ve
// bir günde iki sürümün CI'ını kırdı (kırılınca arka uç / güvenlik / lint
// işleri de atlanıyor).
//
// Yeni kural: bütçe = max(1500 ms, 45 × kalibrasyon). 45, bütçenin tanımlandığı
// makinedeki oran (1500 ms / ≈33 ms kalibrasyon) — yani sıkılık AYNI, yalnız
// cetvel makineyle birlikte uzuyor. Hızlı makinede taban 1500 ms kalır (bütçe
// gevşemez); yavaş makinede aynı ORANDA gerileme yine yakalanır.
const LAYOUT_BUDGET_FLOOR_MS = 1500;
const LAYOUT_BUDGET_PER_CALIB = 45;
export function layoutBudgetMs(calibMs: number): number {
  return Math.max(LAYOUT_BUDGET_FLOOR_MS, LAYOUT_BUDGET_PER_CALIB * calibMs);
}

describe('TopologyFlowGraph layout cost (perf probe)', () => {
  it('bütçe makineyle ölçeklenir: hızlı makinede taban, yavaş makinede oran', () => {
    expect(layoutBudgetMs(10)).toBe(1500);
    expect(layoutBudgetMs(33)).toBe(1500);
    expect(layoutBudgetMs(225)).toBe(10125);
    expect(layoutBudgetMs(NaN)).toBeNaN(); // ölçüm bozuksa bütçe de bozuk → test düşer, sessizce geçmez
  });
  it('renders synthetic maps and reports ms', { timeout: 120_000 }, () => {
    const out: string[] = [];
    for (const [n, e] of [[50, 3], [100, 5], [300, 10], [500, 20], [500, 40]] as const) {
      const data = synth(n, e);
      renderToString(wrap(<TopologyFlowGraph data={data} focus={null} hoverNode={null} onHoverNode={() => {}} onSelectNode={() => {}} dropMessaging={false} />));
      const runs: number[] = [];
      for (let r = 0; r < 3; r++) {
        const t0 = performance.now();
        renderToString(wrap(<TopologyFlowGraph data={data} focus={null} hoverNode={null} onHoverNode={() => {}} onSelectNode={() => {}} dropMessaging={false} />));
        runs.push(performance.now() - t0);
      }
      runs.sort((a, b) => a - b);
      out.push(`nodes=${n} edges=${data.edges.length} median=${runs[1].toFixed(1)}ms min=${runs[0].toFixed(1)} max=${runs[2].toFixed(1)}`);
    }
    const calib = calibrate();
    console.log('\nTOPOLOGY_LAYOUT_PERF\n' + out.join('\n') + `\ncalibration=${calib.toFixed(1)}ms`);
    const cap = out[out.length - 1];
    const med = Number(/median=([\d.]+)ms/.exec(cap)?.[1] ?? 'NaN');
    expect(med, `yerleşim bütçesi aşıldı (500/20k): ${cap} calibration=${calib.toFixed(1)}ms budget=${layoutBudgetMs(calib).toFixed(0)}ms`).toBeLessThan(layoutBudgetMs(calib));
  });
});
