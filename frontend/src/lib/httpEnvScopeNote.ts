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
import { t, tf, type Lang } from './i18n'; // v0.10.2305 — bant metni isteğe bağlı lang (varsayılan 'tr')

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

// v0.10.2305 — katalogdan (hm.env.tier.* / hm.env.src.*); bilinmeyen değer ham.
const TIERS = new Set(['exact', 'ci', 'prefix', 'entity']); // entity: v0.10.2211
const SOURCES = new Set(['configured', 'default', 'discovered']);
const tierText = (k: string, lang: Lang) => (TIERS.has(k) ? t(`hm.env.tier.${k}`, lang) : k);
const sourceText = (k: string, lang: Lang) => (SOURCES.has(k) ? t(`hm.env.src.${k}`, lang) : k);

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

function termText(tm: HttpEnvTerm, lang: Lang): string {
  const vals = tm.values ?? [];
  const same = vals.length === 1 && vals[0] === tm.want;
  return `${tm.role} ${tm.want}${same ? '' : ` → ${vals.join(' | ')}`}, ${tierText(tm.tier, lang)}`;
}

export interface HttpEnvScopeNote {
  tone: 'info' | 'warning' | 'neutral';
  text: string;
  title: string;
}

/** Bant metni (null = bant yok). */
export function httpEnvScopeNote(v: HttpEnvScopeView, lang: Lang = 'tr'): HttpEnvScopeNote | null {
  const ALL_ENVS = t('hm.env.all', lang);
  const sc = v.scope;
  const terms = sc?.terms ?? [];
  const how = sc?.label
    ? tf('hm.env.how', lang, { l: sc.label, src: sourceText(sc.source, lang) })
      + (sc.partial ? t('hm.env.partial', lang) : '')
    : t('hm.env.howDefault', lang);
  switch (v.state) {
    case 'off':
      return null;
    case 'pending':
      return { tone: 'neutral', text: t('hm.env.pending', lang), title: how };
    case 'applied':
      return {
        tone: 'info',
        text: tf('hm.env.applied', lang, { sel: appliedSelectors(terms), terms: terms.map(tm => termText(tm, lang)).join('; ') }),
        title: `${how} PromQL: ${v.envSel}`,
      };
    case 'unresolved': {
      const miss = terms.find(t => t.tier === 'none');
      const why = miss
        ? tf('hm.env.miss', lang, { l: miss.label || sc?.label || '?', w: miss.want })
        : sc?.reason || t('hm.env.noMatch', lang);
      return { tone: 'warning', text: tf('hm.env.unresolved', lang, { why, all: ALL_ENVS }), title: how };
    }
    case 'error':
      return {
        tone: 'warning',
        text: tf('hm.env.error', lang, { r: v.reason ?? t('hm.env.unknownErr', lang), all: ALL_ENVS }),
        title: how,
      };
  }
}
