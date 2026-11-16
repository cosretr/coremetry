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

import type {
  AnomalyEvent, ExceptionGroup, InboxItem, NoiseReason, Problem, ProblemVerdict, ProblemVerdictKind, ProblemVerdictPolicy,
} from '@/lib/types';
import { t, type Lang } from '@/lib/i18n'; // v0.10.2283 — gerekçe etiketleri arayüz dilinde

// v0.10.1032 (operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor.
// Exception gibi detay gözükmüyor.") — imza artık yalnız kuyruk satırından
// (InboxItem) değil, TAM SAYFA detayların elindeki kayıttan da kurulur: alarm
// problemi (Problem), exception grubu (ExceptionGroup) ve anomali olayı
// (AnomalyEvent). Üç kalıp AŞAĞIDAKİ üç küçük fonksiyonda TEK yerde; satır
// imzası (inboxSignature) da onları çağırır. Böylece aynı kayıt kuyruktan ya da
// detaydan işaretlense AYNI imzaya yazılır (problemVerdict.test.ts eşitliği
// alan alan çiviler) — iki kopya ayrışsaydı detayda "problem değil" denen satır
// kuyrukta görünmeye devam ederdi.

/** Alarm kuralı imzası: p:<ruleId>|<servis> (boş kural → null). */
export function problemSignature(p: Pick<Problem, 'ruleId' | 'service'>): string | null {
  return p.ruleId ? `p:${p.ruleId}|${p.service ?? ''}` : null;
}

/** Exception / HTTP hata grubu imzası: e:<fingerprint> (boş → null). */
export function exceptionSignature(g: Pick<ExceptionGroup, 'fingerprint'>): string | null {
  return g.fingerprint ? `e:${g.fingerprint}` : null;
}

/** Anomali imzası: a:<tür>|<servis>|<desen> (boş tür → null). Olay kimliği GİRMEZ. */
export function anomalySignature(e: { kind?: string; service?: string; pattern?: string }): string | null {
  return e.kind ? `a:${e.kind}|${e.service ?? ''}|${e.pattern ?? ''}` : null;
}

/** Satırın imzası; öğretilemeyen satırda (olay, eksik kimlik) null. */
export function inboxSignature(it: Pick<InboxItem, 'kind' | 'service' | 'problem' | 'exception' | 'anomaly'>): string | null {
  switch (it.kind) {
    case 'problem':
      return it.problem ? problemSignature({ ruleId: it.problem.ruleId, service: it.service }) : null;
    case 'exception':
    case 'httperror':
      return it.exception ? exceptionSignature(it.exception) : null;
    case 'anomaly':
      return it.anomaly ? anomalySignature({ kind: it.anomaly.kind, service: it.service, pattern: it.anomaly.pattern }) : null;
    default:
      return null;
  }
}

/** v0.10.1032 — karar düğmelerinin öznesi: imza + kayda yazılan üst veri
 *  (etiket / tür / servis). Üst veri yalnız "Problem değil" görünümünde
 *  okunur; imza kararın kendisidir. */
export interface VerdictSubject {
  signature: string;
  label: string;
  kind: string;
  service: string;
  /** v0.10.1531 (R3) — işaret anındaki görünüm önceliği ve standart büyüklük:
   *  sunucu bildirim susturmasında baypas kıyası için saklar (öncelik yükseldi /
   *  etki ≥ 2×). Bilinmiyorsa yok. */
  priority?: string;
  magnitude?: number;
}

/** v0.10.1531 — standart büyüklük (Go chstore.StdMagnitude): oran, yoksa değer. */
export function stdMagnitudeOf(std: InboxItem['std']): number | undefined {
  if (!std) return undefined;
  const r = std.impact.primary.ratio ?? 0;
  const m = r > 0 ? r : std.impact.primary.value;
  return Number.isFinite(m) && m > 0 ? m : undefined;
}

