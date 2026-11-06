// LogsActionsMenus — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı,
// dilim 1; operatör onaylı taslak 2026-10-24).
//
// Sorgu çubuğunun sağ kümesindeki üç açılır yüzey. Eskiden bunların her biri
// çubukta ya da üstündeki satırda ayrı bir düğmeydi (Trace ID kutusu, ◆ With
// trace, Takımım, Copy link, ↗ Discover in Elastic, Reset, ✨ Desenleri
// anlat, CSV/NDJSON) ve çubuk 1280px'te üç satıra sarıyordu. Hepsi TEK
// TIKLA ulaşılır kaldı (capabilityReach.test.tsx çiviliyor):
//   • + Filter ▾  (dialog — içinde girdi var): Trace ID, ◆ With trace,
//                 Takımım, + alan süzgeci (v0.10.1854: coremetry FilterChipEditor, yeni hap).
//   • Share ▾     (menu): Copy link · ↗ Discover in Elastic (desen/takım
//                 etkinken çizilmez — aynı dilim sözü tutmaz, bugünkü kural).
//   • ⋯           (menu): Reset. (v0.10.1499: ✨ Desenleri anlat desen
//                 görünümünün araç çubuğuna; v0.10.1502: Export CSV/NDJSON
//                 tablo araç çubuğunun ⋯'ine — LogsExportMenu.)
// Saf sunum: durum ve eylemler Logs.tsx'ten prop olarak gelir.
import { useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { MenuItem } from '@/components/ui/Menu';
import { Popover, PopoverHrefItem } from '@/components/ui/Popover';
import { Popover as OverlayPopover, PopoverAnchor, PopoverContent } from '@/components/coremetry/overlay';
import { FilterChipEditor } from '@/components/coremetry/FilterBar';
import { chipToLogFilter, LOG_FILTER_OPS, type LogFilter } from '@/lib/logFilters';
import { api } from '@/lib/api';
import { copyToClipboard } from '@/lib/clipboard';
import { absoluteShareHref } from '@/lib/shareUrl';
import { logsShareHref, type LogsShareView } from '@/lib/logsShareView'; // v0.10.1899 (5.2)
import { toast } from '@/lib/toast';
import { useT } from '@/lib/i18n';

// Share affordance — copies a link to the CURRENT filtered logs view
// (filters live in the URL; recipients sign in to Coremetry). v0.8.102:
// open to every role. v0.9.1280: the relative window is pinned to absolute
// times on the way to the clipboard (lib/shareUrl.ts) — same as ShareButton.
const LOG_SHARE_TITLE =
  'Copy a shareable link to this filtered logs view (filters are '
  + 'encoded in the URL; recipients sign in to Coremetry to open it)';

// v0.10.1854 — yeni alan süzgeci coremetry FilterChipEditor'la (LogPillEditor silindi).
const NEW_CHIP = { key: '', op: '=' as const, value: '' };
const suggestValues = (since?: string) => (key: string, q: string, signal: AbortSignal) =>
  api.logsFieldValues(key, q, 20, since, signal).then(r => r?.values ?? []);

export interface LogsAddFilterMenuProps {
  traceIdDraft: string;
  onTraceIdDraft: (v: string) => void;
  onApply: () => void;
  hasTrace: boolean;
  onToggleHasTrace: () => void;
  /** Kullanıcının çözülmüş takımı ('' = yok / özellik kapalı). */
  myTeamName: string;
  /** Takım süzgeci zaten etkin mi (etkinse Takımım önerilmez). */
  teamActive: boolean;
  onApplyMyTeam: () => void;
  onAddPill: (f: LogFilter) => void;
  since?: string;
}

export function LogsAddFilterMenu(p: LogsAddFilterMenuProps) {
  const tr = useT();
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  return (
    <OverlayPopover open={adding} onOpenChange={setAdding}>
    <PopoverAnchor asChild>
    <span className="ld-addf">
      <Button ref={anchorRef} variant="secondary" aria-haspopup="dialog" aria-expanded={open}
        title={tr('logs.dv.addFilterTitle')}
        onClick={() => { setAdding(false); setOpen(o => !o); }}>
        {tr('logs.dv.addFilter')} ▾
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="dialog"
        ariaLabel={tr('logs.dv.addFilterTitle')} width={300}>
        {/* Trace ID filter — operators paste a trace ID from a problem /
            incident and see only its log lines. Mirrors the ?traceId= URL
            param. Exact term match on trace.id (ES) / trace_id (CH). Trimmed
            + lowercased so `0xABC…` or whitespace padding still works. */}
        <label className="ld-addf-label">
          <span>{tr('logs.dv.traceId')}</span>
          <input
            placeholder="Trace ID"
            aria-label="Filter logs by trace ID"
            value={p.traceIdDraft}
            onChange={e => p.onTraceIdDraft(e.target.value.trim().toLowerCase().replace(/^0x/, ''))}
            onKeyDown={e => { if (e.key === 'Enter') { p.onApply(); setOpen(false); } }}
            title="Filter logs to a single trace. An absolute (custom) range is honoured; with a relative range the server derives the trace's own window (falls back to full retention)."
            className="mono ld-addf-trace" />
        </label>
        <div className="ld-addf-row">
          {/* v0.8.406 — "sadece trace'i olan loglar"; oto-uygular (K3: durum = active tint). */}
          <Button variant="secondary" size="sm" active={p.hasTrace}
            onClick={p.onToggleHasTrace}
            title="Show only logs correlated with a trace — every row can pivot to its trace">
            ◆ With trace
          </Button>
          {!!p.myTeamName && !p.teamActive && (
            <Button variant="secondary" size="sm" title={tr('logs.team.applyTitle')}
              onClick={() => { p.onApplyMyTeam(); setOpen(false); }}>
              {tr('logs.team.mine')}: {p.myTeamName}
            </Button>
          )}
        </div>
        <Button variant="ghost" size="sm" onClick={() => { setOpen(false); setAdding(true); }}>
          {tr('logs.dv.fieldFilter')}
        </Button>
      </Popover>
    </span>
    </PopoverAnchor>
    <PopoverContent align="start" className="w-auto p-3">
      <FilterChipEditor chip={NEW_CHIP} ops={LOG_FILTER_OPS} suggestValues={suggestValues(p.since)}
        onApply={c => { setAdding(false); if (c.key.trim()) p.onAddPill(chipToLogFilter(c)); }}
        onCancel={() => setAdding(false)} />
    </PopoverContent>
    </OverlayPopover>
  );
}

export interface LogsShareMenuProps {
  /** buildKibanaURL sonucu; null = bağlantı yok (yapılandırılmamış / desen / takım). */
  kibanaHref: string | null;
  /** v0.10.1899 (Log Search Faz 5 dilim 5.2) — göndericinin etkin görünümü:
   *  URL'de olmayan kolon / satır yüksekliği / yoğunluk / sayfa / örnek boyu /
   *  aralık linke eklenir (alıcıda aynı tablo). Verilmezse eski davranış. */
  view?: LogsShareView;
}

export function LogsShareMenu({ kibanaHref, view }: LogsShareMenuProps) {
  const tr = useT();
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const copy = async () => {
    setOpen(false);
    const now = Date.now();
    if (await copyToClipboard(view ? logsShareHref(window.location.href, now, view) : absoluteShareHref(window.location.href, now))) {
      toast.success(tr('logs.dv.linkCopied'));
    }
  };
  return (
    <>
      <Button ref={anchorRef} variant="secondary" aria-haspopup="menu" aria-expanded={open}
        onClick={() => setOpen(o => !o)}>
        {tr('logs.dv.share')} ▾
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="menu"
        ariaLabel={tr('logs.dv.share')} width={240}>
        <MenuItem onClick={() => void copy()} title={LOG_SHARE_TITLE}>{tr('logs.dv.copyLink')}</MenuItem>
        {/* v0.9.574 — Kibana derin linki: servis / trace-id / arama → KQL,
            pencere → zaman aralığı. Settings → Kibana link boşsa, desen ya da
            takım süzgeci etkinse (KQL'e çevrilemez) çizilmez. */}
        {kibanaHref && (
          <PopoverHrefItem href={kibanaHref} onSelect={() => setOpen(false)}
            title="Open the current filter slice in Elastic Discover">
            ↗ Discover in Elastic
          </PopoverHrefItem>
        )}
      </Popover>
    </>
  );
}

export interface LogsMoreMenuProps {
  onReset: () => void;
}

// v0.10.1502 — Export CSV/NDJSON tablo araç çubuğunun ⋯ menüsüne taşındı
// (LogsExportMenu, dışa aktardığı satırların hemen üstünde); sayfa ⋯'i
// sayfa düzeyi eylemleri taşır.
export function LogsMoreMenu(p: LogsMoreMenuProps) {
  const tr = useT();
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button ref={anchorRef} variant="secondary" aria-haspopup="menu" aria-expanded={open}
        aria-label={tr('logs.dv.more')} title={tr('logs.dv.more')}
        onClick={() => setOpen(o => !o)}>
        ⋯
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="menu"
        ariaLabel={tr('logs.dv.more')} width={240}>
        <MenuItem onClick={() => { setOpen(false); p.onReset(); }} title="Clear every filter and the search box">Reset</MenuItem>
      </Popover>
    </>
  );
}

