// @vitest-environment jsdom
//
// RolloutDrawer.recurring — v0.10.1049, yinelenen anomali ayrımı.
//
// Operatör: "Yinelenen anomali ayrımı: her gece tekrar eden bir anomali artık
// her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor."
//
// NE ÇİVİLİYOR: "Aktif anomaliler" listesi HİÇBİR satırı gizlemez. Sunucunun
// predatesDeploy=true dediği satır (gece işinin 3. gecesi — rollout'tan önce
// de düzenli görülüyordu) listede KALIR ve nötr "yinelenen" işaretini taşır;
// vaka A / B gibi işaretsiz satırlar (predatesDeploy yok) işaretsiz, yeni gibi.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { AnomalyEvent, Problem, RolloutDetail } from '@/lib/types';

const m = vi.hoisted(() => ({ detail: null as unknown }));
vi.mock('@/lib/queries', () => ({
  useRolloutDetail: () => ({ data: m.detail, isPending: false, isError: false }),
  useEntityClusters: () => ({ data: undefined }),
}));

import { RolloutDrawer } from './RolloutDrawer';

const t0 = 1_759_370_400_000; // ms
const anomaly = (over: Partial<AnomalyEvent>): AnomalyEvent => ({
  id: 'x', kind: 'trace_op', pattern: 'p', service: 'batch-svc', startedAt: (t0 + 30 * 60_000) * 1e6,
  lastSeen: (t0 + 40 * 60_000) * 1e6, peakRatio: 4, currentRatio: 3, currentCount: 9, sample: '', status: 'active',
  ...over,
});

function detail(anomalies: AnomalyEvent[], problems: Problem[] = []): RolloutDetail {
  return {
    rollout: {
      clusterId: 'c1', namespace: 'ns', workload: 'batch-svc', kind: 'Deployment', revision: 'r2', startedAt: t0,
      status: 'completed', prevRevision: 'r1', image: 'repo/batch', imageTag: 'v2', prevImage: 'repo/batch', prevImageTag: 'v1',
      firstSpanAt: 0, trafficConfirmedAt: 0, ksmStartedAt: 0, podsReadyAt: 0, ksmNotReadySince: 0, completedAt: 0,
      detectedBy: 'span', spanCount: 1, note: '', updatedAt: t0,
    },
    services: [{
      service: 'batch-svc', health: 'yellow',
      before: { errorRate: 0, p99Ms: 10, throughput: 1 }, after: { errorRate: 0, p99Ms: 12, throughput: 1 },
      problems, anomalies, newErrors: [],
    }],
    since: t0 * 1e6, generatedAt: t0 * 1e6,
  };
}

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null;
  host = null;
  document.body.innerHTML = '';
});

function render() {
  host = document.createElement('div');
  document.body.appendChild(host);
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  act(() => {
    root = createRoot(host!);
    root.render(
      <MemoryRouter>
        <RolloutDrawer id={{ clusterId: 'c1', namespace: 'ns', workload: 'batch-svc', revision: 'r2', startedAt: t0 }} onClose={() => {}} />
      </MemoryRouter>,
    );
  });
}

describe('RolloutDrawer — yinelenen anomali işaretlenir, gizlenmez', () => {
  it('gece işi 3. gece: satır listede + "yinelenen"; vaka A/B satırı işaretsiz', () => {
    m.detail = detail([
      anomaly({ id: 'nightly', pattern: 'nightly-job', episodeCount: 3, firstStartedAt: (t0 - 2 * 86_400_000) * 1e6, predatesDeploy: true }),
      anomaly({ id: 'caseA', pattern: 'broken-by-deploy', episodeCount: 2, firstStartedAt: (t0 - 3_600_000) * 1e6 }),
    ]);
    render();
    const rows = Array.from(document.querySelectorAll('tr')).filter(tr => /nightly-job|broken-by-deploy/.test(tr.textContent ?? ''));
    expect(rows).toHaveLength(2);
    const nightly = rows.find(tr => tr.textContent?.includes('nightly-job'))!;
    const caseA = rows.find(tr => tr.textContent?.includes('broken-by-deploy'))!;
    const mark = nightly.querySelector('[data-recurring]') as HTMLElement | null;
    expect(mark?.textContent).toBe('yinelenen');
    expect(mark?.getAttribute('data-recurring')).toBe('3');
    expect(mark?.title).toMatch(/^Yinelenen anomali: bu 3\. kez, ilk kez /);
    expect(caseA.querySelector('[data-recurring]')).toBeNull();
  });

  // v0.10.1054 — operatör: "Anomaliden terfi eden problem de 'yinelenen'
  // kuralına uysun; bugün deploy'a hâlâ eski kurala göre bağlanıyor." "Deploy'dan
  // beri açık problemler" listesi de HİÇBİR satırı gizlemez: sunucunun
  // predatesDeploy=true dediği terfi Problem'i listede KALIR ve aynı işareti
  // taşır; vaka A ve alarm kuralı işaretsiz.
  it('terfi Problem\'i: gece işi 3. gece listede + "yinelenen"; vaka A ve alarm kuralı işaretsiz', () => {
    const prob = (over: Partial<Problem>): Problem => ({
      id: 'p', ruleId: 'builtin:error_rate', ruleName: 'r', severity: 'warning', service: 'batch-svc', metric: 'm',
      value: 1, threshold: 1, status: 'open', description: '', startedAt: (t0 + 30 * 60_000) * 1e6, ...over,
    });
    m.detail = detail([], [
      prob({ id: 'n', ruleId: 'anomaly-auto:0123456789abcdef', ruleName: 'Anomaly · nightly-job', episodeCount: 3, firstStartedAt: (t0 - 2 * 86_400_000) * 1e6, predatesDeploy: true }),
      prob({ id: 'a', ruleId: 'anomaly-auto:1123456789abcdef', ruleName: 'Anomaly · broken-by-deploy', episodeCount: 2, firstStartedAt: (t0 - 3_600_000) * 1e6 }),
      prob({ id: 'r', ruleName: 'High error rate' }),
    ]);
    render();
    const rows = Array.from(document.querySelectorAll('tr')).filter(tr => /nightly-job|broken-by-deploy|High error rate/.test(tr.textContent ?? ''));
    expect(rows).toHaveLength(3);
    const nightly = rows.find(tr => tr.textContent?.includes('nightly-job'))!;
    const mark = nightly.querySelector('[data-recurring]') as HTMLElement | null;
    expect(mark?.textContent).toBe('yinelenen');
    expect(mark?.getAttribute('data-recurring')).toBe('3');
    for (const t of ['broken-by-deploy', 'High error rate']) {
      expect(rows.find(tr => tr.textContent?.includes(t))!.querySelector('[data-recurring]'), t).toBeNull();
    }
  });
});
