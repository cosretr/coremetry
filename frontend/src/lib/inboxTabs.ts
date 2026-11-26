// inboxTabs — v0.10.2481 (operatör: "yapalım, sade ve güzel"): Problems,
// Incidents ve Anomalies TEK sayfa (/inbox), iki sekme:
//   Problemler (varsayılan; URL'e yazılmaz) · Sinyaller (?tab=signals).
// Sinyaller eski /anomalies listesinin görünümlerini taşır (?view= vb.).
// Eski /anomalies adresi buraya yönlenir (parametreler korunur); detay
// sayfaları (/anomalies/:id, /anomalies/ml/:id) yerinde.
import { urlSpec, z, zParam } from '@/lib/urlSchema';
export { anomaliesListRedirect } from '@/lib/anomaliesRedirect';

export type InboxTab = 'problems' | 'signals';

/** Sinyaller sekmesinin adresi (Go mcptools.SignalsTabHref ile aynı). */
export const SIGNALS_HREF = '/inbox?tab=signals';

/** ?tab= — yalnız "signals" geçerli; başka her değer (eski /anomalies
 *  ?tab=ml|groups dahil) varsayılana (Problemler) düşer ve yazılmaz. */
export const INBOX_TAB_SPEC = urlSpec({
  tab: zParam<string>(z.string().check(z.refine(v => v === 'signals')), ''),
});

/** SAF: URL değeri → sekme. */
export function resolveInboxTab(raw: string | null | undefined): InboxTab {
  return raw === 'signals' ? 'signals' : 'problems';
}

/** Sinyaller sekmesinin sahip olduğu parametreler — Problemler'e dönüşte
 *  silinir (Problemler'in paramları sekme değişiminde hiç dokunulmaz). */
export const SIGNALS_OWNED_PARAMS = ['view', 'page', 'donePage', 'sort', 'doneSort', 'signal', 'attached'] as const;

/** SAF: sekme değişiminin yeni sorgu metni (başında '?' yok). */
export function inboxTabSearch(search: string, tab: InboxTab): string {
  const p = new URLSearchParams(search);
  if (tab === 'signals') {
    p.set('tab', 'signals');
  } else {
    p.delete('tab');
    for (const k of SIGNALS_OWNED_PARAMS) p.delete(k);
  }
  return p.toString();
}

/** SAF: Sinyaller sekmesinde kayıtlı görünümün sorgusu. Eski Anomalies
 *  kayıtları (page='anomalies') ?tab= taşımaz; uygulanınca da Sinyaller'de
 *  açılsın diye sekme eklenir (varsa dokunulmaz). */
export function withSignalsTab(qs: string): string {
  const p = new URLSearchParams(qs);
  if (p.get('tab') === 'signals') return p.toString();
  const out = new URLSearchParams([['tab', 'signals']]);
  for (const [k, v] of p) if (k !== 'tab') out.append(k, v);
  return out.toString();
}
