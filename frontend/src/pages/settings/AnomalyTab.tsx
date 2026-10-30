import { useEffect, useState } from 'react';
import type { AnomalyRuntimeConfig } from '@/lib/types'; // v0.10.890
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, CheckboxField, type FieldProps } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import type { AnomalyPromotionConfig, AnomalyTrackedConfig, AnomalySensitivityConfig, AnomalyBehaviorConfig, ExceptionTriageConfig, ProblemPriorityConfig, DBSlowQueryConfig, DBHealthConfig } from '@/lib/types';
import { FlashBox, humanize } from './shared';
import { droppedPatternCount, formatBatchPatterns, parseBatchPatterns } from './batchPatterns'; // v0.10.1039
import { ServiceSlowdownSubsection } from './ServiceSlowdownSubsection'; // v0.10.1091
import { REDSourceSubsection } from './REDSourceSubsection'; // v0.10.1478
import { CalendarSubsection } from './CalendarSubsection'; // v0.10.1483
import { ErrorRampSubsection } from './ErrorRampSubsection';
import { errorRampErrors, withErrorRampDefaults } from './errorRamp';

// Faz 2 dilim 1 R17 — elle yazılmış `<Field><input/><div hint/></Field>`
// (shared.tsx) → coremetry Field ailesi (`hint`), ham onay kutuları →
// CheckboxField (eski aria-label'lar korunur), ham select → SelectField, ui
// Button → shadcn Button, kaydet satırları → ActionRow + FlashBox; satır-içi
// stiller utility. Kapalı bölümün sönükleşmesi `data-disabled` +
// DIM. Alan sırası, metinler, varsayılanlar ve PUT gövdeleri aynı.
const H2 = 'mb-1.5 text-[length:var(--fs-lg)] font-semibold';
const LEAD = 'mb-[18px] text-[length:var(--fs-sm)] leading-[1.55] text-(--text2)';
const BOXED = 'rounded-[4px] border border-(--border) px-2.5 py-2';
const HR = 'mt-7 mb-[22px] border-0 border-t border-(--border)';
const SEP = 'mt-1 border-t border-(--border) pt-4';
const STACK = 'grid gap-3';
const DIM = 'data-[disabled]:opacity-50';
const NOTE = 'text-[length:var(--fs-xs)] leading-normal text-(--text3)';
const WARN = 'mt-1 block text-(--warn)';
const WARN_LINE = 'mt-1 text-[length:var(--fs-xs)] text-(--warn)';
const SUBHEAD = 'text-[length:var(--fs-md)] font-semibold text-(--text)';
const GRID = 'grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-3';

/** Sayı alanı: denetim 180px'te kalır, ipucu kolonun tamamını kullanır. */
function Num(props: FieldProps) {
  return <Field type="number" controlClassName="max-w-[180px]" {...props} />;
}

/** Kaydet satırı: birincil eylem sağda (ActionRow), sonuç altında. */
function SaveRow({ label, onClick, busy, disabled, flash }: {
  label: string; onClick: () => void; busy: boolean; disabled?: boolean;
  flash: { kind: 'ok' | 'err'; text: string } | null;
}) {
  return (
    <div className="mt-[18px]">
      <ActionRow confirm={<Button onClick={onClick} disabled={disabled} loading={busy}>{label}</Button>} />
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
    </div>
  );
}

