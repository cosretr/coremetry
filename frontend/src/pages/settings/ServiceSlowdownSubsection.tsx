import type { ServiceSlowdownConfig } from '@/lib/types';
import { Field } from './shared';

// ServiceSlowdownSubsection — v0.10.1091: "yaygın yavaşlama" hızlı yolunun
// vidaları (anomaly_sensitivity.serviceSlowdown; Dedektör hassasiyeti bölümü,
// operasyon gecikmesi kutusunun hemen altında, aynı Kaydet).
//
// Operatör (prod, iki gün üst üste): "Dün söylediğim CRM sorunu yine oldu, bir
// sürü anomali geldi ama P1 problem gelmedi." Tek kovada ≥ 3 operasyon p99 ≥ 5 s
// VE ≥ 20× kendi tabanı → servis üzerinde critical (P1) Problem.
//
// Sunucu Normalize'da bölümü daima doldurur; bu düşüş yalnız bu sürümden ESKİ
// bir backend'e bakan sekme için (kaydedilirse sunucu kelepçeler).
const SERVICE_SLOWDOWN_DEFAULTS: ServiceSlowdownConfig = {
  enabled: true, minOps: 3, minCallsPerOp: 30, minP99Ms: 5000, riseFactor: 20,
  minCallsTotal: 100, dropPct: 40, clearBuckets: 2, maxNewPerTick: 10,
};

type NumKey = Exclude<keyof ServiceSlowdownConfig, 'enabled'>;

// Alan başına tek satır Türkçe yardım + aralık (sunucu kelepçesiyle aynı).
const FIELDS: { k: NumKey; label: string; min: number; max: number; step?: number; help: string }[] = [
  { k: 'minOps', label: 'En az operasyon', min: 2, max: 20,
    help: 'Aynı 5 dk kovada eşikleri birlikte aşan farklı operasyon sayısı. Varsayılan 3.' },
  { k: 'minP99Ms', label: 'Operasyon p99 tabanı (ms)', min: 500, max: 600000, step: 500,
    help: 'Her operasyonun p99’u en az bu kadar olmalı; problemin eşiği de bu. Varsayılan 5000 ms.' },
  { k: 'riseFactor', label: 'Artış katı (× kendi tabanı)', min: 3, max: 1000, step: 1,
    help: 'p99, operasyonun son 24 saatteki p99’unun en az bu katı olmalı. Varsayılan 20×.' },
  { k: 'minCallsPerOp', label: 'Operasyon başına en az çağrı', min: 10, max: 100000,
    help: 'Kovada (ve tabanında) bu kadar çağrısı olmayan operasyon sayılmaz. Varsayılan 30.' },
  { k: 'minCallsTotal', label: 'Servis toplam çağrı tabanı', min: 10, max: 10000000,
    help: 'Servisin kovadaki toplam çağrısı en az bu kadar olmalı. Varsayılan 100.' },
  { k: 'dropPct', label: 'Trafik çöküşü (% düşüş)', min: 10, max: 95, step: 5,
    help: 'Trafik önceki saatin ortalamasına göre bu kadar düşer ve servis p99’u 3 katına çıkarsa da açılır. Varsayılan %40.' },
  { k: 'clearBuckets', label: 'Kapanış için temiz kova', min: 1, max: 12,
    help: 'Problem bu kadar ardışık temiz 5 dk kovadan sonra kapanır. Varsayılan 2.' },
  { k: 'maxNewPerTick', label: 'Tik başına en çok yeni problem', min: 1, max: 100,
    help: 'Filo çapında bir olayda en kötüler önce açılır, kalanı sonraki dakikada. Varsayılan 10.' },
];

export function ServiceSlowdownSubsection({ value, onChange }: {
  value?: ServiceSlowdownConfig;
  onChange: (v: ServiceSlowdownConfig) => void;
}) {
  const v: ServiceSlowdownConfig = { ...SERVICE_SLOWDOWN_DEFAULTS, ...value };
  // `!== false` ŞART: alan yoksa (eski satır) kural AÇIKtır.
  const on = v.enabled !== false;
  const set = (patch: Partial<ServiceSlowdownConfig>) => onChange({ ...v, ...patch });
  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginTop: 4 }}>
      <label style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <input type="checkbox" aria-label="Yaygın yavaşlama problemi"
          checked={on} onChange={e => set({ enabled: e.target.checked })} />
        <span style={{ fontSize: 13, color: 'var(--text)' }}>Yaygın yavaşlama problemi (P1)</span>
      </label>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, marginLeft: 24, lineHeight: 1.5 }}>
        Açık (varsayılan): tek bir 5 dk kovada bir servisin en az {v.minOps} operasyonunun p99&apos;u
        {' '}{v.minP99Ms} ms&apos;yi ve kendi normalinin {v.riseFactor} katını aşarsa servis üzerinde
        critical problem açılır (sürdürme beklenmez). Operasyon anomalileri ayrıca açılmaya devam eder.
      </div>
      <div style={{
        display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))',
        marginTop: 10, marginLeft: 24, opacity: on ? 1 : 0.5,
      }}>
        {FIELDS.map(f => (
          <Field key={f.k} label={f.label}>
            <input type="number" min={f.min} max={f.max} step={f.step ?? 1}
              aria-label={`Yaygın yavaşlama: ${f.label}`}
              disabled={!on} value={v[f.k]}
              onChange={e => set({ [f.k]: Number(e.target.value) })} />
            <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, lineHeight: 1.5 }}>{f.help}</div>
          </Field>
        ))}
      </div>
    </div>
  );
}
