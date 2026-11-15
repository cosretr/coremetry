// mlGroupMembers — v0.10.2257: yeni ML kartında (MLRecordCard) grubun diğer
// üye olayları. Veri grup sayfasının zaten yüklediği liste satırlarından
// (useMLEvents → MLGroupRow.members); üye başına sorgu yok. Üyenin olay
// kaydı (ml_events.record) varsa tepe / beklenen / birim / sınıf / operasyon
// oradan; yoksa satırın observed / baseline değerleri birimsiz. SAF.
import type { Lang } from '@/lib/i18n';
import type { MLEvent, MLRecordView } from '@/lib/types';
import { fmtRecordValue } from './mlRecord';

/** Bu sayının üstünde blok kapalı açılır. */
export const GROUP_MEMBERS_OPEN_MAX = 5;

export interface GroupMemberRow {
  id: string;
  service: string;
  /** operasyon (kayıttan) ya da dedektör kimliği */
  what: string;
  whatIsDetector: boolean;
  detector: string;
  cls: string;
  peak: string;
  expected: string;
  at: number; // unix ns (başlangıç)
}

type RecordLite = Pick<MLRecordView, 'subject' | 'series' | 'peakValue' | 'expectedValue' | 'class'>;

/** SAF: üyenin kayıt JSON'u (bozuk / eksik → null). */
export function parseMemberRecord(raw: string | undefined): RecordLite | null {
  if (!raw) return null;
  try {
    const r = JSON.parse(raw) as Partial<RecordLite>;
    if (!r || typeof r !== 'object' || !r.series || typeof r.peakValue !== 'number' || typeof r.expectedValue !== 'number') return null;
    return r as RecordLite;
  } catch {
    return null;
  }
}

/** SAF: lider dışındaki üyeler, başlangıç sırasıyla. */
export function groupMemberRows(members: MLEvent[], leadId: string, lang: Lang): GroupMemberRow[] {
  return members
    .filter(m => m.id !== leadId)
    .sort((a, b) => a.startedAt - b.startedAt || a.id.localeCompare(b.id))
    .map(m => {
      const rec = parseMemberRecord(m.record);
      const unit = rec?.series.unit ?? '';
      const op = rec?.subject?.operation || '';
      return {
        id: m.id, service: m.service, detector: m.detector,
        what: op || m.detector, whatIsDetector: !op,
        cls: rec?.class || m.class || '',
        peak: fmtRecordValue(rec ? rec.peakValue : m.observed, unit, lang),
        expected: fmtRecordValue(rec ? rec.expectedValue : m.baseline, unit, lang),
        at: m.startedAt,
      };
    });
}
