import { useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { humanize, FlashBox } from './shared';

// Faz 2 dilim 1 R14 — inline stil → Tailwind utility; ui/Button → shadcn;
// Preview/Upload satırı ActionRow (birincil sağda). Dosya girdisi ve iki
// kip radyosu native (plan Q3 muafiyeti). Metinler ve akış aynı.
const NOTE = 'text-[length:var(--fs-sm)] leading-normal text-(--text3)';
const HINT = 'text-[length:var(--fs-xs)] leading-snug text-(--text3)';

// ── Backup / Restore tab ────────────────────────────────────────────────────
//
// Operator-set state lives in a small set of ReplacingMergeTree
// tables (system_settings, notification_channels, alert_rules,
// dashboards, saved_views, slos, maintenance_windows, monitors,
// service_contracts, status_page_*). The backend dumps the whole
// catalogue to JSON via /api/admin/config/export and replays via
// /api/admin/config/import.
//
// Two use cases drive the UI shape here:
//   1. **Clean install** — operator runs COREMETRY_CH_RESET_SCHEMA=1,
//      then comes here and uploads the JSON from before the reset.
//   2. **Promotion between environments** — export dev/staging
//      config, import to prod (or vice-versa) to keep alert rules
//      and dashboards in lock-step.
//
// The import has two modes:
//   - **merge** (default) — insert rows verbatim with their stored
//     version columns; ReplacingMergeTree picks the newest
//     (per ORDER BY key). Local edits made AFTER the export win.
//   - **replace** — bump every row's version to now() so imported
//     state always wins. Opt-in, since it can shadow local edits.
type DiffResult = {
  tables: Record<string, {
    willAdd: string[]; willOverwrite: string[];
    unchanged: number; onlyInDB: number;
  }>;
  exportedAt: string;
  coremetryVersion?: string;
};

export function BackupTab() {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [mode, setMode] = useState<'merge' | 'replace'>('merge');
  const [file, setFile] = useState<File | null>(null);
  // v0.5.398 — diff preview before import. Operator picks a file,
  // clicks "Preview diff" first, sees per-table {willAdd /
  // willOverwrite / unchanged / onlyInDB} counts so they can
  // confirm the import scope is what they expected. Clears on
  // file change so a stale preview never lingers next to a new
  // file selection.
  const [diff, setDiff] = useState<DiffResult | null>(null);

  const onExport = async () => {
    setMsg(null); setBusy(true);
    try {
      await api.exportConfig();
      setMsg({ kind: 'ok', text: 'Export downloaded.' });
    } catch (e) {
      setMsg({ kind: 'err', text: humanize(e) });
    } finally { setBusy(false); }
  };

  const onImport = async () => {
    if (!file) return;
    setMsg(null); setBusy(true);
    try {
      const r = await api.importConfig(file, mode);
      const tables = Object.entries(r.tables).map(([t, n]) => `${t}=${n}`).join(', ');
      const skipped = r.skippedUnknown && r.skippedUnknown.length
        ? ` (skipped unknown: ${r.skippedUnknown.join(', ')})`
        : '';
      setMsg({
        kind: 'ok',
        text: `Imported ${r.rows} rows · mode=${r.mode} · ${tables}${skipped}`,
      });
      setFile(null);
      setDiff(null);
    } catch (e) {
      setMsg({ kind: 'err', text: humanize(e) });
    } finally { setBusy(false); }
  };

  const onPreviewDiff = async () => {
    if (!file) return;
    setMsg(null); setBusy(true);
    try {
      const r = await api.diffConfig(file);
      setDiff(r as DiffResult);
    } catch (e) {
      setMsg({ kind: 'err', text: humanize(e) });
    } finally { setBusy(false); }
  };

  // Reset preview when the operator picks a different file so
  // the displayed diff never lies about the file in scope.
  const onFileChange = (f: File | null) => {
    setFile(f);
    setDiff(null);
    setMsg(null);
  };

  return (
    <div className="card max-w-[760px] p-4">
      <h3 className="mb-3 mt-0">Backup / Restore</h3>
      <div className={cn(NOTE, 'mb-4')}>
        Export every operator-set table (settings, channels, alert
        rules, dashboards, saved views, SLOs, maintenance windows,
        monitors, status page) to a JSON file. Replay later to
        recover from a schema reset, promote config between
        environments, or share dashboards with another install.
        Excludes runtime data (spans, problems, incidents, audit
        log) — those are rebuilt from ingest.
      </div>

      <div className="mb-5 border-t border-(--divider) pt-4">
        <div className="mb-2 font-semibold">Export</div>
        <Button onClick={() => { void onExport(); }} loading={busy}>
          Download config (JSON)
        </Button>
        <div className={cn(HINT, 'mt-1.5')}>
          File contains every row from system_settings, alert_rules,
          dashboards, saved_views, notification_channels, slos,
          maintenance_windows, anomaly_silences, monitors,
          service_metadata, service_contracts, status_page_*.
          Includes secrets in their stored form (AI keys, SMTP
          passwords, LDAP bind passwords, Tempo tokens, the SSO / OIDC
          client secret) — treat the file like a secret.
        </div>
      </div>

      <div className="border-t border-(--divider) pt-4">
        <div className="mb-2 font-semibold">Restore</div>
        {/* Yerel dosya girdisi ve iki kip radyosu bilinçli olarak native (plan Q3). */}
        <input
          type="file"
          accept="application/json,.json"
          aria-label="Config JSON file"
          onChange={e => onFileChange(e.target.files?.[0] ?? null)}
          disabled={busy}
          className="mb-3 block text-[length:var(--fs-sm)]"
        />
        <div role="radiogroup" aria-label="Import mode" className="mb-4 flex flex-col gap-2">
          <label className="flex items-start gap-1.5 text-[length:var(--fs-sm)]">
            <input
              type="radio"
              name="iox-mode"
              checked={mode === 'merge'}
              onChange={() => setMode('merge')}
              disabled={busy}
              className="mt-0.5 accent-(--accent)"
            />
            <span>
              <strong>Merge</strong> — preserve local edits made after the export.
              {' '}Imported rows only win where no newer local edit exists.
            </span>
          </label>
          <label className="flex items-start gap-1.5 text-[length:var(--fs-sm)]">
            <input
              type="radio"
              name="iox-mode"
              checked={mode === 'replace'}
              onChange={() => setMode('replace')}
              disabled={busy}
              className="mt-0.5 accent-(--accent)"
            />
            <span>
              <strong>Replace</strong> — imported rows always win, even
              over newer local edits. Use this for clean-install
              restore.
            </span>
          </label>
        </div>
        {/* v0.9.1006 (M4/O6) — kuru çalıştırma İKİNCİL. Eskiden bu ve
            "Upload + apply" iki ÖZDEŞ dolu mavi butondu ve hangisinin
            yazdığı ayırt edilemiyordu; üstelik bu buton varyantını
            yazmadığı için ihlal kaynakta görünmüyordu (M3).
            Faz 2 dilim 1 R14 — ActionRow: birincil EN SAĞDA (D4). */}
        <ActionRow
          secondary={
            <Button variant="outline"
              disabled={!file}
              onClick={() => { void onPreviewDiff(); }}
              title="Dry-run: shows what would be added / overwritten / left alone, no writes." loading={busy}>
              Preview diff
            </Button>
          }
          confirm={
            <Button
              disabled={!file}
              onClick={() => { void onImport(); }}
              loading={busy}>
              Upload + apply
            </Button>
          } />
        <div className={cn(HINT, 'mt-1.5')}>
          Live settings hot-reload after the import — no restart
          needed. Unknown tables in the file are skipped (forward-
          compat). Truncate is never performed; import always
          inserts on top of what's there.
        </div>

        {/* v0.5.416 — dry-run diff result panel. Per-table:
            +willAdd  · ~willOverwrite · =unchanged · DB-only.
            Operator confirms scope before triggering import. */}
        {diff && (
          <div className="mt-3.5 rounded-md border border-(--border) bg-(--bg1) px-3 py-2.5 text-[length:var(--fs-sm)]">
            <div className="mb-2 flex items-center justify-between text-[length:var(--fs-xs)] font-bold uppercase tracking-[0.4px] text-(--text2)">
              <span>Diff preview</span>
              <span className="font-normal normal-case tracking-normal text-(--text3)">
                from {diff.exportedAt || '?'}
                {diff.coremetryVersion && ` · ${diff.coremetryVersion}`}
              </span>
            </div>
            <div className="flex flex-col gap-1">
              {Object.entries(diff.tables).length === 0 && (
                <span className="text-(--text3)">No tables in this file matched the catalogue.</span>
              )}
              {Object.entries(diff.tables).map(([t, d]) => (
                <div key={t} className="grid grid-cols-[180px_repeat(4,1fr)] gap-2 border-b border-(--divider) py-[3px] font-mono text-[length:var(--fs-xs)] tabular-nums">
                  <span className="font-semibold text-(--text)">{t}</span>
                  {/* v0.10.929 (K5) — "yeni" bir değişiklik, iyileşme değil: vurgu (--accent2), yeşil değil. */}
                  <span className={d.willAdd.length > 0 ? 'text-(--accent2)' : 'text-(--text3)'}
                    title={d.willAdd.length > 0 ? d.willAdd.slice(0, 20).join('\n') : 'no new rows'}>
                    +{d.willAdd.length} new
                  </span>
                  <span className={d.willOverwrite.length > 0 ? 'text-(--warn)' : 'text-(--text3)'}
                    title={d.willOverwrite.length > 0 ? d.willOverwrite.slice(0, 20).join('\n') : 'no overwrites'}>
                    ~{d.willOverwrite.length} overwrite
                  </span>
                  <span className="text-(--text3)">
                    ={d.unchanged} same
                  </span>
                  <span className="text-(--text3)"
                    title="Rows present in DB but not in the file. Import never deletes — these stay.">
                    {d.onlyInDB} DB-only
                  </span>
                </div>
              ))}
            </div>
            <div className={cn(HINT, 'mt-2')}>
              <strong>+new</strong>: rows in file that don't exist locally · <strong>~overwrite</strong>: rows whose version differs (merge mode keeps newer; replace mode forces file) · <strong>=same</strong>: identical · <strong>DB-only</strong>: kept as-is, import never deletes.
            </div>
          </div>
        )}
      </div>

      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
    </div>
  );
}
