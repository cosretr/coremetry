// scopeParams.ts — v0.10.1082: /traces listesinin SÜZGEÇ parametreleri tek
// yerde; liste isteği ve Errors şeridi (/api/traces/error-histogram) aynı
// fonksiyondan okur.
//
// Operator-reported (prod): "Error seçildiğinde histogram gelmiyor." Errors +
// `function_code` / `k8s.pod.name` çipiyle liste dolu, şerit 0. Şerit kendi
// süzgecini metric-batch gövdesine elle kuruyordu (çipler + `status = error`
// + giriş kapsamında `kind`) ve hata yüklemi listeninkinden ayrışmıştı. Bu
// sınıfta şerit artık listenin parametrelerini AYNEN yollar; sunucu aynı
// ayrıştırıcıdan (parseTraceFilter) geçirir. SAF; vitest'li.

import type { TracesParams } from '@/lib/api';
import type { FilterExpr, FilterGroup } from '@/lib/types';
import { effectiveTraceSearch } from '@/lib/traceSearchTerm';
import { decodeFilters, decodeFilterGroup } from '@/lib/urlState';
import { stripScope } from '@/components/traces/volumeSeries';

export interface TraceScopeInput {
  filter: {
    service: string; search: string;
    minMs: string; maxMs: string; hasError: boolean; rootOnly: boolean;
    requireServices: string[];
  };
  env: string;
  cluster: string;
  /** İstek çipleri (advFilters + seçilen operasyon, advFiltersEff). */
  filtersEff: FilterExpr[];
  /** Gruplu (OR / iç içe) kodlu kök; düz-AND '' döner. */
  groupParam: string;
}

/** Listenin süzgeç parametreleri (sayfalama / sıralama / pencere hariç). */
export type TraceScopeParams = Omit<TracesParams, 'limit' | 'offset' | 'sort' | 'order' | 'count' | 'from' | 'to' | 'extraAttrs' | 'dsl'>;

/** traceScopeParams — SAF: liste isteğinin süzgeç alanları. */
export function traceScopeParams(i: TraceScopeInput): TraceScopeParams {
  const f = i.filter;
  // v0.10.1423 — Trace ID kutusu kalktı: `traceId` artık sayfadan gitmez.
  // Kimlik (function_id / trace id) operasyon kutusunun `search` terimidir.
  return {
    service: f.service || undefined,
    search: effectiveTraceSearch(f),
    minMs: f.minMs || undefined,
    maxMs: f.maxMs || undefined,
    hasError: f.hasError || undefined,
    rootOnly: f.rootOnly || undefined,
    env: i.env || undefined,
    cluster: i.cluster || undefined,
    services: f.requireServices.length ? f.requireServices : undefined,
    // Gruplu kip düz çipleri supersede eder (sunucu kuralı); ikisi birden gitmez.
    filterGroup: i.groupParam || undefined,
    filters: i.groupParam ? undefined : (i.filtersEff.length ? JSON.stringify(i.filtersEff) : undefined),
  };
}

/**
 * errorStripEligible — SAF: şerit /api/traces/error-histogram'dan mı okunur?
 * Go `chstore.TraceErrorHistogramEligible` aynası: Errors + span-düzeyi çip
 * (düz ya da gruplu), arama / trace id / süre / services YOK. Bu hâlde liste
 * hatayı iki basamakta (çipe uyan hatalı span ↔ trace düzeyi) arar ve şerit
 * aynı kararı sunucuda paylaşır.
 *
 * v0.10.1101 — çipsiz Errors (yalnız servis / ortam / küme) da buraya gelir:
 * metric-batch o hâlde GİRİŞ span'lerinin hatasını sayıyordu (kind kısıtı),
 * liste herhangi bir span'i hatalı trace'leri — istemci / iç span hataları
 * şeritte ve başlıkta yoktu. Sunucu dar rollup'tan (yoksa listenin ham
 * WHERE'iyle) okur. Çipsizken Root metric-batch'te kalır (kök trace düzeyi).
 * Errors kapalı her hâl metric-batch'te (hacim / gecikme fast-path'leri).
 */
export function errorStripEligible(p: TraceScopeParams): boolean {
  if (!p.hasError) return false;
  // Trace-level NOT HAS: the strip stays on metric-batch, which carries the
  // list's anti-join (Go TraceErrorHistogramEligible mirrors this).
  if (scopeHasTraceExclude(p)) return false;
  if (p.search || p.traceId) return false;
  if (p.minMs || p.maxMs) return false;
  if (p.services && p.services.length) return false;
  if (p.filters || p.filterGroup) return true;
  return !p.rootOnly;
}

/**
 * chipStripEligible — SAF (v0.10.1801; Go `traceChipTraceEligible` aynası).
 *
 * Operator-reported (test ortamı): servis + `http.route` + `channel_code`,
 * 24 sa → liste dolu, şerit "0 SPANS · No traces in view to bucket". Liste
 * servisli özel çipi trace düzeyinde keser (çipler farklı span'lerde
 * olabilir); metric-batch iki çipi AYNI span'de AND'liyordu. Errors kapalı +
 * servis + ≥2 çip + spans kapsamı (en az biri giriş-span anahtarı değil) →
 * şerit listenin kümesini /api/traces/histogram'dan (④ çip-trace) okur.
 * Arama / trace id / süre / services / NOT HAS yok. Giriş kapsamı (yalnız
 * http.* / k8s.* …) metric-batch'te kalır: aynı span, tavansız sayım.
 */
export function chipStripEligible(p: TraceScopeParams): boolean {
  if (p.hasError || !p.service) return false;
  if (p.search || p.traceId) return false;
  if (p.minMs || p.maxMs) return false;
  if (p.services && p.services.length) return false;
  if (scopeHasTraceExclude(p)) return false;
  const leaves = scopeChipLeaves(p);
  return leaves.length >= 2 && stripScope(leaves, '') === 'spans';
}

/** scopeChipLeaves — SAF: düz çipler ya da (gruplu kipte) grubun TÜM yaprakları. */
function scopeChipLeaves(p: TraceScopeParams): FilterExpr[] {
  if (p.filterGroup) {
    const g = decodeFilterGroup(p.filterGroup);
    if (!g) return [];
    const walk = (x: FilterGroup): FilterExpr[] => [...x.filters, ...(x.groups ?? []).flatMap(walk)];
    return walk(g);
  }
  return decodeFilters(p.filters);
}

/** scopeHasTraceExclude — SAF: does the scope carry a trace-level NOT HAS chip? */
export function scopeHasTraceExclude(p: TraceScopeParams): boolean {
  const flat = decodeFilters(p.filters);
  const g = decodeFilterGroup(p.filterGroup);
  const leaves = [...flat, ...(g ? [...g.filters, ...(g.groups ?? []).flatMap(s => s.filters)] : [])];
  return leaves.some(f => f.op === 'NOT HAS');
}

/** errorStripScopeOnly — SAF (v0.10.1101): Errors şeridi çipsiz mi (③ kapsam kipi)? İpucu metni için. */
export function errorStripScopeOnly(p: TraceScopeParams): boolean {
  return errorStripEligible(p) && !p.filters && !p.filterGroup;
}
