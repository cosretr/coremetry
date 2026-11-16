import type { Tone } from '@/components/ui';
import { t, type Lang } from '@/lib/i18n'; // v0.10.2288 — etiketler lang alır (varsayılan 'tr')
import type {
  ChatQueryEvidence, SREHypothesis, SREHypothesisBand, SREHypothesisCheck, SREInvestigation, SRETimelineKind,
} from '@/lib/types';

// hypothesisView — v0.10.1288 (AI SRE Faz 2). ProblemInvestigationCards'ın
// saf yardımcıları (bileşen dosyası yalnız bileşen dışa aktarsın —
// react-refresh). Go internal/sre hypotheses.go / timeline.go sabitlerinin
// aynası. Skor Go'da hesaplanır; burada yalnız çizilir.

/** Kartta gösterilen en çok hipotez (Go HypothesesShown). */
export const HYPOTHESES_SHOWN = 3;

/** Bant rozeti: olasılık bir uyarı değil — yüksek dikkat çeker, düşük soluk. */
export function bandTone(b: SREHypothesisBand): Tone {
  switch (b) {
    case 'yüksek': return 'warning';
    case 'orta': return 'info';
    default: return 'neutral';
  }
}

/** Bant rozetinin metni: değer sunucu belirtecidir (Go aynası); yalnız
 *  gösterim dile göre — TR'de belirtecin kendisi, bilinmeyen olduğu gibi. */
export function bandLabel(b: SREHypothesisBand, lang: Lang = 'tr'): string {
  switch (b) {
    case 'yüksek': return t('hyp.band.high', lang);
    case 'orta': return t('hyp.band.mid', lang);
    case 'düşük': return t('hyp.band.low', lang);
    default: return b;
  }
}

/** Kontrol sonucunun görünümü (+1 doğrular · −1 çürütür · 0 bilinmiyor). */
export function outcomeView(o: SREHypothesisCheck['outcome'], lang: Lang = 'tr'): { glyph: string; label: string } {
  if (o > 0) return { glyph: '✓', label: t('hyp.outcome.pos', lang) };
  if (o < 0) return { glyph: '✗', label: t('hyp.outcome.neg', lang) };
  return { glyph: '?', label: t('hyp.outcome.unknown', lang) };
}

/** Skor iki ondalık (Go aynı yuvarlamayı yapar). */
export function fmtScore(s: number): string {
  return Number.isFinite(s) ? s.toFixed(2) : '—';
}

/** Gösterilecek kartlar: skor sırası korunur (sunucu sıralı), ilk 3. */
export function shownHypotheses(inv: SREInvestigation): SREHypothesis[] {
  return (inv.hypotheses ?? []).slice(0, HYPOTHESES_SHOWN);
}

/** checkEvidence — kontrolün künyesi: "<adım>.evidences[i]" → adımın i'nci
 *  kanıtı; yalnız "<adım>" ya da bulunamayan künye → yok. */
export function checkEvidence(inv: SREInvestigation, ref: string | undefined): ChatQueryEvidence[] {
  if (!ref) return [];
  const m = /^([a-z_]+)\.evidences\[(\d+)\]$/.exec(ref);
  if (!m) return [];
  const ev = inv.steps.find(s => s.name === m[1])?.evidence?.[Number(m[2])];
  return ev ? [ev] : [];
}

/** İşaret metni ("elendi" / "onaylandı" + kim). */
export function humanLabel(h: SREHypothesis, lang: Lang = 'tr'): string | null {
  const hs = h.humanState;
  if (!hs) return null;
  const who = hs.by ? ` · ${hs.by}` : '';
  return t(hs.state === 'dismissed' ? 'hyp.human.dismissed' : 'hyp.human.confirmed', lang) + who;
}

/** Doğrula / Ele / Onayla yalnız bitmiş kayıtta (kuyruk / koşu işareti ezerdi). */
export function canAct(inv: SREInvestigation): boolean {
  return inv.state === 'done' || inv.state === 'partial';
}

// v0.10.2288 — olay türü adları katalogda (hyp.tl.*); TIMELINE_KIND bilinen
// türlerin listesi (Go timeline.go aynası). Bilinmeyen tür olduğu gibi.
export const TIMELINE_KIND: readonly SRETimelineKind[] = [
  'problem_start', 'deploy', 'spike_start', 'first_error', 'alert', 'alert_resolved', 'incident_attach',
];

export function timelineKindLabel(kind: string, lang: Lang = 'tr'): string {
  return (TIMELINE_KIND as readonly string[]).includes(kind) ? t(`hyp.tl.${kind}`, lang) : kind;
}

/** Kök-neden paneline kaydırma hedefi (ProblemDetail sarmalayıcısı). */
export const ROOT_CAUSE_ANCHOR = 'problem-root-cause';
