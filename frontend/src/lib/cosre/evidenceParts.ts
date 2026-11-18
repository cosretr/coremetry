// evidenceParts — v0.10.2363 (docs/cosre/investigation-design.md S5). v2
// sohbetin kanıt blokları (data-block-{exception,trace,logs,anomaly}; yükler
// internal/mcptools/evidence_parts.go) → kartların okuduğu daraltılmış tipler.
// Saf: sayı türetmez, yalnız yükün alanlarını tip olarak doğrular; href'i
// olmayan yük çizilmez (kart linksiz kalmaz).

export const EVIDENCE_PART_TYPES = ['exception', 'trace', 'logs', 'anomaly'] as const;
export type EvidencePartType = (typeof EVIDENCE_PART_TYPES)[number];

export interface ExceptionPartPayload {
  fingerprint: string; type: string; message?: string; count: number;
  firstSeenMs?: number; lastSeenMs?: number; state?: string;
  sampleTraceId?: string; sampleSpanId?: string; frames?: string[];
  entity?: { service: string }; href: string;
}
export interface TracePartPayload {
  traceId: string; service: string; operation: string; startMs?: number;
  durationMs: number; spanCount: number;
  errorSpan?: { spanId?: string; service: string; name: string; exceptionType?: string; message?: string };
  href: string;
}
export interface LogsPartRow { ts: number; sev: string; service?: string; body: string; traceId?: string }
export interface LogsPartPayload { query: string; total: number; rows: LogsPartRow[]; truncated?: boolean; href: string }
export interface AnomalyPartPayload {
  id: string; detector: string; signal: string; service?: string; part?: string;
  startedMs?: number; lastSeenMs?: number; observed: number; baseline: number;
  sampleTraceIds?: string[]; href: string;
}

export type EvidencePart =
  | { id: string; type: 'exception'; payload: ExceptionPartPayload }
  | { id: string; type: 'trace'; payload: TracePartPayload }
  | { id: string; type: 'logs'; payload: LogsPartPayload }
  | { id: string; type: 'anomaly'; payload: AnomalyPartPayload };

const PREFIX = 'data-block-';
const isRec = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const isStr = (v: unknown): v is string => typeof v === 'string';
const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v);

function valid(type: EvidencePartType, p: Record<string, unknown>): boolean {
  if (!isStr(p.href) || !p.href) return false;
  switch (type) {
    case 'exception': return isStr(p.type) && isNum(p.count);
    case 'trace': return isStr(p.traceId) && isNum(p.durationMs) && isNum(p.spanCount);
    case 'logs': return isNum(p.total) && Array.isArray(p.rows);
    case 'anomaly': return isStr(p.detector) && isNum(p.observed) && isNum(p.baseline);
  }
}

/** Mesaj parçalarından dört kanıt bloğu, seq sırasıyla; bozuk yük atlanır. */
export function evidencePartsOf(parts: readonly { type: string; id?: string; data?: unknown }[]): EvidencePart[] {
  const out: Array<{ seq: number; part: EvidencePart }> = [];
  parts.forEach((p, i) => {
    if (!p.type.startsWith(PREFIX)) return;
    const type = p.type.slice(PREFIX.length) as EvidencePartType;
    if (!(EVIDENCE_PART_TYPES as readonly string[]).includes(type)) return;
    const d = isRec(p.data) ? p.data : {};
    if (!isRec(d.payload) || !valid(type, d.payload)) return;
    // valid() alanları doğruladı; birleşim tipi tipin kendisinden.
    const part = { id: p.id ?? `${type}-${i}`, type, payload: d.payload as unknown } as EvidencePart;
    out.push({ seq: isNum(d.seq) ? d.seq : i, part });
  });
  return out.sort((a, b) => a.seq - b.seq).map(x => x.part);
}
