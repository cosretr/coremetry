// httpEnvScopeNote.ts — v0.10.1377 (metrik kipi: env/cluster süzgeci VM
// PromQL'inde). SAF: ortam kapsamı (v0.10.1429'dan beri /red-family aile
// envScope'u; eski GET /api/metrics/http-env-scope v0.10.1437'de kaldırıldı) → sayfanın
// durumu (sorgular hangi eşleyiciyle, ne zaman atılır) + bant metni.
//
// Durumlar:
//   off        — env/cluster seçili değil (ya da yüzey kapalı): sorgular
//                bugünküyle bayt bayt aynı, bant yok;
//   pending    — çözüm bekleniyor: sorgular BEKLER (süzgeçsiz bir an bile
//                çizilmez — "önce tüm ortamlar, sonra prod" yanıltır);
//   applied    — envSel = sunucunun matcher'ı, her sorguya girer;
//   unresolved — metrikte bu ortam yok: süzgeçsiz çizilir, bant SÖYLER;
//   error      — etiket okuması / uç başarısız: süzgeçsiz çizilir, bant söyler.
// Sessizce düşen süzgeç yok: uygulanmayan her hâlde "TÜM ortamları kapsıyor".
import type { HttpEnvScope, HttpEnvTerm } from './types';

export type HttpEnvScopeState = 'off' | 'pending' | 'applied' | 'unresolved' | 'error';

export interface HttpEnvScopeView {
  state: HttpEnvScopeState;
  /** PromQL ortam eşleyicisi — YALNIZ applied'da (HttpScope.envSel). */
  envSel?: string;
  scope?: HttpEnvScope;
  /** error hâlinin sebebi (HTTP hatası ya da failed gövdesi). */
  reason?: string;
}

/** Çözüm → görünüm. enabled=false ya da terim yoksa off (sorgular eski hâliyle). */
export function httpEnvScopeView(
  env: string, cluster: string,
  q: { data?: HttpEnvScope; error?: Error | null }, enabled = true,
): HttpEnvScopeView {
  if (!enabled || !(env.trim() || cluster.trim())) return { state: 'off' };
  if (q.error) return { state: 'error', reason: q.error.message };
  const sc = q.data;
  if (!sc) return { state: 'pending' };
  if (sc.failed) return { state: 'error', scope: sc, reason: sc.reason || 'etiket okuması başarısız' };
  if (sc.applied && sc.matcher) return { state: 'applied', envSel: sc.matcher, scope: sc };
  return { state: 'unresolved', scope: sc };
}

/** Sorgular atılabilir mi? (pending dışında her hâl). */
export function httpEnvScopeReady(v: HttpEnvScopeView): boolean {
  return v.state !== 'pending';
}

const TIER_TEXT: Record<string, string> = {
  exact: 'birebir', ci: 'harf duyarsız eşleşme', prefix: 'önek eşleşmesi',
  entity: 'cluster varlığının span ortamları', // v0.10.2211
};

const SOURCE_TEXT: Record<string, string> = {
  configured: 'Ayarlar → Metrik backend\'i → Ortam', default: 'varsayılan', discovered: 'keşif (varsayılan etiketin değeri yok)',
};

/** Etiket başına uygulanan değerler (aynı etikette iki terim → kesişim; Go httpEnvScopeFilters ile aynı). */
function appliedSelectors(terms: HttpEnvTerm[]): string {
  const byLabel = new Map<string, string[]>();
  for (const t of terms) {
    if (!t.label || !t.values?.length) continue;
    const prev = byLabel.get(t.label);
    byLabel.set(t.label, prev ? prev.filter(v => t.values!.includes(v)) : [...t.values]);
  }
  return [...byLabel.entries()].map(([l, vs]) => `${l}=${vs.join('|')}`).join(', ');
}

function termText(t: HttpEnvTerm): string {
  const vals = t.values ?? [];
  const same = vals.length === 1 && vals[0] === t.want;
  return `${t.role} ${t.want}${same ? '' : ` → ${vals.join(' | ')}`}, ${TIER_TEXT[t.tier] ?? t.tier}`;
}

export interface HttpEnvScopeNote {
  tone: 'info' | 'warning' | 'neutral';
  text: string;
  title: string;
}

const ALL_ENVS = 'seriler TÜM ortamları kapsıyor';

/** Bant metni (null = bant yok). */
export function httpEnvScopeNote(v: HttpEnvScopeView): HttpEnvScopeNote | null {
  const sc = v.scope;
  const terms = sc?.terms ?? [];
  const how = sc?.label
    ? `Ortam etiketi ${sc.label} (${SOURCE_TEXT[sc.source] ?? sc.source}); istenen ad canlı değerlere karşı çözülür: birebir → harf duyarsız → tek önek (eu-a ↔ prod-eu-a). Birden çok değer eşleşirse hepsi uygulanır.`
      + (sc.partial ? ' VM değer listesi kısmi döndü.' : '')
    : 'Ortam adı VictoriaMetrics HTTP histogramının ortam etiketine çözülür.';
  switch (v.state) {
    case 'off':
      return null;
    case 'pending':
      return { tone: 'neutral', text: 'Ortam süzgeci çözülüyor…', title: how };
    case 'applied':
      return {
        tone: 'info',
        text: `Ortam süzgeci metrikte uygulandı: ${appliedSelectors(terms)} (${terms.map(termText).join('; ')})`,
        title: `${how} PromQL: ${v.envSel}`,
      };
    case 'unresolved': {
      const miss = terms.find(t => t.tier === 'none');
      const why = miss
        ? `metrikte bu ortam yok: ${miss.label || sc?.label || '?'} değerleri arasında "${miss.want}" eşleşmedi`
        : sc?.reason || 'eşleşme yok';
      return { tone: 'warning', text: `Ortam süzgeci uygulanamadı (${why}) — ${ALL_ENVS}`, title: how };
    }
    case 'error':
      return {
        tone: 'warning',
        text: `Ortam süzgeci çözülemedi (${v.reason ?? 'bilinmeyen hata'}) — süzgeç uygulanmadı, ${ALL_ENVS}`,
        title: how,
      };
  }
}
