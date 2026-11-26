// traceNarrative — v0.10.2480 (operatör-bildirimli, prod v0.10.2478: "Trace
// açıklaması daha iyi olsun istiyorum ama çok da göz yormasın").
//
// Trace açıklamasının ANLATIM biçimi: model yalnız TÜR + KARAR + Sonraki adım
// yazar (Go copilot.SystemPromptTraceNarrative); Akış / LOG / Kanıt satırlarını
// UI sunucunun hesapladığı `trace_evidence` bloğundan çizer
// (Go api/trace_explain_evidence.go). Bu modül saf: ayrıştırıcı + satır
// metinleri (vitest node). TÜR kümesi Go copilot.TraceNarrativeLabels'ın aynası.
import type { TraceEvidenceLog, TraceEvidenceView } from './types';

/** Kapalı TÜR kümesi (Go copilot.TraceNarrativeLabels ile aynı sıra). */
export const TRACE_NARRATIVE_LABELS = [
  'Business hata', 'Bağımlılık hatası', 'Zaman aşımı', 'Yavaşlık',
  'Yetki hatası', 'Altyapı hatası', 'Başarılı', 'Belirsiz',
] as const;
export type TraceNarrativeLabel = typeof TRACE_NARRATIVE_LABELS[number];

/** Etiketin i18n anahtar parçası. */
export const TRACE_LABEL_KEY: Record<TraceNarrativeLabel, string> = {
  'Business hata': 'business', 'Bağımlılık hatası': 'dependency', 'Zaman aşımı': 'timeout', 'Yavaşlık': 'slow',
  'Yetki hatası': 'auth', 'Altyapı hatası': 'infra', 'Başarılı': 'ok', 'Belirsiz': 'unclear',
};

export interface TraceNarrative {
  /** Kapalı kümeden etiket; tanınmazsa null (UI etiket çizmez). */
  label: TraceNarrativeLabel | null;
  verdict: string;
  next: string;
}

const lower = (s: string) => s.toLocaleLowerCase('tr');