/** Kuyruk satırından özne — etiket satırın başlığı (sunucu üretir). */
export function inboxVerdictSubject(it: InboxItem): VerdictSubject | null {
  const signature = inboxSignature(it);
  return signature
    ? { signature, label: it.title, kind: it.kind, service: it.service, priority: it.priority, magnitude: stdMagnitudeOf(it.std) }
    : null;
}

/** v0.10.1531 (R3) — gerekçe etiketleri (menü sırası = Go chstore.NoiseReasons).
 *  v0.10.2292 — etiket / ipucu metni lib/i18n'de (verdict.reason.*); bu kayıt TR görünümü. */
const NOISE_REASON_IDS: NoiseReason[] = ['expected', 'too_sensitive', 'known_accepted', 'wrong_attribution', 'other'];
export const NOISE_REASONS: { id: NoiseReason; label: string; hint: string }[] = NOISE_REASON_IDS.map(id => ({
  id, label: t(`verdict.reason.${id}`, 'tr'), hint: t(`verdict.reason.${id}.hint`, 'tr'),
}));
export function noiseReasonLabel(r: NoiseReason | undefined | '', lang: Lang = 'tr'): string {
  return r && NOISE_REASON_IDS.includes(r) ? t(`verdict.reason.${r}`, lang) : '';
}

/** v0.10.2283 — gerekçe ipucu arayüz dilinde. */
export function noiseReasonHint(r: NoiseReason, lang: Lang): string {
  return NOISE_REASON_IDS.includes(r) ? t(`verdict.reason.${r}.hint`, lang) : '';
}

/** Alarm problemi detayından özne. Etiket = kural adı (inbox.go problemToInbox
 *  satır başlığını da RuleName'den kurar). */
export function problemVerdictSubject(p: Problem): VerdictSubject | null {
  const signature = problemSignature(p);
  return signature ? { signature, label: p.ruleName, kind: 'problem', service: p.service, priority: p.priority || undefined } : null;
}

// HTTP hata grubu = çıplak 3 haneli tür; sunucudaki TEK tanım
// chstore.HTTPErrorTypeRe (exception_inbox.go) — test kaynağı karşılaştırır.
const HTTP_ERROR_TYPE_RE = /^[0-9]{3}$/;

/** Exception grubu detayından özne. Etiket = tür (inbox.go exceptionToInbox). */
export function exceptionVerdictSubject(g: ExceptionGroup): VerdictSubject | null {
  const signature = exceptionSignature(g);
  if (!signature) return null;
  return {
    signature, label: g.type,
    kind: HTTP_ERROR_TYPE_RE.test(g.type) ? 'httperror' : 'exception',
    service: g.service,
  };
}

/** Anomali olayından özne. Etiket "<tür> · <desen>" (inbox.go anomalyToInbox). */
export function anomalyVerdictSubject(e: Pick<AnomalyEvent, 'kind' | 'service' | 'pattern'>): VerdictSubject | null {
  const signature = anomalySignature(e);
  return signature ? { signature, label: `${e.kind} · ${e.pattern}`, kind: 'anomaly', service: e.service } : null;
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
export function verdictNotifyHint(sig: string | null | undefined, policy: ProblemVerdictPolicy | null | undefined, lang: Lang = 'tr'): string {
  if (!verdictMutesNotifications(policy)) return t('pd.v.notifyNone', lang);
  return t(verdictCoversNotifications(sig) ? 'pd.v.notifyMute' : 'pd.v.notifyAnomaly', lang);
}

/** v0.10.1032 (operatör: "Çok detay verince daha anlaşılır olmuyor") — detay
 *  sayfalarının TEK kısa cümlesi (çekmece uzun açıklamayı korur). Her tür için
 *  doğru olan çekirdek + yalnız yönetici politikası AÇIKKEN tek bildirim cümleciği. */
export function verdictCompactHint(sig: string | null | undefined, policy: ProblemVerdictPolicy | null | undefined, lang: Lang = 'tr'): string {
  const core = t('pd.v.compact', lang);
  if (!verdictMutesNotifications(policy)) return core;
  return core + t(verdictCoversNotifications(sig) ? 'pd.v.compactMute' : 'pd.v.compactAnomaly', lang);
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
