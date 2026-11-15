// podMeta — /pod meta satırının saf yardımcıları (Clusters S10; plan §10.1,
// operatör yanıtları 2026-11-15). React/DOM yok.

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
export function podAgeText(validFrom: string, nowMs: number): string {
  const t = Date.parse(validFrom);
  if (!Number.isFinite(t) || t > nowMs) return '';
  const min = Math.floor((nowMs - t) / 60_000);
  if (min < 60) return `${Math.max(min, 1)} dakika`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} saat`;
  return `${Math.floor(h / 24)} gün`;
}

/** CoSRE'ye doldurulan (gönderilmeyen) soru — pod'un bağlamıyla. */
export function podCosreQuestion(c: { cluster: string; namespace: string; pod: string; service?: string }): string {
  const ctx = [c.cluster && `cluster ${c.cluster}`, c.namespace && `namespace ${c.namespace}`, c.service && `servis ${c.service}`]
    .filter(Boolean).join(', ');
  return `${c.pod} pod'unun durumu nedir, bir sorun var mı?${ctx ? ` (${ctx})` : ''}`;
}
