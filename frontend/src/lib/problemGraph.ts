// problemGraph.ts — v0.10.2274 (entity grafı ile Problem birleştirme S1,
// docs/problems/entity-graph-merge-design.md): gölge bağ blobunun tipleri
// ve admin raporunun SAF metin yardımcıları. Kural kimlikleri (M-*, N-*)
// sunucuyla sözleşmedir; cümleler burada kimlikten kurulur.

export const GRAPH_LINK_RULES = ['M-SVC', 'M-CALL', 'M-DEP', 'M-WL', 'M-NODE', 'M-HOST'] as const;
export const GRAPH_NOLINK_REASONS = ['N-REL', 'N-TIME', 'N-CAP', 'N-HUB', 'N-KIND', 'N-SIZE', 'N-GRAPH'] as const;

export interface ProblemGraphMember {
  id: string;
  entity: string;
  type: string; // service | database | queue | external
  name: string;
  ruleId?: string;
  title?: string;
  severity?: string;
  startedAt: number; // unix ns
  owner?: boolean;
  link?: string;
  peer?: string;
  via?: string;
  path?: string[];
  strict?: boolean;
  graphMissing?: boolean;
}

export interface ProblemGraphNearMiss {
  id: string;
  name: string;
  type: string;
  startedAt: number;
  reason: string;
  hint?: string;
  peer?: string;
  via?: string;
  groupSize?: number;
}

export interface ProblemGraphReads {
  passes: number; skippedFew: number; refreshes: number; relations: number;
  maxRelPerPass: number; runs: number; evidence: number; errors: number;
}

export interface ProblemGraphShadowReport {
  at: number; since: number; windowH: number; units: number;
  links?: Record<string, number>;
  callStrict: number;
  noLink?: Record<string, number>;
  sizes?: Record<string, number>;
  sizesStrict?: Record<string, number>;
  age?: Record<string, number>;
  ageByRule?: Record<string, Record<string, number>>;
  graphMissing: number;
  familyOff?: Record<string, number>;
  reads: ProblemGraphReads;
  truncated?: boolean;
}

export interface ProblemGraphTopGroup {
  ownerId: string; ownerName: string; entities: number; startedAt: number; rules: Record<string, number>;
}

export interface ProblemGraphShadowResponse {
  available: boolean;
  report: ProblemGraphShadowReport;
  largest: ProblemGraphTopGroup[];
}

const SIZE_ORDER = ['2', '3', '4-5', '6-10', '11-25'];
const AGE_ORDER = ['0-30', '30-60', '60-90', '>90'];

function kv(m: Record<string, number> | undefined, order: readonly string[], fmt: (k: string) => string = k => k): string {
  const parts = order.filter(k => (m?.[k] ?? 0) > 0).map(k => `${fmt(k)} ${m![k]}`);
  return parts.length ? parts.join(' · ') : 'yok';
}

/** SAF: admin gölge raporunun satırları (Anomalies › ML › Teknik detay). */
export function graphShadowLines(r: ProblemGraphShadowReport): string[] {
  const since = new Date(r.since * 1000);
  const hm = `${String(since.getHours()).padStart(2, '0')}:${String(since.getMinutes()).padStart(2, '0')}`;
  const off = r.familyOff && Object.keys(r.familyOff).length
    ? Object.keys(r.familyOff).sort().map(k => `${k} ${r.familyOff![k]}`).join(' · ')
    : 'yok';
  const rd = r.reads;
  return [
    `Pencere ${r.windowH} sa · sayaç başlangıcı ${hm} (lider değişince sıfırlanır) · bu tik ${r.units} aday varlık`,
    `Kurulurdu (farklı bağ): ${kv(r.links, GRAPH_LINK_RULES)} · M-CALL katı sürüm (çağıranda bağımlılık olayı): ${r.callStrict}`,
    `Bağlanmazdı (farklı Problem): ${kv(r.noLink, GRAPH_NOLINK_REASONS)} (N-CAP yalnız ölçülür, uygulanmadı)`,
    `Küme boyu, düz M-CALL: ${kv(r.sizes, SIZE_ORDER, k => `${k} varlık`)} · katı M-CALL: ${kv(r.sizesStrict, SIZE_ORDER, k => `${k} varlık`)}`,
    `Bağlanırken sahibin yaşı (servisler arası): ${kv(r.age, AGE_ORDER, k => `${k} dk`)}`,
    `Graf verisi yok: ${r.graphMissing} Problem · kural ailesi kapalı geçen okuma turu: ${off}`,
    `Okuma: ${rd.passes} tik, ${rd.skippedFew} tik aday < 2 (okuma yok), ${rd.refreshes} okuma turu · entity_relations ${rd.relations} (tur başına en çok ${rd.maxRelPerPass}) · tazelik ${rd.runs} · bağımlılık olayı ${rd.evidence} · hata ${rd.errors}`,
  ];
}
