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
// Varsayılan yalnız GÖRÜNÜM: "problem değil" imzaları varsayılan listeden çıkar
// ve kendi görünümünde toplanır; Problem yaşam döngüsüne dokunmaz. v0.10.1016:
// yönetici politikayı açarsa (muteNotifications; varsayılan KAPALI) p: ve e:
// imzalarının mail / kanal bildirimi de susar — a: (anomali) kapsam dışı.
// İmza kalıpları sunucuda yeniden üretilir (internal/notify/verdict_silence.go
// verdictSignature) — biri değişirse diğeri de değişmeli (Go testi pinler).

import type { InboxItem, ProblemVerdict, ProblemVerdictKind, ProblemVerdictPolicy } from '@/lib/types';

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

/** Politika "problem değil" bildirimi de susturuyor mu (eksik politika = hayır). */
export function verdictMutesNotifications(policy: ProblemVerdictPolicy | null | undefined): boolean {
  return policy?.muteNotifications === true;
}

/** Bu imzanın bildirimi politikadan etkilenir mi: alarm kuralı (p:) ve
 *  exception / HTTP hata grubu (e:). Anomali (a:) kapsam dışı. */
export function verdictCoversNotifications(sig: string | null | undefined): boolean {
  return !!sig && (sig.startsWith('p:') || sig.startsWith('e:'));
}

/** Çekmecedeki açıklamanın bildirimle ilgili son cümlesi. */
export function verdictNotifyHint(sig: string | null | undefined, policy: ProblemVerdictPolicy | null | undefined): string {
  if (!verdictMutesNotifications(policy)) return 'bildirimleri susturmaz, geri alınabilir.';
  return verdictCoversNotifications(sig)
    ? 'bu imzanın mail / kanal bildirimlerini de susturur (yönetici ayarı açık), geri alınabilir.'
    : 'anomali satırlarının bildirimi bu işaretten etkilenmez, geri alınabilir.';
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
