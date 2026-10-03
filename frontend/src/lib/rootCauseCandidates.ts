import type { ChangedService, RootCause, RootCauseHypothesis } from '@/lib/types';

// rootCauseCandidates.ts — v0.10.700 (Dynatrace paritesi #2, dilim 1).
//
// Ribbon'un genişletilmiş "Ranked candidates" listesi bugüne dek YALNIZ canlı
// fan-out'un correlations'ını çiziyordu; işçinin kalıcı hipotez adayları
// (hop, path, kind, gerekçe, zamansal çarpan) hiç görünmüyordu. Kalıcı
// hipotez varsa onu çiz (operatör kararı 2026-09-12), yoksa eski yol.
// Skor ölçekleri farklı: hipotez 0..1 (yüzde), correlations serbest sayı.

export interface RibbonCandidate {
  service: string;
  scoreLabel: string;
  hops: number;
  reason?: string;
  kind?: string;
  temporalReason?: string;
  source: 'hypothesis' | 'live';
}

export function pctLabel(f: number): string {
  return `${Math.round(Math.max(0, Math.min(1, f)) * 100)}%`;
}

export function ribbonCandidates(rc: {
  service: string;
  correlations?: ChangedService[] | null;
  hypothesis?: RootCauseHypothesis | null;
}): RibbonCandidate[] {
  const hyp = rc.hypothesis?.candidates ?? [];
  if (hyp.length > 0) {
    return hyp
      .filter(c => c.service && c.service !== rc.service)
      .map(c => ({
        service: c.service,
        scoreLabel: pctLabel(c.score),
        hops: c.hops,
        reason: c.reason,
        kind: c.kind,
        temporalReason: c.temporalReason,
        source: 'hypothesis' as const,
      }));
  }
  // v0.10.1063 — canlı yolda iyileşen / yalnız sakinleşen servis "aday"
  // değildir (operatör: "<svc-B> ile ilgili olduğunu düşünüyor ama alakasız").
  // v0.10.1090 — YALNIZ sunucunun uygun işaretlediği (özneyle kenarlı +
  // konuma göre kötüleşen) satır; 1063 bağlantısız worse/lost satırları hâlâ
  // "Ranked candidates" altında listeliyordu. Hiçbiri yoksa şerit
  // ribbonNoCandidateNote'u (panelin localizedNote'u) basar.
  return (rc.correlations ?? [])
    .filter(c => c.service && c.service !== rc.service && c.causeEligible === true)
    .sort((a, b) => b.score - a.score)
    .map(c => ({
      service: c.service,
      scoreLabel: String(Math.round(c.score)),
      hops: 0,
      reason: c.reasons?.[0],
      source: 'live' as const,
    }));
}

// coMovingCause — v0.10.1063. "Co-moving … propagation" manşetine çıkabilecek
// satır: YALNIZ sunucunun uygun işaretlediği (kötüleşen ya da trafiği kesilen
// VE özneyle topoloji kenarı olan) ve skoru ≥ 20 olan. Operatör: "<svc-B> ile
// ilgili olduğunu düşünüyor ama alakasız" — en yüksek skorlu satır iyileşen,
// bağlantısız bir servisti. causeEligible taşımayan (eski önbellekli) yanıt →
// manşet yok.
export function coMovingCause(rc: RootCause, service: string): ChangedService | undefined {
  return (rc.correlations ?? []).find(c => c.service !== service && c.causeEligible === true && c.score >= 20);
}

// localizedNote — v0.10.1063. "localized" manşetinin eki: başka servisler
// kıpırdadıysa neden hiçbiri manşette değil, DÜRÜSTÇE. "Hiçbiri bağlı ve
// kötüleşen değil" yalnız topoloji GERÇEKTEN okunduysa (topologyKnown) söylenir;
// okunamadıysa ya da alan yoksa (eski yanıt) "bağlantı doğrulanamadı".
// v0.10.1090 — `who`: panel listeyi ALTTA çizer ("services below"), şerit
// çizmez ("other services"); hüküm cümlesi iki yüzeyde aynı.
export function localizedNote(rc: RootCause, service: string, who = 'services below'): string {
  const moved = (rc.correlations ?? []).some(c => c.service !== service);
  if (!moved) return '';
  if (rc.topologyKnown === true) {
    return `; ${who} moved in the same window but none is a connected, worsening dependency of ${service}`;
  }
  return `; ${who} moved in the same window — bağlantı doğrulanamadı (topology unavailable)`;
}

// ribbonNoCandidateNote — v0.10.1090. Şeridin canlı yolunda uygun aday
// yokken "Ranked candidates" yerine basılan DÜRÜST satır: localizedNote'un
// aynı hükmü, tek başına cümle olarak. Kıpırdayan başka servis yoksa ''.
export function ribbonNoCandidateNote(rc: RootCause): string {
  const note = localizedNote(rc, rc.service, 'other services').replace(/^; /, '');
  return note ? note.charAt(0).toUpperCase() + note.slice(1) + '.' : '';
}