/** Markdown süsünü at: kalın/italik işaretleri, baştaki başlık/madde/alıntı işaretleri. */
function plain(line: string): string {
  return line.replace(/\*\*|__/g, '').replace(/^\s*(?:#{1,6}\s+|[-*•>]\s+)/, '').trim();
}

/** Satır tek başına bir TÜR etiketi mi ("TÜR: …", parantez, son nokta toleranslı). */
export function matchTraceLabel(line: string): TraceNarrativeLabel | null {
  let s = plain(line).replace(/^(?:tür|tur|type|etiket)\s*:\s*/i, '').replace(/[.:]+$/, '').trim();
  const bare = s.replace(/\s*\([^)]*\)\s*$/, '').trim();
  for (const cand of [s, bare]) {
    const l = lower(cand);
    for (const lab of TRACE_NARRATIVE_LABELS) if (lower(lab) === l) return lab;
  }
  s = lower(s);
  // "İş kuralı reddi" — v0.10.2480 taslağının adı; model eski örnekten alırsa.
  if (s === 'iş kuralı reddi') return 'Business hata';
  return null;
}

const WAIT_LINE = /^\s*[*_#>\s]*dış bağımlılık bekleme:/i;
const KARAR_RE = /(^|\n)[ \t]*(?:[*_#>-]+[ \t]*)*karar[ \t]*(?:\*\*|__)?[ \t]*:[ \t]*(?:\*\*|__)?/i;
const NEXT_RE = /(^|\n)[ \t]*(?:[*_#>-]+[ \t]*)*(?:önerilen[ \t]+)?sonraki[ \t]+adım[ \t]*(?:\*\*|__)?[ \t]*:[ \t]*(?:\*\*|__)?/i;

const flat = (s: string) => s.split('\n').map(plain).filter(Boolean).join(' ').replace(/\s+/g, ' ').trim();

/**
 * parseTraceNarrative — modelin çıktısı → {etiket, KARAR, Sonraki adım}.
 * Sunucunun mekanik "dış bağımlılık bekleme: …" satırı (Kanıt'ın kritik yolu
 * gösterir) atılır. KARAR işareti yoksa etiket sonrası, Sonraki adım öncesi
 * metin KARAR sayılır. `streaming`: ilk satır henüz bitmediyse KARAR boş
 * (yarım etiket metin gibi titremesin).
 */
export function parseTraceNarrative(text: string, streaming = false): TraceNarrative {
  const lines = (text ?? '').replace(/\r/g, '').split('\n').filter(l => !WAIT_LINE.test(l));
  let i = 0;
  while (i < lines.length && !lines[i].trim()) i++;
  if (streaming && i === lines.length - 1 && !KARAR_RE.test(lines[i])) return { label: null, verdict: '', next: '' };
  let label: TraceNarrativeLabel | null = null;
  if (i < lines.length) {
    label = matchTraceLabel(lines[i]);
    if (label) i++;
  }
  const rest = lines.slice(i).join('\n');
  const n = NEXT_RE.exec(rest);
  const head = n ? rest.slice(0, n.index) : rest;
  const next = n ? flat(rest.slice(n.index + n[0].length)) : '';
  const k = KARAR_RE.exec(head);
  const verdict = flat(k ? head.slice(k.index + k[0].length) : head);
  return { label, verdict, next };
}

/** "6.6 ms" / "50 ms" / "2.30 s" — Go tracechain.FmtMs aynası. */
export function fmtTraceMs(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)} s`;
  if (ms < 10) return `${ms.toFixed(1)} ms`;
  return `${Math.round(ms)} ms`;
}

/** Başlık satırının HTTP kodu: kökün kodu, yoksa kaynak halkanın. */
export function traceHeadStatus(v: TraceEvidenceView | null | undefined): number | undefined {
  const ch = v?.chain ?? [];
  if (!ch.length) return undefined;
  return ch[ch.length - 1].status || ch[0].status || undefined;
}

/**
 * traceFlowLine — "Akış" satırı (deterministik, sunucu verisinden): zincir
 * varsa kaynak çağrı + kod + süre → istemciye kök kodu; yoksa kritik yolun en
 * büyük paylı adımı; ikisi de yoksa kök süresi.
 */
/** Akış satırının sözcükleri (UI dili; varsayılan Türkçe). {x} yer tutucu. */
export interface TraceFlowWords {
  error: string; toClient: string; hops: string; endErr: string; share: string; external: string; waiting: string; ok: string;
}
export const TRACE_FLOW_TR: TraceFlowWords = {
  error: 'hata', toClient: 'istemciye {s}', hops: '… {n} halka', endErr: '{svc} hata ile bitti',
  share: "kök süresinin %{n}'i", external: 'dış bağımlılık beklemesi', waiting: '{svc} bekleniyor', ok: 'hata yok',
};
const fillW = (s: string, vars: Record<string, string | number>) => s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));

export function traceFlowLine(v: TraceEvidenceView, w: TraceFlowWords = TRACE_FLOW_TR): string {
  const ch = v.chain ?? [];
  if (ch.length) {
    const o = ch[0];
    const parts = [o.call, o.status ? String(o.status) : (o.error ? o.error.split(':')[0] : w.error), fmtTraceMs(o.durMs)];
    let s = `${o.service} → ${parts.join(' · ')}`;
    const mid = ch.length - 2 + (v.chainSkipped ?? 0);
    if (ch.length > 1) {
      const r = ch[ch.length - 1];
      if (mid > 0) s += ` → ${fillW(w.hops, { n: mid })}`;
      s += ` → ${r.status ? fillW(w.toClient, { s: r.status }) : fillW(w.endErr, { svc: r.service })}`;
    }
    return s;
  }
  const c = v.critical;
  if (c) {
    const tail = c.mode === 'external' ? ` · ${w.external}` : c.mode === 'dependent' && c.child ? ` · ${fillW(w.waiting, { svc: c.child })}` : '';
    return `${c.service} · ${c.target} · ${fmtTraceMs(c.durMs)} · ${fillW(w.share, { n: c.sharePct })}${tail}`;
  }
  return v.rootMs ? `${fmtTraceMs(v.rootMs)} · ${w.ok}` : w.ok;
}

/** Görünen LOG satırları (sunucunun seçtiği ilk logsVisible kayıt, tavan 2). */
export function visibleTraceLogs(v: TraceEvidenceView): TraceEvidenceLog[] {
  return (v.logs ?? []).slice(0, Math.max(0, Math.min(2, v.logsVisible ?? 0)));
}

/** Kanıt'ta listelenecek ek loglar (görünenler hariç). */
export function extraTraceLogs(v: TraceEvidenceView): TraceEvidenceLog[] {
  return (v.logs ?? []).slice(visibleTraceLogs(v).length);
}

export type TraceLogEmpty = 'none' | 'failed' | 'nostore' | 'quiet' | null;

/** LOG satırının boş durumu: log yok / okunamadı / depo yok / hata-uyarı yok; null = satır var. */
export function traceLogEmpty(v: TraceEvidenceView): TraceLogEmpty {
  if (visibleTraceLogs(v).length) return null;
  if (v.logState === 'failed') return v.logReason === 'nostore' ? 'nostore' : 'failed';
  if (v.logState === 'rows' && v.logRows > 0) return 'quiet';
  return 'none';
}

export interface TraceEvidenceSummary {
  spans: number;
  hops: number;
  /** görünen LOG satırlarının kapsadığı kayıtlar HARİÇ log satırı */
  extraLogs: number;
  oracle: number;
  stack: boolean;
}

/** Katlanır Kanıt'ın tek satırlık özeti: görünen log satırı ikinci kez sayılmaz. */
export function traceEvidenceSummary(v: TraceEvidenceView): TraceEvidenceSummary {
  const shown = visibleTraceLogs(v).reduce((n, l) => n + (l.count && l.count > 1 ? l.count : 1), 0);
  return {
    spans: v.spans,
    hops: (v.chain?.length ?? 0) + (v.chainSkipped ?? 0),
    extraLogs: Math.max(0, (v.logRows ?? 0) - shown),
    oracle: v.oracleRows ?? 0,
    stack: !!v.frame,
  };
}
