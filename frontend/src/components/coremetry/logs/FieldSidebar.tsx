// coremetry/logs/FieldSidebar — v0.10.1853 (Log Search Faz 2 dilim 2.2; audit §6
// "Alan kenar çubuğu", mockup görünüm 1 `aside.side`). components/LogFieldsPanel
// (v0.8.255 Discover rayı) shadcn'e taşındı; davranış aynı, üstüne "İş alanları".
//
// Bölümler (yukarıdan aşağı): alan ara · İş alanları (sabit, en üstte —
// `system_settings['logs_search'].businessFields`; eşlemede çözülen yoksa bölüm
// GİZLİ) · Seçili alanlar (tablo kolonları) · Popüler alanlar · Mevcut alanlar.
// Satıra tık = üst değerler (yalnız açılınca TEK fieldstats isteği; liste
// boyunca ön-yükleme yok, 60 sn staleTime = sunucu TTL'i — ES-maliyet
// sözleşmesi). Değer satırında + filtrele / − hariç tut; altta kolon ekle,
// ∃ var / yok, daha fazla (20). Pencere toplamı ≥ 200.000 ise ES random_sampler
// (`est`, v0.10.1852) — etiket "~ örneklenmiş (%p) · N değer".
//
// ui/ importu ve tamamen statik satır içi stil YOK (yüzde çubuğunun genişliği
// hesaplanmış değer). Paylaşılan bileşik: servis Logs sekmesi kompakt kipi
// (dilim 3.10) aynısını takacak.
import { useMemo, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronRight, Minus, Plus } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { api } from '@/lib/api';
import { logFieldGlyph } from '@/lib/logFieldTypes';
import { liftBadge } from '@/lib/logFieldLift';
import { fieldStatsEst, fieldSampleLabel } from '@/lib/logFieldSample';
import { getRaw, setRaw } from '@/lib/storage';
import { useMediaQuery, WIDE_QUERY } from '@/lib/useMediaQuery';
import type { ResolvedBusinessField } from '@/lib/logBusinessFields';

const OPEN_KEY = 'logs.fieldsPanel.open';
const LIFT_KEY = 'logs.fieldsPanel.lift'; // v0.10.509 (C5)

/** fieldstats isteğinin dilimi — liste ile aynı süzgeç (Logs.tsx memo'su). */
export interface FieldStatsScope {
  from?: number;
  to?: number;
  service?: string;
  cluster?: string;
  env?: string;
  search?: string;
  pattern?: string;
  team?: string;
  severity?: number;
  traceId?: string;
  spanId?: string;
  lang?: 'lucene';
}

// v0.9.452 — backend'in conventionalLogFields listesiyle AYNI adlar
// (internal/logstore/elasticsearch.go): mapping'de VARSA öne çıkar.
const POPULAR_FIELDS: readonly string[] = [
  'kubernetes.container_name', 'openshift.labels.cluster', 'message', 'kubernetes.namespace_name',
  'kubernetes.pod_name', 'backendUrl', 'service_name', 'lastError.message', 'responseCode', 'throwable',
];

// fieldsPanelOpen — v0.10.1501: saklı tercih HER ZAMAN kazanır ('1' açık, '0'
// kapalı); tercih yoksa ≥1280px açık. SAF.
export function fieldsPanelOpen(stored: string | null, wide: boolean): boolean {
  if (stored === '1') return true;
  if (stored === '0') return false;
  return wide;
}

const H5 = 'mx-1 mt-2.5 mb-1 text-[length:var(--fs-xs)] font-semibold text-(--text3)';
const NOTE = 'px-1 text-[length:var(--fs-xs)] text-(--text3)';
const ROW = 'h-auto w-full justify-start gap-1.5 rounded-sm px-1.5 py-1 font-normal [content-visibility:auto] [contain-intrinsic-size:0_24px]';

interface FieldActions {
  onToggleColumn: (id: string) => void;
  onPillAdd: (key: string, value: string) => void;
  onPillExclude: (key: string, value: string) => void;
  onExists?: (key: string, negated: boolean) => void;
}

