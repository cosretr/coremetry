// MLPrecisionShadowSettings — v0.10.2486 (precision programı B.2): Settings ›
// Anomaly › ML, gürültü kapılarının yanında otomatik gölge kuralı ayarı
// (`ml.precisionShadow`). Admin ekranı: komşuları gibi çevrilmez. Kayıt
// mevcut PUT /api/admin/ml yolundan (ml.config audit'i); `on`a geçiş
// MLSubsection'daki onay adımını ister (sunucu da 409 ile ister).
import { useState } from 'react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField } from '@/components/coremetry/Field';
import type { MLConfig, MLPrecisionMode, MLPrecisionShadow, MLPrecisionView } from '@/lib/types';
import { count } from '@/lib/format/number';
import { percent } from '@/lib/format/percent';
import { fedLine, precisionStatusOf } from '@/components/coremetry/anomalies/mlPrecisionShadow';

/** Sunucu varsayılanı (chstore.DefaultMLPrecisionShadow) — blob eski sunucudan gelirse. */
export const PRECISION_DEFAULTS: MLPrecisionShadow = {
  mode: 'shadow', minLabels: 20, demoteUpper: 0.5, restoreLower: 0.6, windowDays: 30, maxPerDay: 1, maxShare: 0.25, pins: {},
};

const NOTE = 'text-[length:var(--fs-xs)] leading-normal text-(--text3)';

/** Dedektör satırının rozeti (Settings › ML dedektör listesi). Açık durumda rozet yok. */
export function PrecisionBadge({ view, detector }: { view: MLPrecisionView | undefined; detector: string }) {
  const st = precisionStatusOf(view, detector, 'tr');
  if (!st || st.status === 'on') return null;
  return <Badge variant="outline" className="ml-1.5" title={st.title} data-testid={`ml-precision-badge-${detector}`}>{st.text}</Badge>;
}

/** `on` onay penceresinin gövdesi: düşerdi listesi + kesilecek gerçek Problem sayısı. */
export function PrecisionOnConfirmBody({ view, labelOf }: { view: MLPrecisionView | undefined; labelOf: (id: string) => string }) {
  const list = view?.wouldDemote ?? [];
  const real = list.reduce((a, d) => a + (view?.fed?.[d]?.real ?? 0), 0);
  return (
    <div data-testid="ml-precision-confirm">
      <p className="m-0">Kural açılınca listedeki dedektörlerin olayları yazılmaya devam eder ama Problem açmaz, Problem önceliğini yükseltmez ve bildirim göndermez.</p>
      {list.length === 0
        ? <p className="mt-2 mb-0">Şu an “düşerdi” listesi boş; kural ilk değerlendirmede frenlere (günde 1, %25 tavanı) göre düşürür.</p>
        : (
          <ul className="mt-2 mb-0 pl-4">
            {list.map(d => <li key={d}>{fedLine(labelOf(d), view?.fed?.[d], 'tr')}</li>)}
          </ul>
        )}
      <p className={`mt-2 mb-0 font-semibold ${real > 0 ? 'text-[color:var(--warn)]' : ''}`}>
        Kesilecek onaylı gerçek Problem: {count(real, 'tr')}{real > 0 ? ' — 0 değilken on önerilmez (belgenin “gölgede %100 recall” kuralı).' : ''}
      </p>
    </div>
  );
}

