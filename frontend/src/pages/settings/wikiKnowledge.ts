// wikiKnowledge — v0.10.1122 ("karma"): Azure DevOps Wiki bölümünün SAF
// yardımcıları (WikiKnowledgeSection.tsx). Ayrı dosya: tablo-testli.
import type { WikiConfig, WikiMode, WikiSyncStatus, WikiTestSearchResult } from '@/lib/types';

/** Virgül / satır sonu ayrılmış liste → tekil, kırpılmış dizi. */
export function parseList(text: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of text.split(/[\n,]/)) {
    const v = raw.trim().replace(/^\/+|\/+$/g, '');
    if (!v || seen.has(v.toLowerCase())) continue;
    seen.add(v.toLowerCase());
    out.push(v);
  }
  return out;
}

/** Dizi → düzenleme kutusu metni. */
export function listText(xs?: string[]): string {
  return (xs ?? []).join('\n');
}

/** Sayı kutusu → ayar değeri: boş/0 = varsayılan (0), aralık sunucuda kelepçelenir. */
export function numOrZero(text: string): number {
  const n = Number(text.trim());
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
}

/** Kaydedilecek gövde. v0.10.1124: canlı arama tercihi artık `mode`
 *  (eski disableLiveSearch bayrağı gönderilmez; sunucuda mod boşken okunur). */
export function wikiBody(enabled: boolean, projects: string, wikis: string, interval: string, maxPages: string, mode: WikiMode): WikiConfig {
  return {
    enabled,
    projects: parseList(projects),
    wikis: parseList(wikis),
    intervalMin: numOrZero(interval),
    maxPages: numOrZero(maxPages),
    mode,
  };
}

/** v0.10.1136 — wiki bağlam boyutu sınırları (sunucu wiki.Min/MaxContextChars ile aynı). */
export const WIKI_CONTEXT_MIN = 4000;
export const WIKI_CONTEXT_MAX = 48000;

/**
 * v0.10.1136 — "Wiki bağlam boyutu (karakter)" kutusunun doğrulaması: boş =
 * otomatik (geçerli); değilse tamsayı ve [min, max]. Hata metni ya da ''.
 */
export function contextCharsError(text: string, min = WIKI_CONTEXT_MIN, max = WIKI_CONTEXT_MAX): string {
  const t = text.trim();
  if (!t) return '';
  const n = Number(t);
  if (!Number.isInteger(n) || n < min || n > max) return `Boş (otomatik) ya da ${min}–${max} arası bir tamsayı olmalı.`;
  return '';
}

/** v0.10.1136 — okuma ayarlarının PUT alanları (boş bağlam = 0 = otomatik). */
export function wikiReadingFields(contextChars: string, pageSelect: boolean): Pick<WikiConfig, 'contextChars' | 'disablePageSelect'> {
  return { contextChars: numOrZero(contextChars), disablePageSelect: !pageSelect };
}

/** v0.10.1124 — yürürlükteki mod (yapılandırmadan; eski bayrak = sync). */
export function effectiveMode(c: WikiConfig | undefined): WikiMode {
  if (c?.mode === 'live' || c?.mode === 'sync' || c?.mode === 'hybrid') return c.mode;
  return c?.disableLiveSearch ? 'sync' : 'hybrid';
}

export const WIKI_MODES: { value: WikiMode; label: string; help: string }[] = [
  { value: 'hybrid', label: 'Karma (önerilen)',
    help: 'Wiki düzenli senkronla Coremetry içine indekslenir; yerel sonuç zayıfsa ya da indeks bayatsa Azure DevOps Search\'e de sorulur.' },
  { value: 'live', label: 'Yalnız canlı arama',
    help: 'Senkron yok, içerik Coremetry\'de saklanmaz: her wiki sorusu Azure DevOps Search\'e gider ve en iyi 3 sayfa API\'den okunur (10 dk bellek önbelleği). Soru başına 1–3 sn ekler ve sunucuda Search uzantısı gerekir.' },
  { value: 'sync', label: 'Yalnız senkron',
    help: 'Yalnız yerel indeks; canlı arama yok. Azure DevOps\'a soru anında hiç gidilmez — en hızlısı, Search uzantısı gerekmez; içerik son senkron kadar günceldir.' },
];

export type WikiStatusTone = 'ok' | 'warn' | 'err' | 'idle';

