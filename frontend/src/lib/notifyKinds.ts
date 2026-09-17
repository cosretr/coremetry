/**
 * notifyKinds — kanal başına olay türü süzgeci (v0.10.747; operatör:
 * "anomali, incident ve problems ayrı ayrı gelsin").
 *
 * Gramer Inbox'ın `InboxKind` gramerinin aynısı (problem | anomaly |
 * incident) — sunucu chstore.NotifyKindsAll ile birebir. Boş liste =
 * süzgeç yok (mevcut kanallar aynen). "incident" seçeneği v0.10.748 ile
 * (incident açılış/çözüm bildirimi üreticisi) açıldı.
 */
import type { NotifyKind } from './types';

export interface NotifyKindOption {
  value: NotifyKind;
  label: string;
  hint: string;
}

/** UI'daki seçenekler, sırayla. */
export const NOTIFY_KIND_OPTIONS: NotifyKindOption[] = [
  { value: 'problem', label: 'Problem', hint: 'Operatör kuralları: alert rule, builtin, SLO, DB, runtime, watcher' },
  { value: 'anomaly', label: 'Anomali', hint: 'Anomali motoru: metrik anomalisi, service silent, dış tarayıcı, exception fırtınası / paylaşılan bağımlılık' },
  { value: 'incident', label: 'Incident', hint: 'Incident açılışı ve çözümü (otomatik korelasyon + manuel); critical incident P1, warning P2' },
];

const KNOWN: NotifyKind[] = ['problem', 'anomaly', 'incident'];
const LABEL: Record<NotifyKind, string> = { problem: 'Problem', anomaly: 'Anomali', incident: 'Incident' };

export function isNotifyKind(v: string): v is NotifyKind {
  return (KNOWN as string[]).includes(v);
}

/** Kırp + küçült + tekrar at + bilinmeyeni DÜŞÜR (sunucu reddeder; UI eski bir değeri sessizce taşımasın). Sıra korunur. */
export function normalizeKinds(input: readonly string[] | undefined | null): NotifyKind[] {
  const out: NotifyKind[] = [];
  for (const raw of input ?? []) {
    const k = String(raw).trim().toLowerCase();
    if (!isNotifyKind(k) || out.includes(k)) continue;
    out.push(k);
  }
  return out;
}

/** Onay kutusu: varsa çıkar, yoksa ekle; sıra KNOWN sırasına oturur (JSON diff'i kararlı). */
export function toggleKind(current: readonly NotifyKind[], k: NotifyKind): NotifyKind[] {
  const set = new Set(current);
  if (set.has(k)) set.delete(k); else set.add(k);
  return KNOWN.filter(x => set.has(x));
}

/** Tablo hücresi: boş → "Hepsi"; dolu → etiketler " · " ile. */
export function kindsSummary(kinds: readonly string[] | undefined | null): string {
  const ks = normalizeKinds(kinds);
  if (ks.length === 0) return 'Hepsi';
  return ks.map(k => LABEL[k]).join(' · ');
}
