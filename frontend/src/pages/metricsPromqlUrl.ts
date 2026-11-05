// metricsPromqlUrl.ts — v0.10.1306: /metrics?editor=1 PromQL sekmesinin URL
// kodeği (saf; MetricQueryEditor.tsx tüketir, metricsPromqlUrl.test.ts pinler).
//
// Kanıt çipi (internal/mcptools/evidence_link.go promqlEditorHref) aracın
// koştuğu VictoriaMetrics / Thanos sorgusunu şu adreste açar:
//
//   /metrics?editor=1&tab=promql&promql=<ifade>&src=vm|ch|thanos
//            &cluster=<id|ad>&range=custom:<fromMs>-<toMs>
//
// URL = gerçeğin kaynağı (/frontend-design-system §4 (eski /frontend-conventions)): editör bu dört
// parametreyi URL'den TOHUMLAR, operatörün değişikliğini replace:true ile
// GERİ yazar (applyPromqlParams) ve içe aktarma promqlSig ile imza-korumalıdır
// — ilgisiz bir URL yazımı (range) yazılan sorguyu silmez (v0.8.253 sınıfı).
//
// Neden `src` ve `metricsrc` ayrı anahtar: `metricsrc` (lib/metricSource.ts)
// sayfanın TÜM metrik isteklerini sabitleyen, hiçbir UI'ın yazmadığı elle
// deneme parametresi. `src` yalnız bu sekmenin TEK PromQL isteğini yönlendirir
// (vm|ch → aynı METRIC_SOURCE_PARAM değeri, withMetricSource ile o isteğe
// damgalanır; thanos → editor+ konsolu /api/promql/query_range).

export type PromqlView = 'builder' | 'code' | 'promql';
/** '' = kurulumun varsayılan metrik backend'i (parametre yok). */
export type PromqlSrc = '' | 'vm' | 'ch' | 'thanos';

export interface PromqlParams {
  tab: PromqlView;
  promql: string;
  src: PromqlSrc;
  cluster: string;
}

/** internal/api maxPromQLQueryLen aynası — her iki uç 8 KiB üstünü reddeder. */
export const PROMQL_MAX_LEN = 8192;

const VIEWS: ReadonlySet<string> = new Set<PromqlView>(['builder', 'code', 'promql']);
const SRCS: ReadonlySet<string> = new Set<PromqlSrc>(['vm', 'ch', 'thanos']);

/**
 * URL → parametreler. Tanınmayan tab 'builder', tanınmayan src '' (varsayılan
 * backend). 8 KiB üstü sorgu DÜŞER (sunucu zaten reddederdi; dev bir URL
 * editöre taşınmaz). `thanos` yalnız cluster ile anlamlı — cluster yoksa src ''.
 */
export function decodePromqlParams(sp: URLSearchParams): PromqlParams {
  const rawTab = sp.get('tab') ?? '';
  const rawSrc = (sp.get('src') ?? '').trim();
  const promql = sp.get('promql') ?? '';
  const cluster = (sp.get('cluster') ?? '').trim();
  let src: PromqlSrc = SRCS.has(rawSrc) ? (rawSrc as PromqlSrc) : '';
  if (src === 'thanos' && !cluster) src = '';
  return {
    tab: VIEWS.has(rawTab) ? (rawTab as PromqlView) : 'builder',
    promql: promql.length > PROMQL_MAX_LEN ? '' : promql,
    src,
    cluster: src === 'thanos' ? cluster : '',
  };
}

/**
 * Parametreleri `prev` üstüne yazar; yabancı paramlar (range, mq, editor,
 * metricsrc…) KORUNUR. Varsayılan/boş değer parametreyi SİLER — temiz editör
 * URL'i temiz kalır, kodek gidiş-dönüş yapar. Sorgu 8 KiB üstündeyse yazılmaz.
 */
export function applyPromqlParams(prev: URLSearchParams, p: PromqlParams): URLSearchParams {
  const next = new URLSearchParams(prev);
  const put = (k: string, v: string) => { if (v) next.set(k, v); else next.delete(k); };
  put('tab', p.tab === 'builder' ? '' : p.tab);
  put('promql', p.promql.length > PROMQL_MAX_LEN ? '' : p.promql);
  const thanos = p.src === 'thanos' && !!p.cluster.trim();
  put('src', p.src === 'thanos' && !thanos ? '' : p.src);
  put('cluster', thanos ? p.cluster.trim() : '');
  return next;
}

/** İmza — YALNIZ bu kodeğin sahip olduğu parametreler (range/mq dahil değil). */
export function promqlSig(p: PromqlParams): string {
  return JSON.stringify([p.tab, p.promql, p.src, p.cluster]);
}