export interface LogsExportMenuProps {
  /** Sayfada görünen (yüklenmiş + yerel daraltılmış) satır sayısı. */
  exportCount: number;
  onExport: (kind: 'csv' | 'ndjson') => void;
}

export function LogsExportMenu(p: LogsExportMenuProps) {
  const tr = useT();
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const n = p.exportCount.toLocaleString();
  // v0.9.302 — export what is ALREADY loaded: zero backend calls, the file
  // matches the screen (local narrow filter included). A synchronous
  // unbounded export is deliberately not offered.
  const exportTitle = p.exportCount > 0
    ? `Downloads the ${n} rows currently in the page — not the whole query. Load more first if you need more, or narrow the query.`
    : tr('logs.dv.exportNone');
  const pick = (kind: 'csv' | 'ndjson') => () => { setOpen(false); p.onExport(kind); };
  return (
    <>
      <Button ref={anchorRef} variant="secondary" size="sm" aria-haspopup="menu" aria-expanded={open}
        aria-label={tr('logs.dv.export')} title={tr('logs.dv.export')}
        onClick={() => setOpen(o => !o)}>
        ⋯
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="menu"
        ariaLabel={tr('logs.dv.export')} width={260}>
        <MenuItem disabled={p.exportCount === 0} title={exportTitle} onClick={pick('csv')}>
          Export CSV ({n})
        </MenuItem>
        <MenuItem disabled={p.exportCount === 0}
          title={p.exportCount > 0 ? 'One JSON object per line — unambiguous for bodies containing commas, quotes or newlines, and what log pipelines ingest.' : exportTitle}
          onClick={pick('ndjson')}>
          Export NDJSON ({n})
        </MenuItem>
      </Popover>
    </>
  );
}
