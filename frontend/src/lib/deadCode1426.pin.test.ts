// deadCode1426.pin.test.ts — v0.10.1426 pini: özne kipiyle ilgisiz ölü kod
// temizliği geri gelmesin.
//
// v0.10.1413 çekmece özne kipinin artıklarını sildi ve "üretimde çağıranı
// olmayan ama o dilime alınmayanları" kuyruğa yazdı: importsuz modüller
// (AggregateFlame, ServiceAttrsPanel, lib/serviceHealth, lib/tableColumns,
// lib/perf worker hattı + streaming), yedi kullanılmayan tip, 28 kullanılmayan
// api.* yöntemi ve hiç okunmayan genel i18n anahtarları (btn.* / label.* /
// sev.* / state.* / nav.<ölü>). v0.10.1426 bunları sildi. Bu pin üç şeyi
// tutar: (1) silinen modül dosyaları yok ve hiçbir kaynak dosya onları import
// etmiyor; (2) api nesnesinde / types.ts'te / i18n kataloğunda silinen adlar
// yok; (3) BİLEREK kalanlar (nav.monitors — sunucu sayfa defteri okur;
// lib/perf'in canlı modülleri) hâlâ yerinde.
import { describe, it, expect } from 'vitest';
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { api } from './api';
import { t } from './i18n';

const SRC = resolve(__dirname, '..');

const DELETED_MODULES = [
  'components/AggregateFlame.tsx',
  'components/ServiceAttrsPanel.tsx',
  'lib/serviceHealth.ts',
  'lib/tableColumns.ts',
  'lib/tableColumns.test.ts',
  'lib/perf/streaming.ts',
  'lib/perf/useTransformWorker.ts',
  'lib/perf/workerClient.ts',
  'lib/perf/transform.worker.ts',
];

// Import yolu parçaları: `from '@/lib/serviceHealth'`, `from './AggregateFlame'`,
// `import('../perf/workerClient')`, `new URL('./transform.worker.ts', …)`.
const DELETED_SPECIFIERS = [
  '/AggregateFlame', '/ServiceAttrsPanel', '/serviceHealth', '/tableColumns',
  '/perf/streaming', '/perf/useTransformWorker', '/perf/workerClient', '/perf/transform.worker',
  './streaming', './useTransformWorker', './workerClient', './transform.worker',
];

// v0.10.2177 (Clusters S9) — clusterNamespaceDetail geri geldi, tüketicisi
// var: namespace detayı görünümü (pages/clusters/NamespaceDetail.tsx).
const REMOVED_API_METHODS = [
  'anomalyRootCause', 'copilotCompareTraces', 'correlations',
  'deployHistory', 'flowTopologyDrawIOURL', 'getMonitor', 'getSLO', 'getTopologyHidden',
  'linkAiConversation', 'metricsCompare', 'operations', 'problemRootCause', 'putAIRates',
  'putFailureSLO', 'putTopologyHidden', 'serviceBlastRadius', 'serviceEnvironments',
  'serviceStructure', 'sloStatus', 'spanBreakdown', 'topology', 'topologyDrawIOURL',
  'topologyEdgeInstances', 'topologyFlow', 'topologyFlows', 'topologyOps', 'unshareAiConversation',
  'serviceAttrs', // tek tüketicisi silinen ServiceAttrsPanel'di
];

const REMOVED_TYPES = [
  'AIExplainStreamAnswer', 'InsightSeverity', 'ClusterSourcesResponse', 'ClusterSourceEntry',
  'ServiceEdgeStats', 'DeploymentReport', 'ArgoCDDiscoverError',
  // Tek tüketicisi silinen api.* yöntemi olan tipler (ve yalnız onların
  // alt tipleri): topology / topologyFlow(s) / spanBreakdown / deployHistory /
  // metricsCompare / serviceStructure.
  'TopologyNode', 'TopologyEdge', 'TopologyResponse',
  'ServiceTopologyNodeKind', 'ServiceTopologyNode', 'ServiceTopologyEdge', 'ServiceTopologyResponse',
  'RootFlow', 'FlowsResponse', 'BreakdownPoint', 'AggSpanNode', 'DeployHistoryRow',
  'MetricCompareSeries', 'MetricCompareReport',
  'ServiceAttrRow', 'ServiceAttrsResponse', // tek tüketicisi api.serviceAttrs → ServiceAttrsPanel zinciriydi
];

