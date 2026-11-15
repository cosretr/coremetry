// podMeta — /pod meta satırının saf yardımcıları (Clusters S10; plan §10.1,
// operatör yanıtları 2026-11-15). React/DOM yok.
// v0.10.2246 — metinler arayüz dilinde (podpage.*); varsayılan 'tr'.
import { tf, type Lang } from '@/lib/i18n';

const VERSION_KEYS = ['service.version', 'app.kubernetes.io/version', 'app_kubernetes_io_version', 'label_app_kubernetes_io_version'];

/** Sürüm etiketi: service.version, yoksa app.kubernetes.io/version (KSM yazımları dahil); yoksa ''. */
export function podVersion(labels: Record<string, string> | undefined | null): string {
  if (!labels) return '';
  for (const k of VERSION_KEYS) {
    const v = labels[k];
    if (v && v.trim()) return v.trim();
  }
  return '';
}

/** Ömrün başından bu yana: "3 gün", "5 saat", "12 dakika"; geçersizse ''. */
export function podAgeText(validFrom: string, nowMs: number, lang: Lang = 'tr'): string {
  const ts = Date.parse(validFrom);
  if (!Number.isFinite(ts) || ts > nowMs) return '';
  const min = Math.floor((nowMs - ts) / 60_000);
  if (min < 60) return tf('podpage.age.min', lang, { n: Math.max(min, 1) });
  const h = Math.floor(min / 60);
  if (h < 24) return tf('podpage.age.hour', lang, { n: h });
  return tf('podpage.age.day', lang, { n: Math.floor(h / 24) });
}

/** CoSRE'ye doldurulan (gönderilmeyen) soru — pod'un bağlamıyla. */
export function podCosreQuestion(c: { cluster: string; namespace: string; pod: string; service?: string }, lang: Lang = 'tr'): string {
  const ctx = [c.cluster && `cluster ${c.cluster}`, c.namespace && `namespace ${c.namespace}`, c.service && tf('podpage.ask.service', lang, { svc: c.service })]
    .filter(Boolean).join(', ');
  return `${tf('podpage.ask.q', lang, { pod: c.pod })}${ctx ? ` (${ctx})` : ''}`;
}
