import { useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Field } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import { humanize, FlashBox } from './shared';
import type { PurgeResult } from '@/lib/types';

// ── Danger zone — Purge telemetry data ──────────────────────────────────────
//
// A "factory reset" of observability DATA: every span / log / metric and the
// aggregation, topology, trace and derived-analysis tables are TRUNCATEd from
// ClickHouse, while ALL configuration is preserved (LDAP/system_settings,
// alert rules, saved views, users, dashboards, monitors, status page,
// notification channels — and the audit_log, which records the purge itself).
// Admin-only (the whole Settings area is admin-gated); the backend re-checks
// the role on the route. Two-step type-to-confirm so it can't fire by accident.
const CONFIRM = 'purge all telemetry';

// Faz 2 dilim 1 R14 — inline stil → Tailwind utility; ui/Button → shadcn
// (`destructive`); onay cümlesi kutusu coremetry Field (etiket bağlı).
// Type-to-confirm kapısı, sıra ve metinler aynı.
const LIST = 'm-0 pl-[18px] text-[length:var(--fs-sm)] leading-relaxed text-(--text2)';

const DELETED = [
  'Spans, logs, metrics, profiles (raw signals)',
  'RED / topology / trace rollup materialized views',
  'Auto-generated analysis (anomalies, root-cause, AI usage, monitor results)',
];
const KEPT = [
  'LDAP / SSO / every system setting',
  'Alert rules, saved views, dashboards',
  'Users, custom roles, notification channels',
  'Monitors, runbooks, SLOs, status page, maintenance windows',
  'Your problems, incidents, exceptions, events & runbook history',
  'The audit log — it records this purge',
];

export function DangerZoneTab() {
  const [arming, setArming] = useState(false);
  const [phrase, setPhrase] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const onPurge = async () => {
    setBusy(true); setMsg(null);
    try {
      const res: PurgeResult = await api.purgeTelemetry();
      const parts = [`Purged ${res.tablesPurged.length} table(s)`];
      if (res.skipped?.length) parts.push(`${res.skipped.length} skipped (absent on this install)`);
      if (res.errors?.length) parts.push(`${res.errors.length} error(s): ${res.errors.join('; ')}`);
      setMsg({ kind: res.errors?.length ? 'err' : 'ok', text: parts.join(' · ') });
      setArming(false); setPhrase('');
    } catch (e) {
      setMsg({ kind: 'err', text: humanize(e) });
    } finally { setBusy(false); }
  };

  return (
    <div className="card max-w-[760px] p-4">
      <h3 className="mb-3 mt-0">⚠ Danger zone — Purge telemetry data</h3>
      <div className="mb-4 text-[length:var(--fs-sm)] leading-normal text-(--text3)">
        A factory reset of observability data: every span, log and metric, plus
        the aggregation / topology / trace views, are emptied from ClickHouse.
        <strong> All configuration is preserved.</strong> This cannot be undone.
      </div>

      <div className="mb-[18px] flex flex-wrap gap-6">
        <div className="min-w-[240px] flex-1">
          <div className="mb-1.5 font-semibold">Deleted</div>
          <ul className={LIST}>
            {DELETED.map(d => <li key={d}>{d}</li>)}
          </ul>
        </div>
        <div className="min-w-[240px] flex-1">
          <div className="mb-1.5 font-semibold">Kept</div>
          <ul className={LIST}>
            {KEPT.map(k => <li key={k}>{k}</li>)}
          </ul>
        </div>
      </div>

      {!arming ? (
        <Button variant="destructive" onClick={() => { setArming(true); setMsg(null); }}>
          Purge telemetry data…
        </Button>
      ) : (
        <div className="border-t border-(--divider) pt-3.5">
          <Field label={<>Type <code>{CONFIRM}</code> to confirm:</>}
            className="mb-3 max-w-[280px]"
            value={phrase}
            onChange={e => setPhrase(e.target.value)}
            placeholder={CONFIRM}
            autoFocus />
          {/* v0.9.1007 (M5/O8) — K5 ile K6'nın kesiştiği tek nokta:
              YIKICI ONAY EN SOLDAYDI, yani kas hafızasının "iptal"
              beklediği yerde. Type-to-confirm kapısı riski büyük ölçüde
              emiyordu (bu yüzden kritik değildi) ama sıra yine de
              tersti. Onay `confirm` yuvasına, iptal soluna geçti. */}
          <ActionRow
            secondary={(
              <Button variant="ghost" disabled={busy} onClick={() => { setArming(false); setPhrase(''); }}>
                Cancel
              </Button>
            )}
            confirm={(
              <Button variant="destructive" disabled={phrase.trim() !== CONFIRM} onClick={() => { void onPurge(); }} loading={busy}>
                Confirm — delete all telemetry
              </Button>
            )} />
        </div>
      )}

      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
    </div>
  );
}
