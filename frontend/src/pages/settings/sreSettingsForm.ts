// sreSettingsForm.ts — v0.10.1286 (AI SRE ayar ekranı). SAF: form ↔ tel.
//
// Sınırlar ve varsayılanlar internal/sre/settings.go'nun AYNASI
// (DefaultSettings + NormalizeSettings clampInt(v, lo, hi, def)). Sunucu
// aralık dışını SESSİZCE tavana kırpar, 0'ı varsayılana çevirir; form ise
// aralık dışını hata olarak gösterir (operatör "500 yazdım, 200 kaydoldu"
// sürprizini yaşamasın). Boş kutu = varsayılan (kutuya varsayılan basılmaz,
// placeholder'da görünür — vmForm dersi).
import type { SREPriority, SRESettings, SREEvidenceGateMode } from '@/lib/types';

export type SRENumKey =
  | 'maxPerTick' | 'maxPerHour' | 'serviceCooldownMin' | 'maxConcurrent'
  | 'itemBudgetSec' | 'maxAttempts' | 'maxQueue' | 'maxLLMCallsPerHour';

export interface SRENumBound {
  key: SRENumKey;
  label: string;
  hint: string;
  min: number;
  max: number;
  def: number;
}

/** NormalizeSettings sırası ve sınırları — settings.go ile birebir. */
export const SRE_NUM_BOUNDS: readonly SRENumBound[] = [
  { key: 'maxPerTick', label: 'Tik başına en çok kayıt', hint: 'üreticinin bir tikte açtığı kayıt', min: 1, max: 10, def: 2 },
  { key: 'maxPerHour', label: 'Saatte en çok kayıt', hint: 'otomatik kuyruk; elle "Araştır" hariç', min: 1, max: 200, def: 20 },
  { key: 'serviceCooldownMin', label: 'Servis bekleme (dk)', hint: 'aynı serviste ikinci problem "paylaşılan" kayıt alır', min: 1, max: 240, def: 15 },
  { key: 'maxConcurrent', label: 'Eşzamanlı araştırma', hint: 'pod başına', min: 1, max: 8, def: 2 },
  { key: 'itemBudgetSec', label: 'Araştırma bütçesi (sn)', hint: 'tek araştırmanın süre tavanı', min: 30, max: 600, def: 90 },
  { key: 'maxAttempts', label: 'En çok deneme', hint: 'çökme sonrası yeniden deneme tavanı', min: 1, max: 5, def: 2 },
  { key: 'maxQueue', label: 'Kuyruk tavanı', hint: 'dolunca yeni kayıt reddedilir', min: 10, max: 1000, def: 200 },
  { key: 'maxLLMCallsPerHour', label: 'Saatte en çok LLM çağrısı', hint: 'anlatım adımı için (Faz 1b)', min: 1, max: 500, def: 30 },
];

export const SRE_PRIORITIES: readonly SREPriority[] = ['P1', 'P2', 'P3'];
/** settings.go DefaultAutoPriorities — autoPriorities yokken anlamı. */
export const SRE_DEFAULT_PRIORITIES: readonly SREPriority[] = ['P1', 'P2'];

export interface SREForm {
  enabled: boolean;
  priorities: SREPriority[];
  nums: Record<SRENumKey, string>;
  /** CoSRE Faz B3 — kanıt kapısı kipi ('' = shadow, varsayılan). */
  evidenceGate: SREEvidenceGateMode;
  /** CoSRE Faz C1 — bildirimde araştırma özeti (varsayılan açık). */
  notifySummary: boolean;
}

/** Kanıt kapısı kipleri (Go sre.NormalizeGateMode; '' = shadow). */
export const SRE_GATE_MODES: { value: SREEvidenceGateMode; label: string }[] = [
  { value: '', label: 'gölge — karar kayda yazılır, gösterilmez (varsayılan)' },
  { value: 'show', label: 'göster — "Kanıt yetersiz" bandı' },
  { value: 'off', label: 'kapalı — hesaplanmaz' },
];

export type SREFormErrors = Partial<Record<SRENumKey, string>>;

const emptyNums = (): Record<SRENumKey, string> =>
  Object.fromEntries(SRE_NUM_BOUNDS.map(b => [b.key, ''])) as Record<SRENumKey, string>;

