import type { ErrorRampConfig } from '@/lib/types';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { cn } from '@/lib/cn';
import { ERROR_RAMP_BOUNDS, errorRampFieldError, withErrorRampDefaults, type ErrorRampNumKey } from './errorRamp';

// ErrorRampSubsection — v0.10.1518 sürekli teknik hata (5xx) yükselişi
// kuralının vidaları (anomaly_sensitivity.errorRamp; Dedektör hassasiyeti
// bölümü, yaygın yavaşlamanın altında, aynı Kaydet). Kural
// internal/anomaly/error_ramp.go; aynı `anomaly:<servis>:error_rate` satırını
// açar. Aralıklar errorRamp.ts'te (Go Normalize ile birebir, pinli); aralık
// dışı alan satır içinde uyarı gösterir ve Kaydet'i AnomalyTab durdurur.

const FIELDS: { k: ErrorRampNumKey; label: string; step: number; help: string }[] = [
  { k: 'windowBuckets', label: 'Süreklilik (× 5 dk)', step: 1,
    help: 'Oran bu kadar ardışık tamamlanmış 5 dk kovanın HER BİRİNDE eşiğin üstünde kalmalı. 2–24, varsayılan 6.' },
  { k: 'ratioFactor', label: 'Artış katı (× taban medyanı)', step: 0.1,
    help: 'Kova oranı servisin kendi taban medyanının en az bu katı olmalı (ve taban p90’ının üstünde). 1,2–50, varsayılan 2.' },
  { k: 'minRatioPct', label: 'Oran tabanı (%)', step: 0.05,
    help: 'Kova oranı en az bu yüzde olmalı; tabanı %0 olan serviste eşik budur. 0,01–50, varsayılan 0,2.' },
  { k: 'minErrorsPerSec', label: 'Hata hızı tabanı (hata/sn)', step: 0.5,
    help: 'Kova başına en az bu kadar teknik hata/sn (1 = 5 dk’da 300 hata). Düşük hacimli servisi susturan kapı. 0,05–100000, varsayılan 1.' },
];

// Faz 2 dilim 1 R17 — coremetry Field / CheckboxField (eski aria-label'lar
// aynı); satır-içi stiller utility, kapalı bölüm `data-disabled`.
const SEP = 'mt-1 border-t border-(--border) pt-4';
const NOTE = 'mt-1 pl-6 text-[length:var(--fs-xs)] leading-normal text-(--text3)';

export function ErrorRampSubsection({ value, onChange }: {
  value?: ErrorRampConfig;
  onChange: (v: ErrorRampConfig) => void;
}) {
  const v = withErrorRampDefaults(value);
  const on = v.enabled;
  const set = (patch: Partial<ErrorRampConfig>) => onChange({ ...v, ...patch });
  const minutes = Number.isFinite(v.windowBuckets) ? v.windowBuckets * 5 : 0;
  return (
    <div className={SEP}>
      <CheckboxField aria-label="Sürekli teknik hata yükselişi"
        checked={on} onCheckedChange={c => set({ enabled: c })}
        label="Sürekli teknik hata (5xx) yükselişi"
        hint={<>
          Açık (varsayılan): <b>teknik hata</b> (HTTP 5xx / sunucu hatası) oranı servisin kendi tabanına
          göre <b>{minutes} dakika</b> boyunca kesintisiz yüksek kalırsa servisin hata oranı problemi açılır
          (aynı <code>error_rate</code> satırı, ikinci bir problem yok). Yakaladığı: yüksek hacimli
          servislerde saatlere yayılan rampa. Ani-sapma kolunun %1 mutlak tabanı bunu gizliyordu &mdash;
          saniyede 1000 istek alan bir gateway&apos;de 8 hata/sn yalnız %0,8&apos;dir.
        </>} />
      <div className={NOTE}>
        Yok saydıkları: <b>4xx</b> (iş hatası, teknik hata değil); {minutes} dakikadan <b>kısa
        sıçramalar</b> (onları ani-sapma kolu değerlendirir); <b>olağan günlük yükseliş</b> (taban
        14 günün aynı saatinden gelir, eşik taban p90&apos;ının altına inmez; trafikle orantılı artan hata
        sayısı oranı değiştirmez); <b>düşük hacim</b> (saniyede {v.minErrorsPerSec} hatanın altı).
      </div>
      <div className={NOTE}>
        Karar kaynağı: VictoriaMetrics bağlıysa ve servisin HTTP/gRPC sunucu histogramı varsa
        örneklenmemiş VM serisi (hata kümesi yalnız <code>5..</code>); VM servisi biliyorsa hata oranı
        span&apos;e düşmez. Yoksa örneklenmiş span. Geçmiş bir aralığın kova kova kararı:
        {' '}<code>GET /api/settings/anomaly-red-source/explain</code>.
      </div>
      <div data-disabled={!on || undefined}
        className="mt-2.5 ml-6 grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-3 data-[disabled]:opacity-50">
        {FIELDS.map(f => {
          const b = ERROR_RAMP_BOUNDS[f.k];
          const err = errorRampFieldError(f.k, v[f.k]);
          return (
            <Field key={f.k} label={f.label} type="number" min={b.min} max={b.max} step={f.step}
              aria-label={`Teknik hata yükselişi: ${f.label}`} aria-invalid={err ? true : undefined}
              disabled={!on} value={Number.isFinite(v[f.k]) ? v[f.k] : ''}
              onChange={e => set({ [f.k]: e.target.value === '' ? NaN : Number(e.target.value) })}
              hint={<>
                {f.k === 'windowBuckets' && !err && (
                  <span className="block text-(--text2)">= {minutes} dakika</span>
                )}
                {err && <span role="alert" className="block text-(--warn)">{err}</span>}
                <span className={cn('block', (err || f.k === 'windowBuckets') && 'mt-1')}>{f.help}</span>
              </>} />
          );
        })}
      </div>
    </div>
  );
}
