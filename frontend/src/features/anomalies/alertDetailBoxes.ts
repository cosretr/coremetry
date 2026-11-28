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
import { problemSummary } from './problemTitle';
import { showRootCauseInSummary } from './anomalyDetail';
import { tf, type Lang } from '@/lib/i18n'; // v0.10.2284 — metinler arayüz dilinde (varsayılan 'tr')
import { stdImpactView, prioReasonView, detailPrioReasonMsg, stdHeadlineView, fmtStdClock } from '@/lib/problemStandardView'; // v0.10.2299 — etki EN anahtardan (TR aynı); v0.10.2300 gerekçe

type BoxProblem = Pick<Problem, 'ruleName' | 'metric' | 'value' | 'threshold' | 'service' | 'ruleId'
  | 'priorityReason' | 'pod' | 'clusters' | 'rootCause' | 'recentDeploy'>;

/** "Ne oluyor" — her zaman dolu (alertProblemSummary her girdide cümle verir). */
export function alertWhatText(p: BoxProblem, std?: ProblemStandard | null, lang: Lang = 'tr'): string {
  // v0.10.2475 — consumer lag: ham ölçü satırı ("ml_group_lag value 17936.00,
  // threshold 5.00") yerine standart manşet (normal → şimdi, topic).
  const parts = [std?.lag ? stdHeadlineView(std, lang).text : alertProblemSummary(p, lang)];
  // v0.10.2487 (dilim 3) — kanıt cümlesi liste satırının özetiyle AYNI çiftten (EN'de anahtardan).
  const ev = problemSummary({ std }, lang)?.text.trim();
  if (ev) parts.push(ev);
  const why = prioReasonView(p.priorityReason, detailPrioReasonMsg(p.priorityReason, std), lang).text.trim();
  if (why) parts.push(tf('pd.text.prioWhy', lang, { why }));
  return parts.join(' ');
}

const CALLERS_SHOWN = 3;

/** "Kimi etkiliyor" — özne yoksa null (kutu yok). */
export function alertWhoText(p: BoxProblem, std?: ProblemStandard | null, lang: Lang = 'tr'): string | null {
  const subject = p.service ? subjectLabel(p.service) : '';
  if (!subject) return null;
  const impact = std?.impact?.known && std.impact.primary ? (stdImpactView(std, lang).text ?? '').trim() : '';
  // v0.10.2475 — consumer lag: hangi topic'te kaç mesaj geride ve ne zamandan beri.
  const parts = [std?.lag && impact
    ? tf('pd.text.lagWho', lang, { s: subject, impact, at: fmtStdClock(std.since.startedAt) })
    : impact ? tf('pd.text.affectedImpact', lang, { s: subject, impact }) : tf('pd.text.affected', lang, { s: subject })];
  const callers = std?.impact?.callers ?? [];
  if (callers.length > 0) {
    const more = callers.length - CALLERS_SHOWN;
    parts.push(tf('pd.text.callers', lang, { n: callers.length, c: callers.length, names: callers.slice(0, CALLERS_SHOWN).join(', '), more: more > 0 ? tf('pd.text.callersMore', lang, { c: more }) : '' }));
  }
  if (p.pod && p.pod !== p.service) parts.push(tf('pd.text.pod', lang, { pod: p.pod }));
  const cl = p.clusters ?? [];
  if (cl.length > 0) parts.push(tf('pd.text.cluster', lang, { list: cl.join(', ') }));
  return `${parts.join(' · ')}.`;
}

export interface AlertCause {
  text: string;
  /** "%72 güven" */
  confidence: string;
}

/** "Olası neden" — hipotez yoksa ya da güven eşiğin altındaysa null (kutu yok). */
export function alertCause(p: BoxProblem, lang: Lang = 'tr'): AlertCause | null {
  const rc = p.rootCause;
  const suspect = rc?.topSuspect?.trim() ?? '';
  if (!rc || !suspect || !showRootCauseInSummary(rc.confidence)) return null;
  let text = p.service && suspect === p.service
    ? tf('pd.text.causeSelf', lang, { s: subjectLabel(suspect) })
    : tf('pd.text.causeOther', lang, { s: subjectLabel(suspect) });
  const d = p.recentDeploy;
  if (d?.version) {
    text += tf('pd.text.causeDeploy', lang, { m: Math.max(1, Math.round(d.ageSeconds / 60)), v: d.version });
  }
  return { text, confidence: tf('pd.text.conf', lang, { p: Math.round(rc.confidence * 100) }) };
}