// ── Anomaly promotion tab ───────────────────────────────────────
//
// Tunes the evaluator's anomaly auto-promotion (v0.5.59). The
// detector continuously flags "pattern X is occurring N× more
// than baseline" rows on /anomalies; when they sustain past
// the configured thresholds the evaluator graduates them to
// first-class Problems so the existing notify pipeline pages
// the on-call. Master enable flag lets operators kill the
// feature for a chatty detector without changing thresholds.
export function AnomalyPromotionTab() {
  const [cfg, setCfg] = useState<AnomalyPromotionConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    api.getAnomalyPromotion()
      .then(c => setCfg(c))
      .catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);

  const save = async () => {
    if (!cfg) return;
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putAnomalyPromotion(cfg);
      setCfg(saved);
      setFlash({ kind: 'ok', text: 'Saved — next evaluator tick picks it up automatically.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  if (!cfg) {
    return (
      <div className="max-w-[640px]">
        <h2 className={H2}>Anomaly auto-promotion</h2>
        {flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />}
      </div>
    );
  }

  return (
    <div className="max-w-[640px]">
      <h2 className={H2}>Anomaly auto-promotion</h2>
      {/* v0.9.827 — DÜRÜSTLÜK DÜZELTMESİ.
          Eski metin "the anomaly detector" diyordu ve bu, sayfadaki en
          pahalı yanlış anlamaydı: operatör bu kutucuğu kapatıp METRİK
          dedektörünün susacağını sanıyordu. Aşağıdaki vidalar YALNIZ
          log/trace desen anomalilerinin (recorder → AnomalyEvent)
          terfi hattını yönetiyor. Metrik dedektörü (error_rate, p99,
          istek hızı) tamamen ayrı bir hat ve eşikleri "Dedektör
          hassasiyeti" bölümünde. */}
      <p className={cn(LEAD, 'mb-2.5')}>
        Log ve iz (trace) verisinde tekrarlayan desenler kendi hareketli
        ortalamalarına göre işaretlenir; bu bölüm o işaretlerden hangilerinin
        birinci sınıf <b>Problem</b>&apos;e terfi edeceğini ayarlar. Dedektör çok
        konuşkansa eşikleri sıkın, ya da kalibre ederken tümden kapatın.
      </p>
      <p className={cn(LEAD, BOXED)}>
        <b>Kapsam:</b> bu vidalar yalnız <b>log/iz desen anomalilerinin terfi
        hattını</b> yönetir. <b>Metrik</b> dedektörü (hata oranı, P99 gecikme,
        istek hızı) ayrı bir hattır ve bu kutucuktan etkilenmez &mdash; onun
        eşikleri aşağıdaki <b>Dedektör hassasiyeti</b> bölümünde.
      </p>

      <CheckboxField className="mb-4" checked={cfg.enabled}
        onCheckedChange={v => setCfg({ ...cfg, enabled: v })}
        label={<>Güçlü log/iz anomalilerini Problem&apos;e terfi ettir</>} />
      {/* v0.10.1588 — terfi Problem'i incident'a bağlansın (Davis paritesi
          FAZ 0.5 / D). Alan yoksa AÇIK (`!== false`); kayıt açık boolean. */}
      <CheckboxField className="mb-4" aria-label="Terfi Problem'i incident'a bağlansın"
        checked={cfg.attachToIncident !== false}
        onCheckedChange={v => setCfg({ ...cfg, attachToIncident: v })}
        disabled={!cfg.enabled}
        label={<>Terfi edilen Problem incident&apos;a bağlansın (aynı servis / komşu; yoksa yeni incident)</>} />

      <div data-disabled={!cfg.enabled || undefined} className={cn(STACK, DIM)}>
        <Num label="Minimum peak ratio (× baseline)" min={1} max={1000} step={0.5}
          value={cfg.minPeakRatio}
          onChange={e => setCfg({ ...cfg, minPeakRatio: Number(e.target.value) })}
          disabled={!cfg.enabled}
          hint={<>
            5× = pattern is occurring at least 5 times more than its
            rolling baseline. Default 5.
          </>} />

        {/* v0.9.247 — severity cut-off, previously a hard-coded 20 in the
            evaluator. It sat invisibly ABOVE the promotion gate, so an
            operator raising "minimum peak ratio" to 20+ to get FEWER pages
            silently turned every surviving promotion critical. Surfacing it
            makes the two decisions independent: the gate controls HOW MANY
            promote, this controls how many of those page. */}
        <Num label="Critical at peak ratio (x baseline)" min={cfg.minPeakRatio} max={1000} step={0.5}
          value={cfg.criticalPeakRatio}
          onChange={e => setCfg({ ...cfg, criticalPeakRatio: Number(e.target.value) })}
          disabled={!cfg.enabled}
          hint={<>
            At or above this ratio a promoted anomaly opens as <b>critical</b>;
            below it, <b>warning</b>. Must be &ge; the minimum peak ratio &mdash;
            setting them equal means every promoted anomaly pages. Default 20.
            {cfg.criticalPeakRatio <= cfg.minPeakRatio && (
              <span className={WARN}>
                Su an esit: promote edilen HER anomali critical acilir.
              </span>
            )}
          </>} />

        <Num label="Minimum sustained (seconds since started_at)" min={60} max={86400} step={60}
          value={cfg.minSustainedSec}
          onChange={e => setCfg({ ...cfg, minSustainedSec: Number(e.target.value) })}
          disabled={!cfg.enabled}
          hint="Filters out one-tick flares. Default 300s (5 min)." />

        <Num label="Minimum count" min={1} max={1000000} step={1}
          value={cfg.minCount}
          onChange={e => setCfg({ ...cfg, minCount: Number(e.target.value) })}
          disabled={!cfg.enabled}
          hint={<>
            Absolute volume floor — a 100× ratio on 2 occurrences is
            meaningless. Default 10.
          </>} />

        {/* v0.10.1587 — sayım-yalnız (oransız) olaylar: operasyonda yeni hata,
            yeni log deseni. Oran kapısı yerine bu sayım tabanı; critical açılmaz. */}
        <Num label="Yeni hata / yeni desen: en az sayım" min={cfg.minCount} max={1000000} step={1}
          aria-label="Yeni hata en az sayım"
          value={cfg.countOnlyMinCount ?? 20}
          onChange={e => setCfg({ ...cfg, countOnlyMinCount: Number(e.target.value) })}
          disabled={!cfg.enabled}
          hint={<>
            Tabanı olmayan olaylarda (operasyonda ilk kez görülen hata, yeni log deseni)
            oran yoktur; terfi bu sayıya ve sürdürmeye bakar ve uyarı (warning) olarak
            açılır. En az &quot;Minimum count&quot; kadar. Varsayılan 20.
          </>} />
      </div>

      <SaveRow label="Save" onClick={save} busy={busy} flash={flash} />

      <hr className={HR} />
      <TrackedMetricsSection />

      <hr className={HR} />
      <SensitivitySection />

      <hr className={HR} />
      <EscalationSection />
      <DBSlowQuerySection />

      <hr className={HR} />
      <ExceptionTriageSection />

      <hr className={HR} />
      <ProblemPrioritySection />
    </div>
  );
}

// ── İzlenen metrikler (v0.9.800) ────────────────────────────────
//
// Yukarıdaki bölüm bir sinyalin Problem'e TERFİ edip etmeyeceğini
// ayarlıyor; bu bölüm sinyalin hiç ÖLÇÜLÜP ölçülmeyeceğini. İkisi ayrı
// sorular: promosyonu kısmak gürültüyü azaltır ama metrik yine
// hesaplanır ve /anomalies'te görünür.
//
// request_rate VARSAYILAN OLARAK KAPALI (operatör 2026-08-09): istek
// hacmi bu filoda kampanya, batch penceresi ve nöbet devriyle normalde
// de sıçrayıp düşüyor — "istek hızı beklenenden farklı" bir olay değil.
// Metrik koddan sökülmedi, vidaya bağlandı: geri açmak tek tık.
const TRACKED_METRICS: { key: string; label: string; hint: string }[] = [
  { key: 'error_rate', label: 'Hata oranı (error_rate)', hint: 'Yükselen hata oranı. Gerçek olay sinyali — açık kalması önerilir.' },
  { key: 'p99_ms', label: 'P99 gecikme (p99_ms)', hint: 'Yükselen kuyruk gecikmesi. Gerçek olay sinyali — açık kalması önerilir.' },
  { key: 'request_rate', label: 'İstek hızı (request_rate)', hint: 'Hacim sıçraması VE düşüşü. Varsayılan kapalı: false-pozitif üretiyor (kampanya, batch, nöbet devri).' },
];

function TrackedMetricsSection() {
  const [cfg, setCfg] = useState<AnomalyTrackedConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    api.getAnomalyTracked()
      .then(c => setCfg(c))
      .catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);

  const save = async () => {
    if (!cfg) return;
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putAnomalyTracked(cfg);
      setCfg(saved);
      setFlash({ kind: 'ok', text: 'Kaydedildi — dedektör bir sonraki taramada yeni seti kullanır.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  // En az biri açık kalmalı. Backend zaten 400 döner; operatör kaydet
  // düğmesine basmadan görmeli — motoru tümden kapatmanın yolu bu vida
  // değil (Background ayarları).
  const noneOn = !!cfg && !TRACKED_METRICS.some(m => cfg[m.key]);

  return (
    <div>
      <h2 className={H2}>İzlenen metrikler</h2>
      <p className={LEAD}>
        Dedektör her taramada yalnız burada seçili metrikleri ölçer: kapalı bir metrik
        için baseline sorgusu bile açılmaz, dolayısıyla ne yeni tespit üretilir ne de
        tarama maliyeti ödenir. Üstteki promosyon kapısından farkı şu &mdash; o, ölçülen
        bir sapmanın <b>Problem&apos;e dönüşüp dönüşmeyeceğini</b> ayarlar; bu ise
        sapmanın <b>hiç aranıp aranmayacağını</b>.
      </p>

      {!cfg ? (
        flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />
      ) : (
        <>
          <div className={STACK}>
            {TRACKED_METRICS.map(m => (
              <CheckboxField key={m.key} label={m.label} hint={m.hint}
                checked={!!cfg[m.key]}
                onCheckedChange={v => setCfg({ ...cfg, [m.key]: v })} />
            ))}
          </div>

          {noneOn && (
            <div className={cn(WARN_LINE, 'mt-2.5')}>
              En az bir metrik açık kalmalı. Anomali motorunu tümden kapatmak için
              Background ayarlarını kullanın.
            </div>
          )}

          <div className={cn(NOTE, 'mt-3')}>
            Bir metriği kapatmak açık kayıtları silmez: yeni tespit üretilmez, mevcut
            açık anomaliler bayat ufkuyla kendiliğinden kapanır. Elle kurulmuş alarm
            kuralları bu vidadan etkilenmez.
          </div>

          <SaveRow label="Kaydet" onClick={save} disabled={noneOn} busy={busy} flash={flash} />
        </>
      )}
    </div>
  );
}

// ── Dedektör hassasiyeti (v0.9.826) ─────────────────────────────
//
// Üstteki bölüm hangi metriğin ÖLÇÜLECEĞİNİ ayarlıyor; bu bölüm
// ölçülenin ne zaman OLAY sayılacağını.
//
// Neden vida gerekti: bu eşikler bugüne kadar ALTI kez kodda değişti
// (v0.8.220 dwell, v0.9.48 düz-MAD tabanı, v0.9.180 mutlak taban,
// v0.9.193 criticalZ 5→6…) ve her seferinde operatör bir çentik ötede
// aynı duvara çarptı. Son vaka: medyanı 1.90ms olan bir op 9.69ms'e
// çıkınca 8.0σ critical anomali açılıyordu — istatistiksel olarak
// gerçek, operasyonel olarak hiçbir şey.
const SENSITIVITY_METRICS: { key: string; label: string; unit: string }[] = [
  { key: 'error_rate', label: 'Hata oranı (error_rate)', unit: '%' },
  { key: 'p99_ms', label: 'P99 gecikme (p99_ms)', unit: 'ms' },
  { key: 'request_rate', label: 'İstek hızı (request_rate)', unit: '/sn' },
];

// Alan sözlüğü — her vidanın NE kapattığını tek cümlede söyler.
// Ayrı bir tablo çünkü metrik başına üç kez tekrarlanıyor ve metinlerin
// ayrışması (aynı vidanın iki metrikte farklı anlatılması) bu ekranın
// en olası bozulma biçimi.
const SENSITIVITY_FIELDS: {
  key: 'floorPct' | 'absFloor' | 'minAbsDelta' | 'minMAD' | 'minBaselineRate';
  label: string; hint: string; step: number; unitKind: 'ratio' | 'value' | 'rate';
}[] = [
  { key: 'floorPct', label: 'Göreli değişim tabanı', step: 0.01, unitKind: 'ratio',
    hint: '|fark| / medyan. 0.10 = medyanın %10\'undan küçük hareketler olay sayılmaz.' },
  { key: 'absFloor', label: 'Mutlak değer tabanı', step: 0.5, unitKind: 'value',
    hint: 'Güncel değer bunun ALTINDAysa yükseliş yönlü anomali açılmaz. Düşüşleri etkilemez.' },
  { key: 'minAbsDelta', label: 'Mutlak fark tabanı', step: 0.5, unitKind: 'value',
    hint: '|güncel − medyan| bunun altındaysa açılmaz. Küçük medyanlarda yüzde tabanının kör noktasını kapatır.' },
  { key: 'minMAD', label: 'En küçük MAD (σ tabanı)', step: 0.1, unitKind: 'value',
    hint: 'Sapma ölçüsünün alt sınırı. Çok sıkı bir geçmişte z\'nin patlamasını engeller — asıl gürültü kaynağı budur.' },
  { key: 'minBaselineRate', label: 'En düşük hacim', step: 0.1, unitKind: 'rate',
    hint: 'Son 5 dakikanın istek hızı bunun altındaysa AÇILMAZ. Düşük hacimde yüzdeler gürültüdür. Çözülmeyi etkilemez. Varsayılan: hata oranı 0.2, p99 0.5, istek hızı 0 (kapalı).' },
];

function SensitivitySection() {
  const [cfg, setCfg] = useState<AnomalySensitivityConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // v0.10.1039 — batch kalıpları ayrı METİN state'inde: her tuşta listeye
  // çevirip geri yazmak "a, " yazarken virgülü yutardı. Kaydette listeye
  // çevrilir; yüklemede ve kayıttan sonra sunucunun döndürdüğü (normalize)
  // listeden yeniden doldurulur.
  const [batchText, setBatchText] = useState('');
  // Son kayıtta sunucu normalizasyonunun düşürdüğü giriş sayısı (kısa /
  // tavan / tekrar) — kural sessizce daralmasın, operatör görsün.
  const [batchDropped, setBatchDropped] = useState(0);

  useEffect(() => {
    api.getAnomalySensitivity()
      .then(c => { setCfg(c); setBatchText(formatBatchPatterns(c.batchServicePatterns)); })
      .catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);

  const save = async () => {
    if (!cfg) return;
    // Teknik hata yükselişi: sunucu aralık dışı değeri sessizce VARSAYILANA
    // döndürür (kelepçelemez) — kayıttan ÖNCE reddet, operatör yazdığının
    // kaydedildiğini sanmasın.
    const rampErrs = Object.keys(errorRampErrors(cfg.errorRamp));
    if (rampErrs.length > 0) {
      setFlash({ kind: 'err', text: 'Kaydedilmedi — teknik hata yükselişi alanlarından biri aralık dışında.' });
      return;
    }
    setBusy(true); setFlash(null);
    try {
      // Sunucu kelepçeleyip KAYDEDİLENİ döndürüyor; onu state'e yazmak
      // şart — operatör anlamsız bir değer girdiyse (negatif, aralık
      // dışı) alanın varsayılana döndüğünü GÖRMELİ, yoksa yazdığını
      // kaydedilmiş sanır.
      // v0.10.1039 — boş metin `[]` gider (kural KAPALI); alan atlanmaz,
      // atlansa sunucu varsayılanı (-batch) geri getirirdi.
      const sentPatterns = parseBatchPatterns(batchText);
      // v0.10.1056 — opLatency daima AÇIK boolean gider: kayıtlı blob ne
      // olduğunu söylesin, eski sunucu varsayılanına kalmasın. v0.10.1085 —
      // alan yoksa TRUE (varsayılan açık, sürdürme kuralıyla).
      // v0.10.1061 — logTemplateNew aynı gerekçeyle (alan yoksa false).
      const saved = await api.putAnomalySensitivity({
        ...cfg, batchServicePatterns: sentPatterns, opLatency: cfg.opLatency !== false,
        logTemplateNew: cfg.logTemplateNew === true,
        // v0.10.1091 — bölüm varsa bayrak AÇIK boolean gider (yok = açık).
        ...(cfg.serviceSlowdown
          ? { serviceSlowdown: { ...cfg.serviceSlowdown, enabled: cfg.serviceSlowdown.enabled !== false } }
          : {}),
        // Teknik hata yükselişi — bölüm daima tam gider, enabled AÇIK boolean.
        errorRamp: withErrorRampDefaults(cfg.errorRamp),
      });
      setCfg(saved);
      setBatchText(formatBatchPatterns(saved.batchServicePatterns));
      setBatchDropped(droppedPatternCount(sentPatterns, saved.batchServicePatterns));
      setFlash({ kind: 'ok', text: 'Kaydedildi — dedektör bir sonraki taramada yeni eşikleri kullanır.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const setMetricField = (metric: string, field: string, v: number) => {
    if (!cfg) return;
    setCfg({
      ...cfg,
      metrics: { ...cfg.metrics, [metric]: { ...cfg.metrics[metric], [field]: v } },
    });
  };

  return (
    <div>
      <h2 className={H2}>Dedektör hassasiyeti</h2>
      <p className={LEAD}>
        Bir sapmanın ne zaman <b>olay</b> sayılacağını belirler. Dedektör her metriği
        kendi 5 dakikalık geçmişine göre puanlar (medyan + MAD ile sağlam z-skoru);
        aşağıdaki tabanlar, istatistiksel olarak büyük ama operasyonel olarak
        önemsiz sapmaları eler. Örnek: medyanı 1,90 ms olan bir işlemin 9,69 ms&apos;e
        çıkması 8σ&apos;dır ama kimsenin fark etmediği bir şeydir.
      </p>

      {!cfg ? (
        flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />
      ) : (
        <>
          <div className={cn(STACK, 'mb-[22px]')}>
            <Num label="Kritik z-skoru (açılma eşiği)" min={1} max={50} step={0.5}
              value={cfg.criticalZ}
              onChange={e => setCfg({ ...cfg, criticalZ: Number(e.target.value) })}
              hint={<>
                Bu σ değerinin üstündeki sapmalar <b>critical</b> sayılır. Dedektör yalnız
                critical sapmalarda Problem açtığı için (v0.9.193) bu, fiilen açılma
                eşiğidir: yükseltmek daha az, düşürmek daha çok anomali demek.
                Varsayılan 6,0.
              </>} />

            <Num label="Süreklilik (kaç 5-dakikalık dilim)" min={1} max={24} step={1}
              value={cfg.dwellBuckets}
              onChange={e => setCfg({ ...cfg, dwellBuckets: Number(e.target.value) })}
              hint={<>
                Bir anomalinin açılması için üst üste bu kadar dilimde ateşlemesi gerekir
                &mdash; anlık bir sıçrama sayfa göndermez. Varsayılan 3
                ({(cfg.dwellBuckets || 3) * 5} dakika sürekli). Çözülme bundan
                etkilenmez: tek bir dilimin banda dönmesi problemi kapatır.
              </>} />

            {/* v0.10.587 — dış hat sel kapısı (Oracle audit §6.5). */}
            <Num label="Dış kaynak: tik başına en çok yeni Problem" min={1} max={200} step={1}
              value={cfg.externalOpenCapPerTick ?? 20}
              onChange={e => setCfg({ ...cfg, externalOpenCapPerTick: Number(e.target.value) })}
              hint={<>
                Oracle gibi dış kaynakların anomali hattı bir tikte bundan fazla
                yeni Problem açmaz; en güçlü sapmalar önce, kalanlar için tek bir
                &ldquo;tavan aşıldı&rdquo; özeti açılır. Açık Problem'ler etkilenmez.
                Varsayılan 20.
              </>} />
          </div>

          {SENSITIVITY_METRICS.map(m => {
            const s = cfg.metrics?.[m.key];
            if (!s) return null;
            // Birim etiketi alan tipine göre: oran birimsiz, değer
            // metriğin birimi, hacim her zaman istek/sn.
            const unitFor = (kind: string) =>
              kind === 'ratio' ? '' : kind === 'rate' ? 'istek/sn' : m.unit;
            return (
              <div key={m.key} className="mb-[22px]">
                <div className={cn(SUBHEAD, 'mb-2')}>
                  {m.label}
                </div>
                <div className="ml-3 grid gap-2.5">
                  {SENSITIVITY_FIELDS.map(f => (
                    <Num key={f.key} label={`${f.label}${unitFor(f.unitKind) ? ` (${unitFor(f.unitKind)})` : ''}`}
                      min={0} step={f.step}
                      value={s[f.key]}
                      onChange={e => setMetricField(m.key, f.key, Number(e.target.value))}
                      hint={<>{f.hint} <b>0 = kapalı.</b></>} />
                  ))}
                </div>
              </div>
            );
          })}

          <BehaviorSubsection
            behavior={cfg.behavior}
            onChange={b => setCfg({ ...cfg, behavior: b })}
          />
          {/* v0.10.890 (paritesi #4 dilim 2, Onay 2026-09-23) — JVM heap bandı → Problem
              vidaları. Bu sürümde yalnız vida: kip varsayılan KAPALI, hiçbir davranış
              değişmez; gölge sayaçları 891, canlı kip 892. */}
          <RuntimeSubsection
            runtime={cfg.runtime}
            onChange={r => setCfg({ ...cfg, runtime: r })}
          />

          {/* v0.10.1039 — batch servis ad kalıpları (operatör: "Bazı batch
              işlerde ani yük artışı olabilir, onları anomali gibi düşünme —
              özellikle `-batch` geçen servis isimlerinde"). Etkin liste
              gösterilir (alan yoksa -batch); boş kayıt kuralı KAPATIR. */}
          <div className={SEP}>
            <Field label="Batch servis ad kalıpları" aria-label="Batch servis ad kalıpları"
              placeholder="boş = kural kapalı"
              value={batchText}
              onChange={e => setBatchText(e.target.value)}
              hint={<>
                Adında bu kalıplardan biri geçen servislerde ani yük artışı anomali sayılmaz
                (istek hızı, hacim sıçraması; hata sayısı artışı ancak hata oranı da artmışsa
                olay sayılır). Hata
                oranı anomalileri etkilenmez; gecikme artışı, istek hacmi olağan çalışma hacminin en az
                2 katındayken yeni anomali açmaz; zaten açık olanlar sürer. Boş bırakırsanız kural kapanır.
                <br />
                Virgül ya da boşlukla ayırın; büyük-küçük harf fark etmez, 3 karakterden kısa
                kalıp yok sayılır, en çok 10 kalıp. Varsayılan <code>-batch</code>.
              </>} />
            {/* v0.10.1039 (inceleme) — kural fark edilmeden kapanmasın ya da daralmasın. */}
            {cfg.batchServicePatterns?.length === 0 && (
              <div className={WARN_LINE}>
                Kural kapalı — batch servisler de diğerleri gibi değerlendirilir.
              </div>
            )}
            {batchDropped > 0 && (
              <div className={WARN_LINE}>
                {batchDropped} kalıp alınmadı: 3 karakterden kısa / 10&apos;dan fazla / tekrar.
              </div>
            )}
          </div>
          {/* v0.9.827 — dedektör → incident kapısı.
              Bu çağrı bugüne kadar KOŞULSUZDU ve Settings'teki hiçbir vida
              ona ulaşmıyordu: üstteki "terfi ettir" kutucuğu BAŞKA bir
              hattı yönetiyor. Operatör onu kapatıp metrik dedektörünün
              incident açmaya devam ettiğini görüyordu. */}
          {/* v0.10.543 — service_silent dedektörü: VARSAYILAN KAPALI (operatör
              2026-09-07: "Anomali service silent'lara ihtiyacım yok"). Kapalıyken
              açık kalan service_silent problemleri bir sonraki tikte çözülür. */}
          <div className={SEP}>
            <CheckboxField checked={cfg.serviceSilent === true}
              onCheckedChange={v => setCfg({ ...cfg, serviceSilent: v })}
              label={<>Sessiz servis anomalisi (<code>service_silent</code>)</>}
              hint={<>
                Açıkken, 15 dakika boyunca hiç span üretmeyen (öncesinde düzenli trafiği olan)
                servis için critical &quot;Anomaly · Service silent&quot; problemi açılır. Varsayılan
                KAPALI: kapanıp açılan servisler incident listesini dolduruyordu. Kapatınca açık
                kalanlar bir sonraki tikte çözülür.
              </>} />
          </div>
          {/* v0.10.1056 — trace_op_latency dedektörü anahtarı (operatör: "Trace op
              latency false pozitif geliyor"). v0.10.1085 — varsayılan yeniden AÇIK,
              yalnız sürdürme kuralıyla (operatör: "Önerini yapalım"): alan yoksa AÇIK
              okunur (`!== false`); kayıt açık boolean + kova sayısı gönderir. */}
          <div className={SEP}>
            <CheckboxField aria-label="Operasyon gecikmesi anomalileri"
              checked={cfg.opLatency !== false}
              onCheckedChange={v => setCfg({ ...cfg, opLatency: v })}
              label="Operasyon gecikmesi anomalileri"
              hint={<>
                Açık (varsayılan): bir operasyonun p99&apos;u iki ardışık kovada sürmeli; tek sıçrama
                alarm açmaz. Her kovada en az 30 çağrı, en az 200 ms ve normalin en az 3 katı
                gerekir. Servis düzeyindeki gecikme anomalileri etkilenmez.
              </>} />
            <div className="mt-2.5 ml-6">
              <Num label="Operasyon gecikmesi: kaç ardışık 5-dakikalık kova" min={1} max={6} step={1}
                aria-label="Operasyon gecikmesi ardışık kova"
                disabled={cfg.opLatency === false}
                value={cfg.opLatencyDwellBuckets ?? 2}
                onChange={e => setCfg({ ...cfg, opLatencyDwellBuckets: Number(e.target.value) })}
                hint={<>
                  Sıçrama bu kadar ardışık tamamlanmış kovanın her birinde sürmeli. 1–6,
                  varsayılan 2 ({(cfg.opLatencyDwellBuckets ?? 2) * 5} dakika). 1 = eski
                  davranış: tek kovalık sıçrama da anomali açar.
                </>} />
            </div>
          </div>
          {/* v0.10.1586 — trace_op / log_pattern sürdürme (Davis paritesi FAZ 0.5 / C):
              olay ancak ardışık N pencerede eşiği geçerse açılır; aynı Kaydet. */}
          <div className={SEP}>
            <div className="text-[length:var(--fs-md)] text-(--text)">
              Operasyon hatası ve log deseni olayları: sürdürme
            </div>
            <div className={cn(NOTE, 'mt-1')}>
              Operasyon hata sıçraması / yeni hata ve log deseni (yeni ya da sıçrama) olayı
              ancak bu kadar ardışık 5 dakikalık pencerenin her birinde eşiği geçerse açılır;
              zaten açık olay tek pencerede tazelenir. 1–6, varsayılan 2. 1 = eski davranış.
            </div>
            <div className="mt-2.5 flex flex-wrap gap-4">
              <Num className="w-auto" label="Operasyon hatası: kaç ardışık pencere" min={1} max={6} step={1}
                aria-label="Operasyon hatası ardışık pencere"
                value={cfg.traceOpDwellBuckets ?? 2}
                onChange={e => setCfg({ ...cfg, traceOpDwellBuckets: Number(e.target.value) })} />
              <Num className="w-auto" label="Log deseni: kaç ardışık pencere" min={1} max={6} step={1}
                aria-label="Log deseni ardışık pencere"
                value={cfg.logPatternDwellBuckets ?? 2}
                onChange={e => setCfg({ ...cfg, logPatternDwellBuckets: Number(e.target.value) })} />
            </div>
          </div>
          {/* v0.10.1091 — yaygın yavaşlama hızlı yolu (operatör: "Dün söylediğim
              CRM sorunu yine oldu, bir sürü anomali geldi ama P1 problem
              gelmedi" → "Onay"). Operasyon gecikmesinin yanında, aynı Kaydet. */}
          <ServiceSlowdownSubsection value={cfg.serviceSlowdown}
            onChange={serviceSlowdown => setCfg({ ...cfg, serviceSlowdown })} />
          {/* Sürekli teknik hata (5xx) yükselişi (v0.10.1518 kuralının vidaları). */}
          <ErrorRampSubsection value={cfg.errorRamp}
            onChange={errorRamp => setCfg({ ...cfg, errorRamp })} />
          {/* v0.10.1483 — takvim etkileri: özel günler mevsimsel baseline'ı ve
              tatil/yarım günde istek hızı düşüşü terfisini değiştirir; aynı Kaydet. */}
          <CalendarSubsection value={cfg.calendar}
            onChange={calendar => setCfg({ ...cfg, calendar })} />
          {/* v0.10.1061 — log_template_new dedektörü: VARSAYILAN KAPALI (operatör
              onaylı: "Bu log anomalileri de false pozitif geliyor"). Alan yoksa
              kapalı okunur; kayıt açık boolean gönderir. log_pattern etkilenmez. */}
          <div className={SEP}>
            <CheckboxField aria-label="Yeni log deseni anomalileri"
              checked={cfg.logTemplateNew === true}
              onCheckedChange={v => setCfg({ ...cfg, logTemplateNew: v })}
              label="Yeni log deseni anomalileri"
              hint={<>
                Kapalı (varsayılan): ilk kez görülen log desenleri anomali açmaz; log desen
                sıçramaları etkilenmez.
              </>} />
          </div>
          {/* v0.10.1478 — RED serisi kaynağı (auto = VM örneklenmemiş, span = eski). */}
          {/* v0.10.1489 — alarm kurallarının kaynağı (alertSource) aynı bölümde. */}
          <REDSourceSubsection source={cfg.redSource} env={cfg.redSourceEnv} alertSource={cfg.alertSource}
            onChange={(redSource, redSourceEnv) => setCfg({ ...cfg, redSource, redSourceEnv })}
            onAlertSourceChange={alertSource => setCfg({ ...cfg, alertSource })} />
          {/* v0.10.700 — kök neden hipotezinde zamansal çarpan. Gölge
              (varsayılan): faktör ve gerekçe adaya yazılır, sıra değişmez;
              açık: skor = yapısal × (0.5 + 0.5·t). Önce gölgede izle. */}
          <div className={SEP}>
            <SelectField label="Zamansal kök neden sıralaması" className="max-w-[420px]"
              value={cfg.temporalRanking === 'on' ? 'on' : 'shadow'}
              onChange={e => setCfg({ ...cfg, temporalRanking: e.target.value === 'on' ? 'on' : 'shadow' })}
              hint={<>
                Aday servisin 5 dk hata-oranı serisi tetikleyiciyle birlikte hareket ediyor mu, önde mi
                (0–10 dk). Gölgede yalnız aday satırında gerekçe olarak görünür; açıkken uyumsuz aday
                yapısal skorunun yarısını korur, tam uyumlu aday tamamını. Varsayılan gölge.
              </>}>
              <option value="shadow">Gölge — ölç, sıralamayı değiştirme</option>
              <option value="on">Açık — skor = yapısal × zamansal</option>
            </SelectField>
          </div>
          <div className={SEP}>
            <CheckboxField checked={cfg.attachToIncident !== false}
              onCheckedChange={v => setCfg({ ...cfg, attachToIncident: v })}
              label={<>Dedektör problemini otomatik incident&apos;a bağla</>}
              hint={<>
                Açıkken, dedektörün açtığı her metrik problemi servis ve şiddetine göre
                aktif bir incident&apos;a iliştirilir (yoksa yenisi açılır). Varsayılan
                açık &mdash; bugüne kadarki davranış budur.
                <br />
                <b>Kapatırsanız problem yine açılır ve bildirim yine gider</b>; yalnız
                incident açılmaz. Bu vida &laquo;bana haber verme&raquo; değil,
                &laquo;bunu olay yönetimine sokma&raquo; demektir.
              </>} />
          </div>

          {/* v0.10.1039 — kapsam notu düzeltildi: batch kalıpları metrik
              dedektörünün DIŞINA da (iz operasyon anomalileri, hacim sıçraması
              uyarısı) uzanıyor; eski "yalnız metrik dedektörü" cümlesi yanlış
              kalırdı. */}
          <div className={cn(NOTE, 'mt-4')}>
            Bu eşikler <b>metrik anomali dedektörünü</b> ve davranış motorunu bağlar;
            <b> batch servis ad kalıpları</b> ayrıca iz (trace) operasyon hata ve gecikme
            anomalilerini ve servis hacmi sıçraması uyarısını da etkiler. Elle kurduğunuz alarm kuralları
            kendi eşiklerini kullanır; log/iz desen anomalilerinin Problem&apos;e terfisi
            yukarıdaki terfi hattından geçer. Sertleştirmek açık kayıtları silmez: yeni
            tespit üretilmez, mevcut anomaliler kendi bantlarına dönünce kapanır. Batch
            kalıbına giren servislerin açık istek hızı ve hacim sıçraması problemleri ise
            bir sonraki taramada kapanır (davranış değişiminden terfi edenler, olay
            yenilenmeyi bıraktıktan ~10 dakika sonra).
          </div>

          <SaveRow label="Kaydet" onClick={save} busy={busy} flash={flash} />
        </>
      )}
    </div>
  );
}

// ── Davranış değişimi (v0.9.936) ────────────────────────────────
//
// "Dedektör hassasiyeti"nin ALT BÖLÜMÜ, kardeş bölüm değil: aynı
// blob'un (anomaly_sensitivity) parçası ve aynı Kaydet düğmesiyle
// kaydediliyor. Operatör buraya tek bir soruyla geliyor — "ne zaman
// olay sayılsın?" — ve cevabın iki yarısı var: ANİ sapma (üstteki
// alanlar) ve KALICI değişim (burası).
//
// DÜRÜST KAPSAM METNİ: bu aşamada LLM YOK. Motor tamamen istatistiksel
// (medyan + MAD, haftanın saati baseline'ı). Bunu yazmak zorundayız,
// çünkü "davranış motoru" adı kolayca "yapay zekâ karar veriyor" diye
// okunur ve operatör eşikleri neden ayarladığını anlamaz.
function BehaviorSubsection({ behavior, onChange }: {
  behavior?: AnomalyBehaviorConfig;
  onChange: (b: AnomalyBehaviorConfig) => void;
}) {
  // Sunucu Normalize'da daima dolduruyor; bu düşüş yalnız bu sürümden
  // ESKİ bir backend'e bakan bir sekme için. Boş kutu göstermek yerine
  // varsayılanlarla çiziyoruz — kaydedilirse sunucu zaten kelepçeler.
  const b: AnomalyBehaviorConfig = behavior ?? {
    enabled: true, seasonalZ: 4, regimeRatio: 1.5,
    dwellSeasonal: 3, dwellRegime: 6, maxCandidatesPerTick: 50,
    minSamplesPerBucket: 12, minBucketRepeats: 3,
    minP99Ms: 200, minErrorRatePct: 1, spikyBandFactor: 1.5,
    highVolumeErrorFloor: true, highVolumeErrorsPerSec: 1, highVolumeMinErrorRatePct: 0.2,
  };
  // v0.10.1521 — hacim farkında hata tabanı; yok = AÇIK.
  const hvOn = b.highVolumeErrorFloor !== false;
  // `!== false` ŞART: alan yoksa (eski satır) motor AÇIKtır.
  const on = b.enabled !== false;
  const set = (patch: Partial<AnomalyBehaviorConfig>) => onChange({ ...b, ...patch });

  return (
    <div className={cn(SEP, 'mb-[18px]')}>
      <div className={cn(SUBHEAD, 'mb-1.5')}>
        Davranış değişimi
      </div>
      <p className={cn(LEAD, 'mb-3')}>
        Yukarıdaki eşikler <b>ani</b> sapmayı ölçer (son 5 dakika, 24 saatlik geçmiş).
        Bu bölüm <b>kalıcı</b> değişimi ölçer: her servisin hata oranı, P99 gecikmesi ve
        istek hızı <b>haftanın aynı saatine</b> (168 kova, son 28 gün) göre
        karşılaştırılır. Deploy sonrası P99&apos;un 120 ms&apos;ten 190 ms&apos;e
        <i> oturması</i> 24 saat içinde &laquo;yeni normal&raquo; olur ve ani-sapma
        dedektörü onu ertesi gün göremez &mdash; bu motor tam olarak onu söyler.
      </p>
      <p className={cn(LEAD, BOXED, 'mb-3.5')}>
        <b>Yöntem:</b> tamamen istatistiksel &mdash; medyan + MAD ile sağlam z-skoru ve
        oran karşılaştırması. <b>Bu aşamada yapay zekâ kullanılmıyor.</b> Bulgular
        /anomalies akışına <span className="font-mono">behavior_change</span> olarak düşer ve
        yukarıdaki <b>terfi kapısından</b> geçer; kendiliğinden Problem açmaz.
        Yukarıdaki metrik tabanları (mutlak değer / fark / MAD / hacim) bu motora da
        aynen uygulanır &mdash; 1,90 ms&apos;ten 2,90 ms&apos;e kalıcı bir kayma
        istatistiksel olarak gerçektir ve kimseyi ilgilendirmez.
      </p>

      <CheckboxField className="mb-3.5" checked={on} onCheckedChange={v => set({ enabled: v })}
        label="Kalıcı davranış değişimlerini ara" />

      <div data-disabled={!on || undefined} className={cn('ml-3 grid gap-2.5', DIM)}>
        <Num label="Rejim kayması oranı (× baseline)" min={1.05} max={100} step={0.05}
          value={b.regimeRatio}
          onChange={e => set({ regimeRatio: Number(e.target.value) })}
          disabled={!on}
          hint={<>
            Bu kadar katına çıkan (ya da bu kadar katı altına düşen) ve orada
            <b> kalan</b> bir metrik &laquo;davranış değişti&raquo; sayılır.
            Varsayılan <b>1,5</b>. Düşürmek daha çok, yükseltmek daha az bulgu demek.
          </>} />

        <Num label="Kalıcılık — rejim (kaç 5-dakikalık dilim)" min={1} max={24} step={1}
          value={b.dwellRegime}
          onChange={e => set({ dwellRegime: Number(e.target.value) })}
          disabled={!on}
          hint={<>
            Kaymanın kesintisiz sürmesi gereken dilim sayısı. Varsayılan <b>6</b>
            {' '}({(b.dwellRegime || 6) * 5} dakika). <b>Geçici sıçramayı kalıcı
            kaymadan ayıran tek vida budur</b> &mdash; kısaltırsanız ani-sapma
            dedektörüyle çakışmaya başlar.
          </>} />

        <Num label="Mevsimsel sapma eşiği (σ)" min={1} max={50} step={0.5}
          value={b.seasonalZ}
          onChange={e => set({ seasonalZ: Number(e.target.value) })}
          disabled={!on}
          hint={<>
            Rejim eşiğini geçmeyen ama kendi haftalık kovasından çok uzak değerler
            için. Varsayılan <b>4,0</b> &mdash; üstteki kritik z&apos;den (6,0) düşük
            olması normaldir: burada gürültünün mevsimsel bileşeni baseline&apos;dan
            zaten çıkarılmış.
          </>} />

        <Num label="Kalıcılık — mevsimsel (kaç dilim)" min={1} max={24} step={1}
          value={b.dwellSeasonal}
          onChange={e => set({ dwellSeasonal: Number(e.target.value) })}
          disabled={!on}
          hint={<>
            Varsayılan <b>3</b> ({(b.dwellSeasonal || 3) * 5} dakika). Rejim
            kalıcılığından küçük olmalı; kalıcı bir kayma ikisini de tetiklerse
            <b> rejim</b> kazanır, yani aynı olay iki satır olarak görünmez.
            {b.dwellSeasonal >= b.dwellRegime && (
              <span className={WARN}>
                Rejim kalıcılığından küçük değil: iki sinyalin anlamı çakışır.
              </span>
            )}
          </>} />

        <Num label="Tik başına en fazla bulgu" min={1} max={5000} step={10}
          value={b.maxCandidatesPerTick}
          onChange={e => set({ maxCandidatesPerTick: Number(e.target.value) })}
          disabled={!on}
          hint={<>
            Fırtına koruması: filo geneli bir olayda (ortak bağımlılık çöktü) her
            servis birden bulgu üretir. Tavan <b>en güçlü</b> N tanesini geçirir;
            kesilenler kaybolmaz, bir sonraki taramada hâlâ değişikse yine sıraya
            girerler. Varsayılan <b>50</b>.
          </>} />

        {/* v0.9.957 — örnek-kıtlığı kapısı. İKİ vida çünkü iki farklı
            soru: "kaç örneğim var" ve "kaç FARKLI GÜN gördüm". */}
        <Num label="Kova başına en az örnek" min={1} max={576} step={1}
          value={b.minSamplesPerBucket ?? 12}
          onChange={e => set({ minSamplesPerBucket: Number(e.target.value) })}
          disabled={!on}
          hint={<>
            Bir haftanın-saati kovasının baseline sayılması için gereken en az
            5&#8209;dakikalık örnek. Altında o kova <b>sessiz</b> kalır.
            Varsayılan <b>12</b>.
          </>} />

        <Num label="Kova başına en az farklı gün" min={1} max={4} step={1}
          value={b.minBucketRepeats ?? 3}
          onChange={e => set({ minBucketRepeats: Number(e.target.value) })}
          disabled={!on}
          hint={<>
            Örnek <b>sayısı</b> yetmez, <b>çeşitliliği</b> de gerekir: 24 örneğin
            hepsi iki günden geliyorsa haftadan haftaya yayılım iki gözlemden
            kestiriliyor demektir, sapma ölçüsü patlar ve normal dalgalanmalar
            bulgu görünür. 28 günlük pencerede en fazla <b>4</b> tekrar olur;
            varsayılan <b>3</b>.
          </>} />

        {/* v0.10.1070 — mutlak taban + sıçramalı geçmiş toleransı. */}
        <Num label="P99 için en düşük değer (ms)" min={1} max={60000} step={10}
          value={b.minP99Ms ?? 200}
          onChange={e => set({ minP99Ms: Number(e.target.value) })}
          disabled={!on}
          hint={<>P99 bu değerin altındaysa oran ne olursa olsun bulgu üretilmez. Varsayılan <b>200</b>.</>} />

        <Num label="Hata oranı için en düşük değer (%)" min={0.01} max={100} step={0.1}
          value={b.minErrorRatePct ?? 1}
          onChange={e => set({ minErrorRatePct: Number(e.target.value) })}
          disabled={!on}
          hint={<>Hata oranı bu yüzdenin altındaysa oran ne olursa olsun bulgu üretilmez. Varsayılan <b>1</b>.</>} />

        {/* v0.10.1521 — hacim farkında hata tabanı (v0.10.1518 errorRamp'ın ikizi). */}
        <div>
          <CheckboxField aria-label="Yüksek hacimde düşük hata tabanı"
            checked={hvOn} disabled={!on}
            onCheckedChange={v => set({ highVolumeErrorFloor: v })}
            label="Yüksek hacimde düşük hata tabanı"
            hint={<>
              Açık (varsayılan): yükselen hata oranının penceresindeki <b>her</b> dilim saniyede en az{' '}
              {b.highVolumeErrorsPerSec ?? 1} teknik hata taşıyorsa, %1 tabanı yerine aşağıdaki taban kullanılır
              &mdash; 1000 istek/sn&apos;de 5 hata/sn yalnız %0,5&apos;tir ama kendi haftalık normalinin çok
              üstündeyse gerçek bir olaydır. Oran, kalıcılık, sıçrama bandı kapıları aynen geçerli; düşük hacimli
              servisler %1 tabanında kalır.
            </>} />
          <div data-disabled={!hvOn || undefined} className={cn('mt-2 ml-6 flex flex-wrap gap-3', DIM)}>
            <Num className="w-auto" label="Hata hızı eşiği (hata/sn)" min={0.05} max={100000} step={0.5}
              aria-label="Yüksek hacim: hata hızı eşiği"
              value={b.highVolumeErrorsPerSec ?? 1}
              onChange={e => set({ highVolumeErrorsPerSec: Number(e.target.value) })}
              disabled={!on || !hvOn}
              hint={<>0,05–100000, varsayılan <b>1</b>.</>} />
            <Num className="w-auto" label="Yüksek hacimde hata oranı tabanı (%)" min={0.01} max={100} step={0.05}
              aria-label="Yüksek hacim: hata oranı tabanı"
              value={b.highVolumeMinErrorRatePct ?? 0.2}
              onChange={e => set({ highVolumeMinErrorRatePct: Number(e.target.value) })}
              disabled={!on || !hvOn}
              hint={<>0,01–100, varsayılan <b>0,2</b>.</>} />
          </div>
        </div>

        <Num label="Sıçramalı geçmiş toleransı (× geçmiş p90)" min={1} max={10} step={0.1}
          value={b.spikyBandFactor ?? 1.5}
          onChange={e => set({ spikyBandFactor: Number(e.target.value) })}
          disabled={!on}
          hint={<>Yükseliş, o saatin geçmişteki p90 değerinin bu katını aşmazsa (düzenli kısa sıçramalar) bulgu sayılmaz. Varsayılan <b>1,5</b>.</>} />
      </div>

      <div className={cn(NOTE, 'mt-3 ml-3')}>
        Yeni bir servis ilk dört haftasında <b>sessiz</b> kalır: baseline&apos;ı
        yoksa motor bulgu üretmez (&laquo;veri yok&raquo; bir anomali değildir).
        Yeni <b>kurulan</b> bir Coremetry de aynı sebeple sessizdir; kaç kovanın
        bu yüzden atlandığını ve motorun tarama süresini{' '}
        <span className="font-mono">/admin/stats</span> &rarr; Davranış motoru
        kartından izleyebilirsiniz.
      </div>
    </div>
  );
}

// ── Age-based escalation (v0.9.248) ─────────────────────────────
//
// Lives on this tab rather than its own because an operator arrives
// here asking one question — "why am I getting so many pages?" — and
// the two halves of the answer are the promotion gate above and this
// ladder. It is NOT scoped to anomalies though: every open Problem
// climbs it, whichever rule opened it. The copy says so, because
// putting it under a heading called "Anomaly auto-promotion" would
// otherwise imply a narrower blast radius than it has.
//
// The windows were hard-coded 15 min / 30 min until now, which meant
// tightening promotion barely helped: anything that got through
// escalated itself to critical half an hour later and re-fired the
// notify channel on the way (escalateStaleProblems calls
// SendProblemAlert on every bump).
function EscalationSection() {
  type Esc = { enabled: boolean; infoToWarningSec: number; warningToCriticalSec: number };
  const [cfg, setCfg] = useState<Esc | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    api.getProblemEscalation()
      .then(c => setCfg(c))
      .catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);

  const save = async () => {
    if (!cfg) return;
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putProblemEscalation(cfg);
      setCfg(saved);
      setFlash({ kind: 'ok', text: 'Saved — next evaluator tick picks it up automatically.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const mins = (sec: number) => `${Math.round(sec / 60)} min`;

  return (
    <div>
      <h2 className={H2}>Age-based escalation</h2>
      <p className={LEAD}>
        An open Problem nobody acks climbs the severity ladder on its own:
        info &rarr; warning &rarr; critical. Each bump re-fires the notify channel, so
        severity-gated pagers hear about it again. This applies to <b>every</b> Problem,
        not just promoted anomalies &mdash; turn it off if &quot;still open after 30
        minutes&quot; is normal for your fleet.
      </p>

      {!cfg ? (
        flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />
      ) : (
        <>
          <CheckboxField className="mb-4" checked={cfg.enabled}
            onCheckedChange={v => setCfg({ ...cfg, enabled: v })}
            label="Escalate open Problems as they age" />

          <div data-disabled={!cfg.enabled || undefined} className={cn(STACK, DIM)}>
            <Num label="info → warning after (seconds)" min={60} max={604800} step={60}
              value={cfg.infoToWarningSec}
              onChange={e => setCfg({ ...cfg, infoToWarningSec: Number(e.target.value) })}
              disabled={!cfg.enabled}
              hint={<>Currently {mins(cfg.infoToWarningSec)}. Default 900s (15 min).</>} />

            <Num label="warning → critical after (seconds)" min={cfg.infoToWarningSec} max={604800} step={60}
              value={cfg.warningToCriticalSec}
              onChange={e => setCfg({ ...cfg, warningToCriticalSec: Number(e.target.value) })}
              disabled={!cfg.enabled}
              hint={<>
                Currently {mins(cfg.warningToCriticalSec)}. Default 1800s (30 min).
                Must be &ge; the info &rarr; warning window.
              </>} />
          </div>

          <SaveRow label="Save" onClick={save} busy={busy} flash={flash} />
        </>
      )}
    </div>
  );
}

// ── Exception triyajı (v0.9.775) ────────────────────────────────
//
// Aynı tabda, çünkü operatör buraya tek bir soruyla geliyor: "bir şey
// ne zaman ve ne kadar süre gündemimde kalsın?" Üstteki iki bölüm
// Problem tarafını (promosyon kapısı + yaşa göre tırmanma) ayarlıyor;
// bu bölüm exception gruplarının P1/P2/P3 basamağını ayarlıyor.
//
// Neden bir vida gerekti: pencere üç kez sabit olarak öteledi
// (v0.9.627 5dk→1sa, v0.9.699 uçurum→basamak, v0.9.775 1sa→4sa) ve
// operatör her seferinde bir çentik ötede aynı duvara çarptı. "Ne kadar
// taze hâlâ acildir" filoya, nöbet devrine ve ekrana bakma sıklığına
// bağlı — kodda tahmin edilecek bir sayı değil.
function ExceptionTriageSection() {
  const [cfg, setCfg] = useState<ExceptionTriageConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    api.getExceptionTriage()
      .then(c => setCfg(c))
      .catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);

  const save = async () => {
    if (!cfg) return;
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putExceptionTriage(cfg);
      setCfg(saved);
      setFlash({ kind: 'ok', text: 'Kaydedildi — /inbox bir sonraki okumada yeni pencereleri kullanır.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  // Basamak ters dönerse kaydetmeden ÖNCE söyle: backend zaten 400
  // döner, ama operatör hatayı alanın yanında görmeli.
  const inverted = !!cfg && cfg.p2SameDayHours < cfg.p1FreshHours;

  return (
    <div>
      <h2 className={H2}>Exception triyajı</h2>
      <p className={LEAD}>
        Bir exception grubunun /inbox&apos;taki önceliği iki şeyden çıkar: <b>şiddet</b> (hacim
        ve dakikadaki hız) ve <b>tazelik</b> (en son ne zaman görüldü). Şiddet zamanla
        silinmez, yalnız aciliyeti düşer — bu yüzden basamak: taze bir patlama P1,
        aynı gün içindeki P2, sonrası P3. Aşağıdaki pencereler o basamağın yerini
        belirler.
      </p>

      {!cfg ? (
        flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />
      ) : (
        <>
          <div className={STACK}>
            <Num label="P1 tazelik penceresi (saat)" min={1} max={168} step={1}
              value={cfg.p1FreshHours}
              onChange={e => setCfg({ ...cfg, p1FreshHours: Number(e.target.value) })}
              hint={<>
                Son görülmesi bu kadar saat içinde olan bir patlama P1 kalır. Aynı
                pencere, patlama sayılmayan ama 100+ olaylık grupların P2 kapısı için
                de kullanılır. Varsayılan 4 saat &mdash; Problem tarafındaki
                &laquo;4 saattir açık critical &rarr; P1&raquo; kuralıyla simetrik.
              </>} />

            {/* v0.9.1188 — PATLAMA kapıları. Pencerelerin ÖNÜNDE duruyorlar
                ve sırası bilinçli: bir grup önce "patlama mı" diye ölçülüyor,
                sonra "ne kadar taze" diye. Operatör-bildirimli dördüncü vaka
                tam da burada takılmıştı (180,5/dk, gömülü kapı 200/dk). */}
            <Num label="Patlama eşiği (olay/dakika)" min={1} max={100000} step={10}
              value={cfg.burstMinRate}
              onChange={e => setCfg({ ...cfg, burstMinRate: Number(e.target.value) })}
              hint={<>
                Bir grup, kendi ömrü boyunca dakikada bu kadar olay üretiyorsa
                &laquo;patlama&raquo; sayılır ve aşağıdaki pencerelerden geçer.
                Varsayılan 100/dk (saniyede ~1,7) &mdash; sağlıklı bir servisin
                tekrarlayan uyarısı tipik olarak bunun altında kalır. Eşiği
                yükseltmek P1/P2 selini keser, düşürmek daha çok grubu yukarı taşır.
              </>} />

            <Num label="Patlama taban hacmi (olay)" min={1} max={10000000} step={100}
              value={cfg.burstMinTotal}
              onChange={e => setCfg({ ...cfg, burstMinTotal: Number(e.target.value) })}
              hint={<>
                Hız ne olursa olsun gereken toplam olay. Hız tek başına yetmez:
                5 saniyede 20 olay da 240/dk eder ama patlama değildir.
                Varsayılan 1000.
              </>} />

            <Num label="P1 hacim eşiği (olay)" min={1} max={10000000} step={50}
              value={cfg.p1MinOccurrences}
              onChange={e => setCfg({ ...cfg, p1MinOccurrences: Number(e.target.value) })}
              hint={<>
                Patlama sayılmayan ama bu kadar olay biriktirmiş bir grup, P1
                tazelik penceresi içinde görülmüşse P1 olur. Varsayılan 500.
                <b> Bu kapı v0.9.1189&apos;a kadar beş dakikalık bir uçurumun
                ardındaydı</b> &mdash; 888 olaylık bir grup 1sa12dk sonra P1
                olamıyordu; artık yukarıdaki P1 penceresini kullanıyor.
              </>} />

            {/* v0.10.1092 — Oracle hata tablosu gruplarının (Exceptions'taki
                "Oracle" rozetli satırlar) AYRI hacim eşiği: durgun akış ilk gün
                tüm sayfayı P1 yapmasın. Patlamanın hacim tabanı da bu sayıdır. */}
            <Num label="Oracle grubu P1 eşiği (son 1 saat, olay)" min={1} max={10000000} step={500}
              value={cfg.oracleP1MinOccurrences ?? 5000}
              onChange={e => setCfg({ ...cfg, oracleP1MinOccurrences: Number(e.target.value) })}
              hint={<>
                Oracle hata tablosu gruplarında (kod × operasyon) P1 yalnız son 1
                saat bu eşiği aşıp önceki saatin en az 3 katıysa (patlama) ya da grup
                yeniyse olur; yapışkan hacim P1&apos;i Oracle&apos;a uygulanmaz &mdash;
                satırlar dakikada binlerce akar. Varsayılan 5000.
              </>} />

            {/* v0.9.1194 — FIRTINA. Tekil eşiklerin göremediği sinyal:
                birden çok servisin EŞ-ZAMANLI patlaması (tipik kök: ortak
                bağımlılık). Dedektör anomali tikinde koşar; satır critical
                bildirim kanallarına normal Problem gibi düşer. */}
            <Num label="Fırtına penceresi (dakika)" min={1} max={1440} step={1}
              value={cfg.stormWindowMinutes}
              onChange={e => setCfg({ ...cfg, stormWindowMinutes: Number(e.target.value) })}
              hint={<>
                Bu pencerede <b>ilk kez görülen</b> exception grupları sayılır
                (yalnız yeni — kronik gürültü fırtına kanıtı değildir).
                Varsayılan 10 dakika.
              </>} />

            <Num label="Fırtına eşiği (farklı servis)" min={2} max={1000} step={1}
              value={cfg.stormMinServices}
              onChange={e => setCfg({ ...cfg, stormMinServices: Number(e.target.value) })}
              hint={<>
                Pencerede yeni grup açan farklı servis sayısı bu eşiği bulunca
                tek bir <b>P1 &laquo;Exception fırtınası&raquo;</b> problemi açılır ve
                critical bildirim kanallarına düşer. Varsayılan 5.
              </>} />

            {/* v0.10.1588 — incident bağlama (Davis paritesi FAZ 0.5 / D). Alan
                yoksa AÇIK okunur (`!== false`); kayıt açık boolean gönderir. */}
            <CheckboxField aria-label="Fırtına incident'a bağlansın"
              checked={cfg.stormAttachToIncident !== false}
              onCheckedChange={v => setCfg({ ...cfg, stormAttachToIncident: v })}
              label={<>Exception fırtınası incident&apos;a bağlansın (yoksa kendi incident&apos;ı açılır)</>} />
            <CheckboxField aria-label="Tekil exception problemi açık incident'a katılsın"
              checked={cfg.singleGroupJoinIncident !== false}
              onCheckedChange={v => setCfg({ ...cfg, singleGroupJoinIncident: v })}
              label={<>
                Ölümcül / paylaşılan exception problemi, aynı servis ya da komşusunda AÇIK
                incident varsa ona katılsın (kendi incident&apos;ını açmaz)
              </>} />

            <Num label="P2 aynı-gün penceresi (saat)" min={cfg.p1FreshHours} max={720} step={1}
              value={cfg.p2SameDayHours}
              onChange={e => setCfg({ ...cfg, p2SameDayHours: Number(e.target.value) })}
              hint={<>
                P1 penceresi kapandıktan sonra patlama bu süre boyunca P2
                (&laquo;bugün&raquo;) kalır, sonrası P3. Varsayılan 24 saat.
                P1 penceresinden küçük olamaz.
                {inverted && (
                  <span className={WARN}>
                    P1 penceresinden küçük: basamak ters döner, taze bir patlama
                    P1&apos;i atlayıp doğrudan P3&apos;e düşerdi.
                  </span>
                )}
              </>} />

            <Num label="Sessiz grubu otomatik çözme (saat)" min={1} max={720} step={1}
              value={cfg.staleResolveHours}
              onChange={e => setCfg({ ...cfg, staleResolveHours: Number(e.target.value) })}
              hint={<>
                Bu kadar süredir yeni olay görmeyen açık/ack&apos;li bir grup
                kendiliğinden <b>resolved</b>&apos;a geçer. Varsayılan 24 saat.
                Süpürme yaklaşık 6 dakikada bir koşar.
              </>} />
          </div>

          <SaveRow label="Kaydet" onClick={save} disabled={inverted} busy={busy} flash={flash} />
        </>
      )}
    </div>
  );
}

// ── Alert problemi önceliği (v0.9.838) ──────────────────────────
//
// Operatör-bildirimli: "hâlâ çok fazla alert rule'dan P1 geliyor".
// Prod'da 29 açık critical'in 22'si P1 rozetiyle duruyordu; örnek bir
// error_rate problemi 3.93/1.31 = 3.0× oranıyla gömülü "2× ihlal"
// kapısından geçip otomatik P1 oluyordu. P1'in "her şeyi bırak" anlamı,
// listenin dörtte üçü P1 olduğunda kalmıyor.
//
// Üstteki Exception triyajı bölümünün KARDEŞİ ama AYRI hat: o exception
// gruplarının basamağını, bu alert kurallarının (threshold + SLO burn)
// basamağını ayarlıyor. Aynı tabda çünkü operatör buraya tek bir soruyla
// geliyor: "ne zaman ve ne kadarı gece kaldırsın?"
function ProblemPrioritySection() {
  const [cfg, setCfg] = useState<ProblemPriorityConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    api.getProblemPriority()
      .then(c => setCfg(c))
      .catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);

  const save = async () => {
    if (!cfg) return;
    setBusy(true); setFlash(null);
    try {
      // v0.10.1081 — sloBurnProblems daima AÇIK boolean gider (alan yoksa
      // false): sunucu gövdeyi kayıtlı değerin üstüne çözer.
      const saved = await api.putProblemPriority({ ...cfg, sloBurnProblems: cfg.sloBurnProblems === true });
      setCfg(saved);
      setFlash({ kind: 'ok', text: 'Kaydedildi — /inbox ve bildirimler bir sonraki okumada yeni merdiveni kullanır.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  // Backend zaten 400 döner, ama operatör hatayı alanın yanında görmeli.
  const ratioBad = !!cfg && (cfg.bigBreachRatio < 1.1 || cfg.bigBreachRatio > 100);
  const staleBad = !!cfg && (cfg.staleCriticalHours < 0 || cfg.staleCriticalHours > 720);

  return (
    <div>
      <h2 className={H2}>Alert problemi önceliği</h2>
      <p className={LEAD}>
        <b>P1</b> = critical + eşiğin N katı ihlal, tamamen kayıp (0/X) veya N saattir açık.{' '}
        <b>P2</b> = düz critical, ya da warning + büyük ihlal.{' '}
        <b>P3</b> = geri kalan (sabit warning, info seviyesi).{' '}
        Aşağıdaki iki değer o merdivenin yerini belirler. <b>Değerler bildirim
        min-öncelik süzgecini de besler</b> — katı sıkmak, P1&apos;e ayarlı bir
        kanalın daha az mesaj göndermesi demektir.
      </p>

      {!cfg ? (
        flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />
      ) : (
        <>
          <div className={STACK}>
            <Num label="Büyük ihlal katı (× eşik)" min={1.1} max={100} step={0.1}
              value={cfg.bigBreachRatio}
              onChange={e => setCfg({ ...cfg, bigBreachRatio: Number(e.target.value) })}
              hint={<>
                Değer eşiğin bu kadar katına çıktığında (ya da &laquo;&lt;&raquo;
                kurallarında bu kadar katı altına düştüğünde) ihlal
                &laquo;büyük&raquo; sayılır: critical + büyük ihlal &rarr; P1,
                warning + büyük ihlal &rarr; P2. Varsayılan <b>2.0</b>.
                Yükseltmek P1 sayısını azaltır &mdash; örnek: 3.93/1.31 = 3.0×
                bir problem 2.0&apos;da P1, 4.0&apos;da P2 olur. Alt sınır 1.1;
                1.0 &laquo;eşiği aşan her şey büyük ihlal&raquo; demek olurdu.
                {ratioBad && (
                  <span className={WARN}>
                    1.1 ile 100 arasında olmalı.
                  </span>
                )}
              </>} />

            <Num label="Bayat-critical terfisi (saat, 0 = kapalı)" min={0} max={720} step={1}
              value={cfg.staleCriticalHours}
              onChange={e => setCfg({ ...cfg, staleCriticalHours: Number(e.target.value) })}
              hint={<>
                Bu kadar saattir açık (çözülmemiş) bir critical, başka hiçbir
                tetikleyici olmasa da P1&apos;e çıkar. Varsayılan <b>4 saat</b>.
                <b> 0 yazmak bu terfiyi tamamen kapatır</b> &mdash; uzun süre açık
                kalmak &laquo;ilgilenilmedi&raquo; demek, &laquo;şu an daha
                acil&raquo; demek değil; P1 selini kesmek için ilk vida bu
                olabilir. Kapatmak P1&apos;i büsbütün kapatmaz: ihlal katı ve
                tamamen kayıp kapıları çalışmaya devam eder.
                {staleBad && (
                  <span className={WARN}>
                    0 ile 720 arasında olmalı.
                  </span>
                )}
              </>} />

            {/* v0.10.1072 — inbox görünüm kuralının dar istisna listesi; salt-okunur
                (değiştirmek PUT /api/settings/problem-priority ile, bilinçli). */}
            <div className="flex flex-col gap-1">
              <div className="text-[length:var(--fs-sm)] leading-tight text-(--text2)">
                Inbox&apos;ta kaynak önceliğini koruyanlar (salt-okunur)
              </div>
              <div className="flex flex-wrap gap-1.5 text-[length:var(--fs-sm)]">
                {(cfg.inboxKeepSourcePriority ?? []).length === 0
                  ? <span className="text-(--text3)">yok — exception dışı her satır inbox&apos;ta P3</span>
                  : (cfg.inboxKeepSourcePriority ?? []).map(p => <code key={p}>{p}</code>)}
              </div>
              <div className={NOTE}>
                Exception dışı satırlar inbox&apos;ta P3 görünür; kaynak kimliği
                (kural id&apos;si, incident için <code>incident:&lt;önem&gt;</code>)
                bu kalıplardan birine uyan satır kendi P1/P2&apos;sini korur ve
                gerekçesinde &laquo;kaynak önceliği korundu&raquo; yazar.
                <code>*</code> herhangi bir dizi, eşleşme tam.
              </div>
            </div>

            {/* v0.10.1081 — SLO burn-rate Problem'leri: VARSAYILAN KAPALI (operatör:
                "SLO burn rate problem olmasın, çıkar. SLO ile ilgili beklentim yok.").
                Alan yoksa kapalı okunur; kayıt açık boolean gönderir. */}
            <CheckboxField aria-label="SLO burn-rate problemleri"
              checked={cfg.sloBurnProblems === true}
              onCheckedChange={v => setCfg({ ...cfg, sloBurnProblems: v })}
              label="SLO burn-rate problemleri"
              hint="Kapalı (varsayılan): SLO bütçe yanması Problem, incident ve bildirim açmaz; SLO sayfası ve grafikler aynen çalışır." />
          </div>

          <SaveRow label="Kaydet" onClick={save} disabled={ratioBad || staleBad} busy={busy} flash={flash} />
        </>
      )}
    </div>
  );
}

// v0.10.1073 — db-health varsayılanları (Go chstore.DefaultDBHealth ikizi;
// dbSlowQuery.test.ts pinler).
const DB_HEALTH_DEFAULTS: DBHealthConfig = { enabled: true, errorPct: 5, p99Ms: 2000, p99RiseFactor: 3, minCallerCalls: 10, minCalls: 100, minCallers: 2, maxNewPerTick: 20, minErrorCount: 50, errorRiseFactor: 3, minCallerErrors: 10, excludeSystems: ['couchbase'] };

// v0.10.1084 — "Hariç sistemler" metni → liste (virgül ya da boşlukla ayrılmış).
// Son biçim sunucuda (küçük harf, kırpma, tekrarsız); burada yalnız ayrıştırma.
function parseExcludeSystems(text: string): string[] {
  return text.split(/[,\s]+/).map(s => s.trim()).filter(Boolean);
}

// ── DB yavaş sorgu dedektörü (v0.10.325, operatör isteği) ────────────
// EscalationSection ile aynı anatomi: yükle → düzenle → kaydet; evaluator
// bir sonraki tik'te okur. Yönlendirme mevcut takım kuralı (DB sahibi + SRE).
function DBSlowQuerySection() {
  const [cfg, setCfg] = useState<DBSlowQueryConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // v0.10.1084 — hariç sistem kutusunun yazılan metni (virgül/boşluk yazarken
  // listeden yeniden üretilip silinmesin); kayıttan sonra sunucunun normalize
  // listesine döner (null).
  const [exText, setExText] = useState<string | null>(null);
  useEffect(() => {
    api.getDBSlowQuery().then(c => setCfg(c)).catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);
  const save = async () => {
    if (!cfg) return;
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putDBSlowQuery(cfg);
      setCfg(saved);
      setExText(null);
      setFlash({ kind: 'ok', text: 'Saved — next evaluator tick picks it up automatically.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };
  const num = (k: keyof DBSlowQueryConfig) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setCfg(c => (c ? { ...c, [k]: Number(e.target.value) } : c));
  // v0.10.1073 — db-health vidaları aynı blobda; eski backend health döndürmezse
  // varsayılanlarla çizilir (sunucu Normalize ile aynı değerler).
  const health: DBHealthConfig = cfg?.health ?? DB_HEALTH_DEFAULTS;
  const setHealth = (patch: Partial<DBHealthConfig>) =>
    setCfg(c => (c ? { ...c, health: { ...(c.health ?? DB_HEALTH_DEFAULTS), ...patch } } : c));
  const hnum = (k: Exclude<keyof DBHealthConfig, 'enabled'>) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setHealth({ [k]: Number(e.target.value) });
  return (
    <div className="mt-7">
      <h2 className={H2}>Slow SQL statements</h2>
      <p className={LEAD}>
        A SQL statement whose 5-minute <b>p95</b> stays above the threshold for the given number of
        consecutive buckets (and clears the execution floor) opens a Problem on the database subject.
        The database owner team and SRE are mailed through the existing team routing — no extra channel.
      </p>
      {!cfg ? (
        flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />
      ) : (
        <>
          <CheckboxField className="mb-4" checked={cfg.enabled}
            onCheckedChange={v => setCfg({ ...cfg, enabled: v })}
            label="Open a Problem for slow SQL statements" />
          <div data-disabled={!cfg.enabled || undefined} className={cn(GRID, DIM)}>
            <Field type="number" label="p95 threshold (ms)" min={100} max={600000} step={100} value={cfg.thresholdMs} onChange={num('thresholdMs')} disabled={!cfg.enabled}
              hint="Default 1000 ms (warning)." />
            <Field type="number" label="critical at p95 (ms)" min={cfg.thresholdMs} max={3600000} step={100} value={cfg.criticalMs} onChange={num('criticalMs')} disabled={!cfg.enabled}
              hint="Default 5000 ms. Must be ≥ the threshold." />
            <Field type="number" label="minimum executions / 5 min" min={1} max={1000000} value={cfg.minExecutions} onChange={num('minExecutions')} disabled={!cfg.enabled}
              hint="Default 20 — below this a bucket is noise." />
            <Field type="number" label="consecutive 5-min buckets" min={1} max={12} value={cfg.forBuckets} onChange={num('forBuckets')} disabled={!cfg.enabled}
              hint="Default 2 (≈10 minutes sustained)." />
            <Field type="number" label="hold before resolve (seconds)" min={0} max={86400} step={60} value={cfg.cooldownSec} onChange={num('cooldownSec')} disabled={!cfg.enabled}
              hint="Default 900 s — an open Problem is not resolved before this age." />
          </div>
          {/* v0.10.1073 — veritabanı sağlık kuralı (db-health): yavaş ifadenin
              yanında, aynı blob ve aynı Kaydet. */}
          <h3 className="mt-[22px] mb-1.5 text-[length:var(--fs-md)] font-semibold">Database health</h3>
          <p className={cn(LEAD, 'mb-3')}>
            Veritabanının hata oranı (mutlak), p99'u (eşik + dünkü aynı saate göre artış) ya da hata sayısı (taban + dünkü
            aynı saate göre artış) 2 ardışık 5 dk kovada aşılır ve en az iki çağıran servis etkilenirse veritabanı öznesinde
            Problem açılır; eşiğin 2 katı critical (P1).
          </p>
          <CheckboxField className="mb-4" checked={health.enabled}
            onCheckedChange={v => setHealth({ enabled: v })}
            label="Open a Problem for unhealthy databases" />
          <div data-disabled={!health.enabled || undefined} className={cn(GRID, DIM)}>
            <Field type="number" label="error rate threshold (%)" min={0.1} max={100} step={0.5} value={health.errorPct} onChange={hnum('errorPct')} disabled={!health.enabled}
              hint="Varsayılan %5 — veritabanı çağrılarının hata yüzdesi." />
            <Field type="number" label="p99 threshold (ms)" min={50} max={600000} step={100} value={health.p99Ms} onChange={hnum('p99Ms')} disabled={!health.enabled}
              hint="Varsayılan 2000 ms — mutlak taban; tek başına yetmez, artış katı da aşılmalı." />
            <Field type="number" label="p99 rise vs. yesterday (×)" min={1} max={100} step={0.5} value={health.p99RiseFactor} onChange={hnum('p99RiseFactor')} disabled={!health.enabled}
              hint="Varsayılan 3 — p99, dün aynı 5 dk'nın bu katı olmalı; dünkü veri yoksa p99 kontrolü kapalı." />
            <Field type="number" label="minimum calls per caller / 5 min" min={1} max={1000000} value={health.minCallerCalls} onChange={hnum('minCallerCalls')} disabled={!health.enabled}
              hint={'Varsayılan 10 — bundan az çağrı yapan servis "etkilenen" sayılmaz.'} />
            <Field type="number" label="minimum calls / 5 min" min={1} max={10000000} value={health.minCalls} onChange={hnum('minCalls')} disabled={!health.enabled}
              hint="Varsayılan 100 — bunun altındaki kova gürültü sayılır." />
            <Field type="number" label="minimum affected callers" min={1} max={50} value={health.minCallers} onChange={hnum('minCallers')} disabled={!health.enabled}
              hint="Varsayılan 2 — batch servisler sayılmaz." />
            <Field type="number" label="max new Problems / tick" min={1} max={200} value={health.maxNewPerTick} onChange={hnum('maxNewPerTick')} disabled={!health.enabled}
              hint="Varsayılan 20 — fırtınada her dakika en çok bu kadar yeni açılış." />
            {/* v0.10.1083 — mutlak hata sayısı kolu: yüksek hacimli veritabanında ORA
                patlaması hata yüzdesine yansımıyordu. */}
            <Field type="number" label="error count threshold / 5 min" min={5} max={10000000} value={health.minErrorCount} onChange={hnum('minErrorCount')} disabled={!health.enabled}
              hint="Varsayılan 50 — yüzdeden bağımsız hata sayısı tabanı; 2 katı critical (P1)." />
            <Field type="number" label="error count rise vs. yesterday (×)" min={1} max={100} step={0.5} value={health.errorRiseFactor} onChange={hnum('errorRiseFactor')} disabled={!health.enabled}
              hint="Varsayılan 3 — hata sayısı dün aynı 5 dk'nın bu katı olmalı; dünkü veri yoksa bu kontrol kapalı." />
            <Field type="number" label="minimum errors per caller / 5 min" min={1} max={1000000} value={health.minCallerErrors} onChange={hnum('minCallerErrors')} disabled={!health.enabled}
              hint={'Varsayılan 10 — hata sayısı kolunda bundan az hata alan servis "etkilenen" sayılmaz.'} />
            {/* v0.10.1084 (operatör: "%100 hata oranı gerçek değil") — hariç sistemler
                okumada SQL'de düşer; açık problemleri bir sonraki tikte kapanır. */}
            <Field label="Hariç sistemler" aria-label="Hariç sistemler"
              value={exText ?? (health.excludeSystems ?? DB_HEALTH_DEFAULTS.excludeSystems ?? []).join(', ')}
              onChange={e => { setExText(e.target.value); setHealth({ excludeSystems: parseExcludeSystems(e.target.value) }); }}
              placeholder="couchbase" disabled={!health.enabled}
              hint="hata oranı anlamsız olan istemciler, ör. couchbase: KV 'bulunamadı' cevabı hata sayılır" />
          </div>
          <SaveRow label="Save" onClick={save} busy={busy} flash={flash} />
        </>
      )}
    </div>
  );
}

// RuntimeSubsection — v0.10.890: JVM heap bandı → Problem vidaları (Settings ›
// Anomaly). Sunucu Normalize'da daima doldurur; düşüş yalnız eski backend için.
function RuntimeSubsection({ runtime, onChange }: {
  runtime?: AnomalyRuntimeConfig;
  onChange: (r: AnomalyRuntimeConfig) => void;
}) {
  const r: AnomalyRuntimeConfig = runtime ?? {
    heapMode: 'off', heapSource: 'auto', heapMinMAD: 2, heapFloorPct: 0.10,
    heapMinAbsDelta: 5, heapSilentBuckets: 3, heapMaxPods: 5000,
  };
  const set = (patch: Partial<AnomalyRuntimeConfig>) => onChange({ ...r, ...patch });
  const num = (label: string, key: keyof AnomalyRuntimeConfig, step: number, hint: string) => (
    <Field label={label} title={hint} type="number" step={step} value={Number(r[key] ?? 0)}
      className="w-auto" controlClassName="w-24"
      onChange={e => set({ [key]: Number(e.target.value) } as Partial<AnomalyRuntimeConfig>)} />
  );
  return (
    <div className={cn(SEP, 'mb-[18px]')}>
      <div className={cn(SUBHEAD, 'mb-1.5')}>
        JVM heap bandı → Problem (<code>runtime.jvm_heap_pct</code>)
      </div>
      <p className={cn(LEAD, 'mb-3')}>
        Pods sekmesindeki heap-after-GC bandı (pod başına, 24 saat, medyan ± 3.5·MAD) kritik sapmada
        servis başına <b>bir</b> Problem açar (suçlu pod alanda). Varsayılan <b>kapalı</b>; önce
        <b> gölge</b>: hüküm hesaplanır, sayılır, Problem/bildirim yok. Açık kip v0.10.892 ile gelir.
      </p>
      <div className="flex flex-wrap items-end gap-4">
        <SelectField label="Kip" className="w-auto" value={r.heapMode ?? 'off'}
          onChange={e => set({ heapMode: e.target.value as AnomalyRuntimeConfig['heapMode'] })}>
          <option value="off">Kapalı</option>
          <option value="shadow">Gölge — ölç, Problem açma</option>
          <option value="on">Açık — Problem açar (v0.10.892+; öncesinde gölge gibi)</option>
        </SelectField>
        <SelectField label="Kaynak" className="w-auto" value={r.heapSource ?? 'auto'}
          onChange={e => set({ heapSource: e.target.value as AnomalyRuntimeConfig['heapSource'] })}>
          <option value="auto">Otomatik — VM yapılandırılmışsa VM, değilse ClickHouse</option>
          <option value="vm">VictoriaMetrics</option>
          <option value="ch">ClickHouse</option>
        </SelectField>
        {num('Min MAD (puan)', 'heapMinMAD', 0.5, 'Düz seyreden heap\'te MAD≈0 tuzağı; 0 = taban yok')}
        {num('Taban (0–1)', 'heapFloorPct', 0.05, 'Medyanın bu oran üstü şart; 0 = yalnız istatistik')}
        {num('Min fark (puan)', 'heapMinAbsDelta', 1, 'Medyandan en az bu kadar puan üstü; 0 = kapı yok')}
        {num('Sessiz kova', 'heapSilentBuckets', 1, 'Pod bu kadar 5-dk kova yazmadıysa "sessiz" (1–12)')}
        {num('Pod tavanı', 'heapMaxPods', 100, 'Halka/okuma tavanı (100–50000)')}
      </div>
    </div>
  );
}
