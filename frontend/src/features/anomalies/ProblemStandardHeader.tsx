// ProblemStandardHeader — v0.10.1529 (Problem standardı R2; operatör
// 2026-10-25: "başlık tüm detay sayfalarında"). Alarm/anomali problemi,
// exception grubu ve anomali olayı detayının ÜST bloğu, mockup'taki biçim:
//
//   manşet         özne (mono) — standart manşet   (showTitle=false: sayfanın
//                  kendi başlığı kalır, ör. exception tipi)
//   dört kart      Etki · Ne zamandan beri · Güven · Kaynak
//   üç satır       Neden böyle değerlendiriyoruz · Bağlam · Sonraki adım
//
// Metin Inbox satırıyla AYNI saf çekirdekten (lib/problemStandardView) ve
// aynı sunucu standardından (GET /api/problem-standard, satırla aynı TTL):
// satır "YÜKSEK" derken başlık "orta" demez. Standart yüklenmemişken ya da
// bulunamadığında `fallback` çizilir (önceki tek cümlelik özet) — sayfa
// hiçbir zaman boş başlıkla açılmaz.
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useProblemStandard } from '@/lib/queries';
import {
  splitStdHeadline, stdImpactSecondary, fmtStdClock, stdDurationLine, stdGateSummary, stdGateTitle,
  stdSourceName, stdSamplingNote, stdNextHref, STD_CONFIDENCE_LABEL,
} from '@/lib/problemStandardView';
import type { ProblemStandard } from '@/lib/types';

export function ProblemStandardHeader({ stdRef, fallback, showTitle = true, factsOnly = false }: {
  /** InboxItem.id biçimi: problem:<id> · exception:<fp> · anomaly:<id>. */
  stdRef: string;
  fallback?: ReactNode;
  showTitle?: boolean;
  /** v0.10.2038 — yalnız dört kart (anomali detayının "Teknik ayrıntı"sı):
   *  "Neden böyle değerlendiriyoruz" / "Sonraki adım" detay sayfasında
   *  döngüsel ("Anomali detayını aç"); Problem kartında kalır. */
  factsOnly?: boolean;
}) {
  const q = useProblemStandard(stdRef);
  if (!q.data) return <>{fallback ?? null}</>;
  return <ProblemStandardBlock std={q.data} showTitle={showTitle && !factsOnly} factsOnly={factsOnly} />;
}

/** Saf çizim (test edilebilir; veri çağırandan). */
export function ProblemStandardBlock({ std, showTitle = true, factsOnly = false }: { std: ProblemStandard; showTitle?: boolean; factsOnly?: boolean }) {
  const head = splitStdHeadline(std);
  const impactSub = stdImpactSecondary(std);
  const nextHref = stdNextHref(std);
  return (
    <section className="std-head" aria-label="Problem özeti">
      {showTitle && (
        <h1 className="std-head__title">
          {head.subject && <><span className="std-subject">{head.subject}</span> — </>}{head.rest}
        </h1>
      )}
      <div className="std-head__cards grid-4">
        <div className="std-card">
          <div className="std-card__label">Etki</div>
          <div className="std-card__value std-card__value--mono" style={{ fontSize: 15 }}>{std.impact.primary.text || '—'}</div>
          {impactSub && <div className="std-card__sub">{impactSub}</div>}
        </div>
        <div className="std-card">
          <div className="std-card__label">Ne zamandan beri</div>
          <div className="std-card__value std-card__value--mono">{fmtStdClock(std.since.startedAt)}</div>
          <div className="std-card__sub">{stdDurationLine(std)}</div>
        </div>
        <div className="std-card" title={stdGateTitle(std)}>
          <div className="std-card__label">Güven</div>
          <div className="std-card__value">
            {STD_CONFIDENCE_LABEL[std.confidence.level]}
          </div>
          <div className="std-card__sub mono">{stdGateSummary(std.confidence.gates) || 'ölçülmüş kapı yok'}</div>
        </div>
        <div className="std-card">
          <div className="std-card__label">Kaynak</div>
          <div className="std-card__value">{stdSourceName(std.evidence.source)}</div>
          <div className="std-card__sub">{stdSamplingNote(std)}</div>
        </div>
      </div>
      {!factsOnly && <dl className="std-why">
        <dt>Neden böyle değerlendiriyoruz</dt>
        <dd>{std.evidence.sentence || '—'}</dd>
        {(std.context?.length ?? 0) > 0 && (
          <>
            <dt>Bağlam</dt>
            <dd>{std.context!.join(' · ')} <span className="cell-faint">(önceliği etkilemez)</span></dd>
          </>
        )}
        <dt>Sonraki adım</dt>
        <dd>
          {nextHref ? <Link to={nextHref}>{std.nextStep.text}</Link> : std.nextStep.text}
          {' '}<span className="cell-faint">(öneri — sistem hiçbir şeyi değiştirmez)</span>
        </dd>
      </dl>}
    </section>
  );
}
