// ServiceAttentionStrip — servis sayfasının üst şeridi (v0.10.784).
//
// Operatör (2026-09-18, prod ekranı): "SLO üstte yazmasın; varsa problem
// veya exception gözüksün; tıklanarak gidebilsin; daha az yükseklik."
// Öncesi: bundle'ın `problems` dilimindeki alert-kuralı problemleri
// (SLO burn-rate dahil) tıklanmayan kartlar hâlinde; aynı servisin %92
// hata problemi görünürken 504 HTTP-hata grubu hiç çıkmıyordu.
//
// Kaynak Inbox'ın kendisi (/api/inbox?service=&status=open): sıra ve
// öncelik (P1 → P2 → P3) Inbox'la birebir — entity tutarlılığı. Satır =
// bağlantı (inboxItemHref, Inbox satırıyla aynı hedef). SLO burn-rate
// problemleri satır değil, başlık satırında bir not; anomali satırları
// dışarıda ("∿ Anomalies" düğmesi var). Mockup onaylı:
// scratchpad service-attention-strip-mockup.html.
//
// Inbox okuması başarısız olursa bundle'daki problem listesine düşülür
// (SLO hariç; exception satırı çıkmaz, başlık bunu söyler) — şerit hiçbir
// zaman sessizce kaybolmaz.
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { fmtAgoNs } from '@/lib/utils';
import { attentionRows, inboxItemHref, serviceInboxHref } from '@/lib/inboxHref';
import { useProblemVerdicts } from '@/lib/queries';
import { verdictIndex, filterByVerdictView } from '@/lib/problemVerdict';
import { PriorityBadge } from '@/components/ui/PriorityBadge';
import { useIncidentsEnabled } from '@/components/AuthProvider'; // v0.10.1827
import type { InboxItem, Problem } from '@/lib/types';
import { t, tf, useLang, type Lang } from '@/lib/i18n';
import { prioReasonTitle } from '@/lib/problemStandardView'; // v0.10.2303 — öncelik ipucu EN anahtardan

// v0.10.2312 — tür etiketi arayüz dilinde, çizimde çözülür (EN katalog modül
// yüklenirken yok). Exception / Incident ürün terimi, çevrilmez.
const kindLabel = (k: InboxItem['kind'], lang: Lang): string => (
  k === 'problem' ? t('pd.rule', lang)
  : k === 'httperror' ? t('inbox.kind.httperror', lang)
  : k === 'anomaly' ? t('inbox.kind.anomaly', lang)
  : k === 'exception' ? 'Exception' : 'Incident');

const fmtVal = (v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(2));

// Bundle problemlerinden Inbox satırı — yalnız yedek yol (Inbox ucu hata
// verdiğinde). Öncelik sunucuda hesaplanmadı: severity'den kaba eşleme.
function problemToItem(p: Problem): InboxItem {
  return {
    id: `problem:${p.id}`, kind: 'problem', source: 'Alert rule',
    priority: p.severity === 'critical' ? 'P1' : 'P2', priorityReason: '',
    severity: p.severity, service: p.service, title: p.ruleName,
    description: p.description, startedAt: p.startedAt, lastSeen: p.startedAt,
    status: p.status, displayId: p.displayId,
    problem: { id: p.id, ruleId: p.ruleId, metric: p.metric, value: p.value, threshold: p.threshold },
  };
}

function RowMeta({ it }: { it: InboxItem }) {
  const lang = useLang();
  if (it.kind === 'problem' && it.problem) {
    return (
      <span className="svc-att-meta">
        <span className="mono">{it.problem.metric}</span>
        {' = '}<b>{fmtVal(it.problem.value)}</b>{tf('svcAtt.thr', lang, { v: fmtVal(it.problem.threshold) })}
        {it.startedAt > 0 && ` · ${fmtAgoNs(it.startedAt)}`}
      </span>
    );
  }
  if (it.exception) {
    return (
      <span className="svc-att-meta">
        {`×${it.exception.occurrences.toLocaleString()}`}
        {it.lastSeen > 0 && ` · ${tf('podpage.id.lastSeen', lang, { t: fmtAgoNs(it.lastSeen) })}`}
      </span>
    );
  }
  if (it.incident) {
    return <span className="svc-att-meta">{`${it.incident.severity} · ${it.incident.status}`}</span>;
  }
  return <span className="svc-att-meta">{it.lastSeen > 0 ? fmtAgoNs(it.lastSeen) : ''}</span>;
}

