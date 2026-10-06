// problemLogTemplates — v0.10.1113 (operatör onaylı kuyruk maddesi "Log
// şablonu kök neden bağlantısı"). Problem detayının "Başlangıçta doğan log
// şablonları" bölümünün SAF çekirdeği: başlangıca göre göreli zaman etiketi ve
// satır başına /logs pivotu. Veri GET /api/problems/{id}/log-templates'ten
// (seçim sunucuda, dedektörün aile süzgeciyle).

import { logsHref } from '@/lib/logsUrl';
import type { ProblemLogTemplateEvidence } from '@/lib/types';

const MINUS = '−'; // "−" — tire değil, eksi işareti

function durTR(abs: number): string {
  if (abs < 60) return `${abs} sn`;
  const m = Math.floor(abs / 60);
  const s = abs % 60;
  return s ? `${m} dk ${s} sn` : `${m} dk`;
}

/** Başlangıca göre kısa etiket: "+40 sn", "−2 dk", "−2 dk 30 sn"; 0 → "başlangıçta". */
export function startOffsetLabel(offsetSec: number): string {
  if (!Number.isFinite(offsetSec)) return '—';
  const sec = Math.round(offsetSec);
  if (sec === 0) return 'başlangıçta';
  return `${sec < 0 ? MINUS : '+'}${durTR(Math.abs(sec))}`;
}

/** Uzun cümle (title): "başlangıçtan 40 sn önce" / "… sonra" / "başlangıç anında". */
export function startOffsetTitle(offsetSec: number): string {
  if (!Number.isFinite(offsetSec)) return '';
  const sec = Math.round(offsetSec);
  if (sec === 0) return 'başlangıç anında doğdu';
  return `başlangıçtan ${durTR(Math.abs(sec))} ${sec < 0 ? 'önce' : 'sonra'} doğdu`;
}

/** Satırın /logs pivotu: şablonun servisi + arama metni (sunucunun KESİLMEMİŞ
 *  şablondan türettiği, Şablonlar sekmesiyle aynı), pencere şablonun doğumundan
 *  1 dk önce → problem penceresinin sonu (doğumdan önceki saati açmak ES'e
 *  boşuna yük). Pencere bozuksa doğumdan itibaren 15 dk. */
export function newTemplateLogsHref(
  row: Pick<ProblemLogTemplateEvidence, 'service' | 'query' | 'firstSeen'>,
  problemWin: { fromNs: number; toNs: number },
): string {
  const fromNs = row.firstSeen - 60e9;
  const toNs = problemWin.toNs > fromNs ? problemWin.toNs : row.firstSeen + 15 * 60e9;
  return logsHref({
    window: { fromNs, toNs },
    service: row.service || undefined,
    q: row.query || undefined,
  });
}
