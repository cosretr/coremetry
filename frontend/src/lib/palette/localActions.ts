// lib/palette/localActions — v0.10.1665 (etkileşim dilimi P3, plan §1.2, Q2
// onaylı: altısının hepsi; yenileme aralığı DIŞARIDA). ⌘K'nın YEREL eylemleri:
// hiçbiri sunucu durumu yazmaz — gezinme / yerel tercih / pano. lib/actions
// ACTIONS (durum değiştiren altı eylem, Q12) ayrı kalır ve "changes state"
// etiketini taşır; bunlar etiketsiz.
//
// SAF: kayıt, süzme/sıralama ve bağlam türetme burada (tablo-sürümlü testi
// localActions.test.ts); yan etkiler (pano, tema, yoğunluk, CoSRE olayı,
// yardım penceresi, sabitleme) gövdede `runLocalAction` ile bağlanır.
import { absoluteShareHref } from '@/lib/shareUrl';
import { THEMES, THEME_LABEL, type Theme } from '@/lib/theme';
import { DENSITY_STEPS, type Density } from '@/lib/density';

export type LocalActionId =
  | 'copy-link' | 'ask-cosre' | 'shortcuts-help' | 'pin-service' | 'unpin-service'
  | `theme-${Theme}` | `density-${Density}`;

export interface LocalActionCtx {
  /** Mutlak URL: origin + pathname + search + hash. */
  href: string;
  nowMs: number;
  /** URL'deki servis (`/service?name=`), yoksa null. */
  service: string | null;
  servicePinned: boolean;
  copilotEnabled: boolean;
  theme: Theme;
  density: Density;
}

export interface LocalActionSpec {
  id: LocalActionId;
  label: string;
  hint: string;
  keywords: string[];
}

const DENSITY_LABEL: Record<Density, string> = { comfortable: 'Comfortable', compact: 'Compact', dense: 'Dense' };

/** Bağlama göre görünen yerel eylemler (sıra = kayıt sırası). */
export function localActionCatalog(ctx: LocalActionCtx): LocalActionSpec[] {
  const out: LocalActionSpec[] = [
    { id: 'copy-link', label: 'Copy link to this view', hint: 'Absolute time window — opens the same data', keywords: ['copy', 'link', 'share', 'url', 'paylaş', 'kopyala'] },
  ];
  if (ctx.copilotEnabled) {
    out.push({ id: 'ask-cosre', label: 'Ask CoSRE', hint: ctx.service ? `About ${ctx.service}` : 'Open CoSRE', keywords: ['ask', 'cosre', 'ai', 'copilot', 'sor'] });
  }
  for (const th of THEMES) {
    if (th === ctx.theme) continue;
    out.push({ id: `theme-${th}`, label: `Theme: ${THEME_LABEL[th]}`, hint: 'Switch theme', keywords: ['theme', 'tema', th, THEME_LABEL[th].toLowerCase()] });
  }
  for (const d of DENSITY_STEPS) {
    if (d === ctx.density) continue;
    out.push({ id: `density-${d}`, label: `Density: ${DENSITY_LABEL[d]}`, hint: 'Switch table density', keywords: ['density', 'yoğunluk', d] });
  }
  out.push({ id: 'shortcuts-help', label: 'Keyboard shortcuts', hint: 'Show the ? help', keywords: ['shortcuts', 'keyboard', 'help', 'kısayol', 'yardım'] });
  if (ctx.service) {
    out.push(ctx.servicePinned
      ? { id: 'unpin-service', label: `Unpin ${ctx.service}`, hint: 'Remove from ★ pinned services', keywords: ['unpin', 'pin', 'star'] }
      : { id: 'pin-service', label: `Pin ${ctx.service}`, hint: 'Add to ★ pinned services', keywords: ['pin', 'star', 'favorite', 'sabitle'] });
  }
  return out;
}

/**
 * Sorguya göre süz + sırala — `filterActions` (lib/actions) ile AYNI puanlama:
 * boş sorgu → hiçbiri (eylemler yalnız fiil yazılınca görünür).
 */
export function filterLocalActions(ctx: LocalActionCtx, query: string): LocalActionSpec[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  return localActionCatalog(ctx)
    .map((a, i) => {
      const label = a.label.toLowerCase();
      let score = 0;
      if (a.id.startsWith(q)) score = 500;
      else if (label.startsWith(q)) score = 400;
      else if (a.keywords.some(k => k.startsWith(q))) score = 300;
      else if (a.id.includes(q) || label.includes(q)) score = 100;
      else if (a.keywords.some(k => k.includes(q))) score = 80;
      return { a, score, i };
    })
    .filter(x => x.score > 0)
    .sort((x, y) => y.score - x.score || x.i - y.i)
    .map(x => x.a);
}

/** `/service?name=X` → X; başka sayfa / boş ad → null. */
export function serviceFromLocation(pathname: string, search: string): string | null {
  if (pathname.replace(/\/+$/, '') !== '/service') return null;
  const n = new URLSearchParams(search).get('name');
  return n && n.trim() ? n : null;
}

/** copy-link: göreli pencere mutlak ms'ye dondurulur (alıcı aynı veriyi görür). */
export function shareLinkFor(ctx: Pick<LocalActionCtx, 'href' | 'nowMs'>): string {
  return absoluteShareHref(ctx.href, ctx.nowMs);
}

/** ask-cosre: composer'a düşen (GÖNDERİLMEYEN) metin — sayfanın öznesi. */
export function askCosreQuestion(service: string | null): string {
  return service ? `${service}: ` : '';
}

export interface LocalActionEffects {
  copy(text: string): Promise<boolean>;
  prefill(question: string): void;
  theme(t: Theme): void;
  density(d: Density): void;
  shortcutsHelp(): void;
  togglePin(name: string): boolean;
  toast(kind: 'success' | 'error', msg: string): void;
}

/** Yan etkili çalıştırıcı; palet kapanışı çağıranda. */
export async function runLocalAction(id: LocalActionId, ctx: LocalActionCtx, fx: LocalActionEffects): Promise<void> {
  if (id === 'copy-link') {
    const ok = await fx.copy(shareLinkFor(ctx));
    fx.toast(ok ? 'success' : 'error', ok ? 'Link copied' : 'Copy failed');
    return;
  }
  if (id === 'ask-cosre') { fx.prefill(askCosreQuestion(ctx.service)); return; }
  if (id === 'shortcuts-help') { fx.shortcutsHelp(); return; }
  if (id === 'pin-service' || id === 'unpin-service') {
    if (!ctx.service) return;
    const pinned = fx.togglePin(ctx.service);
    fx.toast('success', pinned ? `Pinned ${ctx.service}` : `Unpinned ${ctx.service}`);
    return;
  }
  const th = THEMES.find(t => id === `theme-${t}`);
  if (th) { fx.theme(th); return; }
  const d = DENSITY_STEPS.find(x => id === `density-${x}`);
  if (d) fx.density(d);
}
