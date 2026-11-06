// pages/explore/legacySource.ts — v0.10.1867 (Metrics Explorer Faz 2 dilim
// 2.7; audit §1 öneri 2). /explore = span analitiği; eski `source=metrics`
// ve `source=logs` panelleri kalktı. Eski linkler (kayıtlı görünüm, yer imi,
// altyapı karosu `/explore?source=metrics&service=&metric=`) ilgili sayfaya
// BİR KEZ yönlenir (Navigate replace):
//   source=metrics → /metrics  (metric varsa `?metric=&service=` — Metrics
//                    Explorer'ın tek-model içe aktarımı sorgu A kurar; yoksa
//                    `?editor=0&service=` → Keşif, servis süzgeçli)
//   source=logs    → /logs     (service + q taşınır)
// range / cluster / env her iki yönde taşınır. Saf; tablo testli.

const CARRY = ['range', 'cluster', 'env'];

export function exploreLegacySourceHref(search: string): string | null {
  const sp = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search);
  const src = sp.get('source');
  if (src !== 'metrics' && src !== 'logs') return null;
  const out = new URLSearchParams();
  const service = (sp.get('service') ?? '').trim();
  if (src === 'logs') {
    if (service) out.set('service', service);
    const q = (sp.get('q') ?? sp.get('search') ?? '').trim();
    if (q) out.set('q', q);
  } else {
    const metric = (sp.get('metric') ?? '').trim();
    if (metric) out.set('metric', metric);
    else out.set('editor', '0');
    if (service) out.set('service', service);
  }
  for (const k of CARRY) {
    const v = sp.get(k);
    if (v) out.set(k, v);
  }
  const qs = out.toString();
  return `/${src}${qs ? `?${qs}` : ''}`;
}
