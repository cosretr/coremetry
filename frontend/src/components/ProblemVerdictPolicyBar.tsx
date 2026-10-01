import { useAuth } from '@/components/AuthProvider';
import { Button, useConfirm } from '@/components/ui';
import { useProblemVerdicts, useSetProblemVerdictPolicy } from '@/lib/queries';
import { verdictMutesNotifications } from '@/lib/problemVerdict';

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
  if (!q.data) return null;
  const on = verdictMutesNotifications(q.data.policy);

  const toggle = async () => {
    if (!on && !await confirm({
      title: '“Problem değil” bildirimleri susturulsun mu?',
      body: <>“Problem değil” olarak işaretli <b>alarm kuralı + servis</b> ve <b>exception / HTTP hata grubu</b> imzaları
        için mail ve kanal bildirimi <b>gönderilmez</b> (çözüm bildirimi dahil). Problemler yine açılır ve bu
        görünümde listelenir. İşareti kaldırmak ya da bu ayarı kapatmak bildirimi en geç 30 saniyede geri getirir.</>,
      confirmLabel: 'Bildirimleri sustur',
    })) return;
    set.mutate(!on);
  };

  return (
    <div className="row gap-2 row-wrap" style={{ marginBottom: 8, alignItems: 'center' }}>
      <span className={`badge ${on ? 'b-warn' : 'b-info'}`}>
        {on ? 'Bildirim: susturuluyor' : 'Bildirim: gönderiliyor'}
      </span>
      <span style={{ fontSize: 12, color: 'var(--text2)' }}>
        {on
          ? 'Bu görünümdeki alarm kuralı ve exception / HTTP hata imzaları mail / kanal bildirimi üretmez (anomali satırları hariç).'
          : 'Bu görünümdeki imzalar listeden gizlenir ama bildirimleri gelmeye devam eder.'}
        {on && q.data.policy?.updatedBy ? ` · ${q.data.policy.updatedBy}` : ''}
      </span>
      {isAdmin && (
        <Button size="sm" variant="secondary" loading={set.isPending} onClick={toggle}>
          {on ? 'Susturmayı kapat' : 'Bildirimleri de sustur'}
        </Button>
      )}
      {set.isError && <span role="alert" className="field-error">Ayar yazılamadı — yeniden deneyin.</span>}
    </div>
  );
}
