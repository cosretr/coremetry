// InfraRoutesToggle — v0.10.2434 (operatör onaylı): metrik kipinde "Kaynak:
// metrik" seçicisinin yanında "Altyapı rotalarını göster" (varsayılan kapalı)
// + gizlenen tesisat rotası sayısı notu. Durum URL'de (?infra=1).
import { CheckboxField } from '@/components/coremetry/Field/Field';
import { useShowInfraRoutes } from '@/lib/infraRoutes';
import { t, tf, useLang } from '@/lib/i18n';

export function InfraRoutesToggle({ hidden }: { hidden?: number | null }) {
  const lang = useLang();
  const [show, setShow] = useShowInfraRoutes();
  return (
    <span className="inline-flex items-center gap-2">
      <CheckboxField label={t('infra.toggle', lang)} checked={show} onCheckedChange={setShow} />
      {!show && !!hidden && hidden > 0 && (
        <span className="text-xs" style={{ color: 'var(--text3)' }} title={t('infra.hiddenTitle', lang)}>
          {tf('infra.hidden', lang, { n: hidden })}
        </span>
      )}
    </span>
  );
}
