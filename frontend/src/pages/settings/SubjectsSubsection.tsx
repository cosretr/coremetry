import type { AnomalySubjectKindConfig, AnomalySubjectsConfig } from '@/lib/types';
import { Field, SelectField, CheckboxField } from '@/components/coremetry/Field';
import { cn } from '@/lib/cn';

// SubjectsSubsection — v0.10.1790 (Davis paritesi FAZ 1.2): anomali tabanının
// özne türleri (anomaly_sensitivity.subjects) + mevsimsel gün sınıfının saat
// dilimi (seasonalTimezone). Dedektör hassasiyeti bölümü, aynı Kaydet.
//
// Üç tür de varsayılan KAPALI: özneler FAZ 1.5 eval kapısını geçmeden
// açılmaz. Servis düzeyi her zaman açık ve burada yok. Dilim seçimi kapalı
// küme (İstanbul / UTC + kayıtlı başka bir ad); sunucu bilinmeyen adı reddeder.

const SEP = 'mt-1 border-t border-(--border) pt-4';
const NOTE = 'mt-1 text-[length:var(--fs-xs)] leading-normal text-(--text3)';

export const SUBJECT_DEFAULT: AnomalySubjectKindConfig = { enabled: false, topN: 20, maxSeriesPerTick: 2000 };
export const SEASONAL_TZ_DEFAULT = 'Europe/Istanbul';

const KINDS: { k: keyof AnomalySubjectsConfig; label: string; help: string }[] = [
  { k: 'endpoint', label: 'Endpoint (HTTP rota)',
    help: 'VictoriaMetrics http_route histogramından servis başına en yoğun rotalar. Rota etiketi olmayan servis servis düzeyinde kalır.' },
  { k: 'db', label: 'Veritabanı instance',
    help: 'Mevcut veritabanı özetlerinden servis başına en yoğun instance’lar.' },
  { k: 'queue', label: 'Kuyruk',
    help: 'Mevcut mesajlaşma özetlerinden servis başına en yoğun kuyruklar.' },
];

export function withSubjectDefaults(v?: Partial<AnomalySubjectsConfig>): AnomalySubjectsConfig {
  return {
    endpoint: { ...SUBJECT_DEFAULT, ...v?.endpoint },
    db: { ...SUBJECT_DEFAULT, ...v?.db },
    queue: { ...SUBJECT_DEFAULT, ...v?.queue },
  };
}

export function SubjectsSubsection({ value, timezone, onChange, onTimezoneChange }: {
  value?: AnomalySubjectsConfig;
  timezone?: string;
  onChange: (v: AnomalySubjectsConfig) => void;
  onTimezoneChange: (tz: string) => void;
}) {
  const v = withSubjectDefaults(value);
  const tz = timezone?.trim() || SEASONAL_TZ_DEFAULT;
  const zones = [SEASONAL_TZ_DEFAULT, 'UTC'];
  if (!zones.includes(tz)) zones.push(tz);
  const set = (k: keyof AnomalySubjectsConfig, patch: Partial<AnomalySubjectKindConfig>) =>
    onChange({ ...v, [k]: { ...v[k], ...patch } });
  return (
    <div className={SEP}>
      <div className="text-[length:var(--fs-md)] text-(--text)">Özneler</div>
      <div className={NOTE}>
        Anomali tabanı servisin yanında endpoint, veritabanı ve kuyruk düzeyinde de kurulabilir.
        Üçü de varsayılan kapalı: değerlendirme seti geçmeden açılmaları önerilmez.
      </div>
      <div className="mt-2.5 flex flex-col gap-3">
        {KINDS.map(({ k, label, help }) => {
          const kv = v[k];
          return (
            <div key={k}>
              <CheckboxField aria-label={`Özne: ${label}`} checked={kv.enabled}
                onCheckedChange={c => set(k, { enabled: c })}
                label={label} hint={help} />
              <div className="mt-2 flex flex-wrap gap-4 pl-6">
                <Field type="number" className="w-auto" controlClassName="max-w-[140px]"
                  label="Servis başına en çok" min={1} max={200} step={1}
                  aria-label={`${label}: servis başına en çok`} disabled={!kv.enabled}
                  value={kv.topN} onChange={e => set(k, { topN: Number(e.target.value) })} />
                <Field type="number" className="w-auto" controlClassName="max-w-[140px]"
                  label="Tik başına seri tavanı" min={1} max={50000} step={100}
                  aria-label={`${label}: tik başına seri tavanı`} disabled={!kv.enabled}
                  value={kv.maxSeriesPerTick} onChange={e => set(k, { maxSeriesPerTick: Number(e.target.value) })} />
              </div>
            </div>
          );
        })}
      </div>
      <div className="mt-3 max-w-[420px]">
        <SelectField label="Mevsimsel taban saat dilimi" aria-label="Mevsimsel taban saat dilimi"
          value={tz} onChange={e => onTimezoneChange(e.target.value)}
          hint={<>
            Hafta içi / cumartesi / pazar sınıfı bu dilimin yerel gününe göre ayrılır. Varsayılan
            Europe/Istanbul. Değiştirildiğinde mevsimsel taban bir kez kayar.
          </>}>
          {zones.map(z => <option key={z} value={z}>{z}</option>)}
        </SelectField>
      </div>
      <div className={cn(NOTE, 'mt-2')}>
        Tavan aşılırsa o özne türü o tik atlanır (yarım filo tabanı kurulmaz).
      </div>
    </div>
  );
}