function FieldValues({ field, scope, isColumn, lift, windowTotal, actions }: {
  field: string;
  scope: FieldStatsScope;
  isColumn: boolean;
  lift: boolean;
  windowTotal?: number;
  actions: FieldActions;
}) {
  const [size, setSize] = useState<5 | 20>(5);
  // v0.10.1852 — pencere toplamı ≥ 200.000 → `est` (ES random_sampler); lift
  // açıkken sunucu örneklemez, parametre de gitmez.
  const est = lift ? undefined : fieldStatsEst(windowTotal);
  const q = useQuery({
    queryKey: ['logs', 'fieldstats', field, scope, size, lift, est],
    // v0.10.1822 — RQ sinyali: alan kapanınca / süzgeç değişince ES terms agg iptal.
    queryFn: ({ signal }) => api.logsFieldStats({ field, ...scope, ...(size !== 5 ? { size } : {}), ...(lift ? { errorLift: 1 as const } : {}), ...(est !== undefined ? { est } : {}) }, signal),
    staleTime: 60_000,
    retry: 1,
  });
  const d = q.data;
  const sampled = fieldSampleLabel(d);
  return (
    <div className="mx-0.5 mt-0.5 mb-1.5 grid gap-1 rounded-sm border bg-(--bg2) px-2 pt-1.5 pb-2 text-[length:var(--fs-xs)]" data-testid={`fs-values-${field}`}>
      {q.isLoading && <div className="text-(--text3)">Üst değerler yükleniyor…</div>}
      {q.isError && <div className="text-status-critical">Üst değerler alınamadı</div>}
      {lift && d?.errorLift && (
        <div className="text-(--text3)" title="Değerler hata seçimine (severity ≥ ERROR) göre; rozet tabana (aynı süzgeç, tüm seviyeler) göre farkı gösterir">
          {d.errorLift.degraded
            ? <StatusBadge status="warn" size="sm">taban okunamadı</StatusBadge>
            : <>hata seçimi {d.errorLift.selectionTotal.toLocaleString('tr-TR')} · taban {d.errorLift.baselineTotal.toLocaleString('tr-TR')} doküman</>}
        </div>
      )}
      {d?.degraded && <div className="text-status-warn">⚠ {d.reason ?? 'log backend yavaş/erişilemez'} — üst değerler hesaplanamadı</div>}
      {d?.partial && (
        <div className="text-status-warn">⚠ kısmi cevap — üst değerler ve payda alt küme{d.shardsFailed ? ` (${d.shardsFailed} shard cevapsız)` : ''}</div>
      )}
      {d && !d.degraded && d.values.length === 0 && <div className="text-(--text3)">Bu pencerede değer yok</div>}
      {/* v0.9.1217 — kapsama: alanın pencere dokümanlarının yüzde kaçında olduğu. */}
      {d && windowTotal != null && windowTotal > 0 && d.total > 0 && !d.sampled && (
        <div className="text-(--text3)">
          dokümanların ~%{Math.min(100, (d.total / windowTotal) * 100).toFixed((d.total / windowTotal) * 100 >= 10 ? 0 : 1)}'inde
        </div>
      )}
      {d && d.values.map(v => {
        const pct = d.total > 0 ? (v.count / d.total) * 100 : 0;
        const b = liftBadge(v, !!lift && !!d.errorLift && !d.errorLift.degraded);
        return (
          <div key={v.value} className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-1.5">
            <span className="truncate font-mono" title={v.value}>{v.value}</span>
            <span className="text-(--text3) tabular-nums"
              title={lift && d.errorLift ? `hata seçiminde %${(v.selPct ?? pct).toFixed(1)} · tabanda %${(v.basePct ?? 0).toFixed(1)}` : undefined}>
              {d.sampled ? '~' : ''}%{pct.toFixed(pct >= 10 ? 0 : 1)}
              {b.kind !== 'none' && (
                // v0.10.929 (K5) — hatada DAHA AZ görülen değer iyileşme sinyali: nötr.
                <span className="ml-1"><StatusBadge size="sm" status={b.kind === 'up' ? 'critical' : 'unknown'}
                  title="lift = hata seçimindeki pay − tabandaki pay (puan); ±5 altı gürültü">{b.label}</StatusBadge></span>
              )}
            </span>
            <span className="inline-flex gap-0.5">
              <Button size="icon-xs" variant="outline" aria-label={`Filtrele: ${field}: ${v.value}`} title="Filtrele"
                onClick={() => actions.onPillAdd(field, v.value)}><Plus /></Button>
              <Button size="icon-xs" variant="outline" aria-label={`Hariç tut: ${field}: ${v.value}`} title="Hariç tut"
                onClick={() => actions.onPillExclude(field, v.value)}><Minus /></Button>
            </span>
            <span className="col-span-3 h-[3px] rounded-sm bg-(--bg3)">
              <span className="block h-[3px] rounded-sm bg-(--accent)" style={{ width: `${Math.max(2, pct)}%` }} />
            </span>
          </div>
        );
      })}
      <div className="mt-1 flex flex-wrap items-center gap-1.5">
        {sampled && (
          <span className="mr-auto text-(--text3)" data-testid="fs-sampled"
            title="Değerler pencerenin rastgele bir alt kümesinden sayıldı (Elasticsearch random_sampler); yüzdeler yaklaşık">
            {sampled} · {d?.values.length ?? 0} değer
          </span>
        )}
        {d && size === 5 && d.values.length >= 5 && (
          <Button variant="outline" size="xs" onClick={() => setSize(20)}>daha fazla (20)</Button>
        )}
        {size === 20 && <Button variant="outline" size="xs" onClick={() => setSize(5)}>daha az</Button>}
        <Button variant="outline" size="xs" onClick={() => actions.onToggleColumn(field)}>
          {isColumn ? 'Kolonu kaldır' : 'Kolon ekle'}
        </Button>
        {actions.onExists && (
          <>
            <Button variant="outline" size="xs" onClick={() => actions.onExists?.(field, false)}
              title={`Yalnız ${field} alanı OLAN kayıtlar (_exists_:${field})`}>∃ var</Button>
            <Button variant="outline" size="xs" onClick={() => actions.onExists?.(field, true)}
              title={`Yalnız ${field} alanı OLMAYAN kayıtlar (NOT _exists_:${field})`}>∃ yok</Button>
          </>
        )}
      </div>
    </div>
  );
}

