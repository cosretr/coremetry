import type { AnomalySubjectKindConfig, AnomalySubjectsConfig, SubjectPromotionMode } from '@/lib/types';
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
/** v0.10.1804 — queue: Kafka consumer lag olayının mutlak tabanı (kayıt). */
export const LAG_MIN_RECORDS_DEFAULT = 1000;

type SubjectKindKey = 'endpoint' | 'db' | 'queue';

// v0.10.1797 (Davis FAZ 3) — terfi kipi seçenekleri; sıra = açılış sırası.
const PROMOTION_MODES: { v: SubjectPromotionMode; label: string }[] = [
  { v: 'off', label: 'Kapalı — Problem açılmaz' },
  { v: 'shadow', label: 'Gölge — Problem açılmaz, Anomalies’te “açılırdı” işareti' },
  { v: 'live', label: 'Canlı — Problem açılır' },
];

function normalizePromotionMode(m?: string): SubjectPromotionMode {
  return m === 'shadow' || m === 'live' ? m : 'off';
}

const KINDS: { k: SubjectKindKey; label: string; help: string }[] = [
  { k: 'endpoint', label: 'Endpoint (HTTP rota)',
    help: 'VictoriaMetrics http_route histogramından servis başına en yoğun rotalar. Rota etiketi olmayan servis servis düzeyinde kalır.' },
  { k: 'db', label: 'Veritabanı instance',
    help: 'Mevcut veritabanı özetlerinden servis başına en yoğun instance’lar.' },
  { k: 'queue', label: 'Kuyruk',
    help: 'Mevcut mesajlaşma özetlerinden servis başına en yoğun kuyruklar; VictoriaMetrics varsa Kafka consumer lag’i en yüksek topic’ler de (lag artışı).' },
];

export function withSubjectDefaults(v?: Partial<AnomalySubjectsConfig>): AnomalySubjectsConfig {
  return {
    endpoint: { ...SUBJECT_DEFAULT, ...v?.endpoint },
    db: { ...SUBJECT_DEFAULT, ...v?.db },
    queue: { ...SUBJECT_DEFAULT, ...v?.queue, lagMinRecords: v?.queue?.lagMinRecords || LAG_MIN_RECORDS_DEFAULT },
    promotionMode: normalizePromotionMode(v?.promotionMode),
    promotionMinSustainedSec: v?.promotionMinSustainedSec || 600,
    promotionMinRatio: v?.promotionMinRatio || 2,
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
  const set = (k: SubjectKindKey, patch: Partial<AnomalySubjectKindConfig>) =>
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
                {/* v0.10.1804 (Davis dilim 3) — Kafka lag'in mutlak tabanı; yalnız kuyrukta. */}
                {k === 'queue' && (
                  <Field type="number" className="w-auto" controlClassName="max-w-[140px]"
                    label="En az lag (kayıt)" min={1} max={1000000000} step={100}
                    aria-label={`${label}: en az consumer lag`} disabled={!kv.enabled}
                    hint="Bunun altındaki consumer lag artışı olay sayılmaz."
                    value={kv.lagMinRecords ?? LAG_MIN_RECORDS_DEFAULT}
                    onChange={e => set(k, { lagMinRecords: Number(e.target.value) })} />
                )}
              </div>
            </div>
          );
        })}
      </div>
      <div className="mt-3 max-w-[420px]">
        <SelectField label="Problem'e terfi" aria-label="Özne terfi kipi"
          value={v.promotionMode} onChange={e => onChange({ ...v, promotionMode: normalizePromotionMode(e.target.value) })}
          hint={<>
            Aynı serviste aynı metrikte kötüleşen rotalar tek Problem olur (veritabanı / kuyruk
            kendi Problem’i). Önce gölgede ölçülür; canlıya geçiş operatör kararıdır.
          </>}>
          {PROMOTION_MODES.map(m => <option key={m.v} value={m.v}>{m.label}</option>)}
        </SelectField>
        <div className="mt-2 flex flex-wrap gap-4">
          <Field type="number" className="w-auto" controlClassName="max-w-[140px]"
            label="En az süre (sn)" min={60} max={86400} step={60}
            aria-label="Özne terfi: en az süre" disabled={v.promotionMode === 'off'}
            value={v.promotionMinSustainedSec} onChange={e => onChange({ ...v, promotionMinSustainedSec: Number(e.target.value) })} />
          <Field type="number" className="w-auto" controlClassName="max-w-[140px]"
            label="Tabanın en az katı" min={1} max={1000} step={0.5}
            aria-label="Özne terfi: tabanın en az katı" disabled={v.promotionMode === 'off'}
            value={v.promotionMinRatio} onChange={e => onChange({ ...v, promotionMinRatio: Number(e.target.value) })} />
        </div>
        <div className={NOTE}>
          Yalnız kötüleşme terfi eder: hata oranı ve gecikme artışı. İstek hızı düşüşü bu yoldan
          Problem açmaz. Tabanı olmayan hata oranı, oran yerine anomali terfisinin sayım tabanını kullanır.
        </div>
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
