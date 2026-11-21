import type { ChatQueryEvidence, ChatEvidenceChartPayload, ChatEvidenceChartRegion, ChatCtxApplied, ChatStepDetail } from '@/lib/types';
import type { ChartTimeRegion } from '@/lib/chart/overlays';
import type { CorePanelMultiItem } from '@/components/chart/corePanelEntry';
import type { EvidenceBlockView } from '@/lib/chatBlocks';
import type { Tone } from '@/components/ui/Badge';
import { fmtClock, fmtDateTime, fmtDurShort } from '@/lib/utils';
import { safeAppPath } from '@/lib/safeAppPath';
import { currentLang, t } from '@/lib/i18n';
import { fmtMs } from './toolSteps';

// evidenceChip — v0.10.1272 (CoSRE adım 3): kanıt çipinin SAF yarısı.
// Çip metni: `[Kaynak] · pencere · env/cluster · N satır · 123 ms`; tam sorgu
// ipucunda. Kaynak ayrı döner (rozet olarak çizilir), kalan parçalar " · " ile.

/** Pencere etiketi: "1h 14:00–15:00"; 24 saati aşan pencere "7d 10.10 14:00–17.10 14:00". */
export function evidenceWindowLabel(w: ChatQueryEvidence['window']): string {
  if (!w || !(w.toMs > w.fromMs) || w.fromMs <= 0) return '';
  const dur = fmtDurShort((w.toMs - w.fromMs) / 1000);
  const hm = (ms: number) => fmtClock(ms).slice(0, 5);
  if (w.toMs - w.fromMs <= 24 * 3600_000) return `${dur} ${hm(w.fromMs)}–${hm(w.toMs)}`;
  const dm = (ms: number) => fmtDateTime(ms).slice(0, 5);
  return `${dur} ${dm(w.fromMs)} ${hm(w.fromMs)}–${dm(w.toMs)} ${hm(w.toMs)}`;
}

/** Tam pencere (ipucu): "16.10.2026 14:00:00 – 16.10.2026 15:00:00". */
export function evidenceWindowFull(w: ChatQueryEvidence['window']): string {
  if (!w || !(w.toMs > w.fromMs) || w.fromMs <= 0) return '';
  return `${fmtDateTime(w.fromMs)} – ${fmtDateTime(w.toMs)}`;
}

/**
 * ctxAppliedLabel — v0.10.1274: ekran kapsamı künyesinin kısa metni
 * ("ekrandan: env prod · 1h"; model farklı env verdiyse "env uat (ekran prod)";
 * env'siz araçta "env uygulanmadı"). Künye boşsa ''.
 */
export function ctxAppliedLabel(ca?: ChatCtxApplied): string {
  if (!ca) return '';
  const inj: string[] = [];
  if (ca.env) inj.push(`env ${ca.env}`);
  if (ca.service) inj.push(`servis ${ca.service}`);
  if (ca.rangeS && ca.rangeS > 0) inj.push(fmtDurShort(ca.rangeS));
  if (ca.traceId) inj.push(`trace ${ca.traceId.slice(0, 8)}`);
  const out: string[] = [];
  if (inj.length) out.push(`ekrandan: ${inj.join(', ')}`);
  if (ca.envKept) out.push(`env ${ca.envKept}${ca.screenEnv ? ` (ekran ${ca.screenEnv})` : ''}`);
  if (ca.serviceKept) out.push(`servis ${ca.serviceKept} (modelin seçimi)`);
  if (ca.envNotApplied) out.push('env uygulanmadı');
  return out.join(' · ');
}

const HOP_LABEL: Record<string, string> = {
  trace: 'Trace', pods: 'Pod', jvm: 'JVM', logs: 'Log', op_baseline: 'Operasyon',
  corroborate: 'Ham span doğrulaması', // v0.10.1299 — service_health_sweep gecikme adayı
};