/** Durum kartının tek satırlık özeti + tonu. */
export function wikiStatusSummary(st: WikiSyncStatus | undefined, mode: WikiMode = 'hybrid'): { text: string; tone: WikiStatusTone } {
  if (mode === 'live') return { text: 'Canlı mod — senkron yok (her soru Azure DevOps Search\'e gider)', tone: 'idle' };
  if (!st || (!st.lastStartedAt && !st.lastFinishedAt)) {
    return { text: 'Henüz senkron yapılmadı', tone: 'idle' };
  }
  if (st.running) return { text: 'Senkron sürüyor…', tone: 'idle' };
  const parts = [
    `${st.indexedPages} sayfa · ${st.indexedChunks} parça indekste`,
    `son geçiş: ${st.fetched} okundu · ${st.unchanged} değişmemiş · ${st.deleted} silindi`,
  ];
  if (st.truncated) parts.push('sayfa tavanına takıldı');
  const errs = st.errors?.length ?? 0;
  if (errs) parts.push(`${errs} hata`);
  return { text: parts.join(' — '), tone: errs ? (st.lastOk ? 'warn' : 'err') : st.truncated ? 'warn' : 'ok' };
}

/**
 * v0.10.1129 — atlanan sayfaların nötr özeti ("Atlanan: 2 içeriksiz klasör,
 * 1 çok büyük sayfa"); hiç yoksa ''. Hata sayısına ve tona KARIŞMAZ.
 */
export function skippedSummary(st: WikiSyncStatus | undefined): string {
  const parts: string[] = [];
  if (st?.skippedEmpty) parts.push(`${st.skippedEmpty} içeriksiz klasör`);
  if (st?.skippedLarge) parts.push(`${st.skippedLarge} çok büyük sayfa`);
  return parts.length ? `Atlanan: ${parts.join(', ')}` : '';
}

/** Atlanan sayfa satırı: ad + neden. */
export function skippedReasonText(reason: string): string {
  return reason === 'large' ? 'çok büyük, atlandı' : 'içeriksiz klasör';
}

/** Manuel istek bekliyor mu (lider henüz almadı). */
export function syncPending(st: WikiSyncStatus | undefined): boolean {
  return !!st && !!st.requestedAt && st.requestedAt > (st.lastStartedAt ?? 0);
}

export function searchLabel(s: WikiSyncStatus['search'], last?: WikiSyncStatus['searchLast'], mode: WikiMode = 'hybrid'): string {
  if (mode === 'sync') return 'Azure DevOps Search: kapalı (yalnız senkron modu)';
  let base: string;
  switch (s) {
    case 'available': base = 'Azure DevOps Search: kullanılabilir'; break;
    case 'unavailable': base = mode === 'live'
      ? 'Azure DevOps Search: sunucuda yok — canlı mod çalışmaz, senkron modunu kullanın'
      : 'Azure DevOps Search: sunucuda yok (yalnız yerel indeks)'; break;
    default: base = 'Azure DevOps Search: henüz denenmedi';
  }
  // v0.10.1124 — son deneme hangi pod'da olursa olsun (paylaşılan özet).
  if (last?.at) {
    const parts = [last.state === 'error' ? `son deneme hata (${last.class ?? '?'}${last.httpStatus ? ` · http ${last.httpStatus}` : ''})` : `son deneme: ${last.hits} sonuç`];
    if (last.apiVersion) parts.push(`api-version ${last.apiVersion}`);
    base += ` — ${parts.join(' · ')}`;
  }
  return base;
}

/** v0.10.1124 — "Aramayı test et" canlı yarısının tek satırı. */
export function liveOutcomeText(r: WikiTestSearchResult): string {
  const l = r.live;
  if (!l.attempted) {
    switch (l.skipped) {
      case 'disabled': return 'Canlı arama denenmedi: yalnız senkron modu.';
      case 'not_configured': return 'Canlı arama denenmedi: Azure DevOps bağlantısı yok.';
      default: return `Canlı arama denenmedi${l.skipped ? ` (${l.skipped})` : ''}.`;
    }
  }
  const info = l.info;
  const head = info.class === 'ok'
    ? `Canlı arama: ${l.results} sonuç (${l.queryMode === 'or' ? 'OR' : 'AND'} sorgusu), ${l.read} sayfa okundu`
    : `Canlı arama başarısız: ${info.class}${info.httpStatus ? ` · http ${info.httpStatus}` : ''}`;
  const ver = info.apiVersion ? ` · api-version ${info.apiVersion}` : '';
  return head + ver + (l.note ? ` — ${l.note}` : '') + (l.error ? ` (${l.error})` : '');
}

/** v0.10.1124 — indeksteki sayfalar listesinin boş-durum metni. */
export function pagesEmptyText(st: WikiSyncStatus | undefined, mode: WikiMode): string {
  if (mode === 'live') return 'Canlı modda sayfa indekslenmez — içerik soru anında Azure DevOps\'tan okunur.';
  return `Henüz indekslenmiş sayfa yok — senkron durumu: ${wikiStatusSummary(st, mode).text}`;
}
