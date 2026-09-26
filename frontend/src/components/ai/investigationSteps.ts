// investigationSteps.ts — v0.10.948 (CoSRE araştırma asistanı, Faz B): "CoSRE'ye
// sor" ilk cevabının DÜRÜST ilerleme + kanıt künyesi için saf çekirdek.
// Sözleşme investigationSteps.test.ts'te pinli.
//
//   - applyExplainStep: sunucunun `step` (çağrıdan ÖNCE) / `step-result`
//     (çağrı BİTİNCE) olaylarını `i` ile eşleyip ChatStepDetail listesine
//     katlar — sohbet balonunun (useChatThread) aynı şekli, ikinci bir model
//     yok. Sabit/sahte ilerleme metni YOK: liste yalnız gelen olaylardan.
//   - explainStepRows: satır görünümü (çalışıyor… · ok · durum rozetleri ·
//     hata · yürütülmedi · sonuç gelmedi) + süre. Rozetler toolSteps'in tek
//     sözlüğünden (sourceStates / SOURCE_STATE_LABELS / sourceStateTone).
//   - sourceFooterItems: cevap çerçevesinin `sources`u → dipnot rozetleri;
//     `ok` DAHİL her kaynak (nötr), ok dışındakiler (boş dahil) "eksik veri".
//     v0.10.948 — rozet adı inceleme bölümünün etiketinden (label + tool):
//     traces/clickhouse hem trace okuması hem dönem kıyası olabilir; eksik
//     veri listesi HANGİ kanıtın eksik olduğunu söylesin. source/backend ipucunda.
//   - evidenceLinks: `id`siz, göreli (ya da http[s]) kanıt linkleri; `id`li
//     kimlik köprüleri satır içi kalır (inlineIdLinks), burada sayılmaz.
import type { AIAnswerLink, ChatStepDetail, ExplainSourceStatus, ExplainStepEvent } from '@/lib/types';
import {
  SOURCE_STATE_LABELS, parseToolError, sourceStateTone, sourceStates, stateUnknown,
  toolErrorLabel, windowPrefix, type SourceStateView,
} from './toolSteps';

/**
 * applyExplainStep — SAF. `step` araç adı taşımıyorsa (etiket adımı) düşer:
 * ilerleme listesi yalnız gerçek okuma çağrısını gösterir. Aynı `i` ile gelen
 * ikinci `step` satırı çoğaltmaz. Önünde `step` olmayan `step-result` de satır
 * olur — sonuç, çağrının koştuğunun kanıtıdır. Yürütülmeyen (`skipped`)
 * çağrının süresi ölçüm değildir, yazılmaz.
 */
export function applyExplainStep(prev: readonly ChatStepDetail[], ev: ExplainStepEvent): ChatStepDetail[] {
  if (ev.kind === 'step') {
    if (ev.i == null || !ev.tool) return [...prev];
    if (prev.some(d => d.i === ev.i)) return [...prev];
    return [...prev, { i: ev.i, tool: ev.tool, args: ev.args, origin: ev.origin }];
  }
  const res: Partial<ChatStepDetail> = {
    ok: ev.ok, preview: ev.preview, truncated: ev.truncated, bytes: ev.bytes, href: ev.href,
    durationMs: ev.skipped ? undefined : ev.durationMs,
    skipped: ev.skipped || undefined,
    sources: ev.sources,
  };
  const at = prev.findIndex(d => d.i === ev.i);
  if (at < 0) return ev.tool ? [...prev, { i: ev.i, tool: ev.tool, ...res }] : [...prev];
  return prev.map((d, k) => (k === at ? { ...d, ...res } : d));
}

export type ExplainStepStatus = 'running' | 'ok' | 'error' | 'skipped' | 'no-result';

export interface ExplainStepRow {
  i: number;
  tool: string;
  status: ExplainStepStatus;
  /** Sunucu ölçümü; yoksa (sürüyor / yürütülmedi / eski sunucu) undefined. */
  durationMs?: number;
  /** ok olmayan kaynak durumları (boş · erişilemedi · yetki yok · …) */
  states: SourceStateView[];
  /** hata sınıfının Türkçe etiketi (status === 'error') */
  errorLabel?: string;
  /** kırpık önizlemede durum görülemedi — nötr «ok» yalan olurdu */
  unknownState: boolean;
}

