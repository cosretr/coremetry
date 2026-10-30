import type { AnomalyCalendarConfig, AnomalySpecialDay, AnomalySpecialDayKind } from '@/lib/types';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { NativeSelect } from '@/components/shadcn/native-select';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { cn } from '@/lib/cn';
import { useMemo } from 'react';
import { Combobox } from '@/components/Combobox';
import { browserTz } from '@/lib/browserTz';

// CalendarSubsection — v0.10.1483: mevsimsel baseline'ların takvim etkileri
// (anomaly_sensitivity.calendar; Dedektör hassasiyeti bölümü, aynı Kaydet).
//
// Özel gün (tarih ya da aralık, etiket, tür): holiday → baseline önceki tatil
// günlerinden (yetmezse pazar), half-day → önceki yarım günlerden (yetmezse
// cumartesi), month-end-like → önceki ay sonu benzeri günlerden. Tatil ve
// yarım günde YENİ istek hızı düşüşü Problem açmaz: /anomalies'te
// "takvim günü düşüşü" olarak görünür, bildirim gitmez.
// v0.10.1490 — tarihler takvim diliminin (timezone, IANA) yerel günü; boş =
// UTC (eski kurulumlar). Tarayıcının dilimi yalnız ÖNERİLİR (tek tık), alana
// kendiliğinden yazılmaz: ilgisiz bir Kaydet tatil pencerelerini kaydırmasın.
// Satırlar düzenlenebilir bir liste (≤ 200, sunucu kelepçeler) — kayıt listesi
// değil, bu yüzden DataTable değil.

// Faz 2 dilim 1 R17 — coremetry Field / CheckboxField, satır denetimleri
// shadcn Input / NativeSelect (aria-label'lar aynı), ui Button → shadcn.
const NOTE = 'text-[length:var(--fs-xs)] leading-normal text-(--text3)';
const CTL = 'h-8 bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';

export const CALENDAR_KINDS: { v: AnomalySpecialDayKind; label: string }[] = [
  { v: 'holiday', label: 'Tatil' },
  { v: 'half-day', label: 'Yarım gün' },
  { v: 'month-end-like', label: 'Ay sonu benzeri' },
];

const EMPTY: AnomalyCalendarConfig = { days: [], monthEndAuto: false, monthEndBusinessDays: 2 };

// tzOptions — tarayıcının bildiği IANA adları (Combobox önerisi); Intl eskiyse boş.
function tzOptions(): string[] {
  try {
    const f = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
    return f ? f('timeZone') : [];
  } catch {
    return [];
  }
}

