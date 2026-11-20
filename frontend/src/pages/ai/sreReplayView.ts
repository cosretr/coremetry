// sreReplayView — CoSRE doğruluk ölçümü A2: replay notlama
// panelinin SAF yardımcıları (bileşen dosyası yalnız bileşen dışa aktarsın).
// Ölçüler Go'da (sre.SummarizeReplay / ItemStats); burada yalnız biçim.
import type { SREReplayGrade, SREReplayListItem, SREReplayRun } from '@/lib/types';

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
  const v = r.variant?.contextPack ? 'bağlam paketi' : 'varsayılan';
  return r.trigger === 'schedule' ? `${v} · haftalık` : v;
}
