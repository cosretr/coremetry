// sreReplayView — CoSRE doğruluk ölçümü A2: replay notlama
// panelinin SAF yardımcıları (bileşen dosyası yalnız bileşen dışa aktarsın).
// Ölçüler Go'da (sre.SummarizeReplay / ItemStats); burada yalnız biçim.
import type { SREReplayGrade, SREReplayListItem, SREReplayPath, SREReplayRun, SREReplayVerdict } from '@/lib/types';

export const GRADE_ORDER: SREReplayGrade[] = ['bullseye', 'on_target', 'miss', 'nowhere'];

/** Not adı (ölçek incident.io: 100 / 65 / 35 / 0). Admin ekranı — çevrilmez. */
export const GRADE_LABEL: Record<SREReplayGrade, string> = {
  bullseye: 'Tam isabet', on_target: 'Hedefte', miss: 'Iska', nowhere: 'Çok uzak',
};

export const GRADE_TONE: Record<SREReplayGrade, 'success' | 'info' | 'warning' | 'danger'> = {
  bullseye: 'success', on_target: 'info', miss: 'warning', nowhere: 'danger',
};

const RUN_STATE_LABEL: Record<string, string> = {
  queued: 'kuyrukta', running: 'tekrar ediliyor', done: 'not bekliyor', partial: 'not bekliyor', error: 'tamamlanamadı', skipped: 'atlandı',
};

/** Puan (0–100) → "67,5"; yoksa "—". */
export function scoreText(v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return '—';
  return (Math.round(v * 10) / 10).toString().replace('.', ',');
}

/** Oran (0..1) → "%75". */
export function pctText(v: number | null | undefined): string {
  return v == null || !Number.isFinite(v) ? '—' : `%${Math.round(v * 100)}`;
}

/** Koşu hücresinin metni: not adı ya da durum / not hatası. */
export function runLabel(r: SREReplayRun): string {
  if (r.grade) return GRADE_LABEL[r.grade] ?? r.grade;
  if (r.gradeErr) return 'notsuz';
  return RUN_STATE_LABEL[r.state] ?? r.state;
}

/** Koşu ipucu: hakem gerekçesi + erişim + sonuç özeti. */
export function runTitle(r: SREReplayRun): string {
  const parts: string[] = [`koşu ${r.rep}`];
  if (r.why) parts.push(r.why);
  if (r.gradeErr) parts.push(r.gradeErr);
  if (r.reason) parts.push(r.reason);
  if (r.reach != null) parts.push(`erişim: ${r.reach ? 'suçluya vardı' : 'suçluya varmadı'} (${r.reachBy === 'judge' ? 'hakem' : 'kanıt'})`);
  if (r.conclusion) parts.push(`sonuç: ${r.conclusion}`);
  return parts.join(' · ');
}

/** Problem satırının erişim özeti: "2/2" (bilinen koşularda suçluya varış). */
export function reachCell(runs: SREReplayRun[]): string | null {
  const known = runs.filter(r => r.reach != null);
  if (known.length === 0) return null;
  return `${known.filter(r => r.reach).length}/${known.length}`;
}

/** Eğilim: en eski → en yeni biten raporların puanı (≤8). */
export function scoreTrend(reports: SREReplayListItem[]): string {
  const pts = reports
    .filter(r => r.summary.score != null)
    .sort((a, b) => a.createdAt - b.createdAt)
    .slice(-8)
    .map(r => scoreText(r.summary.score));
  return pts.length ? pts.join(' → ') : '—';
}

/** Rapor kipi: varyant + tetik. */
export function reportMode(r: Pick<SREReplayListItem, 'variant' | 'trigger'>): string {
  const parts = [r.variant?.contextPack ? 'bağlam paketi' : '', r.variant?.plain ? 'B kapalı (plain)' : '',
    r.variant?.noMemory ? 'notlar kapalı (noMemory)' : '', r.variant?.noKnown ? 'platform bilgisi kapalı (noKnown)' : ''].filter(Boolean);
  const v = parts.length ? parts.join(' + ') : 'varsayılan';
  return r.trigger === 'schedule' ? `${v} · haftalık` : v;
}

/** Yol hücresi: "4 okuma · 2 dalga · 1 boşa" (+ tavan); yolu olan koşu yoksa null.
 *  Koşular ayrı ayrı (koşu 1 / koşu 2) " | " ile. */
