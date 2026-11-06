// coremetry/logs/LogDetailSheet — v0.10.1880 (Log Search Faz 3 dilim 3.1; audit
// §6 "Satır detayı", §9-13 kararı: satır içi genişleme yerine çekmece; mockup
// görünüm 2). coremetry/DetailSheet üstünde; URL `?doc=<ts>.<id>` (çağıranın
// useSheetParam'ı — paylaşılan kalıcı link aynı çekmeceyi açar, Geri kapatır).
//
// Sıra (mockup): başlık (zaman + seviye rozeti + ↑/↓) → DÜZ CÜMLE → eylemler
// (Trace (t) · Filtrele (f) · Hariç tut (e) · Bağlantıyı kopyala · açıkla) →
// [exception yuvası, dilim 3.3] → Alanlar (Tablo/JSON + alan süz; + − ▥) →
// [çevre loglar yuvası, dilim 3.2] → Teknik kanıt (kapalı).
// Dar `_source` satırının tam dokümanı çağıranda çekilir (açılınca TEK `ids`
// isteği); burada durum notu. Saf sunum; ui/ importu ve satır içi stil yok.
// Paylaşılan bileşik: servis Logs sekmesi kompakt kipi (dilim 3.10) aynısını takar.
import { useMemo, useState, type ReactNode } from 'react';
import { ChevronDown, ChevronUp, Columns3, Minus, Plus } from 'lucide-react';
import { DetailSheet } from '@/components/coremetry/DetailSheet';
import { KeyValue, KeyValueRow } from '@/components/coremetry/KeyValue';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { CopyButton } from '@/components/CopyButton';
import { detailFields, plainLogSentence, severityLabel, severityStatus, technicalFields } from '@/lib/logDetail';
import { fmtClock, isoStampLocal } from '@/lib/utils';
import { getRaw, setRaw } from '@/lib/storage';
import type { LogRow } from '@/lib/types';

const TAB_KEY = 'logs.docTab'; // LogTable'ın doküman sekmesi hafızasıyla aynı anahtar
type DocTab = 'table' | 'json';
const TABS = [{ value: 'table' as const, label: 'Tablo' }, { value: 'json' as const, label: 'JSON' }];
const readTab = (): DocTab => (getRaw(TAB_KEY) === 'json' ? 'json' : 'table');

export interface LogDetailSheetProps {
  /** Açık satır; null = kapalı. */
  row: LogRow | null;
  onClose(): void;
  /** Dar okunmuş satırın tam dokümanı: 'loading' | 'error' | undefined (tam). */
  fullDoc?: 'loading' | 'error';
  /** Görünen tablo kolonları (▥ eylemi ekle/kaldır). */
  columns?: readonly string[];
  onFilter?(key: string, value: string): void;
  onExclude?(key: string, value: string): void;
  onToggleColumn?(key: string): void;
  onPrev?(): void;
  onNext?(): void;
  /** Trace + loglar (t). */
  onOpenTrace?(traceId: string): void;
  /** Mutlak kalıcı link (Bağlantıyı kopyala). */
  permalink?: string;
  /** Satır sonu ek eylemler (ör. ✨ açıkla). */
  actions?: ReactNode;
  /** Exception bölümü yuvası (dilim 3.3). */
  exception?: ReactNode;
  /** Çevre loglar bölümü yuvası (dilim 3.2). */
  context?: ReactNode;
  /** Çekmece genişliği anahtarı. */
  resizeKey?: string;
}

const clockOf = (ns: number) => {
  const ms = ns / 1e6;
  return `${fmtClock(ms)}.${String(Math.floor(ms % 1000)).padStart(3, '0')}`;
};