/** Varsayılan form (ayar hiç kaydedilmemiş gibi). */
export function sreDefaultForm(): SREForm {
  return { enabled: true, priorities: [...SRE_DEFAULT_PRIORITIES], nums: emptyNums(), evidenceGate: '', notifySummary: true };
}

/** Tel → form. Varsayılana eşit değer kutuya YAZILIR (sunucunun saklı değeri
 *  odur); yalnız 0/eksik alan boş kutu olur. */
export function sreToForm(s: SRESettings | null | undefined): SREForm {
  if (!s) return sreDefaultForm();
  const nums = emptyNums();
  for (const b of SRE_NUM_BOUNDS) {
    const v = s[b.key];
    nums[b.key] = typeof v === 'number' && v > 0 ? String(v) : '';
  }
  const pr = s.autoPriorities === undefined || s.autoPriorities === null
    ? [...SRE_DEFAULT_PRIORITIES]
    : SRE_PRIORITIES.filter(p => s.autoPriorities!.includes(p));
  const gate = s.evidenceGate === 'show' || s.evidenceGate === 'off' ? s.evidenceGate : '';
  return { enabled: s.enabled !== false, priorities: pr, nums, evidenceGate: gate, notifySummary: s.notifySummary !== false };
}

/** Alan doğrulaması — boş geçerli (varsayılan), aksi hâlde tam sayı ve aralıkta. */
export function validateSREForm(f: SREForm): SREFormErrors {
  const errs: SREFormErrors = {};
  for (const b of SRE_NUM_BOUNDS) {
    const t = f.nums[b.key].trim();
    if (t === '') continue;
    if (!/^\d+$/.test(t)) { errs[b.key] = 'Tam sayı olmalı'; continue; }
    const n = Number(t);
    if (n < b.min || n > b.max) errs[b.key] = `${b.min}–${b.max} aralığında olmalı`;
  }
  return errs;
}

/** Form → tel. Geçersizse alan hataları döner (PUT yapılmaz). Öncelikler
 *  her zaman açık liste olarak gider: hiçbiri seçili değilse [] = otomatik
 *  kuyruk kapalı (nil'e dönmez — sunucu nil'i varsayılan [P1,P2] okur). */
export function sreFormToWire(f: SREForm): { ok: true; wire: SRESettings } | { ok: false; errors: SREFormErrors } {
  const errors = validateSREForm(f);
  if (Object.keys(errors).length > 0) return { ok: false, errors };
  const num = (b: SRENumBound) => {
    const t = f.nums[b.key].trim();
    return t === '' ? b.def : Number(t);
  };
  const wire: SRESettings = {
    enabled: f.enabled,
    autoPriorities: SRE_PRIORITIES.filter(p => f.priorities.includes(p)),
    maxPerTick: 0, maxPerHour: 0, serviceCooldownMin: 0, maxConcurrent: 0,
    itemBudgetSec: 0, maxAttempts: 0, maxQueue: 0, maxLLMCallsPerHour: 0,
  };
  for (const b of SRE_NUM_BOUNDS) wire[b.key] = num(b);
  if (f.evidenceGate) wire.evidenceGate = f.evidenceGate; // '' = shadow → alan yok (bayt bayt eski gövde)
  if (!f.notifySummary) wire.notifySummary = false; // açık = alan yok (nil = açık)
  return { ok: true, wire };
}

/** Öncelik kutusunu çevirir (sıra P1→P3 korunur). */
export function togglePriority(list: readonly SREPriority[], p: SREPriority): SREPriority[] {
  const has = list.includes(p);
  return SRE_PRIORITIES.filter(x => (x === p ? !has : list.includes(x)));
}

/** Kaydet sonrası tek cümle. */
export function sreSavedSummaryTR(s: SRESettings): string {
  if (s.enabled === false) return 'Kaydedildi — AI SRE kapalı: yeni araştırma açılmaz, kuyruk tüketilmez.';
  const pr = s.autoPriorities ?? [...SRE_DEFAULT_PRIORITIES];
  return pr.length === 0
    ? 'Kaydedildi — otomatik kuyruk kapalı; elle "Araştır" çalışır.'
    : `Kaydedildi — ${pr.join(', ')} problemleri otomatik araştırılır (saatte en çok ${s.maxPerHour}).`;
}

export const SRE_LAUNCHER_LABEL: Record<string, string> = {
  deployment: 'ayrı sre deployment’ı',
  inprocess: 'bu süreçte (all modu)',
  none: 'yok',
};
