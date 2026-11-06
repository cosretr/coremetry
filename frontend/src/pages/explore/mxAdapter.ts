// pages/explore/mxAdapter.ts — v0.10.1866 (Metrics Explorer Faz 2 dilim 2.6;
// audit §1 öneri 3 "tek sorgu bileşeni").
//
// Explore'un metrik satırı artık ortak `coremetry/metrics/QueryRow`'u ÇİZER;
// Explore'un modeli (BuilderQuery, `?q=`) ve veri yolu (/api/metrics/query)
// DEĞİŞMEZ. Bu dosya iki model arasındaki köprü:
//
//   toMx(q)       BuilderQuery → MxQuery (yalnız çizim için)
//   fromMx(mx, q) MxQuery → BuilderQuery: değişmeyen alan ÖNCEKİ değerini
//                 aynen korur. Kabul ölçütü bu: var olan `?q=` linkleri aynı
//                 sonucu verir — dokunulmamış bir satır bayt-aynı kalır,
//                 düzenlenen alan eşdeğer biçime çevrilir.
//
// Anahtarlar OLDUĞU GİBİ (promLabel yok): Explore'un sunucu yolu VM'de
// anahtarı kendisi çevirir, CH'de noktalı anahtarı okur.
// Explore'un /api/metrics/query'si yalnız şu fonksiyon × toplama
// birleşimlerini ifade eder; QueryRow'a seçenek kısıtı olarak geçer.
import type { FilterExpr } from '@/lib/types';
import { mxFromExploreQuery } from '@/lib/metricsExplorer/legacy';
import type { MxAgg, MxFilter, MxFn, MxQuery } from '@/lib/metricsExplorer/model';
import type { BuilderQuery } from './model';

export const EXPLORE_FNS: MxFn[] = ['raw', 'rate', 'increase', 'p50', 'p95', 'p99'];
export const EXPLORE_AGGS: MxAgg[] = ['sum', 'avg', 'min', 'max'];
/** Hız ve artış sunucuda hep `sum` ile toplanır (promAggregator). */
export const EXPLORE_FIXED_SUM: MxFn[] = ['rate', 'increase'];

/** Explore scope'u (servis iğnesi) satırda bu anahtarlı süzgeç çipi olur. */
const SCOPE_KEYS = ['service.name', 'service_name'];

export function toMx(q: BuilderQuery): MxQuery {
  const mx = mxFromExploreQuery({ ...q, scope: '' }, [], { verbatim: true });
  return q.scope ? { ...mx, filters: [{ k: 'service.name', op: '=', v: q.scope }, ...mx.filters] } : mx;
}

/** Explore agg adı (fonksiyon × toplama). */
export function exploreAggOf(fn: MxFn, agg: MxAgg): string {
  if (fn === 'rate' || fn === 'increase') return fn;
  if (fn === 'p50' || fn === 'p95' || fn === 'p99') return fn;
  if (fn === 'p90') return 'p95';
  return agg === 'none' || agg === 'count' ? 'avg' : agg;
}

function toExpr(f: MxFilter): FilterExpr {
  if (f.v === '' && f.op === '!=') return { k: f.k, op: 'EXISTS', v: [] };
  if (f.v === '' && f.op === '=') return { k: f.k, op: 'NOT EXISTS', v: [] };
  // =~ / !~ : sunucu iki yolda da kabul eder (chstore.ValidateFilters, vmetrics promMatcher).
  return { k: f.k, op: f.op as FilterExpr['op'], v: [f.v] };
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

export function fromMx(mx: MxQuery, prev: BuilderQuery): BuilderQuery {
  const before = toMx(prev);
  const next: BuilderQuery = { ...prev, enabled: !mx.hidden };
  if (mx.metric !== before.metric) {
    next.metric = mx.metric;
    next.unit = mx.unit ?? '';
  }
  if (mx.fn !== before.fn || mx.agg !== before.agg) next.agg = exploreAggOf(mx.fn, mx.agg);
  if (!same(mx.filters, before.filters)) {
    const scopeF = mx.filters.find(f => SCOPE_KEYS.includes(f.k) && f.op === '=' && f.v);
    next.scope = scopeF?.v ?? '';
    next.filters = mx.filters.filter(f => f !== scopeF).map(toExpr);
  }
  if (!same(mx.by, before.by)) next.splitBy = mx.by;
  return next;
}