const STANDALONE_HOPS: Record<string, string> = {
  baseline: 'cosre.ev.hop.baseline', baseline_chart: 'cosre.ev.hop.baseline_chart',
};

/**
 * evidenceHopLabel — v0.10.1275 (CoSRE adım 6): bileşik kanıtın halka etiketi.
 * Zincir trace'ten başlar: trace halkası "Trace", diğerleri "Trace › Pod" gibi.
 * Atomik kanıt (hop yok) ''. Bilinmeyen halka adı olduğu gibi.
 */
export function evidenceHopLabel(hop?: string): string {
  if (!hop) return '';
  // CoSRE faz D — trace zincirine ait OLMAYAN halkalar (compare_baseline): öneksiz, i18n.
  const own = STANDALONE_HOPS[hop];
  if (own) return t(own, currentLang());
  const l = HOP_LABEL[hop] ?? hop;
  return hop === 'trace' ? l : `Trace › ${l}`;
}

/**
 * stepEvidenceItems — v0.10.1275: adımın kanıt listesi. Yeni sunucu bileşik
 * araçta `evidenceList` (ilki = evidence) gönderir; eski olay yalnız `evidence`.
 */
export function stepEvidenceItems(d: Pick<ChatStepDetail, 'evidence' | 'evidenceList'>): ChatQueryEvidence[] {
  if (d.evidenceList?.length) return d.evidenceList;
  return d.evidence ? [d.evidence] : [];
}

/** Kaynak dışındaki parçalar (sıra sabit; boş olan atlanır). */
export function evidenceChipParts(ev: ChatQueryEvidence): string[] {
  const parts: string[] = [];
  const hop = evidenceHopLabel(ev.hop); // v0.10.1275 — bileşik kanıtın halkası önce
  if (hop) parts.push(hop);
  const win = evidenceWindowLabel(ev.window);
  if (win) parts.push(win);
  const scope = [ev.env, ev.cluster].filter(Boolean).join('/');
  if (scope) parts.push(scope);
  if (typeof ev.rows === 'number') parts.push(`${ev.rows} satır`);
  else if (typeof ev.series === 'number') parts.push(`${ev.series} seri`);
  if (typeof ev.durationMs === 'number') parts.push(fmtMs(ev.durationMs));
  const ca = ctxAppliedLabel(ev.ctxApplied); // v0.10.1274 — hangi kapsamın kullanıldığı
  if (ca) parts.push(ca);
  return parts;
}

/** Erişilebilir ad / düz metin: "ClickHouse · 1h 14:00–15:00 · prod · 2 satır · 37 ms". */
export function evidenceChipText(ev: ChatQueryEvidence): string {
  return [ev.source, ...evidenceChipParts(ev)].join(' · ');
}

const KIND_LABEL: Record<string, string> = { promql: 'PromQL', sql: 'SQL', summary: 'okuyucu özeti' };

/** Sorgu türü etiketi (bilinmeyen tür olduğu gibi). */
export function evidenceKindLabel(kind: string): string {
  return KIND_LABEL[kind] ?? kind;
}

/**
 * Yalnız kök-göreli uygulama yolu tıklanır (sunucu zaten öyle üretir; arşivden
 * gelen kanıt istemciden geçtiği için burada da sınır). "//host" ve "/\host" asla.
 */
export function evidenceHref(ev: ChatQueryEvidence): string | null {
  return safeAppPath(ev.href); // v0.10.1314 — tek kapı (lib/safeAppPath)
}

/**
 * evidenceDefaultOpen — varsayılan açık çip: bloğu olan SON kanıtın ref'i
 * (cevabın son araç sonucu); hiçbirinin bloğu yoksa null.
 */
export function evidenceDefaultOpen(
  items: readonly ChatQueryEvidence[], blocks: ReadonlyMap<string, EvidenceBlockView> | undefined,
): string | null {
  for (let i = items.length - 1; i >= 0; i--) {
    const r = items[i].ref;
    if (r && blocks?.has(r)) return r;
  }
  return null;
}

