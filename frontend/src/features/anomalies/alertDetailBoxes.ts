// alertDetailBoxes — v0.10.2127 (Problems sadeleştirme S7; operatör kararı
// 2026-11-13). Alarm problemi detayının dört kısa kutusunun METNİ, SAF ve
// deterministik (emsal lib/inboxRowText.ts, internal/api/chat_summary_card.go):
// model çağrısı yok, uydurma metin yok. Alan yoksa kutu GİZLİ — boş kutu ya da
// "bilinmiyor" dolgusu çizilmez.
//
//   Ne oluyor       alertProblemSummary (kural · metrik değer/eşik) + sunucu
//                   standardının kanıt cümlesi + öncelik gerekçesi
//   Kimi etkiliyor  özne (+ ölçülen etki) · topolojiyle doğrulanmış çağıranlar
//                   · pod · kümeler
//   Olası neden     Problem.rootCause (root_cause_hypotheses özeti, liste ve
//                   tekil okumada aynı toplu birleştirme) — şüpheli boşsa ya da
//                   güven anomali detayıyla AYNI eşiğin (%50) altındaysa null
//                   = KUTU YOK. Deploy kanıtı yalnız kutu görünürken eklenir.
//   Ne yapılabilir  bağlantılar (çağıran kurar; burada metin yok)
import type { Problem, ProblemStandard } from '@/lib/types';
import { subjectLabel } from '@/lib/problemSubject';
import { alertProblemSummary } from './detailSummary';
import { showRootCauseInSummary } from './anomalyDetail';

type BoxProblem = Pick<Problem, 'ruleName' | 'metric' | 'value' | 'threshold' | 'service' | 'ruleId'
  | 'priorityReason' | 'pod' | 'clusters' | 'rootCause' | 'recentDeploy'>;

/** "Ne oluyor" — her zaman dolu (alertProblemSummary her girdide cümle verir). */
export function alertWhatText(p: BoxProblem, std?: ProblemStandard | null): string {
  const parts = [alertProblemSummary(p)];
  const ev = std?.evidence?.sentence?.trim();
  if (ev) parts.push(ev);
  const why = p.priorityReason?.trim();
  if (why) parts.push(`Öncelik gerekçesi: ${why}.`);
  return parts.join(' ');
}

const CALLERS_SHOWN = 3;

/** "Kimi etkiliyor" — özne yoksa null (kutu yok). */
export function alertWhoText(p: BoxProblem, std?: ProblemStandard | null): string | null {
  const subject = p.service ? subjectLabel(p.service) : '';
  if (!subject) return null;
  const impact = std?.impact?.known ? std.impact.primary?.text?.trim() : '';
  const parts = [impact ? `${subject} etkileniyor (${impact})` : `${subject} etkileniyor`];
  const callers = std?.impact?.callers ?? [];
  if (callers.length > 0) {
    const more = callers.length - CALLERS_SHOWN;
    parts.push(`çağıran ${callers.length} servis: ${callers.slice(0, CALLERS_SHOWN).join(', ')}${more > 0 ? ` ve ${more} diğeri` : ''}`);
  }
  if (p.pod && p.pod !== p.service) parts.push(`pod ${p.pod}`);
  const cl = p.clusters ?? [];
  if (cl.length > 0) parts.push(`küme: ${cl.join(', ')}`);
  return `${parts.join(' · ')}.`;
}

export interface AlertCause {
  text: string;
  /** "%72 güven" */
  confidence: string;
}

/** "Olası neden" — hipotez yoksa ya da güven eşiğin altındaysa null (kutu yok). */
export function alertCause(p: BoxProblem): AlertCause | null {
  const rc = p.rootCause;
  const suspect = rc?.topSuspect?.trim() ?? '';
  if (!rc || !suspect || !showRootCauseInSummary(rc.confidence)) return null;
  let text = p.service && suspect === p.service
    ? `Sorun büyük olasılıkla ${subjectLabel(suspect)} servisinin kendisinde.`
    : `En olası kaynak: ${subjectLabel(suspect)}.`;
  const d = p.recentDeploy;
  if (d?.version) {
    text += ` Başlangıçtan ${Math.max(1, Math.round(d.ageSeconds / 60))} dk önce ${d.version} deploy edildi.`;
  }
  return { text, confidence: `%${Math.round(rc.confidence * 100)} güven` };
}