/**
 * explainStepRows — `live` = inceleme sürüyor (cevap metni henüz gelmedi).
 * Sonucu gelmemiş adım canlıyken «çalışıyor…», bittikten sonra «sonuç
 * gelmedi»: sunucu boş çıktıda step-result yayınlamayabilir ve biten bir
 * incelemede hâlâ "çalışıyor" demek yalan olurdu.
 */
export function explainStepRows(details: readonly ChatStepDetail[], live: boolean): ExplainStepRow[] {
  return details.filter(d => !!d.tool).map((d): ExplainStepRow => {
    const base = { i: d.i, tool: d.tool, states: [] as SourceStateView[], unknownState: false };
    if (d.preview === undefined) return { ...base, status: live ? 'running' : 'no-result' };
    if (d.skipped) return { ...base, status: 'skipped' };
    const durationMs = typeof d.durationMs === 'number' && d.durationMs >= 0 ? d.durationMs : undefined;
    if (d.ok === false) {
      const err = parseToolError(d.preview);
      return { ...base, status: 'error', durationMs, errorLabel: err ? toolErrorLabel(err.cls) : 'hata' };
    }
    const states = sourceStates(d.preview, d.sources);
    return { ...base, status: 'ok', durationMs, states, unknownState: states.length === 0 && stateUnknown(d) };
  });
}

export interface ExplainStepsSummary {
  count: number;
  running: number;
  errors: number;
  skipped: number;
  /** ok olup en az bir kaynağı ok olmayan (boş/kısmi/erişilemedi…) adım */
  degraded: number;
  /**
   * v0.10.948 — en uzun TEK okuma (sunucu ölçümü). Σ YOK: okumalar get_trace'ten
   * sonra PARALEL koşar (trace_investigate.go); eşzamanlı süreler toplanmaz,
   * toplam duvar saatini şişirirdi. Yürütülen bir adımın süresi bilinmiyorsa
   * null («—»).
   */
  longestMs: number | null;
}

export function summarizeExplainSteps(rows: readonly ExplainStepRow[]): ExplainStepsSummary {
  let running = 0, errors = 0, skipped = 0, degraded = 0, longest = 0, unknown = 0;
  for (const r of rows) {
    if (r.status === 'running') running++;
    if (r.status === 'error') errors++;
    if (r.status === 'skipped') { skipped++; continue; }
    if (r.status === 'ok' && r.states.length > 0) degraded++;
    // v0.10.948 — paralel okumaların süreleri TOPLANMAZ (kritik yol kuralı): en uzunu alınır.
    if (typeof r.durationMs === 'number') longest = Math.max(longest, r.durationMs); else unknown++;
  }
  const executed = rows.length - skipped;
  return { count: rows.length, running, errors, skipped, degraded, longestMs: executed > 0 && unknown === 0 ? longest : null };
}

// ── Kaynak durumu dipnotu ────────────────────────────────────────────────

export interface SourceFooterItem {
  /** "Karşılaştırma (compare_periods)" — bölüm etiketi varsa; yoksa "logs/elasticsearch" (backend varsa eklenir) */
  name: string;
  state: string;
  /** Türkçe durum etiketi; `ok` için "ok" */
  label: string;
  tone: 'err' | 'warn' | 'gray';
  /** ipucu: detay · notlar · pencere · sayım */
  title: string;
}

/** v0.10.948 — Kanıt olarak eksik kalan durumlar: ok DIŞINDAKİ HER durum (boş dahil) — sunucu künyesi (footerTR: worst != OK) ve prompt'un "Eksik veri" kuralıyla aynı tanım. */
export function isMissingState(state: string): boolean {
  return state !== 'ok';
}

// v0.10.948 — sayım yalnız sorgusu TAMAMLANAN durumlarda anlamlı: arıza
// durumlarında (erişilemedi · yetki yok · zaman aşımı · hata · yapılandırılmamış)
// sunucu `returned: 0` yollar (omitempty yok) — «0 kayıt» boş sonuç gibi
// okunurdu. İzin listesi (yasak listesi değil): gelecekteki bir arıza durumu
// ya da bilinmeyen ham durum da sayım almaz.
const COUNTED_STATES: ReadonlySet<string> = new Set(['ok', 'empty', 'partial', 'truncated', 'delayed']);

/** Bölüm etiketi — answer çerçevesi daraltılmadan gelir; metin değilse yok sayılır. */
function sectionLabel(st: ExplainSourceStatus): string {
  return typeof st.label === 'string' ? st.label.trim() : '';
}