export function CalendarSubsection({ value, onChange }: {
  value?: AnomalyCalendarConfig;
  onChange: (v: AnomalyCalendarConfig) => void;
}) {
  const v: AnomalyCalendarConfig = { ...EMPTY, ...value, days: value?.days ?? [] };
  const suggested = browserTz().tz;
  const tz = v.timezone ?? '';
  const zones = useMemo(tzOptions, []);
  const setDay = (i: number, patch: Partial<AnomalySpecialDay>) =>
    onChange({ ...v, days: v.days.map((d, j) => (j === i ? { ...d, ...patch } : d)) });
  const removeDay = (i: number) => onChange({ ...v, days: v.days.filter((_, j) => j !== i) });
  const addDay = () => onChange({ ...v, days: [...v.days, { from: '', label: '', kind: 'holiday' }] });
  return (
    <div className="mt-1 border-t border-(--border) pt-4">
      <div className="text-[length:var(--fs-md)] text-(--text)">Takvim etkileri (özel günler)</div>
      <div className={cn(NOTE, 'mt-1')}>
        Tatil günü baseline&apos;ı önceki tatil günlerinden kurulur (yeterli örnek yoksa pazar günü
        sınıfı); yarım gün önceki yarım günlerden (yoksa cumartesi); ay sonu benzeri önceki ay sonu
        günlerinden. Tatil ve yarım günde yeni <b>istek hızı düşüşü</b> Problem açmaz ve bildirim
        göndermez; /anomalies&apos;te &laquo;takvim günü düşüşü&raquo; olarak kaydedilir. Hata oranı,
        gecikme ve sıçramalar her gün olduğu gibi çalışır.
      </div>
      <div className="mt-2.5 max-w-[420px]">
        <div className="flex flex-wrap items-end gap-2">
          {/* Combobox kendi aria-label'ını taşır; etiket görsel (eski shared Field gibi). */}
          <div className="flex flex-col gap-1">
            <span className="text-[length:var(--fs-sm)] leading-tight text-(--text2)">Takvim saat dilimi (IANA)</span>
            <Combobox ariaLabel="Takvim saat dilimi" options={zones} width={260}
              placeholder={suggested ? `UTC (öneri: ${suggested})` : 'UTC'} value={tz}
              onChange={z => onChange({ ...v, timezone: z.trim() || undefined })} />
          </div>
          {suggested && suggested !== tz && (
            <Button size="sm" variant="outline" onClick={() => onChange({ ...v, timezone: suggested })}>
              Tarayıcının dilimini kullan ({suggested})
            </Button>
          )}
        </div>
        <div className={cn(NOTE, 'mt-1')}>
          Özel günler bu dilimin yerel günü olarak eşleşir (ör. Europe/Istanbul: tatilin ilk 3 saati
          UTC&apos;de önceki güne düşer, yine tatil sayılır); ay sonu iş günleri de bu dilimde hesaplanır.
          Boş = UTC günü (önceki davranış). Mevsimsel baseline&apos;ın saat eşlemesi UTC&apos;de kalır.
          Bilinmeyen ad kaydedilmez.
        </div>
      </div>
      <div className="mt-2.5 flex flex-col gap-2">
        {v.days.length === 0 && (
          <div className="text-[length:var(--fs-sm)] text-(--text3)">Özel gün yok.</div>
        )}
        {v.days.map((d, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <Input type="date" aria-label={`Özel gün ${i + 1} başlangıç`} value={d.from}
              onChange={e => setDay(i, { from: e.target.value })} className={cn(CTL, 'w-auto')} />
            <span className="text-[length:var(--fs-sm)] text-(--text3)">–</span>
            <Input type="date" aria-label={`Özel gün ${i + 1} bitiş`} value={d.to ?? ''}
              onChange={e => setDay(i, { to: e.target.value || undefined })} className={cn(CTL, 'w-auto')} />
            <Input type="text" aria-label={`Özel gün ${i + 1} etiket`} placeholder="Etiket" maxLength={64}
              value={d.label} onChange={e => setDay(i, { label: e.target.value })} className={cn(CTL, 'w-auto min-w-40')} />
            <div className="w-auto">
              <NativeSelect size="sm" aria-label={`Özel gün ${i + 1} tür`} value={d.kind} className={CTL}
                onChange={e => setDay(i, { kind: e.target.value as AnomalySpecialDayKind })}>
                {CALENDAR_KINDS.map(k => <option key={k.v} value={k.v}>{k.label}</option>)}
              </NativeSelect>
            </div>
            <Button size="sm" variant="ghost-danger" onClick={() => removeDay(i)} aria-label={`Özel gün ${i + 1} sil`}>Sil</Button>
          </div>
        ))}
        <div>
          <Button size="sm" variant="outline" onClick={addDay}>Özel gün ekle</Button>
        </div>
      </div>
      <CheckboxField className="mt-3" aria-label="Otomatik ay sonu sınıfı"
        checked={v.monthEndAuto} onCheckedChange={c => onChange({ ...v, monthEndAuto: c })}
        label="Otomatik ay sonu sınıfı"
        hint={<>
          Kapalı (varsayılan). Açıkken ayın son iş günleri (hafta içi, tatil değil) &laquo;ay sonu
          benzeri&raquo; sınıfına girer.
        </>} />
      {v.monthEndAuto && (
        <div className="mt-2 ml-6 max-w-[260px]">
          <Field label="Ayın son kaç iş günü (1–5)" type="number" min={1} max={5} aria-label="Ayın son kaç iş günü"
            value={v.monthEndBusinessDays}
            onChange={e => onChange({ ...v, monthEndBusinessDays: Number(e.target.value) })} />
        </div>
      )}
    </div>
  );
}
