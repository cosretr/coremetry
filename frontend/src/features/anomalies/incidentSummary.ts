// incidentSummary — incident tam sayfasının "ne oldu" cümlesi, birincil
// problemi ve "Ne yapabilirim" pivotları, SAF (v0.10.1081).
//
// Operatör (prod, db-health problemiyle açılan incident): "ekteki hata mesela
// hiç anlaşılmıyor." Ekranda yalnız "db:couchbase@<db> — DB health · couchbase"
// başlığı, "kaynak önceliği korundu (kritik incident) · Declared incident,
// critical · last seen …" ve dört düğme vardı — cümle yok, sayı yok, yapılacak
// şey yok. Artık incident sayfasının üstü problem / anomali detayları gibi
// okunur (v0.10.1032 deseni): bağlı problemin kendi cümlesi, bağlı problemler
// listesi, aynı pivot kartı. Kurallar burada ve tablo testli
// (incidentSummary.test.ts); sayfa yalnız çizer.

import type { Incident, Problem } from '@/lib/types';
import { logsHref } from '@/lib/logsUrl';
import { tracesPivotHref } from '@/lib/pivotHref';
import { serviceHref, eventLifespanWindow } from '@/lib/serviceHref';
import { subjectKind, subjectLabel } from '@/lib/problemSubject';
import { alertProblemSummary } from './detailSummary';
import { dbHealthPivots } from './dbHealthPivots';

/** Birincil problem: incident'ı AÇAN problem — bağlılar arasında en erken
 *  başlayan (eşitlikte kimlik sırası, deterministik). Bağlı yoksa null. */
export function incidentPrimaryProblem(problems: readonly Problem[]): Problem | null {
  let best: Problem | null = null;
  for (const p of problems) {
    if (!p) continue;
    if (!best || p.startedAt < best.startedAt || (p.startedAt === best.startedAt && p.id < best.id)) best = p;
  }
  return best;
}

// Kapanış eki (" · auto-resolved: <gerekçe>") yaşam döngüsü bilgisi; olayın
// cümlesi değil — "ne zaman" satırı zaten bittiğini söyler.
const RESOLVE_SUFFIX = ' · auto-resolved:';

/** Problemin tek cümlesi: kendi açıklaması (dedektörün yazdığı düz metin —
 *  ör. db-health "couchbase orders veritabanında hata oranı %100.0 (eşik %5),
 *  3 çağıran servis etkilendi …"); açıklama boşsa alarm özeti cümlesi. */
export function problemSentence(p: Problem): string {
  const raw = (p.description ?? '').trim();
  const cut = raw.indexOf(RESOLVE_SUFFIX);
  const desc = (cut >= 0 ? raw.slice(0, cut) : raw).trim();
  return desc || alertProblemSummary(p);
}

/** Incident'ın manşet cümlesi: birincil problemin cümlesi; problem yüklenmediyse
 *  ya da bağlı yoksa incident'ın kayıtlı özeti (otomatik açılışta = ilk problemin
 *  açıklaması), o da yoksa başlık. Teknik gerekçe ("Declared incident…",
 *  "kaynak önceliği korundu…") ASLA manşete çıkmaz. */
export function incidentHeadline(inc: Pick<Incident, 'title' | 'summary'>, problems: readonly Problem[]): string {
  const primary = incidentPrimaryProblem(problems);
  if (primary) return problemSentence(primary);
  const summary = (inc.summary ?? '').trim();
  return summary || inc.title;
}

export interface PivotLink { to: string; label: string; sub: string }

export interface IncidentPivots {
  links: PivotLink[];
  /** Link yoksa NEDEN olmadığı (boş liste "olay yok" diye okunmasın). */
  note?: string;
}

/**
 * "Ne yapabilirim" kartı — problem detay sayfasının pivotlarıyla aynı
 * üreticiler (dbHealthPivots, logsHref, tracesPivotHref, serviceHref).
 *
 * Tek bağlı problem varsa pivotlar ONDAN (özne, kural, problem penceresi);
 * yoksa incident'ın kendisinden (servisi, ömür penceresi). db-health kuralı
 * veritabanı üçlüsünü taşır → Veritabanı sayfası + trace'ler. Servis
 * olmayan özne (db / dış kaynak) servis adı isteyen pivotları basmaz.
 */
export function incidentPivotLinks(
  inc: Pick<Incident, 'service' | 'startedAt' | 'resolvedAt'>,
  problems: readonly Problem[],
  nowNs: number = Date.now() * 1e6,
): IncidentPivots {
  const single = problems.length === 1 ? problems[0] : null;
  const subject = single ? single.service : (inc.service ?? '');
  const kind = single ? single.kind : undefined;
  const window = (single ? eventLifespanWindow(single, nowNs) : undefined)
    ?? eventLifespanWindow(inc, nowNs)
    ?? { fromNs: nowNs - 3_600e9, toNs: nowNs };

  const db = single ? dbHealthPivots(single.ruleId, window) : null;
  if (db) {
    return {
      links: [
        { to: db.databaseHref, label: 'Veritabanı sayfası', sub: 'db detayı, problem penceresi' },
        { to: db.tracesHref, label: "Trace'ler", sub: 'bu veritabanı, problem penceresi' },
        { to: db.errorTracesHref, label: "Hatalı trace'ler", sub: 'bu veritabanı, yalnız hatalar' },
      ],
    };
  }
  if (!subject) {
    return { links: [], note: 'Bu incident bir servis adı taşımıyor — log / trace / servis pivotları bir servis adı gerektiriyor.' };
  }
  const sk = subjectKind(subject, kind);
  if (sk !== 'service') {
    return {
      links: [],
      note: sk === 'external'
        ? 'Öznesi bir dış metrik kaynağı serisi, bir servis değil — kanıt bağlı problemin sayfasında.'
        : 'Öznesi bir veritabanı örneği, bir servis değil — log / trace pivotları bir servis adı gerektiriyor; bağlı problemin sayfasına git.',
    };
  }
  const where = single ? 'problem penceresi' : 'incident penceresi';
  return {
    links: [
      { to: logsHref({ window, service: subject, severity: 17 }), label: 'Hata logları', sub: `servis, ${where}, ERROR+` },
      { to: logsHref({ window, service: subject }), label: 'Loglar', sub: `servis, ${where}` },
      { to: tracesPivotHref({ window, service: subject, hasError: true, rootOnly: false }), label: "Hatalı trace'ler", sub: `servis, ${where}` },
      { to: serviceHref(subject, { range: window }), label: 'Servis sayfası', sub: where },
    ],
  };
}

/** Bağlı problem satırının kısa adı: kural adı (yoksa metrik) — özne (okunur
 *  etiketiyle: db / dış kaynak öznesi ham kimlik değil, subjectLabel). */
export function attachedProblemLabel(p: Pick<Problem, 'ruleName' | 'metric' | 'service'>): string {
  const name = (p.ruleName ?? '').trim() || (p.metric ?? '').trim() || 'Problem';
  return p.service ? `${name} — ${subjectLabel(p.service)}` : name;
}
