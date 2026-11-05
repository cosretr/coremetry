// MLBaselineBackfill — Settings › Anomaly › ML "Baseline geçmişini doldur"
// (v0.10.1815, öğrenme döngüsü Faz 1 dilim 1.4). Geçmiş doldurma ASLA
// otomatik değil: gün + sinyal seç → tahmin (ES/VM/CH sorgu sayısı, süre) →
// açık onay → başlat; ilerleme sinyal başına (gün X/Y, satır, hata), iptal.
// Viewer ilerlemeyi salt-okur görür (düğmeler yalnız admin). Yoklama yalnız
// koşu sürerken 10 sn (useMLBaselineBackfill; RQ gizli sekmede durur).
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { useConfirm } from '@/components/coremetry/Confirm';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import { keys, useMLBaselineBackfill, backfillActive } from '@/lib/queries';
import { BACKFILL_SIGNAL_TR, backfillEstimateText, backfillProgress, backfillSkippedLines } from '@/lib/mlBaselineBackfill';
import type { MLBaselineBackfillPlan } from '@/lib/types';
import { FlashBox, humanize } from './shared';

const H3 = 'mt-4 mb-1 text-[length:var(--fs-md)] font-semibold';
const NOTE = 'text-[length:var(--fs-xs)] leading-normal text-(--text3)';
const SIGNALS = ['log', 'trace', 'metric'];

export function MLBaselineBackfill() {
  const isAdmin = useAuthRole() === 'admin';
  const qc = useQueryClient();
  const confirm = useConfirm();
  const q = useMLBaselineBackfill();
  const [days, setDays] = useState(56);
  const [signals, setSignals] = useState<string[]>(['log']);
  const [plan, setPlan] = useState<MLBaselineBackfillPlan | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const active = backfillActive(q.data);
  const progress = backfillProgress(q.data);

  const body = () => ({ days: Math.min(56, Math.max(1, Math.round(days) || 1)), signals });
  const estimate = async () => {
    setBusy(true); setFlash(null);
    try { setPlan((await api.mlBaselineBackfillEstimate(body())).plan); } catch (err) { setFlash({ kind: 'err', text: humanize(err) }); } finally { setBusy(false); }
  };
  const start = async () => {
    if (!plan) return;
    const ok = await confirm({
      title: 'Baseline geçmişi doldurulsun mu?',
      body: <>{plan.days} gün · {plan.signals.map(s => BACKFILL_SIGNAL_TR[s] ?? s).join(', ')}: {backfillEstimateText(plan)}. Koşu ML lider pod&apos;unda, hız sınırlı; iptal edilebilir, yarıda kalırsa kaldığı günden sürer.</>,
      confirmLabel: 'Doldurmayı başlat',
    });
    if (!ok) return;
    setBusy(true); setFlash(null);
    try {
      await api.mlBaselineBackfillStart(body());
      setFlash({ kind: 'ok', text: 'Sıraya alındı — ML lideri 30 sn içinde başlatır.' });
      setPlan(null);
      await qc.invalidateQueries({ queryKey: keys.anomalies.mlBackfill });
    } catch (err) { setFlash({ kind: 'err', text: humanize(err) }); } finally { setBusy(false); }
  };
  const cancel = async () => {
    setBusy(true); setFlash(null);
    try { await api.mlBaselineBackfillCancel(); await qc.invalidateQueries({ queryKey: keys.anomalies.mlBackfill }); } catch (err) { setFlash({ kind: 'err', text: humanize(err) }); } finally { setBusy(false); }
  };

  return (
    <div data-testid="ml-baseline-backfill">
      <h3 className={H3}>Baseline geçmişini doldur</h3>
      <p className={NOTE}>
        Tek seferlik, elle: ml_baseline_obs için 56 güne kadar geçmiş gözlem. Kaynak canlı dedektörün kaynağıdır (log → Elasticsearch,
        trace/metrik → VictoriaMetrics); ml_series ve tespit DEĞİŞMEZ. Önce tahmin, sonra onay.
      </p>
      {q.isLoading ? <Spinner /> : q.isError ? <FlashBox kind="err">{humanize(q.error)}</FlashBox> : progress ? (
        <div className="mt-2 flex flex-col gap-0.5 text-xs" data-testid="ml-backfill-progress">
          <div className="font-medium">{progress.head}</div>
          {progress.lines.map(l => <div key={l.signal} className={l.warn ? 'text-[color:var(--warn)]' : 'text-muted-foreground'}>{l.text}</div>)}
        </div>
      ) : <p className={NOTE}>Henüz geçmiş doldurma istenmedi.</p>}
      {isAdmin && (active ? (
        <div className="mt-2"><Button variant="outline" size="sm" onClick={cancel} disabled={busy || q.data?.request?.cancel === true}>İptal et</Button></div>
      ) : (
        <div className="mt-2 grid gap-2">
          <Field type="number" label="Gün" hint="1–56; dünden geriye, en yeni gün önce" controlClassName="max-w-[120px]" min={1} max={56}
            value={String(days)} onChange={e => { setDays(Number(e.target.value)); setPlan(null); }} />
          <div className="flex flex-wrap gap-4">
            {SIGNALS.map(s => (
              <CheckboxField key={s} label={BACKFILL_SIGNAL_TR[s]} checked={signals.includes(s)}
                onCheckedChange={v => { setSignals(v ? [...signals, s] : signals.filter(x => x !== s)); setPlan(null); }} />
            ))}
          </div>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={estimate} disabled={busy || signals.length === 0}>Tahmin et</Button>
            <Button size="sm" onClick={start} disabled={busy || !plan || plan.es + plan.vm === 0}>Başlat…</Button>
          </div>
          {plan && (
            <div className="text-xs" data-testid="ml-backfill-plan">
              <div>{backfillEstimateText(plan)}</div>
              {backfillSkippedLines(plan).map(l => <div key={l} className="text-muted-foreground">{l}</div>)}
              {plan.notCovered.map(l => <div key={l} className="text-muted-foreground">Kapsam dışı: {l}</div>)}
            </div>
          )}
        </div>
      ))}
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
    </div>
  );
}