function Row({ it }: { it: InboxItem }) {
  const lang = useLang();
  const href = inboxItemHref(it);
  if (!href) return null;
  const name = it.exception ? it.exception.type : it.title;
  const sub = it.exception ? it.exception.message : it.displayId;
  const reason = prioReasonTitle(it.priorityReason, it.priorityReasonMsg, lang);
  return (
    <Link to={href} className="svc-att-row" title={reason || it.description || undefined}>
      <PriorityBadge p={it.priority} reason={reason} />
      <span className="svc-att-kind">{kindLabel(it.kind, lang)}</span>
      <span className="svc-att-name">
        {name}
        {sub && <span className="svc-att-sub"> · {sub}</span>}
      </span>
      <RowMeta it={it} />
      <span className="svc-att-go" aria-hidden>→</span>
    </Link>
  );
}

export function ServiceAttentionStrip({ service, fallbackProblems }: {
  service: string;
  // Bundle'ın problem dilimi — yalnız Inbox okuması başarısız olunca.
  fallbackProblems: Problem[];
}) {
  const lang = useLang();
  const q = useQuery({
    queryKey: ['service-attention', service],
    queryFn: () => api.inbox({ service, status: 'open', limit: 20 }),
    staleTime: 30_000,
    refetchInterval: 30_000,
  });
  // v0.10.1508 — Inbox'ın varsayılan (triage) görünümü "problem değil"
  // işaretli imzaları gizler; şerit de aynısını yapar, yoksa "N açık konu"
  // linkin açtığı listeden fazla sayardı. Kararlar okunamazsa dizin boş:
  // hiçbir satır gizlenmez (Inbox'la aynı güvenli taraf).
  const verdictsQ = useProblemVerdicts();
  // v0.10.1827 — Incidents emekliyken incident satırı yok (sunucu da üretmez).
  const incidentsOn = useIncidentsEnabled();
  if (q.isPending) return null;

  // api.inbox 204/boş gövdede null dönebilir — "yok" ile "okunamadı" ayrı.
  const degraded = q.isError || !q.data;
  const items = !q.data
    ? fallbackProblems.filter(p => p.status === 'open').map(problemToItem)
    : filterByVerdictView(q.data.items, verdictIndex(verdictsQ.data?.verdicts), 'triage');
  const { rows, hidden, sloCount } = attentionRows(items, { excludeIncidents: !incidentsOn });
  // v0.10.1508 — şeridin kapsamı linkte AÇIK (tüm öncelikler, tüm ortamlar).
  const inboxHref = serviceInboxHref(service, incidentsOn);
  const sloNote = sloCount > 0 && (
    <span className="svc-att-slo">
      <span className="svc-att-slo-ico">◉</span>
      {tf('svcAtt.slo', lang, { n: sloCount })}
      <a href="#svc-slos" title={t('svcAtt.sloJump', lang)}>↓</a>
    </span>
  );

  if (rows.length === 0) {
    if (!sloNote) return null;
    return <div className="svc-att-thin">{sloNote}</div>;
  }
  return (
    <div className="svc-att" data-degraded={degraded || undefined}>
      <div className="svc-att-head">
        <span className="svc-att-title">{tf('svcAtt.open', lang, { n: rows.length + hidden })}</span>
        {sloNote && <span className="svc-att-sep">·</span>}
        {sloNote}
        {degraded && (
          <span className="svc-att-warn" title={t('svcAtt.degradedTip', lang)}>
            {t('svcAtt.degraded', lang)}
          </span>
        )}
        <span className="svc-att-sp" />
        <Link to={inboxHref}>{t('svcAtt.toInbox', lang)}</Link>
      </div>
      <div className="svc-att-rows">
        {rows.map(it => <Row key={it.id} it={it} />)}
        {hidden > 0 && (
          <div className="svc-att-more">
            {tf('svcAtt.more', lang, { n: hidden })}<Link to={inboxHref}>Inbox</Link>
          </div>
        )}
      </div>
    </div>
  );
}
