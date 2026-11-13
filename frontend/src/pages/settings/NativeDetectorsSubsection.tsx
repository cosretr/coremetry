// NativeDetectorsSubsection — Settings › Anomaly › "Yerleşik dedektörler"
// (v0.10.1746, operatör: bu sürümde hepsi KAPALI, tek tıkla geri). Alarm
// kuralları, Influx/Oracle akışları, Elastic ML poller, exception akışı,
// slo_burn, heartbeat, self-health ve rollout korelasyonu bu listede YOK —
// hep açık. Kaydet → audit (eski/yeni), bir sonraki tikte geçerli.
import { useEffect, useState } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import { FlashBox, humanize } from './shared';

export const NATIVE_DETECTORS: { id: string; label: string; hint: string }[] = [
  { id: 'log_pattern', label: 'Log deseni (regex new/spike)', hint: 'Worker 60 sn, ES _msearch; ~20 sabit desen' },
  { id: 'trace_op', label: 'Trace op anomalileri', hint: 'new_error / error_spike, operation_summary_5m' },
  { id: 'metric_anomaly', label: 'Metrik anomali (hata / gecikme / trafik)', hint: 'CH MV mevsimsel taban; anomaliden Problem\'e terfi ve error_ramp bununla birlikte' },
  { id: 'behavior', label: 'Davranış motoru', hint: '28 günlük MV taraması' },
  { id: 'service_slowdown', label: 'Servis yavaşlaması', hint: 'Evaluator yerleşiği' },
  { id: 'db_health', label: 'DB sağlığı', hint: 'Evaluator yerleşiği' },
  { id: 'db_capacity', label: 'DB kapasitesi', hint: 'Evaluator yerleşiği' },
  { id: 'db_slow_statement', label: 'Yavaş SQL', hint: 'Evaluator yerleşiği' },
  { id: 'runtime_pods', label: 'Runtime (pod + GC)', hint: 'Evaluator yerleşiği; runtime_vm aynı anahtar' },
];

export function NativeDetectorsSubsection() {
  const [cfg, setCfg] = useState<Record<string, boolean> | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    api.getNativeDetectors().then(r => { setCfg(r.detectors); }).catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);

  const save = async () => {
    if (!cfg) return;
    setBusy(true); setFlash(null);
    try {
      const r = await api.putNativeDetectors({ detectors: cfg });
      setCfg(r.detectors);
      setFlash({ kind: 'ok', text: 'Kaydedildi — bir sonraki tikte geçerli, restart gerekmez.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const offAll = async () => {
    setBusy(true); setFlash(null);
    try {
      const r = await api.offAllNativeDetectors();
      setCfg(r.detectors);
      setFlash({ kind: 'ok', text: 'Eski dedektörlerin hepsi kapatıldı. Alarm kuralları, exception akışı, Oracle/Influx, Elastic ML, SLO, heartbeat, self-health etkilenmedi.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div data-testid="native-detectors-settings">
      <h2 className="mb-1.5 text-[length:var(--fs-lg)] font-semibold">Yerleşik dedektörler</h2>
      <p className="mb-[18px] text-[length:var(--fs-sm)] leading-[1.55] text-(--text2)">
        v0.10.1746 ile hepsi <b>kapalı</b> gelir; buradan tek tıkla açılır. Alarm kuralları, Influx/Oracle
        akışları, Elastic ML, exception akışı, SLO yanığı, heartbeat ve self-health bu listede değildir, hep açıktır.
      </p>
      {!cfg ? (
        flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />
      ) : (
        <>
          <div className="grid gap-2 md:grid-cols-2">
            {NATIVE_DETECTORS.map(d => (
              <CheckboxField key={d.id} checked={cfg[d.id] === true} label={d.label} hint={d.hint}
                onCheckedChange={v => setCfg({ ...cfg, [d.id]: v })} />
            ))}
          </div>
          <div className="mt-[18px]">
            <ActionRow confirm={<Button onClick={save} loading={busy}>Save</Button>}
              secondary={<Button variant="outline" onClick={() => { void offAll(); }} disabled={busy} data-testid="native-off-all">Eski dedektörlerin hepsini kapat</Button>} />
            {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
          </div>
        </>
      )}
    </div>
  );
}
