// sourceChips — v0.10.1127: cevabın kaynak çipleri (SAF).
//
// Operatör: tek cevabın altında birden çok özdeş "📄 Kaynak §1" çipi. Kaynak
// listesi parça başınaydı ve çip yalnız parça numarasını gösteriyordu
// (doküman adı v0.9.515'ten beri ipucunda) — farklı sayfaların ilk parçaları
// da aynı sayfanın parçaları da "Kaynak §1" görünüyordu. Sunucu artık hedef
// başına tekilleştirip `label` ("Kaynak 1") gönderiyor; bu yardımcı eski /
// arşivden gelen etiketsiz listeyi de aynı kurala oturtur: hedef (ref, yoksa
// doküman adı) başına tek çip, sıra ilk görülme, etiket "Kaynak N".

import type { RagSource } from '@/lib/types';

export interface SourceChip {
  key: string;
  label: string;
  href?: string;
  title: string;
  /** v0.10.1137 — atıf numarası ([n] = "Kaynak n"), okunur ad ("Wiki · " öneksiz) ve host. */
  n: number;
  name: string;
  host: string;
}

const WIKI_PREFIX = 'Wiki · ';
const hostOfRef = (u?: string) => /^https?:\/\/([^/?#\s]+)/i.exec(u ?? '')?.[1].toLowerCase() ?? '';
// Yalnız http(s) ve kök-göreli href tıklanır (javascript:/data: asla).
const safeHref = (u?: string) => (u && (/^https?:\/\//i.test(u) || (u.startsWith('/') && !u.startsWith('//'))) ? u : undefined);

function targetKey(s: RagSource): string {
  const ref = (s.ref ?? '').trim();
  return ref ? `ref:${ref.toLowerCase()}` : `doc:${s.doc.trim().toLowerCase()}`;
}

export function sourceChips(sources: readonly RagSource[] | undefined): SourceChip[] {
  const out: (SourceChip & { score: number; sections: number[]; doc: string })[] = [];
  const at = new Map<string, number>();
  for (const s of sources ?? []) {
    const k = targetKey(s);
    const secs = s.sections?.length ? s.sections : [s.chunk];
    const i = at.get(k);
    if (i !== undefined) {
      const c = out[i];
      c.score = Math.max(c.score, s.score);
      for (const n of secs) if (!c.sections.includes(n)) c.sections.push(n);
      continue;
    }
    at.set(k, out.length);
    out.push({ key: k, label: '', href: safeHref(s.ref), title: '', n: 0, name: '', host: '', score: s.score, sections: [...secs], doc: s.doc });
  }
  return out.map((c, i) => ({
    key: c.key,
    label: `Kaynak ${i + 1}`,
    href: c.href,
    title: `${c.doc} §${c.sections.join(', §')} · benzerlik ${(c.score * 100).toFixed(0)}%`,
    n: i + 1,
    name: c.doc.startsWith(WIKI_PREFIX) ? c.doc.slice(WIKI_PREFIX.length) : c.doc,
    host: hostOfRef(c.href),
  }));
}