const REMOVED_I18N_KEYS = [
  'btn.save', 'btn.cancel', 'btn.delete', 'btn.reset', 'btn.create', 'btn.edit', 'btn.close', 'btn.refresh', 'btn.search',
  'state.loading', 'state.noData', 'state.failed', 'state.tryWidening',
  'label.service', 'label.severity', 'label.status', 'label.started', 'label.duration',
  'label.errorRate', 'label.latency', 'label.count', 'label.value', 'label.threshold',
  'sev.critical', 'sev.warning', 'sev.info', 'sev.open', 'sev.resolved',
  'nav.analysis', 'nav.serviceMap', 'nav.clickhouse', 'nav.elastic', 'nav.cardinality',
  'nav.cluster', 'nav.catalog', 'nav.audit', 'nav.sql', 'nav.query', 'nav.statusPage',
];

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(name)) out.push(p);
  }
  return out;
}

const IMPORT_LINE = /^\s*(import\b|export\b.*\bfrom\b|.*\bimport\(|.*new URL\()/;

describe('v0.10.1426 — ölü kod geri gelmedi', () => {
  it.each(DELETED_MODULES)('%s yok', (rel) => {
    expect(existsSync(join(SRC, rel)), `${rel} geri geldi`).toBe(false);
  });

  it('hiçbir kaynak dosya silinen modülleri import etmiyor', () => {
    const offenders: string[] = [];
    for (const file of walk(SRC)) {
      if (file.endsWith('deadCode1426.pin.test.ts')) continue;
      const lines = readFileSync(file, 'utf8').split('\n');
      lines.forEach((line, i) => {
        if (!IMPORT_LINE.test(line)) return;
        for (const spec of DELETED_SPECIFIERS) {
          if (line.includes(`${spec}'`) || line.includes(`${spec}"`) || line.includes(`${spec}.ts'`)) {
            offenders.push(`${file.slice(SRC.length + 1)}:${i + 1} → ${spec}`);
          }
        }
      });
    }
    expect(offenders).toEqual([]);
  });

  it('lib/perf: canlı modüller yerinde, worker hattı ve dispatcher yok', () => {
    for (const rel of ['lib/perf/lttb.ts', 'lib/perf/transforms.ts', 'lib/perf/routePrefetch.ts',
      'lib/perf/useDebouncedValue.ts', 'lib/perf/useFpsMeter.ts']) {
      expect(existsSync(join(SRC, rel)), `${rel} canlı tüketicisi var, silinemez`).toBe(true);
    }
    const transforms = readFileSync(join(SRC, 'lib/perf/transforms.ts'), 'utf8');
    expect(transforms).toMatch(/export function percentiles\(/);
    for (const gone of ['runTransform', 'TransformRequest', 'TransformResult', 'aggregateBuckets', 'buildFlameTree']) {
      expect(transforms, `${gone} geri geldi`).not.toMatch(new RegExp(`export (function|type|interface) ${gone}\\b`));
    }
  });

  it.each(REMOVED_API_METHODS)('api.%s yok', (name) => {
    expect(name in api, `api.${name} geri geldi — tüketicisi yoktu`).toBe(false);
  });

  it('types.ts: silinen tipler yeniden bildirilmedi', () => {
    const src = readFileSync(join(SRC, 'lib/types.ts'), 'utf8');
    for (const name of REMOVED_TYPES) {
      expect(src, `${name} geri geldi`).not.toMatch(new RegExp(`export (interface|type) ${name}\\b`));
    }
  });

  it('i18n: silinen genel anahtarlar katalogda yok (anahtar olduğu gibi döner)', () => {
    for (const key of REMOVED_I18N_KEYS) {
      expect(t(key, 'en'), `${key} EN kataloğuna geri geldi`).toBe(key);
      expect(t(key, 'tr'), `${key} TR kataloğuna geri geldi`).toBe(key);
    }
  });

  it('i18n: nav.monitors KALDI — internal/api/pages.go sayfa defteri bu anahtarla etiketler', () => {
    expect(t('nav.monitors', 'en')).toBe('Monitors');
    expect(t('nav.monitors', 'tr')).toBe('Monitörler');
  });
});
