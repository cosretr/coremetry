// SearchDetailDrawer — v0.10.1335: /search sonucunun detay çekmecesi (?sel=).
//
// ui/Drawer (perdeli modal: odak tuzağı, Esc katmanı useEscLayer, ✕); panel
// mevcut slideInRight ile girer (globals.css .srch-* bloğu; azaltılmış
// hareket global kuralla kapalı).
//
// GÖVDE YALNIZ AÇILIŞTA getirilir (ES/CH maliyet disiplini, v0.8.270): liste
// hiçbir detay önceden çekmez; her tür kendi okuyucusunu çekmece mount
// olduğunda bir kez sorar — trace → api.trace + ServiceTimeline, log →
// satırın kendi payload'ı (ek istek yok), problem → api.problem +
// problemAffected, exception → api.getExceptionGroup, service →
// api.serviceBundle RED karoları, endpoint → api.endpointDetail. Okunamayan
// parça SectionUnavailable ile söylenir, boş bırakılmaz.
//
// Eylem satırı: TEK birincil "Open in …" (resultOpenTarget), "Ask CoSRE"
// (AIExplainButton → ?ai=; sayfa ?ai= görünce ?sel='i siler ve çekmeceyi
// çizmez — iki odak tuzağı üst üste binmez), sel'li sayfa linkini kopyala.
import { useMemo } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Badge, Button, Drawer, DrawerSection, SectionUnavailable, StatTile } from '@/components/ui';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { Spinner } from '@/components/Spinner';
import { CopyButton } from '@/components/CopyButton';
import { AIExplainButton } from '@/components/ai/AIExplainButton';
import { IconSparkles } from '@/components/icons';
import { ServiceTimeline } from '@/components/traces/ServiceTimeline';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries/keys';
import { useProblemAffected, useProblemByID } from '@/lib/queries/problems';
import { useEndpointDetail } from '@/lib/queries/endpoints';
import { traceHref } from '@/lib/traceHref';
import type { SearchSel } from '@/lib/searchUrl';
import type { SearchResult, TimeRange } from '@/lib/types';
import { fmtNum, tsLong } from '@/lib/utils';
import { resultOpenTarget, resultSubject } from './resultSubject';
import { endpointPathOf, rowForSel } from './searchText';
import { ResultSeverity } from './ResultRow';

const DETAIL_STALE_MS = 30_000;

