import { useAuth } from '@/components/AuthProvider';
import { Button, useConfirm } from '@/components/ui';
import { useProblemVerdicts, useSetProblemVerdictPolicy } from '@/lib/queries';
import { verdictMutesNotifications } from '@/lib/problemVerdict';
import { t, useLang } from '@/lib/i18n'; // v0.10.2283 — metinler arayüz dilinde (TR aynı)

// ProblemVerdictPolicyBar — v0.10.1016 — "Problem değil" görünümünün başındaki
// durum satırı: bu işaret bildirimi de susturuyor mu. Varsayılan KAPALI
// (v0.10.1015 davranışı: yalnız görünüm). Herkes durumu GÖRÜR; anahtarı yalnız
// admin çevirir (sunucu da öyle). Açmak alarm kaybettirebilen bir karar olduğu
// için onay ister; kapatmak istemez.
export function ProblemVerdictPolicyBar() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  const q = useProblemVerdicts();
  const set = useSetProblemVerdictPolicy();
  const confirm = useConfirm();
  const lang = useLang();
  if (!q.data) return null;
  const on = verdictMutesNotifications(q.data.policy);

  const toggle = async () => {
    if (!on && !await confirm({
      title: t('vpolicy.title', lang),
      body: <>{t('vpolicy.body1', lang)}<b>{t('vpolicy.body2', lang)}</b>{t('vpolicy.body3', lang)}<b>{t('vpolicy.body4', lang)}</b>{t('vpolicy.body5', lang)}<b>{t('vpolicy.body6', lang)}</b>{t('vpolicy.body7', lang)}</>,
      confirmLabel: t('vpolicy.confirm', lang),
    })) return;
    set.mutate(!on);
  };

  return (
    <div className="row gap-2 row-wrap" style={{ marginBottom: 8, alignItems: 'center' }}>
      <span className={`badge ${on ? 'b-warn' : 'b-info'}`}>
        {t(on ? 'vpolicy.on' : 'vpolicy.off', lang)}
      </span>
      <span style={{ fontSize: 12, color: 'var(--text2)' }}>
        {t(on ? 'vpolicy.onText' : 'vpolicy.offText', lang)}
        {on && q.data.policy?.updatedBy ? ` · ${q.data.policy.updatedBy}` : ''}
      </span>
      {isAdmin && (
        <Button size="sm" variant="secondary" loading={set.isPending} onClick={toggle}>
          {t(on ? 'vpolicy.turnOff' : 'vpolicy.turnOn', lang)}
        </Button>
      )}
      {set.isError && <span role="alert" className="field-error">{t('vpolicy.err', lang)}</span>}
    </div>
  );
}
