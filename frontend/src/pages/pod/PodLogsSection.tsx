// PodLogsSection — v0.10.910: pod sayfasında bu pod'un logları (mockup Onay
// 2026-09-24). Varsayılan KAPALI; başlığa tıklanınca yüklenir (ES maliyet
// disiplini: açılınca fetch, liste prefetch yok). Açık/kapalı + seviye + arama
// URL'de (?logs=1&plvl=&plq=, replace). Mevcut primitifler: /api/logs
// (servis log sekmesiyle aynı istemci), LogTable, LogContextModal (satır →
// bağlam; modalın "yalnız bu pod" kapsamı kendisinde).
import { useEffect, useMemo, useState } from 'react';
import { useSearchParams, Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { LogRow } from '@/lib/types';
import { Card, DisclosureButton } from '@/components/ui';
import { PanelTitle } from '@/components/ui/PanelTitle';
import { Spinner, Empty } from '@/components/Spinner';
import { LogTable } from '@/components/LogTable';
import { LogContextModal } from '@/components/LogContextModal';
import { podLogSearch, levelCounts, POD_LOG_MIN_SEV, type PodLogLevel } from './podLogs';

const POD_LOG_LIMIT = 100;

export function PodLogsSection({ pod, from, to, service, cluster, logsLink }: {
  pod: string; from: number; to: number; service?: string; cluster?: string;
  /** /logs sayfasında aynı pod piliyle aç. */
  logsLink: string;
}) {
  const [sp, setSp] = useSearchParams();
  const open = sp.get('logs') === '1';
  const lvl = ((sp.get('plvl') as PodLogLevel | null) ?? 'all');
  const urlQ = sp.get('plq') ?? '';
  const [input, setInput] = useState(urlQ);
  const [text, setText] = useState(urlQ);
  const [pivot, setPivot] = useState<LogRow | null>(null);

  const setParam = (k: string, v: string | null) => setSp(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set(k, v); else next.delete(k);
    return next;
  }, { replace: true });

  useEffect(() => {
    const t = setTimeout(() => { setText(input); setParam('plq', input.trim() || null); }, 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [input]);

  const search = useMemo(() => podLogSearch(pod, text), [pod, text]);
  const minSev = POD_LOG_MIN_SEV[lvl];
  const q = useQuery({
    queryKey: ['pod-logs', pod, from, to, search, minSev ?? 0, service ?? '', cluster ?? ''],
    queryFn: () => api.logs({ limit: POD_LOG_LIMIT, from, to, search, severity: minSev, service: service || undefined, cluster: cluster || undefined }),
    enabled: open && !!pod,
    staleTime: 15_000,
    refetchOnWindowFocus: false, // /api/logs önbelleksiz; ES PIT açar (servis sekmesi gerekçesi)
  });
  const rows = q.data?.logs ?? [];
  const counts = useMemo(() => levelCounts(rows), [rows]);

  // Servis log sekmesinin seviye facet'iyle aynı görsel dil (.ov-facet);
  // erişilebilirlik için düğme (aria-pressed), görünüm sıfırlanır.
  const chip = (v: PodLogLevel, label: string, n?: number) => (
    <button type="button" className={`ov-facet${lvl === v ? ' on' : ''}`} aria-pressed={lvl === v}
      style={{ font: 'inherit', cursor: 'pointer' }}
      onClick={() => setParam('plvl', v === 'all' ? null : v)}>
      {label}{n !== undefined && <span className="n"> {n}</span>}
    </button>
  );

  return (
    <div className="pod-sec">
      <PanelTitle sub="varsayılan kapalı · açılınca yüklenir" right={<Link to={logsLink} className="sec">→ Loglar sayfasında aç</Link>}>Loglar</PanelTitle>
      <Card style={{ padding: 0 }}>
        <DisclosureButton anatomy="section" expanded={open} onClick={() => setParam('logs', open ? null : '1')}>
          Bu pod'un logları <span className="field-hint">· seçili pencere · en yeni {POD_LOG_LIMIT} satır</span>
        </DisclosureButton>
        {open && (
          <div className="pod-ek-body">
            <div style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap', marginBottom: 8 }}>
              {chip('all', 'Tümü')}
              {chip('error', 'ERROR', lvl === 'all' && rows.length ? counts.error : undefined)}
              {chip('warn', 'WARN+', lvl === 'all' && rows.length ? counts.warn : undefined)}
              <input className="field" style={{ flex: '1 1 240px', maxWidth: 360 }} placeholder="Ara (mesaj içinde)…"
                value={input} onChange={e => setInput(e.target.value)} aria-label="Pod logları içinde ara" />
            </div>
            {q.isPending ? <Spinner />
              : q.isError ? <Empty icon="—" title="Log backend'i yanıt vermedi." />
              : q.data?.degraded ? <Empty icon="—" title="Log backend'i yavaş/erişilemez — liste boş gösterilmedi.">{q.data.reason}</Empty>
              : rows.length === 0 ? <Empty icon="—" title="Bu pod için bu pencerede log yok.">
                  Log backend'i ClickHouse ise pod adı log gövdesinde aranır; gövdede geçmiyorsa sonuç çıkmaz — <Link to={logsLink}>Loglar sayfasında</Link> pili kaldırıp servis kapsamıyla bakın.
                </Empty>
              : <LogTable logs={rows} onContextOpen={setPivot} />}
            {rows.length > 0 && (
              <div className="pod-cap">
                {rows.length} satır (en yeni önce){rows.length >= POD_LOG_LIMIT ? ' · daha fazlası için Loglar sayfası' : ''}
                {lvl === 'all' ? ' · çip sayıları bu sayfadaki satırlardan' : ''}
                {' · '}süzgeç kubernetes.pod_name (ES alan süzgeci; CH'de gövde araması) · satıra tıkla → bağlam
              </div>
            )}
          </div>
        )}
      </Card>
      <LogContextModal pivot={pivot} onClose={() => setPivot(null)} search={search} />
    </div>
  );
}
