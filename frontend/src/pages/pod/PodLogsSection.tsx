// PodLogsSection — v0.10.910: pod sayfasında bu pod'un logları (mockup Onay
// 2026-09-24). Varsayılan KAPALI; başlığa tıklanınca yüklenir (ES maliyet
// disiplini: açılınca fetch, liste prefetch yok). Açık/kapalı + seviye + arama
// URL'de (?logs=1&plvl=&plq=, replace). Mevcut primitifler: /api/logs
// (servis log sekmesiyle aynı istemci), LogTable, LogContextModal (satır →
// bağlam; modalın "yalnız bu pod" kapsamı kendisinde).
// v0.10.2233 (Entity S3, A8): pod kapsamı /api/logs?entity=pod:<cid>/<ns>/<pod>
// — sunucu iki backend'in ALAN süzgeçlerine indirir (pod + namespace +
// cluster); CH'de pod adının gövdede aranması kalktı.
import { useEffect, useMemo, useState } from 'react';
import { useSearchParams, Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { LogRow } from '@/lib/types';
import { Card, DisclosureButton, SegmentedControl } from '@/components/ui';
import { PanelTitle } from '@/components/ui/PanelTitle';
import { LogTable } from '@/components/LogTable';
import type { DataTableStateProps } from '@/components/ui/DataTable';
import { LogContextModal } from '@/components/LogContextModal';
import { t, tf, useLang } from '@/lib/i18n';
import { levelCounts, POD_LOG_MIN_SEV, type PodLogLevel } from './podLogs';

const POD_LOG_LIMIT = 100;
// v0.10.967 — kararlı boş dizi: satır gösterilmeyen durumlarda LogTable'a
// (ServiceSignalTabs emsali).
const NO_LOGS: LogRow[] = [];

export function PodLogsSection({ entityId, from, to, logsLink }: {
  /** pod:<cid>/<ns>/<pod> — boşsa (cluster kaydı çözülmedi) bölüm sorgu atmaz. */
  entityId: string; from: number; to: number;
  /** /logs sayfasında aynı pod piliyle aç. */
  logsLink: string;
}) {
  const lang = useLang(); // v0.10.2246 — metinler arayüz dilinde (podpage.logs.*)
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
    const timer = setTimeout(() => { setText(input); setParam('plq', input.trim() || null); }, 300);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [input]);

  const search = text.trim();
  const minSev = POD_LOG_MIN_SEV[lvl];
  const q = useQuery({
    queryKey: ['pod-logs', entityId, from, to, search, minSev ?? 0],
    // v0.10.1822 — RQ sinyali: süzgeç değişince / çıkışta ES işi iptal.
    queryFn: ({ signal }) => api.logs({ limit: POD_LOG_LIMIT, from, to, search: search || undefined, severity: minSev, entity: entityId }, signal),
    enabled: open && !!entityId,
    staleTime: 15_000,
    refetchOnWindowFocus: false, // /api/logs önbelleksiz; ES PIT açar (servis sekmesi gerekçesi)
  });
  const rows = q.data?.logs ?? [];
  const counts = useMemo(() => levelCounts(rows), [rows]);

  // v0.10.967 — tablo standardı dilim 5 (T12 + P-1, P6 LogTable `state`):
  // yükleniyor / backend yanıtsız / yavaş / boş artık LogTable'ın İÇİNDE,
  // sütun başlıkları durur. Sıra eskisiyle aynı. degraded "log yok" değil →
  // hata türü, nedeni satırda. Yenileme düşüp önbellek eski satırları
  // tutuyorsa hata yine görünür: showRows bayat satırları, sayaç çiplerini
  // ve alt notu birlikte gizler (bayat sayı hata satırının altında kalmasın).
  // Arama ya da seviye seçiliyken boş sonuç "eşleşme yok" (ortak temizleme
  // eylemi yok). CH gövde-araması ipucu + Loglar sayfası bağlantısı iki
  // boş türün de detail yuvasında (P-1) — silinmedi, dışarı taşınmadı.
  const degraded = !!q.data?.degraded;
  const showRows = !q.isPending && !q.isError && !degraded && rows.length > 0;
  const bodyHint = (
    <span>
      {t('podpage.logs.hint1', lang)}<Link to={logsLink}>{t('podpage.logs.hint2', lang)}</Link>{t('podpage.logs.hint3', lang)}
    </span>
  );
  const logsState: Omit<DataTableStateProps<LogRow>, 'dt' | 'leading' | 'trailing'> =
    !entityId ? { kind: 'empty', message: t('podpage.logs.noId', lang), detail: bodyHint }
    : q.isPending ? { kind: 'loading' }
    : q.isError ? { kind: 'error', message: t('podpage.logs.noResp', lang) }
    : degraded ? {
        kind: 'error',
        message: `${t('podpage.logs.degraded', lang)}${q.data?.reason ? `: ${q.data.reason}` : '.'}`,
      }
    : (text.trim() || lvl !== 'all') ? {
        kind: 'no-match', message: t('podpage.logs.noMatch', lang), detail: bodyHint,
      }
    : { kind: 'empty', message: t('podpage.logs.empty', lang), detail: bodyHint };

  // v0.10.914 (buton bütünlüğü) — tek seçimli seviye: ortak SegmentedControl.
  const lvlLabel = (label: string, n?: number) => (n !== undefined ? `${label} · ${n}` : label);

  return (
    <div className="pod-sec">
      <PanelTitle sub={t('podpage.logs.sub', lang)} right={<Link to={logsLink} className="sec">{t('podpage.logs.openPage', lang)}</Link>}>{t('podpage.logs.title', lang)}</PanelTitle>
      <Card style={{ padding: 0 }}>
        <DisclosureButton anatomy="section" expanded={open} onClick={() => setParam('logs', open ? null : '1')}>
          {t('podpage.logs.disclosure', lang)} <span className="field-hint">{tf('podpage.logs.disclosureHint', lang, { n: POD_LOG_LIMIT })}</span>
        </DisclosureButton>
        {open && (
          <div className="pod-ek-body">
            <div style={{ display: 'flex', gap: 6, alignItems: 'center', flexWrap: 'wrap', marginBottom: 8 }}>
              <SegmentedControl size="sm" aria-label={t('podpage.logs.level', lang)} value={lvl}
                onChange={v => setParam('plvl', v === 'all' ? null : v)} options={[
                  { value: 'all', label: t('podpage.all', lang) },
                  { value: 'error', label: lvlLabel('ERROR', lvl === 'all' && showRows ? counts.error : undefined) },
                  { value: 'warn', label: lvlLabel('WARN+', lvl === 'all' && showRows ? counts.warn : undefined) },
                ]} />
              <input className="field" style={{ flex: '1 1 240px', maxWidth: 360 }} placeholder={t('podpage.logs.search', lang)}
                value={input} onChange={e => setInput(e.target.value)} aria-label={t('podpage.logs.searchAria', lang)} />
            </div>
            <LogTable logs={showRows ? rows : NO_LOGS} state={logsState} onContextOpen={setPivot} />
            {showRows && (
              <div className="pod-cap">
                {tf('podpage.logs.count', lang, { n: rows.length })}{rows.length >= POD_LOG_LIMIT ? t('podpage.logs.morePage', lang) : ''}
                {lvl === 'all' ? t('podpage.logs.chipNote', lang) : ''}
                {' · '}{t('podpage.logs.filterNote', lang)}
              </div>
            )}
          </div>
        )}
      </Card>
      <LogContextModal pivot={pivot} onClose={() => setPivot(null)} search={search} />
    </div>
  );
}