/** Veri kipi chart yükü → birim başına CorePanel kalemleri (nokta ms → ns). */
export function evidenceChartGroups(p: ChatEvidenceChartPayload): { unit: string; items: CorePanelMultiItem[] }[] {
  const byUnit = new Map<string, CorePanelMultiItem[]>();
  for (const s of p.series ?? []) {
    if (!Array.isArray(s.points) || s.points.length === 0) continue;
    const name = s.group ? `${s.name} · ${s.group}` : s.name;
    // v0.10.1299 — style: 'dashed' → kesikli referans çizgisi (önceki pencere
    // p99'u), 'secondary' → soluk ikincil çizgi (p50 birincil, p99 ikincil).
    const role: CorePanelMultiItem['role'] = s.name === 'error_rate' || s.name === 'errors'
      ? 'error' : s.style === 'secondary' ? 'muted' : 'data';
    const item: CorePanelMultiItem = {
      name,
      role,
      series: [{ groupKey: [name], points: s.points.map(([t, v]) => ({ time: t * 1e6, value: v })) }],
      ...(s.style === 'dashed' ? { dashed: true } : {}),
    };
    const u = s.unit ?? '';
    byUnit.set(u, [...(byUnit.get(u) ?? []), item]);
  }
  return [...byUnit.entries()].map(([unit, items]) => ({ unit, items }));
}

/** Tablo hücresinin düz metni (sayı 4 anlamlı basamak; boş "—"). */
export function evidenceCellText(v: unknown, kind: string): string {
  if (v == null || v === '') return '—';
  if (kind === 'num' && typeof v === 'number') return String(Number(v.toPrecision(4)));
  return String(v);
}

/**
 * evidenceLevelTone — v0.10.1275: `level` sütunu (crit/warn/ok). Yalnız sapan
 * değer renk alır (tablo standardı): ok → null (düz metin).
 */
export function evidenceLevelTone(level: string): Tone | null {
  switch (level.toLowerCase()) {
    case 'crit': return 'danger';
    case 'warn': return 'warning';
    default: return null;
  }
}

/** `level` sütununun Türkçe etiketi. */
export function evidenceLevelLabel(level: string): string {
  switch (level.toLowerCase()) {
    case 'crit': return 'kritik';
    case 'warn': return 'uyarı';
    case 'ok': return 'normal';
    default: return level;
  }
}

/** Log seviyesi rozeti: yalnız sapan seviye renk alır (tablo standardı). */
export function evidenceSeverityTone(sev: string): Tone | null {
  const s = sev.toUpperCase();
  if (s.startsWith('ERR') || s.startsWith('FATAL') || s.startsWith('CRIT')) return 'danger';
  if (s.startsWith('WARN')) return 'warning';
  return null;
}

/**
 * evidenceRegions — v0.10.1284 (explain_log_spike): sunucunun x-bölgeleri
 * (unix ms + ton) → grafik primitiflerinin ChartTimeRegion'u (unix SANİYE,
 * CSS token rengi). Ton sözlüğü dar: err → --err, warn → --warn, gerisi
 * soluk (--text3) — renk sunucudan gelmez. Bozuk bölge (to ≤ from) düşer. SAF.
 */
export function evidenceRegions(regions: ChatEvidenceChartRegion[] | undefined): ChartTimeRegion[] {
  const out: ChartTimeRegion[] = [];
  for (const r of regions ?? []) {
    if (!Number.isFinite(r.fromMs) || !Number.isFinite(r.toMs) || r.toMs <= r.fromMs) continue;
    const color = r.tone === 'err' ? 'var(--err)' : r.tone === 'warn' ? 'var(--warn)' : 'var(--text3)';
    out.push({ fromSec: r.fromMs / 1000, toSec: r.toMs / 1000, endSec: r.toMs / 1000, color, label: r.label });
  }
  return out;
}