export function pathCell(runs: SREReplayRun[]): string | null {
  const parts = runs.filter(r => r.path).map(r => pathText(r.path as SREReplayPath));
  return parts.length ? parts.join(' | ') : null;
}

export function pathText(p: SREReplayPath): string {
  const cap = p.capHit ? (p.cap === 'step' ? ' · adım tavanı' : ' · bütçe tavanı') : '';
  return `${p.toolCalls} okuma · ${p.rounds} dalga · ${p.wasted} boşa${cap}`;
}

/** Yol ipucu: boşa sayılan adımlar. */
export function pathTitle(runs: SREReplayRun[]): string {
  return runs.filter(r => r.path).map(r => {
    const w = r.path?.wastedSteps?.length ? `boşa: ${r.path.wastedSteps.join(', ')}` : 'boşa okuma yok';
    return `koşu ${r.rep}: ${r.path?.steps ?? 0} adım · ${w}`;
  }).join(' · ');
}

/** Medyan → "4" / "2,5" / "—". */
export function medText(v: number | null | undefined): string {
  return scoreText(v);
}

/** İşaretli fark: +12,5 / −3 / 0 / —. pct=true → oran (0..1) puan olarak (%). */
export function deltaText(v: number | null | undefined, pct = false): string {
  if (v == null || !Number.isFinite(v)) return '—';
  const n = pct ? Math.round(v * 1000) / 10 : Math.round(v * 10) / 10;
  const body = Math.abs(n).toString().replace('.', ',');
  const sign = n > 0 ? '+' : n < 0 ? '−' : '';
  return `${sign}${body}${pct ? ' puan' : ''}`;
}

export const VERDICT_LABEL: Record<SREReplayVerdict, string> = {
  improved: 'iyileşti', regressed: 'geriledi', same: 'aynı', ungraded: 'notsuz', only_base: 'yalnız tabanda', only_cand: 'yalnız adayda',
};

export const VERDICT_TONE: Record<SREReplayVerdict, 'success' | 'danger' | 'neutral'> = {
  improved: 'success', regressed: 'danger', same: 'neutral', ungraded: 'neutral', only_base: 'neutral', only_cand: 'neutral',
};

/** Rapor sürümü: başlatan sürüm (+ notlayan farklıysa "→ notlayan"). */
export function buildText(r: { build?: string; gradeBuild?: string }): string {
  if (!r.build) return r.gradeBuild ? `? → ${r.gradeBuild}` : '—';
  return r.gradeBuild && r.gradeBuild !== r.build ? `${r.build} → ${r.gradeBuild}` : r.build;
}

export interface VersionPoint { build: string; score: number; reports: number; at: number }

/** Sürüm eğilimi: puanı olan raporlar başlatan sürüme göre gruplanır (sürümsüz
 *  rapor girmez); sürüm başına ortalama puan, sürümün İLK raporunun zamanına
 *  göre eskiden yeniye; son 12 sürüm. */
export function versionTrend(reports: SREReplayListItem[]): VersionPoint[] {
  const by = new Map<string, { sum: number; n: number; at: number }>();
  for (const r of reports) {
    if (!r.build || r.summary.score == null) continue;
    const cur = by.get(r.build) ?? { sum: 0, n: 0, at: r.createdAt };
    cur.sum += r.summary.score;
    cur.n += 1;
    cur.at = Math.min(cur.at, r.createdAt);
    by.set(r.build, cur);
  }
  return [...by.entries()]
    .map(([build, v]) => ({ build, score: Math.round((v.sum / v.n) * 10) / 10, reports: v.n, at: v.at }))
    .sort((a, b) => a.at - b.at)
    .slice(-12);
}

/** Karşılaştırmanın varsayılan tabanı: seçili rapordan ÖNCEKİ en yeni rapor. */
export function defaultBase(reports: Pick<SREReplayListItem, 'reportId' | 'createdAt'>[], candId: string): string {
  const cand = reports.find(r => r.reportId === candId);
  if (!cand) return '';
  const older = reports.filter(r => r.createdAt < cand.createdAt).sort((a, b) => b.createdAt - a.createdAt);
  return older[0]?.reportId ?? '';
}
