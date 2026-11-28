// HealthDot — v0.10.2488 (entity tutarlılık denetimi dilim 4, §3.4, Karar A2).
//
// Sağlık noktasının TEK tanımı: yalnız AÇIK Problem'lerden.
//   açık kritik Problem        → kırmızı  ('critical')
//   açık uyarı / bilgi Problem → sarı     ('warning')
//   açık Problem yok           → nötr halka ('ok'; K5: sağlıklı renk almaz)
// Hata oranı eşikleri noktada KULLANILMAZ — hata oranı sapması hücre rengidir
// (`lib/format/tone.ts` `errorTone`), sağlık değil. Sunucu tarafı aynı kural:
// `/api/services` `health` alanı `internal/api/health_dot.go` `problemHealth`
// ile yalnız açık Problem sayılarından kurulur.
//
// Önceki tanımlar (hepsi buraya katlandı): `pages/Services.tsx` yerel
// `HealthDot` (sunucunun hata-oranı dallı `scoreHealth`'ini çiziyordu),
// `pages/Service.tsx` `serviceHeadDot` (yalnız hata oranı >5 / >1, Problem'i
// görmüyordu), `ServiceMapNodeDrawer` rozet tonu.
//
// Görünüm mevcut `.ov-dot` sınıflarıyla (yeni CSS yok).
import type { CSSProperties } from 'react';
import { useT } from '@/lib/i18n';

export type HealthLevel = 'critical' | 'warning' | 'ok';

/** `/api/services` `health` ('red' | 'yellow' | 'green' | '') → seviye; yoksa undefined (çizilmez). */
export function healthFromServer(h: string | null | undefined): HealthLevel | undefined {
  if (h === 'red') return 'critical';
  if (h === 'yellow') return 'warning';
  if (h === 'green') return 'ok';
  return undefined;
}

/** SAF: Problem listesinden seviye — yalnız `status === 'open'` olanlar sayılır. */
export function healthFromProblems(ps: ReadonlyArray<{ status?: string; severity?: string }>): HealthLevel {
  let warn = false;
  for (const p of ps) {
    if (p.status !== 'open') continue;
    if (p.severity === 'critical') return 'critical';
    warn = true;
  }
  return warn ? 'warning' : 'ok';
}

const CLS: Record<HealthLevel, string> = { critical: 'red', warning: 'amber', ok: 'green' };

/** Rozet tonu (aynı tek tanımın metinli biçimi, ör. çekmece başlığı). */
export const HEALTH_BADGE: Record<HealthLevel, string> = { critical: 'b-err', warning: 'b-warn', ok: 'b-gray' };

export function HealthDot({ level, title, size, className, style, 'data-tour': tour }: {
  level: HealthLevel | undefined;
  /** Ayrıntılı ipucu (ör. sunucunun nedeni); yoksa seviyenin cümlesi. */
  title?: string;
  /** px; varsayılan `.ov-dot` 8 px. */
  size?: number;
  className?: string;
  style?: CSSProperties;
  /** Rehberli tur çapası — çağıranın literal'i (targetPin). */
  'data-tour'?: string;
}) {
  const t = useT();
  if (!level) return null;
  const label = t(`hd.${level}`);
  return (
    <span role="img" aria-label={label} title={title || label} data-tour={tour}
      data-health={level}
      className={`ov-dot ${CLS[level]}${className ? ` ${className}` : ''}`}
      style={size ? { width: size, height: size, ...style } : style} />
  );
}