export function LogDetailSheet({
  row, onClose, fullDoc, columns = [], onFilter, onExclude, onToggleColumn, onPrev, onNext,
  onOpenTrace, permalink, actions, exception, context, resizeKey = 'logs.detailSheet.w',
}: LogDetailSheetProps) {
  const [tab, setTabState] = useState<DocTab>(readTab);
  const setTab = (t: DocTab) => { setTabState(t); setRaw(TAB_KEY, t); };
  const [needle, setNeedle] = useState('');
  const fields = useMemo(() => (row ? detailFields(row, needle) : []), [row, needle]);
  const tech = useMemo(() => (row ? technicalFields(row) : []), [row]);
  const json = useMemo(() => (row ? JSON.stringify(row, null, 2) : ''), [row]);
  const colSet = useMemo(() => new Set(columns), [columns]);
  const open = !!row;
  const clock = row ? clockOf(row.timestamp) : '';
  const svc = row?.serviceName ?? '';
  return (
    <DetailSheet open={open} onOpenChange={o => { if (!o) onClose(); }} resizeKey={resizeKey} width={640}
      ariaLabel="Log kaydı detayı"
      title={`Log kaydı · ${clock}`}
      badges={row && (
        <span className="inline-flex items-center gap-1.5">
          <StatusBadge status={severityStatus(row.severity)} size="sm">{severityLabel(row)}</StatusBadge>
          {onPrev && <Button variant="outline" size="icon-xs" aria-label="Önceki kayıt (k)" title="Önceki (k)" onClick={onPrev}><ChevronUp /></Button>}
          {onNext && <Button variant="outline" size="icon-xs" aria-label="Sonraki kayıt (j)" title="Sonraki (j)" onClick={onNext}><ChevronDown /></Button>}
        </span>
      )}>
      {row && (
        <div className="grid gap-4 text-sm" data-testid="log-detail">
          <p className="m-0 text-[length:var(--fs-md)] leading-snug text-(--text)" data-testid="log-detail-lead">
            {plainLogSentence(row, clock)}
          </p>
          <div className="flex flex-wrap items-center gap-1.5">
            {row.traceId && onOpenTrace && (
              <Button size="sm" onClick={() => onOpenTrace(row.traceId)} title="Bu kaydın trace'i ve logları (t)">Trace + loglar (t)</Button>
            )}
            {svc && onFilter && (
              <Button variant="outline" size="sm" onClick={() => onFilter('service.name', svc)} title={`service.name: ${svc} (f)`}>Bu servise filtrele (f)</Button>
            )}
            {svc && onExclude && (
              <Button variant="outline" size="sm" onClick={() => onExclude('service.name', svc)} title={`NOT service.name: ${svc} (e)`}>Hariç tut (e)</Button>
            )}
            {permalink && <CopyButton value={permalink} title="Kalıcı doküman linkini kopyala (pencereden bağımsız çözülür)" />}
            {actions}
          </div>
          {fullDoc === 'loading' && <p className="m-0 text-[length:var(--fs-xs)] text-(--text3)" role="status">Tam doküman yükleniyor… (liste yalnız görünen alanları okur)</p>}
          {fullDoc === 'error' && <p className="m-0 text-[length:var(--fs-xs)] text-status-warn" role="status">Tam doküman okunamadı — yalnız listede yüklenen alanlar gösteriliyor.</p>}

          {exception}

          <section aria-label="Alanlar" className="grid gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="m-0 text-[length:var(--fs-sm)] font-semibold">Alanlar</h3>
              <SegmentedControl size="sm" aria-label="Tablo / JSON" options={TABS} value={tab} onChange={setTab} />
              {tab === 'table' && (
                <Input className="h-7 w-40 text-[length:var(--fs-sm)]" placeholder="Alan süz…" aria-label="Alan süz"
                  value={needle} onChange={e => setNeedle(e.target.value)} />
              )}
            </div>
            {tab === 'table' ? (
              <>
                <pre className="m-0 max-h-60 overflow-auto rounded-sm border bg-(--bg2) p-2 font-mono text-[length:var(--fs-xs)] break-words whitespace-pre-wrap">{row.body}</pre>
                <KeyValue labelWidth="wide" data-testid="log-detail-fields">
                  <KeyValueRow k="@timestamp" v={isoStampLocal(new Date(row.timestamp / 1e6).toISOString()) ?? String(row.timestamp)} title={String(row.timestamp)} />
                  {fields.map(f => (
                    <KeyValueRow key={f.key} k={f.key} v={f.value} mono={f.mono} actions={f.actionable ? (
                      <>
                        {onFilter && <Button variant="ghost" size="icon-xs" aria-label={`Filtrele: ${f.key}: ${f.value}`} title="Filtrele" onClick={() => onFilter(f.key, f.value)}><Plus /></Button>}
                        {onExclude && <Button variant="ghost" size="icon-xs" aria-label={`Hariç tut: ${f.key}: ${f.value}`} title="Hariç tut" onClick={() => onExclude(f.key, f.value)}><Minus /></Button>}
                        {onToggleColumn && f.key !== 'level' && (
                          <Button variant="ghost" size="icon-xs" aria-pressed={colSet.has(f.key)}
                            aria-label={`${colSet.has(f.key) ? 'Kolonu kaldır' : 'Kolon ekle'}: ${f.key}`}
                            title={colSet.has(f.key) ? 'Kolonu kaldır' : 'Kolon ekle'} onClick={() => onToggleColumn(f.key)}><Columns3 /></Button>
                        )}
                      </>
                    ) : undefined} />
                  ))}
                </KeyValue>
                {fields.length === 0 && needle && <p className="m-0 text-[length:var(--fs-xs)] text-(--text3)">"{needle}" ile eşleşen alan yok</p>}
              </>
            ) : (
              <div className="grid gap-1">
                <div className="flex justify-end"><CopyButton value={json} title="JSON dokümanı kopyala" /></div>
                <pre className="m-0 max-h-[60vh] overflow-auto rounded-sm border bg-(--bg2) p-2 font-mono text-[length:var(--fs-xs)] break-words whitespace-pre-wrap" data-testid="log-detail-json">{json}</pre>
              </div>
            )}
          </section>

          {context}

          {tech.length > 0 && (
            <details className="rounded-sm border px-2 py-1.5" data-testid="log-detail-tech">
              <summary className="cursor-pointer text-[length:var(--fs-xs)] text-(--text2)">Teknik kanıt: kaynak öznitelikleri, indeks, doküman kimliği</summary>
              <KeyValue labelWidth="wide" className="mt-2" items={tech.map(t => ({ id: t.key, k: t.key, v: t.value, mono: true }))} />
            </details>
          )}
        </div>
      )}
    </DetailSheet>
  );
}
