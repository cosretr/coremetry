// ProblemRequestsSection — v0.10.2380 (triage mockup dilim 1; operatör
// 2026-11-19: "hata oranı artan problemler için de başarılı / başarısız
// gösterebilirsin"). Hata oranı Problem'inin detayındaki İLK grafik: dakikada
// başarılı (yeşil, sol eksen) ve başarısız (kırmızı, sağ eksen) istek +
// "Şimdi ve normalde" rakamları — ML olay kartındaki grafiğin AYNISI
// (MLRecordChart, olay kaydından).
//
// Veri: olay çizelgesinin zaten yaptığı okuma (GET /api/problems/{id}/events,
// aynı sorgu anahtarı — ikinci istek yok); sunucu servis hata oranı olayının
// kaydını satıra iliştirir. Kayıt yoksa (başka tür Problem, eski olay, 5 dk'lık
// eski seri) bölüm HİÇ çizilmez; yüklenirken yer tutucu yok (ek bilgidir).
import { lazy, Suspense } from 'react';
import { Spinner } from '@/components/Spinner';
import { useProblemEvents } from '@/lib/queries/problems';
import { t, useLang } from '@/lib/i18n';
import { requestsCapable } from '@/components/coremetry/anomalies/mlRequests';
import { Sect } from './detailSections';

// Grafik parçası ayrı yüklenir: Problem detayının çoğu açılışında bu bölüm yok.
const MLRecordChart = lazy(() => import('@/components/coremetry/anomalies/MLRecordChart').then(m => ({ default: m.MLRecordChart })));

export function ProblemRequestsSection({ problemId }: { problemId: string }) {
  const q = useProblemEvents(problemId);
  const lang = useLang();
  const rec = q.data?.items?.find(it => requestsCapable(it.record))?.record;
  if (!rec) return null;
  return (
    <Sect title={t('mlReq.title', lang)}>
      <Suspense fallback={<Spinner />}>
        <MLRecordChart rec={rec} frameless />
      </Suspense>
    </Sect>
  );
}