export function SearchDetailDrawer({ sel, row, range, env, q, win, t, onClose }: {
  sel: SearchSel;
  row: SearchResult | null;
  range: TimeRange;
  env: string;
  q: string;
  win: { from: number; to: number };
  t: (k: string) => string;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const r = useMemo(() => row ?? rowForSel(sel), [row, sel]);
  const pageUrl = typeof window !== 'undefined' ? window.location.href : '';
  if (!r) {
    return (
      <Drawer onClose={onClose} width={560} ariaLabel={t(`search.type.${sel.type}`)}
        header={<span className="srch-dh"><Badge tone="neutral">{t(`search.type.${sel.type}`)}</Badge></span>}>
        <div className="srch-drawer"><SectionUnavailable what="Log line" /></div>
      </Drawer>
    );
  }
  const subject = resultSubject(r, { fromNs: win.from, toNs: win.to });
  const target = resultOpenTarget(r, { range, env, q });

  return (
    <Drawer onClose={onClose} width={560} ariaLabel={`${t(`search.type.${r.type}`)} — ${r.title}`}
      header={
        <span className="srch-dh">
          <Badge tone="neutral">{t(`search.type.${r.type}`)}</Badge>
          <ResultSeverity r={r} />
          <span className="srch-dh-title" title={r.title}>{r.title}</span>
        </span>
      }>
      <div className="srch-drawer">
        {r.subtitle && <p className="srch-dsub">{r.subtitle}</p>}
        <ActionRow
          secondary={<>
            {subject && (
              <AIExplainButton subject={subject}
                label={<><IconSparkles /> <span>{t('search.askCosre')}</span></>} />
            )}
            {pageUrl && <CopyButton value={pageUrl} title="Copy link to this result" />}
          </>}
          confirm={target && (
            <Button variant="primary" size="sm" onClick={() => navigate(target.href)}>{t(target.labelKey)}</Button>
          )} />
        <DetailBody r={r} win={win} env={env} range={range} />
      </div>
    </Drawer>
  );
}

function DetailBody({ r, win, env, range }: {
  r: SearchResult; win: { from: number; to: number }; env: string; range: TimeRange;
}) {
  switch (r.type) {
    case 'trace': return <TraceBody id={r.id} />;
    case 'log': return <LogBody r={r} range={range} />;
    case 'problem': return <ProblemBody id={r.id} />;
    case 'exception': return <ExceptionBody fp={r.payload?.fingerprint || r.id} />;
    case 'service': return <ServiceBody svc={r.id} win={win} env={env} />;
    case 'endpoint': return <EndpointBody svc={r.payload?.service || r.service} name={r.payload?.name || r.title} win={win} env={env} />;
  }
}

function TraceBody({ id }: { id: string }) {
  const q = useQuery({
    queryKey: keys.search.detail('trace', id),
    queryFn: ({ signal }) => api.trace(id, signal),
    staleTime: DETAIL_STALE_MS,
    placeholderData: undefined,
    retry: 0,
  });
  const sum = useMemo(() => {
    const spans = q.data?.spans;
    if (!spans || spans.length === 0) return null;
    const root = spans.find(s => !s.parentSpanId) ?? spans[0];
    const minStart = spans.reduce((m, s) => Math.min(m, s.startTime), Infinity);
    const maxEnd = spans.reduce((m, s) => Math.max(m, s.endTime), 0);
    return {
      root, totalMs: (maxEnd - minStart) / 1e6, spanCount: spans.length,
      services: new Set(spans.map(s => s.serviceName)).size,
      errSpans: spans.filter(s => s.statusCode === 'error').length,
    };
  }, [q.data]);
  if (q.isPending) return <Spinner label="Loading trace…" />;
  if (!sum || !q.data) return <SectionUnavailable what="Trace" />;
  return (
    <>
      <DrawerSection title="Summary">
        <KeyValue items={[
          { k: 'Duration', v: `${sum.totalMs.toFixed(0)} ms` },
          { k: 'Spans', v: `${fmtNum(sum.spanCount)} · ${sum.services} services` },
          { k: 'Root op', v: `${sum.root.name} · ${sum.root.serviceName}`, mono: true },
          { k: 'Errors', v: sum.errSpans > 0 ? <span className="cell-err">{fmtNum(sum.errSpans)}</span> : '0' },
        ]} />
      </DrawerSection>
      <DrawerSection title="Service timeline">
        <ServiceTimeline spans={q.data.spans} />
      </DrawerSection>
    </>
  );
}

function LogBody({ r, range }: { r: Extract<SearchResult, { type: 'log' }>; range: TimeRange }) {
  const p = r.payload;
  if (!p) return <SectionUnavailable what="Log line" />;
  return (
    <>
      <DrawerSection title="Message">
        <pre className="srch-log-body">{p.body}</pre>
      </DrawerSection>
      <DrawerSection title="Attributes">
        <KeyValue items={[
          { k: 'Time', v: p.timestamp ? tsLong(p.timestamp) : '' },
          { k: 'Severity', v: p.severityText || (p.severity ? String(p.severity) : '') },
          { k: 'Service', v: p.serviceName },
          { k: 'Environment', v: r.env ?? '' },
          { k: 'Trace id', mono: true, v: p.traceId
            ? <Link to={traceHref(p.traceId, { pageRange: range })}>{p.traceId}</Link> : '' },
          { k: 'Span id', mono: true, v: p.spanId ?? '' },
        ]} />
      </DrawerSection>
    </>
  );
}

function ProblemBody({ id }: { id: string }) {
  const pr = useProblemByID(id);
  const aff = useProblemAffected(id);
  if (pr.isPending) return <Spinner label="Loading problem…" />;
  const p = pr.data;
  return (
    <>
      <DrawerSection title="Problem">
        {p ? (
          <KeyValue items={[
            { k: 'Id', v: p.displayId ?? '', mono: true },
            { k: 'Rule', v: p.ruleName },
            { k: 'Severity', v: p.severity },
            { k: 'Status', v: p.status },
            { k: 'Service', v: p.service },
            { k: 'Value', v: Number.isFinite(p.value) ? `${p.value} (threshold ${p.threshold})` : '' },
            { k: 'Description', v: p.description },
          ]} />
        ) : <SectionUnavailable what="Problem" />}
      </DrawerSection>
      <DrawerSection title="Affected">
        {aff.isPending ? <Spinner />
          : aff.data && aff.data.entities.length > 0 ? (
            <ul className="srch-affected">
              {aff.data.entities.slice(0, 12).map(e => (
                <li key={`${e.kind}:${e.id}`}><Badge tone="neutral">{e.kind}</Badge> <span className="mono">{e.id}</span></li>
              ))}
              {aff.data.total > 12 && <li className="srch-more">+{fmtNum(aff.data.total - 12)}</li>}
            </ul>
          ) : <SectionUnavailable what="Affected entities" />}
      </DrawerSection>
    </>
  );
}

function ExceptionBody({ fp }: { fp: string }) {
  const q = useQuery({
    queryKey: keys.search.detail('exception', fp),
    queryFn: () => api.getExceptionGroup(fp),
    staleTime: DETAIL_STALE_MS,
    placeholderData: undefined,
    retry: 0,
  });
  if (q.isPending) return <Spinner label="Loading exception group…" />;
  const g = q.data;
  if (!g) return <SectionUnavailable what="Exception group" />;
  return (
    <DrawerSection title="Exception group">
      <KeyValue items={[
        { k: 'Type', v: g.type, mono: true },
        { k: 'Message', v: g.message },
        { k: 'Service', v: g.service },
        { k: 'State', v: g.state },
        { k: 'Occurrences', v: fmtNum(g.occurrences) },
        { k: 'First seen', v: g.firstSeen ? tsLong(g.firstSeen) : '' },
        { k: 'Last seen', v: g.lastSeen ? tsLong(g.lastSeen) : '' },
      ]} />
    </DrawerSection>
  );
}

function ServiceBody({ svc, win, env }: { svc: string; win: { from: number; to: number }; env: string }) {
  const q = useQuery({
    queryKey: keys.search.detail('service', svc, { from: win.from, to: win.to, env }),
    queryFn: () => api.serviceBundle(svc, win, { env }),
    staleTime: DETAIL_STALE_MS,
    placeholderData: undefined,
    retry: 0,
  });
  if (q.isPending) return <Spinner label="Loading service…" />;
  const s = q.data?.service;
  if (!s) return <SectionUnavailable what="Service RED" />;
  const sec = Math.max(1, (win.to - win.from) / 1e9);
  const errPct = s.errorRate * 100;
  return (
    <DrawerSection title="RED">
      <div className="grid-4 srch-tiles">
        <StatTile label="Rate">{`${(s.spanCount / sec).toFixed(s.spanCount / sec < 10 ? 2 : 0)}/s`}</StatTile>
        <StatTile label="Errors" tone={errPct >= 5 ? 'err' : errPct >= 1 ? 'warn' : undefined}>{`${errPct.toFixed(2)}%`}</StatTile>
        <StatTile label="P99">{`${s.p99DurationMs.toFixed(0)} ms`}</StatTile>
        <StatTile label="Open problems" tone={(s.openProblems ?? 0) > 0 ? 'warn' : undefined}>{String(s.openProblems ?? 0)}</StatTile>
      </div>
    </DrawerSection>
  );
}

function EndpointBody({ svc, name, win, env }: { svc: string; name: string; win: { from: number; to: number }; env: string }) {
  const q = useEndpointDetail(svc
    ? { service: svc, path: endpointPathOf(name), from: win.from, to: win.to, ...(env ? { env } : {}) }
    : null);
  if (!svc) return <SectionUnavailable what="Endpoint detail" />;
  if (q.isPending) return <Spinner label="Loading endpoint…" />;
  const d = q.data;
  const sb = d?.statusBreakdown;
  return (
    <>
      <DrawerSection title="Status">
        {sb ? (
          <KeyValue items={[
            { k: '2xx', v: fmtNum(sb.http2xx) },
            { k: '3xx', v: fmtNum(sb.http3xx) },
            { k: '4xx', v: sb.http4xx > 0 ? <span className="cell-warn">{fmtNum(sb.http4xx)}</span> : '0' },
            { k: '5xx', v: sb.http5xx > 0 ? <span className="cell-err">{fmtNum(sb.http5xx)}</span> : '0' },
          ]} />
        ) : <SectionUnavailable what="Status breakdown" />}
      </DrawerSection>
      <DrawerSection title="Exemplars">
        {d?.exemplars && (d.exemplars.slowTraceId || d.exemplars.errorTraceId) ? (
          <KeyValue items={[
            { k: 'Slowest', mono: true, v: d.exemplars.slowTraceId
              ? <Link to={traceHref(d.exemplars.slowTraceId)}>{d.exemplars.slowTraceId}</Link> : '' },
            { k: 'Error', mono: true, v: d.exemplars.errorTraceId
              ? <Link to={traceHref(d.exemplars.errorTraceId)}>{d.exemplars.errorTraceId}</Link> : '' },
            { k: 'Top exceptions', v: d.topExceptions ? fmtNum(d.topExceptions.length) : '' },
          ]} />
        ) : <SectionUnavailable what="Exemplars" />}
      </DrawerSection>
    </>
  );
}