function footerTitle(st: ExplainSourceStatus, label: string, where: string): string {
  // v0.10.948 — ipucu source/backend ile başlar: rozet bölüm etiketini gösterse de
  // hangi arka uca gidildiği (traces/clickhouse) üzerine gelince görünür.
  const lbl = sectionLabel(st);
  const parts = [`${where}${lbl ? ` (${lbl})` : ''}: ${label}`];
  if (st.detail) parts.push(st.detail);
  if (st.notes?.length) parts.push(st.notes.join('; '));
  if (st.fromIso && st.toIso) parts.push(`pencere ${st.fromIso} → ${st.toIso}`);
  if (typeof st.returned === 'number' && COUNTED_STATES.has(st.state)) {
    parts.push(`${st.returned} kayıt${st.limit ? ` (limit ${st.limit})` : ''}`);
  }
  return parts.join(' — ');
}

/**
 * sourceFooterItems — SAF. Her Status bir birincil rozet; `flags`taki ek
 * geçerli durumlar (kısmi + limitli) ayrı rozet. Bilinmeyen durum ham adıyla
 * gösterilir (uydurma etiket yok). Pencere öneki (compare_periods: sorun /
 * referans) detaydan.
 */
export function sourceFooterItems(sources: readonly ExplainSourceStatus[] | undefined): SourceFooterItem[] {
  const out: SourceFooterItem[] = [];
  for (const st of sources ?? []) {
    if (!st || typeof st.source !== 'string' || typeof st.state !== 'string' || !st.state) continue;
    // v0.10.948 — ad bölüm etiketinden (label + tool); etiketsiz (eski sunucu /
    // sohbet adımı) satır source/backend'e düşer. Aynı `name` bayrak rozetlerinde.
    const where = `${st.source}${st.backend ? `/${st.backend}` : ''}`;
    const lbl = sectionLabel(st);
    const tool = typeof st.tool === 'string' ? st.tool.trim() : '';
    const base = lbl ? `${lbl}${tool ? ` (${tool})` : ''}` : where;
    const name = `${windowPrefix(st.detail)}${base}`;
    const label = st.state === 'ok' ? 'ok' : (SOURCE_STATE_LABELS[st.state] ?? st.state);
    out.push({ name, state: st.state, label, tone: st.state === 'ok' ? 'gray' : sourceStateTone(st.state), title: footerTitle(st, label, where) });
    const seen = new Set([st.state]);
    for (const f of st.flags ?? []) {
      if (typeof f !== 'string' || f === 'ok' || seen.has(f)) continue;
      seen.add(f);
      const fl = SOURCE_STATE_LABELS[f] ?? f;
      out.push({ name, state: f, label: fl, tone: sourceStateTone(f), title: footerTitle(st, fl, where) });
    }
  }
  return out;
}

/** Eksik veri listesi: ok dışı durumdaki kaynak adları (tekil, sıra korunur). */
export function missingSources(items: readonly SourceFooterItem[]): string[] {
  const out: string[] = [];
  for (const it of items) if (isMissingState(it.state) && !out.includes(it.name)) out.push(it.name);
  return out;
}

// ── Kanıt linkleri ──────────────────────────────────────────────────────

/**
 * evidenceLinks — SAF. `id`li linkler kimlik köprüsüdür ve metnin İÇİNDE
 * çizilir; burada yalnız `id`siz rota linkleri kalır. Kabul: uygulama-içi
 * göreli yol (`/…`, `//` DEĞİL) ya da http(s). Başka şema (javascript:,
 * data:) ve boş etiket düşer; aynı href iki kez çizilmez.
 */
export function evidenceLinks(links: readonly AIAnswerLink[] | undefined): AIAnswerLink[] {
  const out: AIAnswerLink[] = [];
  const seen = new Set<string>();
  for (const l of links ?? []) {
    if (!l || l.id || typeof l.href !== 'string' || typeof l.label !== 'string') continue;
    const href = l.href.trim();
    const label = l.label.trim();
    if (!href || !label || seen.has(href)) continue;
    const internal = href.startsWith('/') && !href.startsWith('//');
    if (!internal && !/^https?:\/\//i.test(href)) continue;
    seen.add(href);
    out.push({ label, href });
  }
  return out;
}

export function isInternalHref(href: string): boolean {
  return href.startsWith('/') && !href.startsWith('//');
}
