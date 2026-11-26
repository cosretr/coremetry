// InboxCells — Problems (/inbox) satır hücreleri (Ekip, İlgilenen, başlık çipleri, soluk satır).
// v0.10.2170 (Problems S10, docs/frontend/problems-page-plan.md) — Inbox.tsx'ten
// taşındı; içerik aynen (davranış değişmez).
import { SafeHref } from '@/components/ui/SafeHref'; // v0.10.1315 — olay/katalog linki XSS kapısı
import { Chip } from '@/components/ui';
import { Users, Shield } from 'lucide-react';
import { tsLong, tsCompact, fmtFixed, fmtDurShort } from '@/lib/utils';
import { inboxRowLabel } from '@/lib/inboxRowText';
import { derivedTeamTitle } from '@/lib/problemSubject';
import { isInboxExcFamily } from '@/lib/inboxHref';
import { TriageStatusBadge } from '@/features/anomalies/ProblemDetail';
import { RecurringMarker } from '@/features/anomalies/RecurringMarker'; // v0.10.1049
import { promotedRecurrence } from '@/features/anomalies/detailSummary'; // v0.10.1054
import { Badge } from '@/components/ui/Badge';
import type { InboxItem, InboxCluster, ProblemStandard } from '@/lib/types';
import { Link } from 'react-router';
import { t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2283 — hücre metinleri arayüz dilinde (TR aynı)
import {
  prioReasonView, stdRequestImpact, stdImpactView, stdImpactSecondary, stdConfidenceTone, stdConfidenceLabel, stdGateTitle,
} from '@/lib/problemStandardView'; // v0.10.2300 (S3) — gerekçe EN anahtardan; v0.10.2395 Etki / Güven
import { OrigLang } from '@/features/anomalies/OrigLang';
import { graphRowSubText, type ProblemGraphRowSummary } from '@/lib/problemGraph';
import { FrequentChip } from '@/components/coremetry/problems/ProblemRepeatBlock';

const isExcFamily = isInboxExcFamily;

// teamChipTitle — ekip rozetinin title'ı (v0.9.1345).
//
// Rozet TIKLANABİLİR olduğu için title zaten bir süzgeç ipucu taşıyor;
// takım TÜRETİLMİŞSE (db konuları) çekince o ipucunun ÖNÜNE geçiyor —
// operatörün önce okuması gereken şey değerin kesin olmadığıdır.
// Rozette ayrıca görünür bir '≈' var: yalnız tooltip'e saklamak, üstüne
// gelmeyen operatör için kesin bir atıftan farksız olurdu.
function teamChipTitle(it: InboxItem, filterHint: string, lang: 'en' | 'tr'): string {
  if (!it.teamsVia) return filterHint;
  return `${derivedTeamTitle(it.teamsVia, it.service, lang)}\n\n${filterHint}`;
}

// v0.9.255 — durum rozeti. Üç kaynağın state sözlükleri farklı (problem:
// open/acknowledged/resolved, exception: new/acknowledged/resolved/ignored/
// regressed, anomaly: active/resolved), o yüzden eşleme tonlara göre yapılıyor,
// kaynağa göre değil. Bilinmeyen bir durum GİZLENMEZ — ham hâliyle gri basılır,
// çünkü tanımadığımız bir durumu yok saymak operatöre satırın durumu yokmuş
// gibi gösterirdi.
// v0.10.922 (sade palet adım 1) — ton eşlemesi buradan KALKTI, tek sözlük
// ProblemDetail'de (TriageStatusBadge / STATUS_TONE): open/active/
// acknowledged/ignored nötr, new/regressed amber, resolved yeşil (geçiş).
// Eskiden "open" burada amber, Problems satırında kırmızıydı; "acknowledged"
// burada mavi, detayda amberdi. Kelime (ham durum) aynen basılıyor.
// v0.10.1037 (operatör: "Exceptions'ta NEW ile REGRESSED renkleri aynı,
// düzelt.") — sözlükte new nötre indi; amber yalnız regressed. Bu rozet
// sözlükten okuduğu için burada ayrı eşleme yok.
function StatusBadge({ s }: { s?: string }) {
  const lang = useLang();
  if (!s) return null;
  const k = s.toLowerCase();
  return <TriageStatusBadge s={k} label={k} title={tf('inbox.cell.status', lang, { s })} />;
}

// v0.10.922 (sade palet adım 1) — atanan kişi/takım üst veri: mavi değil nötr.
// TeamCell — v0.10.2121 "Ekip" kolonu (plan §4): tek ekip → ad (tık: ?team=
// süzgeci, owner VEYA SRE); çok ekipli exception → "N ekip" (liste title'da);
// eşleme yok → soluk "sahipsiz". Owner yoksa SRE.
export function TeamCell({ it, active, onPick }: { it: InboxItem; active: string; onPick: (v: string) => void }) {
  const lang = useLang();
  if (it.teams && it.teams.length >= 2) {
    return <span className="ib-team" title={it.teams.join(', ')}>{tf('inbox.cell.teams', lang, { n: it.teams.length })}</span>;
  }
  const team = it.ownerTeam || it.sreTeam;
  if (!team) return <span className="cell-faint">{t('inbox.cell.unowned', lang)}</span>;
  const on = active === team;
  return (
    <Chip size="xs" active={on}
      onClick={e => { e.stopPropagation(); onPick(on ? '' : team); }}
      title={teamChipTitle(it, on ? t('inbox.cell.teamOff', lang) : tf('inbox.cell.teamOnly', lang, { t: team }), lang)}>
      {it.ownerTeam ? <Users size={11} strokeWidth={1.75} /> : <Shield size={11} strokeWidth={1.75} />} {it.teamsVia ? '≈ ' : ''}{team}
    </Chip>
  );
}

export function AssigneePill({ v }: { v: string }) {
  const isTeam = !v.includes('@');
  return (
    <span className="badge b-gray">
      {isTeam && <Users size={11} strokeWidth={1.75} />}{v}
    </span>
  );
}

// RowChips — v0.10.1084: başlık satırının satır içi çipleri (Exceptions
// satırının "<1h" çipiyle aynı yer). Sıra: durum, ilk görülme < 1 sa, öğretme
// işareti, yinelenen, runbook, deploy. AI özeti artık TriageTitleCell'de
// (köken ✨ + yaş damgası korunur; v0.9.530 gerekçesi orada).
// v0.10.2342 (entity grafı S5, karar 5) — frequentEnforce: "sık tekrar eden"
// tutması uygulanıyor; Problem satırındaki "yinelenen" çipi yerini ona bırakır.
// Yalnız sunucu söylerse (S5 açık); kapalıyken satır bugünkü.
export function RowChips({ it, real, oraLabel, frequentEnforce }: { it: InboxItem; real: boolean; oraLabel?: string; frequentEnforce?: boolean }) {
  const lang = useLang();
  return (
    <>
      {/* v0.9.255 — durum rozeti. `status` alanı telde vardı ama hiç
          çizilmiyordu: "all" pivotunda çözülmüş bir satır yenisiyle birebir
          aynı görünüyordu. */}
      <StatusBadge s={it.status} />
      {/* v0.10.1109 — Oracle hata tablosu grubu rozeti (Exceptions satırıyla aynı). */}
      {oraLabel && (
        <Badge tone="info" style={{ fontSize: 9, padding: '0 5px' }}
          title={t('inbox.cell.oraTitle', lang)}>
          {oraLabel}
        </Badge>
      )}
      {/* Exceptions satırıyla aynı işaret: son bir saatte ORTAYA ÇIKTI. */}
      {it.startedAt > 0 && Date.now() - it.startedAt / 1e6 < 60 * 60 * 1000 && (
        <span className="badge b-warn" style={{ fontSize: 9, padding: '0 5px' }}
          title="First seen within the last hour — this did not exist before that.">
          &lt;1h
        </span>
      )}
      {/* v0.10.1015 — öğretme işareti (yalnız "gerçek problem"; "problem
          değil" satırları kendi görünümünde). */}
      {real && (
        <span className="badge b-gray" style={{ fontSize: 9 }} title={t('inbox.cell.realTitle', lang)}>{t('inbox.cell.real', lang)}</span>
      )}
      {/* v0.10.1049 — yinelenen anomali: nötr tek kelime, sayı + ilk görülme
          ipucunda (yinelenmemişte hiçbir şey). */}
      {it.anomaly && (
        <RecurringMarker episodeCount={it.anomaly.episodeCount} firstStartedAt={it.anomaly.firstStartedAt} />
      )}
      {/* v0.10.1054 — anomaliden terfi etmiş Problem satırı da aynı işareti
          taşır (sunucu yalnız terfi Problem'ine kaynak olayın sayacını
          iliştirir; diğer satırlarda hiçbir şey). */}
      {it.problem && !frequentEnforce && promotedRecurrence(it.problem) && (
        <RecurringMarker episodeCount={it.problem.episodeCount} firstStartedAt={it.problem.firstStartedAt} priorDeploy={it.priorDeploy} />
      )}
      {/* Backend'in ZATEN hesaplayıp attığı iki bilgi (v0.9.255): "runbook
          var mı" ve "az önce deploy oldu mu". */}
      {it.runbookUrl && (
        // v0.10.922 (sade palet adım 1) — üst veri: nötr.
        <SafeHref href={it.runbookUrl}
          onClick={e => e.stopPropagation()}
          className="badge b-gray" title={t('inbox.cell.runbook', lang)}>📕 runbook</SafeHref>
      )}
      {/* v0.10.1055 (operatör: "Okdir") — ipucu "undefined v…" başlıyordu:
          deploy nesnesinde `service` yok (chstore.RecentDeploy), servis
          satırın kendisinde. */}
      {it.recentDeploy && (
        <span className="badge b-warn"
          title={`${it.service} ${it.recentDeploy.version} — ${tsLong(it.recentDeploy.timeUnixNs)}`}>
          ⟳ deploy {it.recentDeploy.version}
        </span>
      )}
    </>
  );
}

// DetailLine — başlığın altındaki SOLUK satırın içeriği (sarmalayıcı
// TriageTitleCell'in `.triage-sub`u, "…" orada). v0.10.1084: Exceptions
// biçimi — exception ailesinde yalnız mesaj (oluşum kendi kolonunda, gerekçe
// P rozetinin ipucunda); diğer türlerde kaynak · (kural / incident adı) ·
// türe özel ölçü.
export function DetailLine({ it }: { it: InboxItem }) {
  const lang = useLang();
  if (isExcFamily(it)) {
    return <>{it.exception!.message || '—'}</>;
  }
  const label = inboxRowLabel(it);
  const lead = <>{it.source}{label && <> · {label}</>}</>;
  const pr = prioReasonView(it.priorityReason, it.priorityReasonMsg, lang);
  const prNode = pr.orig ? <OrigLang>{pr.text}</OrigLang> : pr.text;
  if (it.kind === 'problem' && it.problem) {
    return (
      <>
        {lead}{' · '}
        <span className="mono">{it.problem.metric}</span>
        {' = '}
        {/* v0.10.922 (sade palet adım 1) — değer düz metin (--text, 600),
            Problems satırıyla aynı: aciliyet rengi P rozetinde. */}
        <span className="mono"><b style={{ color: 'var(--text)', fontWeight: 600 }}>{fmtFixed(it.problem.value, 2)}</b></span>
        <span className="mono" style={{ color: 'var(--text3)' }}> / {fmtFixed(it.problem.threshold, 2)}</span>
        {it.priorityReason && <span> · {prNode}</span>}
      </>
    );
  }
  if (it.kind === 'incident' && it.incident) {
    // v0.10.1081 (operatör: "ekteki hata mesela hiç anlaşılmıyor") — incident
    // satırının metni DÜZ cümle (artık kalın başlıkta; v0.10.1084 sunucu
    // katlamasında birincil bağlı problemin cümlesi). "Declared incident,
    // critical" / "kaynak önceliği korundu …" gerekçesi satırda YOK — P
    // rozetinin ipucunda duruyor. Burada yalnız kaynak + incident adı.
    return lead;
  }
  if (it.kind === 'anomaly' && it.anomaly) {
    return (
      <>
        {lead}{' · '}
        peak <span className="mono">{fmtFixed(it.anomaly.peakRatio, 1)}x</span>
        {' · '}now <span className="mono">{fmtFixed(it.anomaly.currentRatio, 1)}x</span>
        {it.priorityReason && <span> · {prNode}</span>}
      </>
    );
  }
  const rest = it.priorityReason ? prNode : it.description;
  return <>{lead}{rest && <> · {rest}</>}</>;
}

// GraphRowSub — v0.10.2279 (entity grafı S2, taslak bölüm 2): "Ne bozuk"
// hücresinin alt satırı — "köken orders-db · 4 varlık · 5 olay" ya da
// "köken belirsiz · en erken başlayan …"; tek servisli Problem'de hiçbir
// şey. Gölge önizleme: satır katlanmaz, sıralanmaz, süzülmez.
// v0.10.2341 (S4) — folded: satır kümeyi katlıyor (yalnız S4 açıkken); ipucu buna göre.
export function GraphRowSub({ row, folded }: { row?: ProblemGraphRowSummary; folded?: boolean }) {
  const lang = useLang();
  const text = graphRowSubText(row, (k, v) => tf(k, lang, v));
  if (!text) return null;
  return <div className="triage-sub" data-testid="pg-row-sub" title={tf(folded ? 'pg.row.titleOn' : 'pg.row.title', lang)}>{text}</div>;
}

// graphClusterSummary — SAF (v0.10.2473): katlanan satırın özeti. Etkilenen
// servis sayısı kümenin TAMAMINDAN (süzgeçten bağımsız); bilinmiyorsa (0)
// varlık sayısı. Problem sayısı açılan listeyle aynı: bu görünümdeki üyeler.
export function graphClusterSummary(c: InboxCluster, t: (k: string, v: Record<string, string | number>) => string): string {
  const m = c.members.length;
  return c.services && c.services > 0
    ? t('pg.cluster.summary', { s: c.services, m })
    : t('pg.cluster.summaryEnt', { n: c.entities, m });
}

// GraphClusterMembers — v0.10.2341 (entity grafı S4): katlanan kümenin üyeleri.
// Yalnız sunucu katladıysa (S4 açık) gelir; kapalıyken satırda hiçbir şey
// çizilmez. Her üye kendi Problem detayına açılır (katlanan satır erişilebilir
// kalır). Kapalı <details>: satır yüksekliği değişmez.
export function GraphClusterMembers({ cluster, hrefOf }: { cluster?: InboxCluster; hrefOf: (problemId: string) => { to: string; replace?: boolean } | null }) {
  const lang = useLang();
  if (!cluster || cluster.members.length < 2) return null;
  return (
    <details className="triage-sub" data-testid="pg-cluster" onClick={e => e.stopPropagation()}>
      <summary style={{ cursor: 'pointer' }} data-testid="pg-cluster-summary">{graphClusterSummary(cluster, (k, v) => tf(k, lang, v))}</summary>
      <ul style={{ margin: 0, padding: 0, listStyle: 'none' }}>
        {cluster.members.map(m => {
          const l = hrefOf(m.problemId);
          const label = `${m.name}${m.title ? ` · ${m.title}` : ''}`;
          return (
            <li key={m.problemId} data-testid="pg-cluster-member">
              {l ? <Link to={l.to} replace={l.replace} className="cell-link">{label}</Link> : label}
              {m.priority ? ` · ${m.priority}` : ''}
              {m.owner ? ` · ${tf('pg.owner', lang)}` : m.link ? ` · ${m.link}` : ''}
              {m.late ? ` · ${tf('pg.late', lang)}` : ''}
            </li>
          );
        })}
      </ul>
    </details>
  );
}

// GraphFrequentChip — v0.10.2281 (entity grafı S3): nötr "sık tekrar eden"
// gölge çipi (renk yok; ipucu: gölge ölçüm, bildirim değişmedi). Mevcut
// "yinelenen" çipi yerinde kalır.
export function GraphFrequentChip({ row, enforce }: { row?: ProblemGraphRowSummary; enforce?: boolean }) {
  const lang = useLang();
  if (!row?.frequent) return null;
  return <FrequentChip f={row.frequent} t={(k, v) => tf(k, lang, v)} enforce={enforce} />;
}

// ── v0.10.2395 — Etki / Güven / Kanıt hücreleri (triage mockup liste dilimi) ──
// Veri satırda (std, eventCount); hücre başına istek yok. Eksik değer soluk "—".

// ImpactCell — "Etki": detay kartının (ProblemStandardBlock) AYNI çekirdeği:
// istek bölümlü etkide kısa biçim ("Dakikada 210 başarısız · 2.890 başarılı"),
// pay ve ölçülmüş ek bilgi alt satırda; yoksa standardın etki cümlesi.
export function ImpactCell({ std }: { std?: ProblemStandard }) {
  const lang = useLang();
  if (!std) return <span className="cell-faint">—</span>;
  const req = stdRequestImpact(std, lang);
  const main = req ? { text: req.main, orig: req.orig } : stdImpactView(std, lang);
  if (!main.text) return <span className="cell-faint">—</span>;
  const sub = stdImpactSecondary(std, lang);
  return (
    <div className="std-impact" title={sub ? `${main.text}\n${sub}` : main.text}>
      <div className="std-impact__main" data-testid="ib-impact">{main.orig ? <OrigLang>{main.text}</OrigLang> : main.text}</div>
      {sub && <div className="std-impact__sub">{sub}</div>}
    </div>
  );
}

// ConfidenceCell — "Güven": YÜKSEK yeşil / ORTA sarı / DÜŞÜK nötr (stdConfidenceTone);
// kapı dökümü title'da (detay kartıyla aynı metin).
export function ConfidenceCell({ std }: { std?: ProblemStandard }) {
  const lang = useLang();
  if (!std) return <span className="cell-faint">—</span>;
  return (
    <Badge tone={stdConfidenceTone(std.confidence.level)} title={stdGateTitle(std, lang)}>
      {stdConfidenceLabel(std.confidence.level, lang)}
    </Badge>
  );
}

// EvidenceCell — "Kanıt": Problem'e bağlı olay sayısı (eventCount, sahibin
// kendi sinyali dahil). Sayı gelmediyse (üyesi olamayan tür / sayım okunamadı)
// "kanıt yok" DENMEZ: soluk "—". Tipli kanıt sayısı uydurulmaz.
export function EvidenceCell({ it }: { it: InboxItem }) {
  const lang = useLang();
  const n = it.eventCount ?? 0;
  if (n < 1) return <span className="cell-faint">—</span>;
  return <span title={t('inbox.evidence.title', lang)}>{tf('inbox.evidence', lang, { n })}</span>;
}

// ClosedWhen — v0.10.2476 (Kapanan görünümü): zaman hücresi kapanış anını ve
// Problem'in süresini (başlangıç → kapanış) çizer; tam damga title'da.
export function ClosedWhen({ startedAt, resolvedAt, lang }: { startedAt: number; resolvedAt: number; lang: Lang }) {
  const dur = startedAt > 0 && resolvedAt > startedAt ? fmtDurShort((resolvedAt - startedAt) / 1e9) : null;
  return (
    <span title={tf('inbox.closed.at.title', lang, { t: tsLong(resolvedAt) })}>
      <span className="mono">{tsCompact(resolvedAt)}</span>
      <div className="ib-when__ago">{dur ? tf('inbox.closed.lasted', lang, { d: dur }) : t('inbox.closed.at', lang)}</div>
    </span>
  );
}