export interface FieldSidebarProps extends FieldActions {
  /** Eşlemeden gelen alanlar (/api/logs/fields; CH backend'inde boş). */
  fields: string[];
  /** Eşlemenin gerçek yol sayısı (liste kırpıldıysa "ilk N / M"). */
  fieldsTotal?: number;
  /** Alan → eşleme tipi (glif). */
  types?: Record<string, string>;
  /** Tablonun etkin dinamik kolonları. */
  columns: string[];
  scope: FieldStatsScope;
  /** Pencere toplamı (kapsama paydası + örnekleme `est`). */
  windowTotal?: number;
  /** v0.10.1853 — çözülmüş iş alanları (lib/logBusinessFields); boşsa bölüm gizli. */
  businessFields?: readonly ResolvedBusinessField[];
}

export function FieldSidebar({
  fields, fieldsTotal, types, columns, scope, windowTotal, businessFields = [],
  onToggleColumn, onPillAdd, onPillExclude, onExists,
}: FieldSidebarProps) {
  const wide = useMediaQuery(WIDE_QUERY);
  const [stored, setStored] = useState<string | null>(() => getRaw(OPEN_KEY));
  const open = fieldsPanelOpen(stored, wide);
  // v0.10.509 (C5) — "Hatalıyı ayıran": kalıcı; açıkken genişletilen alan için
  // iki fieldstats (hata seçimi + taban). Kapalıyken ek istek yok.
  const [lift, setLift] = useState<boolean>(() => getRaw(LIFT_KEY) === '1');
  const toggleLift = () => setLift(v => { setRaw(LIFT_KEY, v ? '0' : '1'); return !v; });
  const [needle, setNeedle] = useState('');
  const [expanded, setExpanded] = useState<string | null>(null);
  const toggleOpen = () => { const next = open ? '0' : '1'; setRaw(OPEN_KEY, next); setStored(next); };
  const actions: FieldActions = { onToggleColumn, onPillAdd, onPillExclude, onExists };

  const n = needle.trim().toLowerCase();
  const match = (f: string) => !n || f.toLowerCase().includes(n);
  const selectedSet = useMemo(() => new Set(columns), [columns]);
  const business = useMemo(
    () => businessFields.filter(b => match(b.field) || match(b.label)),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- match = needle
    [businessFields, n]);
  const businessSet = useMemo(() => new Set(businessFields.map(b => b.field)), [businessFields]);
  const selected = useMemo(() => columns.filter(f => !businessSet.has(f) && match(f)),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- match = needle
    [columns, businessSet, n]);
  const popular = useMemo(() => {
    const set = new Set(fields);
    return POPULAR_FIELDS.filter(f => set.has(f) && !selectedSet.has(f) && !businessSet.has(f) && match(f));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- match = needle
  }, [fields, selectedSet, businessSet, n]);
  const available = useMemo(() => {
    const pop = new Set(POPULAR_FIELDS);
    return fields.filter(f => !selectedSet.has(f) && !businessSet.has(f) && !pop.has(f) && match(f));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- match = needle
  }, [fields, selectedSet, businessSet, n]);

  if (!open) {
    return (
      <Button variant="outline" size="xs" onClick={toggleOpen} title="Alan panelini göster"
        className="h-auto w-6 self-stretch p-0 [writing-mode:vertical-rl]">
        ƒ Alanlar
      </Button>
    );
  }

  const row = (f: string, label?: ReactNode, chosen?: boolean) => {
    const g = logFieldGlyph(types?.[f]);
    const isOpen = expanded === f;
    return (
      <div key={f}>
        <Button variant="ghost" size="xs" aria-expanded={isOpen} title={`${f} — üst değerler için tıkla`}
          data-testid={`fs-field-${f}`}
          className={ROW + (isOpen ? ' bg-(--accent-soft)' : '') + (chosen ? ' text-(--accent2)' : ' text-(--text2)')}
          onClick={() => setExpanded(cur => (cur === f ? null : f))}>
          <span className="w-4 flex-none rounded-[3px] border text-center font-mono text-[10px] font-semibold text-(--text3)"
            title={g?.label} aria-label={g?.label}>{g?.glyph ?? '·'}</span>
          <span className="min-w-0 flex-1 truncate text-left font-mono">{label ?? f}</span>
          {isOpen ? <ChevronDown className="text-(--text3)" /> : <ChevronRight className="text-(--text3)" />}
        </Button>
        {isOpen && (
          <FieldValues field={f} scope={scope} lift={lift} windowTotal={windowTotal}
            isColumn={selectedSet.has(f)} actions={actions} />
        )}
      </div>
    );
  };

  return (
    <aside className="ld-fields" aria-label="Alanlar" data-testid="field-sidebar">
      <div className="mb-1.5 flex items-center gap-1.5">
        <Input className="h-7 flex-1 text-[length:var(--fs-sm)]" placeholder="Alan ara…" aria-label="Alan ara"
          value={needle} onChange={e => setNeedle(e.target.value)} />
        <Button variant="outline" size="xs" aria-pressed={lift} onClick={toggleLift}
          className={lift ? 'bg-(--accent-soft) text-(--accent2)' : undefined}
          title="Hatalıyı ayıran: genişletilen alanın değerlerini hata logları (severity ≥ ERROR) için sayar ve tabana göre farkı (lift, puan) gösterir — açıkken alan başına bir ek sorgu">
          ⚠ ayıran
        </Button>
        <Button variant="outline" size="icon-xs" aria-label="Alan panelini gizle" title="Alan panelini gizle" onClick={toggleOpen}>«</Button>
      </div>

      {business.length > 0 && (
        <section aria-label="İş alanları" data-testid="fs-business">
          <h5 className={H5}>İş alanları</h5>
          {business.map(b => row(b.field, b.label === b.field ? undefined : <>{b.label} <span className="text-(--text3)">· {b.field}</span></>, selectedSet.has(b.field)))}
        </section>
      )}

      <h5 className={H5}>Seçili alanlar</h5>
      {selected.length === 0 && <div className={NOTE}>—</div>}
      {selected.map(f => row(f, undefined, true))}

      {popular.length > 0 && (
        <>
          <h5 className={H5}>Popüler alanlar</h5>
          {popular.map(f => row(f))}
        </>
      )}
      <h5 className={H5}>Mevcut alanlar{fields.length > 0 ? ` (${(fieldsTotal ?? fields.length).toLocaleString('tr-TR')})` : ''}</h5>
      {fields.length === 0 && <div className={NOTE}>Bu backend'de eşleme keşfi yok</div>}
      {available.map(f => row(f))}
      {typeof fieldsTotal === 'number' && fieldsTotal > fields.length && (
        <div className={NOTE + ' pt-1.5'}
          title={`Eşleme ${fieldsTotal.toLocaleString('tr-TR')} alan yolu taşıyor; ray akıcı kalsın diye liste kırpıldı — yukarıdan arayın.`}>
          ilk {fields.length.toLocaleString('tr-TR')} / {fieldsTotal.toLocaleString('tr-TR')} alan · aramak için yazın
        </div>
      )}
    </aside>
  );
}
