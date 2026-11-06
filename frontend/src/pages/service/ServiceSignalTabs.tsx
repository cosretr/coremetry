import { usePivotCtx, withPivotCtx } from '@/lib/pivotContext';
import { useMemo } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { encodeRange } from '@/lib/urlState';
import { timeRangeToNs, fmtNum, isMessagingDep } from '@/lib/utils';
import type { TimeRange, ServiceMap } from '@/lib/types';
import { Spinner } from '@/components/Spinner';
import { useUrlEnv } from '@/lib/useUrlEnv';
import { CompactLogSearch } from '@/components/coremetry/logs/CompactLogSearch'; // barrel değil: uPlot'u yalnız bu sekme yükler
import { TopologyPillGraph, type PillNode, type PillEdge, type PillLevel } from '@/components/TopologyPillGraph';
import { FocusedNeighborhood } from '@/components/topology/FocusedNeighborhood';
import { parseTopologyHops, topologyHopsUrlValue } from './topologyHops';
import { serviceHref } from '@/lib/serviceHref';

// Service-scoped Logs / Topology tabs — the design's tab strip beyond
// Overview/Operations/Details. All read-only, all reuse the app-wide
// primitives so the operator's eye builds the same scan pattern as the
// standalone surfaces.
//
// v0.9.212 — the Traces tab that used to live here is gone: it was a 25-row
// "slowest traces" table plus an "Open in Traces →" link, and the service
// header's ⋮ Traces chip already lands on that same page.

// v0.10.1900 (Log Search Faz 3 dilim 3.10; audit §11) — Logs sekmesi kendi
// ince kabuğu (arama kutusu + seviye çipleri + 200 satır) yerine Log Search
// bileşiğinin KOMPAKT, servise sabitlenmiş kipini takar
// (coremetry/logs/CompactLogSearch). Açılış ES bütçesi eskisiyle aynı:
// liste + histogram. URL paramları (`lq`, `llvl`) aynen; sayfanın memo'lu
// penceresi (v0.9.361) aynen geçer.
export function ServiceLogsTab({ service, range, windowNs, onZoom, onZoomReset }: {
  service: string; range: TimeRange;
  // v0.9.373 — histogram brush'ı sayfanın zoom yoluna bağlar (unix-SANİYE).
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
  // v0.9.361 — sayfanın ZATEN memo'lu penceresi: sekme dönüşünde taze
  // nanosaniye basılıp liste + histogram önbellekleri ıskalanmasın.
  windowNs?: { from: number; to: number };
}) {
  const local = useMemo(() => timeRangeToNs(range), [range]);
  const { from, to } = windowNs ?? local;
  // v0.9.453 — global ortam süzgeci bu sekmede de (liste + histogram).
  const [env] = useUrlEnv();
  return (
    <CompactLogSearch service={service} range={range} from={from} to={to} env={env || undefined}
      onZoom={onZoom} onZoomReset={onZoomReset} rangeParam={encodeRange(range)} />
  );
}

// buildPillTiers — project a (2-hop-scoped) ServiceMap onto the shared
// pill renderer. Kafka/broker nodes are dropped (isMessagingDep — the same
// exclusion the circle graph applied) so a broadcast topic can't explode
// the layout; survivors lay out in tiers around the focus:
// upstream callers | focus | direct deps | 2nd-hop backends.
function buildPillTiers(map: ServiceMap, focus: string): {
  nodes: PillNode[]; edges: PillEdge[]; columns: string[][];
} {
  const live = map.nodes.filter(n => !isMessagingDep(n.kind, n.subkind));
  const byId = new Map(live.map(n => [n.service, n] as const));
  const edges = map.edges.filter(e => byId.has(e.caller) && byId.has(e.callee));

  const lvl = (er: number): PillLevel => (er > 0.05 ? 'red' : er > 0.01 ? 'amber' : 'green');
  const nodes: PillNode[] = live.map(n => ({
    id: n.service,
    name: n.subkind || n.service.replace(/^(db|queue|ext):/, ''),
    level: lvl(n.errorRate),
    sub: `${(n.errorRate * 100).toFixed(2)}% · ${fmtNum(n.spanCount)}`,
    title: `${n.service} · ${fmtNum(n.spanCount)} spans · ${(n.errorRate * 100).toFixed(2)}% err`,
  }));
  const pillEdges: PillEdge[] = edges.map(e => {
    const er = Math.max(byId.get(e.caller)?.errorRate ?? 0, byId.get(e.callee)?.errorRate ?? 0);
    return { from: e.caller, to: e.callee, level: er > 0.05 ? 'err' : er > 0.01 ? 'warn' : undefined };
  });

  // Tiers: callers | focus | direct deps | 2nd-hop. Each node placed once.
  const placed = new Set<string>();
  const callers: string[] = [];
  for (const e of edges) if (e.callee === focus && e.caller !== focus && byId.has(e.caller) && !placed.has(e.caller)) { callers.push(e.caller); placed.add(e.caller); }
  placed.add(focus);
  const deps: string[] = [];
  for (const e of edges) if (e.caller === focus && e.callee !== focus && byId.has(e.callee) && !placed.has(e.callee)) { deps.push(e.callee); placed.add(e.callee); }
  const depSet = new Set(deps);
  const second: string[] = [];
  for (const e of edges) if (depSet.has(e.caller) && byId.has(e.callee) && !placed.has(e.callee)) { second.push(e.callee); placed.add(e.callee); }
  for (const n of live) if (!placed.has(n.service)) { second.push(n.service); placed.add(n.service); }
  const columns = [callers, [focus], deps, second].filter(c => c.length > 0);
  return { nodes, edges: pillEdges, columns };
}

