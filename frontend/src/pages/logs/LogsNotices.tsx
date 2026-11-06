// LogsNotices — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı, dilim 1).
//
// /logs'un DÜRÜSTLÜK rozetleri tek yerde: sayfada beş ayrı `<div
// marginBottom:10>` olarak dağılmışlardı (env / takım / with-trace /
// partial / degraded) ve her biri kendi satırını yiyordu. Metinler ve
// kapılar BAYT BAYT aynı — yalnız yerleşim tek sarılan satıra indi. Saf
// sunum: durum Logs.tsx'te, burada yalnız `data` (statik sorgunun cevabı)
// okunur. Canlı kuyruk bu bayrakları taşımaz (hepsi `!live`).
import type { LogsResponse } from '@/lib/types';
import { Button } from '@/components/shadcn/button';
import { scopeReasonText } from '@/lib/logsScope';
import { useT } from '@/lib/i18n';

export interface LogsNoticesProps {
  live: boolean;
  env: string;
  team: string;
  hasTrace: boolean;
  /** Statik liste sorgusunun cevabı (yükleniyorken undefined). */
  data: LogsResponse | undefined;
}

export function LogsNotices({ live, env, team, hasTrace, data }: LogsNoticesProps) {
  const tr = useT();
  const shardsFailed = data?.shardsFailed ?? 0;
  const items = [
    /* v0.8.400 — HONEST env-filter chip (the v0.8.398 pattern: state that
       the filter could NOT apply instead of silently answering unfiltered).
       The ES backend sets envUnapplied when ?env= was requested but no
       environment field resolved in the index mapping. Never set by CH. */
    !live && !!env && !!data?.envUnapplied && (
      <span key="env" className="badge b-warn"
        title={'The env filter could not be applied on this log source: no deployment-environment field was found in the index mapping (self-discovery probed resource.deployment.environment[.name], deployment.environment[.name], labels.deployment_environment, env, environment).\nThe rows below are UNFILTERED — all environments.\nFix: set the Environment field in Settings → Elasticsearch → Document field map.'}>
        ⚠ env “{env}” not applied — this log source has no recognisable environment field
      </span>
    ),
    /* v0.10.1477 — HONEST team-filter chip (env çipinin eşi): takım alanı
       yapılandırılmamış ya da CH backend'i → sonuçlar takıma göre SÜZÜLMEMİŞ. */
    !live && !!team && !!data?.unappliedFilters?.includes('team') && (
      <span key="team" className="badge b-warn" data-testid="logs-team-unapplied" title={tr('logs.team.unappliedTitle')}>
        ⚠ {tr('logs.team.unapplied')}
      </span>
    ),
    /* v0.9.1084 — HONEST with-trace chip: ES mapping'inde yapısal trace-id
       alanı yoksa exists filtresi hiçbir doc'la eşleşemez. */
    !live && hasTrace && !!data?.hasTraceUnapplied && (
      <span key="trace" className="badge b-warn"
        title={'The with-trace filter could not be applied on this log source: none of the structural trace-id fields (trace.id, trace_id, traceId, TraceId or a configured override) exist in the index mapping — the trace ids likely live only inside the log message body.\nTrace→log pivots keep working via body match; this filter cannot.\nFix: map a trace-id field in Settings → Elasticsearch → Document field map (if your pipeline emits one).'}>
        ⚠ “with trace” not applied — this log source has no structural trace-id field
      </span>
    ),
    /* v0.9.288 — honesty envelope: ES soft timeout (10s) → partial answer. */
    !live && !!data?.partial && (
      <span key="partial" className="badge b-warn"
        title={'Elasticsearch did not finish this query: it hit the soft timeout (10s) and/or lost shards, and returned the part it had computed.\nEVERY number on this page is a subset — the row count, the severity chips and the histogram above them.\nA dip in the chart may be this timeout rather than a drop in traffic.\nNarrow the window, add a service filter, or make the search more selective.'}>
        ⚠ partial result — ES returned what it had computed
        {shardsFailed > 0 && ` · ${shardsFailed} shard${shardsFailed === 1 ? '' : 's'} did not answer`}
      </span>
    ),
    /* v0.10.415 (B1) — yavaş/erişilemeyen backend 200 {degraded} döner;
       sonuç 15 sn sunucu önbelleğinde, boş durumda ↻ Retry var. */
    !live && !!data?.degraded && (
      <span key="degraded" className="badge b-warn"
        title={'Log backend zaman aşımı/erişilemezlik nedeniyle cevap vermedi; sunucu 5xx yerine boş liste + bu bayrakla döndü.\nBu SAYFADAKİ hiçbir sayı gerçek değil — satır sayısı, seviye çipleri, histogram.'}>
        ⚠ log backend yavaş/erişilemez — bu liste eksik
        {data?.reason ? ` (${data.reason})` : ''}
      </span>
    ),
  ].filter(Boolean);
  if (items.length === 0) return null;
  return <div className="ld-notices" role="status">{items}</div>;
}

// LogsNeedService — v0.10.1855 (Log Search Faz 2 dilim 2.4): varsayılan kapsam
// "Servislerim" çözülemedi → tablonun İÇİNDE servis seçimi istenir; açık kaçış
// "Tüm servislerde ara" (`scope=all`). Sınıflar Tailwind (@source pages/logs).
export function LogsNeedService({ reason, onAll }: { reason: string; onAll: () => void }) {
  return (
    <div className="grid justify-items-center gap-2 text-(--text2)" data-testid="logs-need-service">
      <span>Varsayılan kapsam "Servislerim" çözülemedi — {scopeReasonText(reason)}. Sorgu servis seçilmeden çalışmaz; üstteki seçiciden bir servis seçin.</span>
      <Button variant="secondary" size="sm" data-testid="logs-scope-all" onClick={onAll}
        title="Kapsamsız arama: tüm indekslerde (pencereyle sınırlı) — pahalı olabilir">Tüm servislerde ara</Button>
    </div>
  );
}
