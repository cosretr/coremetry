// ProblemBreakdownSection — v0.10.2388 (triage mockup dilim 3; operatör onaylı
// mockup'taki "Endpoint'e / Sürüme / Exception'a göre" kırılımları, hedef:
// hata oranı Problem'i açıldığında hatanın NEREDE olduğunu sayfa kendisi
// söylesin). Deterministik: model çağrısı yok; veri GET
// /api/problems/{id}/breakdown (örneklenmemiş sunucu metriği + tam hata
// kayıtları + bağımlılık olayları).
//
// Yükleme: bu dosya ProblemDetail'den TEMBEL yüklenir ve yalnız
// breakdownCapable Problem'de mount edilir — başka tür Problem'de ne paket
// baytı ne istek. Yüklenirken yer tutucu yok (kardeş ProblemRequestsSection
// duruşu: ek bilgidir); çizilecek blok yoksa bölüm HİÇ çizilmez. İstek
// düşerse bölüm başlığıyla tek satır "okunamadı" (sayfanın geri kalanı aynen).
import { useMemo } from 'react';
import { ProblemBreakdownBlocks, ProblemBreakdownError, type BreakdownT } from '@/components/coremetry/problems/ProblemBreakdownBlocks';
import { tf, useLang } from '@/lib/i18n';
import { breakdownDeployFact, breakdownWindowMinutes, hasBreakdownBlocks } from '@/lib/problemBreakdown';
import { useProblemBreakdown } from '@/lib/queries/problems';
import type { Problem } from '@/lib/types';
import { fmtClock } from '@/lib/utils';
import { Sect } from './detailSections';

const clockHM = (ns: number) => fmtClock(ns / 1e6).slice(0, 5);

export default function ProblemBreakdownSection({ problem }: { problem: Problem }) {
  const q = useProblemBreakdown(problem.id);
  const lang = useLang();
  const t = useMemo<BreakdownT>(() => (key, vars) => tf(key, lang, vars), [lang]);
  if (q.isError) {
    return (
      <Sect title={t('pbd.title')}>
        <ProblemBreakdownError t={t} />
      </Sect>
    );
  }
  const d = q.data;
  if (!d || !hasBreakdownBlocks(d)) return null;
  const sub = t(d.open ? 'pbd.sub.open' : 'pbd.sub.closed', { m: breakdownWindowMinutes(d) });
  return (
    <Sect title={t('pbd.title')} sub={sub}>
      <ProblemBreakdownBlocks data={d} deploy={breakdownDeployFact(problem)} t={t} lang={lang} clock={clockHM} />
    </Sect>
  );
}