export function MLPrecisionShadowSettings({ cfg, setCfg, view, detectors, labelOf }: {
  cfg: MLConfig; setCfg: (c: MLConfig) => void; view: MLPrecisionView | undefined;
  detectors: string[]; labelOf: (id: string) => string;
}) {
  const p = { ...PRECISION_DEFAULTS, ...cfg.precisionShadow, pins: cfg.precisionShadow?.pins ?? {} };
  const set = (patch: Partial<MLPrecisionShadow>) => setCfg({ ...cfg, precisionShadow: { ...p, ...patch } });
  const [pinDet, setPinDet] = useState('');
  const [pinVal, setPinVal] = useState<'on' | 'shadow'>('on');
  const num = (label: string, key: 'minLabels' | 'demoteUpper' | 'restoreLower' | 'windowDays' | 'maxPerDay' | 'maxShare', hint: string, step = 1) => (
    <Field type="number" label={label} hint={hint} controlClassName="max-w-[180px]" step={step} small
      value={String(p[key])} onChange={e => set({ [key]: Number(e.target.value) })} />
  );
  const pins = Object.entries(p.pins);
  return (
    <div data-testid="ml-precision-settings" className="mb-3 rounded-md border border-border px-3 py-2">
      <SelectField controlClassName="max-w-[320px]" label="Otomatik gölge kuralı (dedektör karnesi)" disabled={!cfg.enabled}
        value={p.mode} onChange={e => set({ mode: e.target.value as MLPrecisionMode })}
        hint={`Günde bir, son ${count(p.windowDays, 'tr')} günün olay oyları ve onaylı vakalarıyla: en az ${count(p.minLabels, 'tr')} etiket, en az 2 kişi ve 5 gün, tek servis payı en çok %60 iken Wilson üst sınırı ${percent(p.demoteUpper, 'tr', { from: 'ratio', digits: 0 })} altında kalan ML dedektörü gölgeye düşer; gölgede en az 7 gün ve en az 10 yeni etiketle alt sınır ${percent(p.restoreLower, 'tr', { from: 'ratio', digits: 0 })} ve üstüne çıkınca döner. Günde en çok ${count(p.maxPerDay, 'tr')} düşüş, açık ML dedektörlerinin en çok ${percent(p.maxShare, 'tr', { from: 'ratio', digits: 0 })}'i; son 2 saatte açık P1'e bağlı dedektör o gün düşmez; dönüşten sonra 14 gün yeniden düşmez. Etki kuralı dedektörleri, consumer lag ve ML dışı aileler muaftır. shadow: yalnız "düşerdi" kaydı (davranış değişmez). on: gölgedeki dedektörün olayı Problem açmaz ve bildirim göndermez — açık onay ister.`}>
        <option value="shadow">shadow — yalnız rapor (varsayılan)</option>
        <option value="on">on — uygula (onay ister)</option>
        <option value="off">off — değerlendirme</option>
      </SelectField>
      <div className="grid gap-3 md:grid-cols-3">
        {num('En az etiket', 'minLabels', 'Kurala girebilen etiket (10–1000)')}
        {num('Düşme: Wilson üst <', 'demoteUpper', 'Oran (0–0,9)', 0.05)}
        {num('Dönüş: Wilson alt ≥', 'restoreLower', 'Oran; düşme eşiğinden küçük olamaz', 0.05)}
        {num('Pencere (gün)', 'windowDays', '7–90')}
        {num('Günde en çok düşüş', 'maxPerDay', '1–10')}
        {num('En çok gölgede payı', 'maxShare', 'Açık ML dedektörlerine oran (0–1)', 0.05)}
      </div>
      <h4 className="mt-3 mb-1 text-[length:var(--fs-sm)] font-semibold">Sabitlemeler</h4>
      <p className={NOTE}>on: kural bu dedektörü hiç düşürmez (gölgedeyse döner). shadow: operatör gölgeye alır — yalnız kip on iken etkili. Muaf dedektör sabitlemeyle de gölgeye girmez.</p>
      {pins.length > 0 && (
        <ul className="my-1 pl-0 list-none" data-testid="ml-precision-pins">
          {pins.map(([d, v]) => (
            <li key={d} className="flex items-center gap-2 text-[length:var(--fs-sm)]">
              <span>{labelOf(d)} · {v}</span>
              <Button size="sm" variant="ghost" onClick={() => { const next = { ...p.pins }; delete next[d]; set({ pins: next }); }}>Kaldır</Button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap items-end gap-2">
        <SelectField small label="Dedektör" controlClassName="max-w-[240px]" value={pinDet} onChange={e => setPinDet(e.target.value)}>
          <option value="">—</option>
          {detectors.map(d => <option key={d} value={d}>{labelOf(d)}</option>)}
        </SelectField>
        <SelectField small label="Sabitle" controlClassName="max-w-[140px]" value={pinVal} onChange={e => setPinVal(e.target.value as 'on' | 'shadow')}>
          <option value="on">on</option>
          <option value="shadow">shadow</option>
        </SelectField>
        <Button size="sm" variant="outline" disabled={!pinDet} onClick={() => { set({ pins: { ...p.pins, [pinDet]: pinVal } }); setPinDet(''); }}>Ekle</Button>
      </div>
      {view && (view.wouldDemote?.length ?? 0) > 0 && (
        <p className={`${NOTE} mt-2`} data-testid="ml-precision-would">
          {view.mode === 'on' ? 'Gölgede' : 'Düşerdi'}: {(view.wouldDemote ?? []).map(labelOf).join(', ')}
        </p>
      )}
    </div>
  );
}
