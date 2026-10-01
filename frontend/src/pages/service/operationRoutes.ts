/**
 * operationRoutes — v0.10.1023, Operations (Raw) çıplak HTTP fiili satırlarını
 * rotaya göre açma. Operatör bildirimi: "Operation kısmında POST GET neden
 * detail gözükmüyor, sonra trace'e girince çıkıyor."
 *
 * Bundle'ın operations satırları operation_summary_5m'den gelir (GROUP BY
 * name, rota boyutu yok): http.route'suz enstrümantasyonda span adı yalnız
 * fiildir ve satır çıplak "GET" görünür; Traces listesi aynı span'i
 * v0.10.756'dan beri "GET /metrics" basar. /operations/routes ucu
 * (spanmetrics_1m) çıplak fiil başına (fiil, rota) satırlarını verir; bu modül
 * ham satırı o satırlarla DEĞİŞTİRİR — yalnız tabloda. Bundle satırları
 * (copilot, Overview OpsCard, SpanDetail, ProblemDetail ham adı eşler)
 * dokunulmadan kalır. `name` gerçek span adı olarak kalır; rota `route`
 * alanında, gösterim opDisplayName(name, route).
 *
 * SAF — React/DOM yok; operationRoutes.test.ts tablo-güdümlü pinler.
 */
import type {
  FilterExpr, OperationRoutesFor, OperationRoutesResponse, OperationRoutesResult, OperationRow, OperationSummary,
} from '@/lib/types';
import { isBareHTTPMethod } from '@/lib/opDisplayName';

/**
 * routeRowsForBundle — v0.10.1023 (inceleme R1/R2): bundle'ın ham satırlarına
 * uygulanacak rota satırları, ya da undefined (= bölme yok, ham satırlar
 * aynen). Service.tsx tablonun satırlarını VE sekme rozetini YALNIZ bunun
 * çıktısından kurar. Satır döner ancak:
 *   • Raw kip (Normalized'da bölme yok);
 *   • bundle bir pencere için yüklenmiş, o pencere BU servisin ve env boş
 *     (servis değişirken eski servisin satırları + yeni servisin rotaları
 *     karışmasın; MV'de deploy_env yok);
 *   • sorgu başarılı ve veri GERÇEK (isPlaceholderData değil — küresel
 *     keepPreviousData önceki anahtarın satırlarını verirdi);
 *   • yanıtın damgası (servis, from, to, env) bundle'ınkiyle birebir aynı;
 *   • yanıt kapsıyor ve kesik değil (usableRouteRows).
 */
export function routeRowsForBundle(
  query: { data?: OperationRoutesResult; isPlaceholderData: boolean; isSuccess: boolean },
  opsWindow: OperationRoutesFor | null,
  svc: string,
  normalized: boolean,
): OperationSummary[] | undefined {
  if (normalized || !opsWindow || !svc || opsWindow.svc !== svc || opsWindow.env) return undefined;
  if (!query.isSuccess || query.isPlaceholderData || !query.data) return undefined;
  const f = query.data.for;
  if (f.svc !== opsWindow.svc || f.from !== opsWindow.from || f.to !== opsWindow.to || f.env !== opsWindow.env) {
    return undefined;
  }
  return usableRouteRows(query.data.resp) ?? undefined;
}

/**
 * usableRouteRows — yanıt bölmeye uygun mu: yalnız kapsanmış (covered) ve
 * kesilmemiş (truncated değil) yanıt satır verir. Kapsanmayan pencere (env
 * seçili, spanmetrics_1m kapsamı dışında, <5 dk) ve kesilmiş yanıt (yarım
 * bölme "All" toplamını eksiltirdi) null → tablo bugünkü ham satırları gösterir.
 */
export function usableRouteRows(resp: OperationRoutesResponse | null | undefined): OperationSummary[] | null {
  if (!resp || !resp.covered || resp.truncated) return null;
  return resp.rows ?? null;
}

/**
 * opRowKey — satırın tekil kimliği (React key, açık detay satırı, taze satır
 * araması). Ad tek başına artık tekil değil: bölünmüş "GET" + "/metrics"
 * satırı ile gerçekten "GET /metrics" adlı bir span AYNI gösterim metnini
 * taşır; NUL ayırıcı ("GET\0/metrics" vs "GET /metrics\0") ikisini ayırır.
 * Artık satır (bölmeden gelen rotasız GET) ham "GET" satırıyla AYNI kimliği
 * taşımaz (inceleme R6): ikisi farklı span kümeleri — ham↔bölünmüş geçişinde
 * açık detay paneli öbürüne yapışmasın.
 */
export function opRowKey(op: Pick<OperationRow, 'name' | 'route' | 'splitResidual'>): string {
  return op.name + '\u0000' + (op.route ?? '') + (op.splitResidual ? '\u0000residual' : '');
}

/**
 * expandBareVerbRows — ham satırlardan, adı çıplak fiil olan ve routeRows'ta
 * o fiilin EN AZ BİR dolu-rotalı satırı bulunan her satırı çıkarır, yerine o
 * fiilin rota satırlarını koyar (rotasız artık satır dahil: `route: ''` +
 * splitResidual). Diğer her satır olduğu gibi kalır; eşleşme birebir ad
 * eşitliğiyle (ham "post" gibi küçük harfli bir ad sunucunun büyük harfli
 * IN listesinde yoktur → bölünmez). Ham satırı olmayan fiilin rota satırları
 * eklenmez. Sıra önemsiz (tablo sıralar). split = değiştirilen ham satır sayısı.
 */
export function expandBareVerbRows(
  raw: OperationSummary[],
  routeRows: OperationSummary[] | null | undefined,
): { rows: OperationRow[]; split: number } {
  if (!routeRows || routeRows.length === 0) return { rows: raw, split: 0 };
  const byVerb = new Map<string, OperationSummary[]>();
  for (const r of routeRows) {
    if (!isBareHTTPMethod(r.name)) continue;
    const list = byVerb.get(r.name);
    if (list) list.push(r); else byVerb.set(r.name, [r]);
  }
  const rows: OperationRow[] = [];
  let split = 0;
  for (const op of raw) {
    const group = isBareHTTPMethod(op.name) && !op.route ? byVerb.get(op.name) : undefined;
    if (!group || !group.some(r => (r.route ?? '') !== '')) {
      rows.push(op);
      continue;
    }
    split++;
    for (const r of group) {
      const route = r.route ?? '';
      rows.push(route ? { ...r, route } : { ...r, route: '', splitResidual: true });
    }
  }
  return { rows, split };
}

/**
 * opTraceFilters — satırın Traces/Explore çip listesi (opHref'in kurduğu
 * `{ k, op, v }` şekli). Daima `name = <ad>`; dolu rotada ek olarak
 * `http.route = <rota>`; bölünmeden gelen artık satırda `http.route NOT
 * EXISTS` (filtre DSL'i `NOT (http_route != '')` derler — yalnız rotasız
 * span'ler). Bölünmemiş ham satır yalnız ad çipini taşır (bugünkü davranış).
 */
export function opTraceFilters(op: Pick<OperationSummary, 'name' | 'route'>, splitResidual: boolean): FilterExpr[] {
  const f: FilterExpr[] = [{ k: 'name', op: '=', v: [op.name] }];
  const route = op.route ?? '';
  if (route) f.push({ k: 'http.route', op: '=', v: [route] });
  else if (splitResidual) f.push({ k: 'http.route', op: 'NOT EXISTS', v: [] });
  return f;
}
