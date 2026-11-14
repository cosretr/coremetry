// ProblemsSection.tsx — the ?problem=<id> full-page host (AlertProblemHost)
// that resolves a notification deep link. Mounted by /problems
// (AnomaliesPage) and /inbox (Problems).
//
// The ?problem= page-level host STAYS mounted on /problems as well:
// every notification e-mail links to /problems?problem=<id>
// (notify.problemURL / api.problemLink), and that contract is locked
// by problem_url_test.go / email_html_test.go / webhook_template_test.go.
//
// v0.10.2170 (Problems S10, docs/frontend/problems-page-plan.md) — the
// "Alert rules" table component (ProblemsSection) lost its last mount in
// v0.10.2125 (S5: same alert problems are rows of the /inbox list) and is
// deleted; the file name stays so importers keep their module path.
//
// Imported by its module path (NOT the features/anomalies barrel):
// the barrel re-exports AnomaliesPage, which would drag the whole
// Exceptions page into the /inbox chunk.
//
// v0.10.2170 — the "Problem" Topbar is drawn by the mounting page (Inbox,
// AnomaliesPage), next to this host: env-picker honesty (envApplies.test)
// is judged per file and those pages read env; the host itself does not.
import { useMemo } from 'react';
import { Spinner, Empty } from '@/components/Spinner';
import { Button } from '@/components/ui/Button';
import { useProblems, useProblemByID, keys } from '@/lib/queries';
import { useQueryClient } from '@tanstack/react-query';
import { AlertProblemDetail } from './ProblemDetail';
import { findProblemInCaches } from './problemResolve';
import { PageShell } from '@/components/coremetry/PageShell';

// AlertProblemHost — resolves ?problem=<id> to a row and renders the
// Variant-B full-page detail.
//
// ÜÇ BASAMAKLI ÇÖZÜM, ucuzdan pahalıya:
//   1. cache — açık sayfaların halihazırda yüklü listeleri (bedava);
//   2. liste — "herhangi bir durumdan en yeni 200" (zaten pollanıyor);
//   3. by-id — tekil okuma (v0.9.825), YALNIZ ilk ikisi başarısızsa.
//
// 3. basamak neden şart: bildirim gönderilen problem çoğu zaman
// ÇÖZÜLÜR ve liste penceresinden düşer. Yani e-postadaki bağlantı tam
// da en çok gerektiği anda — "gece 3'te gelen sayfayı sabah aç" —
// "Problem not found" diyordu. Kayıt aslında duruyordu (problems
// tablosu, 90 günlük TTL); ekran olmayan bir veri kaybı bildiriyordu.
//
// Artık "bulunamadı" YALNIZ sunucu 404 döndüğünde çıkıyor — yani kayıt
// GERÇEKTEN yokken. Çözülmüş kayıt tam detayıyla, RESOLVED rozeti ve
// çözülme damgasıyla açılıyor (AlertProblemDetail).
// v0.9.837 — `backLabel` names the surface the host was opened FROM.
// Both /problems and /inbox render it now, and an "← Exceptions"
// button on the Problems queue would send the operator somewhere they
// were never standing.
export function AlertProblemHost({ id, isAdmin, onBack, backLabel = '← Exceptions' }: {
  id: string;
  isAdmin: boolean;
  onBack: () => void;
  backLabel?: string;
}) {
  const qc = useQueryClient();
  const q = useProblems({ limit: 200 });
  const cached = useMemo(() => findProblemInCaches(qc.getQueriesData({ queryKey: keys.problems.all }), id),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [qc, id, q.dataUpdatedAt]);
  const fromList = cached ?? (q.data?.items ?? []).find(x => x.id === id);

  // Tekil okuma YALNIZ liste yolu bitip satır çıkmadığında açılır.
  // Koşulsuz açmak, listeden zaten gelen her satır için bedava bir CH
  // sorgusu demek olurdu — açık problemlerin neredeyse tamamı listede.
  const listSettled = !q.isLoading && !q.isFetching;
  const byID = useProblemByID(id, { enabled: !fromList && listSettled });
  const p = fromList ?? byID.data ?? undefined;

  // Sunucu 404 dedi = kayıt GERÇEKTEN yok. null ile undefined ayrı:
  // undefined "henüz yüklenmedi" demek ve o anda "bulunamadı" yazmak
  // yanlış cümle olurdu.
  const genuinelyGone = byID.isSuccess && byID.data === null;
  const loading = !p && !genuinelyGone && (q.isLoading || byID.isLoading || byID.isFetching);
  // Liste hatası ancak tekil okuma da kurtaramadıysa hata ekranıdır.
  const failed = !p && !genuinelyGone && !loading && (q.isError || byID.isError);

  if (p) {
    return (
      <AlertProblemDetail
        problem={p}
        isAdmin={isAdmin}
        onBack={onBack}
        onChanged={() => {
          void q.refetch();
          void qc.invalidateQueries({ queryKey: keys.problems.all });
        }}
      />
    );
  }
  if (loading) return (<PageShell><Spinner /></PageShell>);
  if (failed) {
    return (
      <PageShell>
        <Empty icon="⚠" title="Problem yüklenemedi">
          <Button variant="secondary" size="sm" onClick={() => { void q.refetch(); void byID.refetch(); }}>Tekrar dene</Button>{' '}
          <Button variant="secondary" size="sm" onClick={onBack}>{backLabel}</Button>
        </Empty>
      </PageShell>
    );
  }
  return (
    <PageShell>
      <Empty icon="❓" title="Problem kaydı yok">
        Bu kimlikle bir problem bulunamadı — kayıt 90 günlük saklama
        penceresini aşmış ya da bağlantı eksik kopyalanmış olabilir.{' '}
        <Button variant="secondary" size="sm" onClick={onBack}>{backLabel}</Button>
      </Empty>
    </PageShell>
  );
}
