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
import type { FilterExpr } from '@/lib/types';
import { effectiveTraceSearch } from '@/lib/traceSearchTerm';

export interface TraceScopeInput {
  filter: {
    service: string; search: string; traceId: string;
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
  // Yalnız TAM 32-hex trace id sunucuya gider (önek araması v0.9.82'de
  // kalktı); 32-hex olmayan değer effectiveTraceSearch ile arama terimidir.
  const tid = f.traceId.trim().toLowerCase();
  return {
    service: f.service || undefined,
    search: effectiveTraceSearch(f),
    traceId: /^[0-9a-f]{32}$/.test(tid) ? tid : undefined,
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
  if (p.search || p.traceId) return false;
  if (p.minMs || p.maxMs) return false;
  if (p.services && p.services.length) return false;
  if (p.filters || p.filterGroup) return true;
  return !p.rootOnly;
}

/** errorStripScopeOnly — SAF (v0.10.1101): Errors şeridi çipsiz mi (③ kapsam kipi)? İpucu metni için. */
export function errorStripScopeOnly(p: TraceScopeParams): boolean {
  return errorStripEligible(p) && !p.filters && !p.filterGroup;
}
