// wikiKnowledge — v0.10.1122 ("karma"): Azure DevOps Wiki bölümünün SAF
// yardımcıları (WikiKnowledgeSection.tsx). Ayrı dosya: tablo-testli.
import type { WikiConfig, WikiSyncStatus } from '@/lib/types';

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

/** Kaydedilecek gövde. */
export function wikiBody(enabled: boolean, projects: string, wikis: string, interval: string, maxPages: string, liveSearch: boolean): WikiConfig {
  return {
    enabled,
    projects: parseList(projects),
    wikis: parseList(wikis),
    intervalMin: numOrZero(interval),
    maxPages: numOrZero(maxPages),
    disableLiveSearch: !liveSearch || undefined,
  };
}

export type WikiStatusTone = 'ok' | 'warn' | 'err' | 'idle';

/** Durum kartının tek satırlık özeti + tonu. */
export function wikiStatusSummary(st: WikiSyncStatus | undefined): { text: string; tone: WikiStatusTone } {
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

/** Manuel istek bekliyor mu (lider henüz almadı). */
export function syncPending(st: WikiSyncStatus | undefined): boolean {
  return !!st && !!st.requestedAt && st.requestedAt > (st.lastStartedAt ?? 0);
}

export function searchLabel(s: WikiSyncStatus['search']): string {
  switch (s) {
    case 'available': return 'Azure DevOps Search: kullanılabilir';
    case 'unavailable': return 'Azure DevOps Search: sunucuda yok (yalnız yerel indeks)';
    default: return 'Azure DevOps Search: henüz denenmedi';
  }
}
