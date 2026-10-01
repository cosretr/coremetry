// problemVerdict — v0.10.1015 — Problems sekmesinde ÖĞRETME'nin saf çekirdeği
// (operatör: "bütün hepsi gelsin, ben hangisi gerçek problem hangisi değil
// zamanla öğretelim").
//
// Karar OLAYA değil İMZAYA bağlıdır: aynı kural + servis (ya da aynı exception
// grubu) yeniden geldiğinde kendiliğinden aynı sınıfa düşer. İmza burada
// üretilir; sunucu yalnız biçimini doğrular (internal/chstore/problem_verdict.go
// ValidProblemSignature — önekler birebir aynı olmalı).
//
//   problem (alarm kuralı)      p:<ruleId>|<servis>
//   exception / HTTP hata grubu e:<fingerprint>
//   anomali                     a:<tür>|<servis>|<desen>
//   olay (incident)             imza YOK — elle açılan kayıt, öğretilmez
//
// Yalnız GÖRÜNÜM: "problem değil" imzaları varsayılan listeden çıkar ve kendi
// görünümünde toplanır; bildirimlere ve Problem yaşam döngüsüne dokunmaz.

import type { InboxItem, ProblemVerdict, ProblemVerdictKind } from '@/lib/types';

/** Satırın imzası; öğretilemeyen satırda (olay, eksik kimlik) null. */
export function inboxSignature(it: Pick<InboxItem, 'kind' | 'service' | 'problem' | 'exception' | 'anomaly'>): string | null {
  switch (it.kind) {
    case 'problem':
      return it.problem?.ruleId ? `p:${it.problem.ruleId}|${it.service ?? ''}` : null;
    case 'exception':
    case 'httperror':
      return it.exception?.fingerprint ? `e:${it.exception.fingerprint}` : null;
    case 'anomaly':
      return it.anomaly?.kind ? `a:${it.anomaly.kind}|${it.service ?? ''}|${it.anomaly.pattern ?? ''}` : null;
    default:
      return null;
  }
}

export type VerdictIndex = Map<string, ProblemVerdict>;

/** Liste → imza dizini (aynı imza iki kez gelirse İLK = en yeni kazanır). */
export function verdictIndex(list: ProblemVerdict[] | null | undefined): VerdictIndex {
  const m: VerdictIndex = new Map();
  for (const v of list ?? []) if (!m.has(v.signature)) m.set(v.signature, v);
  return m;
}

/** Satırın kararı (yoksa undefined). */
export function verdictOf(it: InboxItem, index: VerdictIndex): ProblemVerdictKind | undefined {
  const sig = inboxSignature(it);
  return sig ? index.get(sig)?.verdict : undefined;
}

/** Görünüm: 'triage' = varsayılan (problem değil HARİÇ her şey), 'noise' = yalnız "problem değil". */
export type VerdictView = 'triage' | 'noise';

export function parseVerdictView(raw: string | null | undefined): VerdictView {
  return raw === 'noise' ? 'noise' : 'triage';
}

/** Satırları görünüme göre süzer. Kararı olmayan ve "gerçek" satır triage'da kalır. */
export function filterByVerdictView<T extends InboxItem>(items: T[], index: VerdictIndex, view: VerdictView): T[] {
  return items.filter(it => (verdictOf(it, index) === 'noise') === (view === 'noise'));
}

/** Yüklü listedeki "problem değil" satır sayısı (çip sayacı). */
export function countNoise(items: InboxItem[] | null | undefined, index: VerdictIndex): number {
  let n = 0;
  for (const it of items ?? []) if (verdictOf(it, index) === 'noise') n++;
  return n;
}