// DEFAULT_TOPOLOGY_HOPS — servis Topology sekmesinin varsayılan komşuluk
// derinliği (v0.9.558, operatör talebi).
//
// Tek yerde durur çünkü İKİ yerde kullanılıyor: URL'den okurken
// geri-düşüş değeri ve URL'e yazarken "varsayılansa yazma" kararı.
// İkisi ayrışırsa kullanıcının seçimi sessizce yutulur — sabiti
// bölmek, tam da o hatanın davetiyesidir.
const DEFAULT_TOPOLOGY_HOPS = 2;

// ── Topology: tiered pill-card neighbourhood (shared TopologyPillGraph) ──
export function ServiceTopologyTab({ service, range }: { service: string; range: TimeRange }) {
  const pctx = usePivotCtx(); // v0.10.1237
  const navigate = useNavigate();
  const rangeParam = encodeRange(range);
  // v0.8.93 — render the SAME focused topology as the /topology page
  // (FocusedNeighborhood) so the service tab and the full page show one
  // identical graph (was a different ServiceGraph neighborhood view).
  // v0.9.381 (redesign D5) — hops + errorsOnly URL'e taşındı (?hops=2&
  // eonly=1): sekme değişiminde/paylaşımda kaybolmuyordu iddiası artık
  // doğru. replace:true; yabancı paramlar korunur (prev kopyalanır).
  // v0.9.558 — varsayılan 2 hop (operatör: "topology sekmesinde
  // servicelerin direkt 2 hop gelsin").
  //
  // Gerekçe: 1 hop yalnız doğrudan komşuları gösterir ve bir servisin
  // sorunu çoğu zaman komşusunun komşusundan gelir — operatörün her
  // seferinde elle 2'ye çıkarması gerekiyordu.
  //
  // Maliyet sınırlı: ReadServiceTopologyAggForFocus hop-farkında ve
  // hop başına BİR sınırlı MV sorgusu yapıyor (api katmanında 3'e
  // kırpılı), yani 2 hop = 2 sorgu. Kullanıcı kontrolden 1'e
  // düşürebilir.
  const [tparams, setTparams] = useSearchParams();
  const hops = Math.min(3, Math.max(1,
    parseInt(tparams.get('hops') ?? String(DEFAULT_TOPOLOGY_HOPS), 10) || DEFAULT_TOPOLOGY_HOPS));
  const errorsOnly = tparams.get('eonly') === '1';
  const setHops = (h: number) => setTparams(prev => {
    const next = new URLSearchParams(prev);
    // Karşılaştırma VARSAYILANLA yapılır, sabit 1'le değil. Eskiden
    // `h > 1` yazıyordu; varsayılan 2 olunca bu, kullanıcının 1 hop
    // seçimini URL'den siler ve okuma yine 2 döndürürdü — seçim
    // sessizce yutulurdu. Bu repoda üç kez tekrarlamış tek-yön-okuma
    // hata sınıfı (v0.8.256/265/267).
    if (h !== DEFAULT_TOPOLOGY_HOPS) next.set('hops', String(h)); else next.delete('hops');
    return next;
  }, { replace: true });
  const setErrorsOnly = (v: boolean) => setTparams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('eonly', '1'); else next.delete('eonly');
    return next;
  }, { replace: true });
  return (
    <div className="card" style={{ marginTop: 4 }}>
      <div className="ov-card-h">
        <h3>Topology</h3>
        <span className="ov-sub">{service} neighborhood</span>
        <span className="ov-right">
          <Link className="ov-sub" to={`/topology?focus=${encodeURIComponent(service)}&range=${rangeParam}`}>
            Open full Topology →
          </Link>
        </span>
      </div>
      <div className="ov-card-b">
        <FocusedNeighborhood
          range={range}
          focus={service}
          hops={hops}
          errorsOnly={errorsOnly}
          onHops={setHops}
          onErrorsOnly={setErrorsOnly}
          onRecenter={(s) => navigate(serviceHref(s, { range, tab: 'topology' }))}
          onClear={() => navigate(withPivotCtx(`/topology?range=${rangeParam}`, pctx, { carry: ['env'] }))}
        />
      </div>
    </div>
  );
}
