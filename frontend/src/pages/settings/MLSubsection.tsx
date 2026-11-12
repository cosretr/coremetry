// MLSubsection — Settings › Anomaly › "Log ML" (dilim 0; operatör
// 2026-10-31). Ana anahtar (kapalıyken ES'e sorgu yok), dedektör başına
// anahtar, dedektör başına ayrı "Problem'e terfi" anahtarı, temel eşikler.
// Hepsi varsayılan KAPALI. Blob bir sonraki tikte okunur (restart yok);
// PUT audit'e eski/yeni değerle düşer (sunucu).
import { useEffect, useState } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import type { MLConfig, MLQuality, MLMetricSource } from '@/lib/types';
import { FlashBox, humanize } from './shared';
import { MLBaselineSections } from './MLBaselineSections';
import { MLRecommendedCard } from './MLRecommendedCard';
import { metricSourceLabel } from './mlMetricSource';

export const ML_LOG_DETECTORS: { id: string; label: string; hint: string }[] = [
  { id: 'log_volume', label: 'Log hacmi', hint: 'Servis × seviye hacmi, mevsimsel median+MAD, iki yönlü' },
  { id: 'error_rate', label: 'Hata oranı', hint: 'Error seviyesi sayısı, yalnız yukarı, mevsimsel' },
  { id: 'service_silent', label: 'Servis sustu', hint: 'Beklenen hacim varken log gelmiyor' },
  { id: 'template_spike', label: 'Şablon spike', hint: 'Şablon ailesinin ES\'te gerçek sayımı kendi tabanının üstüne çıktı (tik başına 1 istek, ≤100 aile)' },
  { id: 'rare_template', label: 'Nadir şablon', hint: 'Taban günlerinde hiç görülmeyen şablon ailesi belirdi (yeni aileye bir kez geçmiş sayımı)' },
  { id: 'rare_error_code', label: 'Nadir hata kodu', hint: 'Serviste 14 gündür görülmemiş kod' },
  { id: 'pod_outlier', label: 'Aykırı pod', hint: 'Aynı servisin pod\'ları arasında hata sayısı sapması' },
  { id: 'change_point', label: 'Kırılma noktası', hint: 'CUSUM; ±15 dk içinde rollout varsa eşlenir' },
];
export const ML_TRACE_DETECTORS: { id: string; label: string; hint: string }[] = [
  { id: 'op_latency', label: 'Endpoint gecikmesi', hint: 'Servis × endpoint: p95 (en az 100 ms) ve yavaş istek oranı, tek olay; mevsimsel, yukarı (VM RED)' },
  { id: 'op_error_rate', label: 'Endpoint hata oranı', hint: 'Servis × endpoint hata oranı, yukarı' },
  { id: 'throughput_drop', label: 'Trafik düşüşü', hint: 'Endpoint çağrı sayısı tabanın altına düştü' },
  { id: 'dependency_latency', label: 'Bağımlılık gecikmesi', hint: 'VM client histogramları: HTTP/DB/messaging hedefi bazında p95 (rollup yok — tail sampling)' },
  { id: 'trace_pod_outlier', label: 'Aykırı pod (trace)', hint: 'VM server RED, pod etiketi varsa: pod\'lar arasında hata oranı / p95 sapması' },
  { id: 'trace_change_point', label: 'Kırılma (trace)', hint: 'CUSUM + rollout eşleştirmesi' },
  // v0.10.2018 — T-err: hata span'leri örneklenmediği için sayı CH'de tam; oran DEĞİL.
  { id: 'trace_error_count', label: 'Operasyon hata sayısı', hint: 'ClickHouse spanmetrics_1m: servis × operasyon (server/consumer) hata span sayısı kendi tabanının üstüne çıktı; oran hesaplanmaz' },
];
export const ML_METRIC_DETECTORS: { id: string; label: string; hint: string }[] = [
  { id: 'metric_jvm', label: 'JVM', hint: 'Heap, GC, thread (servis düzeyi; Thanos\'ta cluster başına)' },
  { id: 'metric_pool', label: 'Connection pool', hint: 'Havuz kullanımı, bekleme' },
  { id: 'metric_kafka', label: 'Kafka', hint: 'Consumer lag, throttle' },
  { id: 'metric_http_client', label: 'HTTP client', hint: 'Dış çağrı p95, 5xx oranı' },
  { id: 'metric_k8s', label: 'Kubernetes', hint: 'CPU, bellek, restart (pod → servis toplanır)' },
];

const H3 = 'mt-4 mb-1 text-[length:var(--fs-md)] font-semibold';
const ML_DETECTOR_LABEL: Record<string, string> = Object.fromEntries(
  [...ML_LOG_DETECTORS, ...ML_TRACE_DETECTORS, ...ML_METRIC_DETECTORS].map(d => [d.id, d.label]));
/** Dedektör adı → ekrandaki etiket (bilinmeyen ad aynen). */
const mlDetectorLabel = (id: string) => ML_DETECTOR_LABEL[id] ?? id;
const NOTE = 'text-[length:var(--fs-xs)] leading-normal text-(--text3)';

export function MLSubsection() {
  const [cfg, setCfg] = useState<MLConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // v0.10.2019 — metrik ailesi kaynağı (salt-okunur; ML lideri saatte bir seçer).
  const [sources, setSources] = useState<Record<string, MLMetricSource>>({});

  useEffect(() => {
    api.getMl().then(setCfg).catch(err => setFlash({ kind: 'err', text: humanize(err) }));
    // tek seferlik okuma (karne ucu, 60 sn sunucu önbelleği); okunamazsa satır "henüz ölçülmedi" der.
    api.mlScorecard().then(sc => setSources(sc?.tick?.metricSources ?? {})).catch(() => undefined);
  }, []);

  const save = async () => {
    if (!cfg) return;
    setBusy(true); setFlash(null);
    try {
      setCfg(await api.putMl(cfg));
      setFlash({ kind: 'ok', text: 'Kaydedildi — bir sonraki ML tikinde geçerli, restart gerekmez.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const num = (label: string, key: keyof MLConfig, hint: string, step = 1) => cfg && (
    <Field type="number" label={label} hint={hint} controlClassName="max-w-[180px]" step={step}
      value={String(cfg[key] as number)}
      onChange={e => setCfg({ ...cfg, [key]: Number(e.target.value) })} />
  );

  const qnum = (label: string, key: keyof MLQuality, hint: string, step = 1) => cfg && (
    <Field type="number" label={label} hint={hint} controlClassName="max-w-[180px]" step={step}
      value={String(cfg.quality?.[key] as number)}
      onChange={e => setCfg({ ...cfg, quality: { ...cfg.quality, [key]: Number(e.target.value) } })} />
  );

  return (
    <div data-testid="ml-settings">
      <h2 className="mb-1.5 text-[length:var(--fs-lg)] font-semibold">ML</h2>
      <p className="mb-[18px] text-[length:var(--fs-sm)] leading-[1.55] text-(--text2)">
        ML dedektörleri: log sinyali (Elasticsearch: hacim, hata oranı, şablon, nadir kod, pod, kırılma) ve
        trace sinyali (rollup: endpoint gecikmesi/hata/trafik, bağımlılık, exception, pod, kırılma). Olaylar
        /anomalies listesine düşer. Aynı serviste en az 2 farklı sinyal birlikte bozulursa grup Problem açar
        (aşağıdaki anahtar); tek sinyal Problem üretmez. Eski terfi dedektör başına ayrı anahtar ve karne eşiğiyle açılır. Ana anahtar kapalıyken ES&apos;e hiç sorgu atılmaz.
      </p>
      {!cfg ? (
        flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />
      ) : (
        <>
          {/* v0.10.1886 — tek düğmeyle önerilen kurulum (VM'de verisi olan dedektörler). */}
          <MLRecommendedCard labelOf={mlDetectorLabel}
            onApplied={c => { setCfg(c); setFlash(null); }} />
          <CheckboxField className="mb-3" checked={cfg.enabled}
            onCheckedChange={v => setCfg({ ...cfg, enabled: v })}
            label="ML açık (ana anahtar)"
            hint="Kapalı: tik yalnız sayaç günceller, Elasticsearch'e sorgu yok." />
          <CheckboxField className="mb-3" checked={cfg.baseline?.enabled === true} disabled={!cfg.enabled}
            onCheckedChange={v => setCfg({ ...cfg, baseline: { enabled: v } })}
            label="Baseline gözlem işi (günlük)"
            hint="03:00 sonrası dünün ml_series kovalarından saatlik bant gözlemi yazar (ml_baseline_obs). Tespit bu sürümde değişmez; yalnız CH okur." />
          {/* v0.10.2054 (D4) — tek anahtar; yok = açık. */}
          <CheckboxField className="mb-3" checked={cfg.groupProblems !== false} disabled={!cfg.enabled}
            onCheckedChange={v => setCfg({ ...cfg, groupProblems: v })}
            label="Anomali grubu Problem açar"
            hint="Aynı serviste en az 2 farklı sinyalde (trace, log, metrik) anomali varsa grup bir Problem açar (P2) ve bildirim mevcut kanallardan gider. Tek sinyal anomali olarak kalır. Kapalı: yalnız kayıt (Problem açardı)." />

          <p className={NOTE}>Soldaki anahtar dedektörü açar (gölge). Sağdaki &quot;terfi&quot; anahtarı karne eşiğini geçen olayı Problem&apos;e kopyalar.</p>
          {([['Log dedektörleri', ML_LOG_DETECTORS], ['Trace dedektörleri', ML_TRACE_DETECTORS], ['Metrik aileleri', ML_METRIC_DETECTORS]] as const).map(([title, list]) => (<div key={title}>
          <h3 className={H3}>{title}</h3>
          <div className="mt-2 grid gap-2">
            {list.map(d => {
              const p = cfg.promotion[d.id] ?? { enabled: false, minHitRate: 0.8, minSamples: 20 };
              return (
                <div key={d.id} className="grid grid-cols-1 gap-2 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_120px_120px] md:items-start">
                  <CheckboxField checked={cfg.detectors[d.id] === true} disabled={!cfg.enabled}
                    onCheckedChange={v => setCfg({ ...cfg, detectors: { ...cfg.detectors, [d.id]: v } })}
                    label={d.label} hint={d.id.startsWith('metric_') ? `${d.hint} · ${metricSourceLabel(sources[d.id], cfg.enabled && cfg.detectors[d.id] === true)}` : d.hint} />
                  <CheckboxField checked={p.enabled} disabled={!cfg.enabled || cfg.detectors[d.id] !== true}
                    onCheckedChange={v => setCfg({ ...cfg, promotion: { ...cfg.promotion, [d.id]: { ...p, enabled: v } } })}
                    label="Problem'e terfi" hint="Varsayılan kapalı; eşik + asgari örnek şart" />
                  <Field type="number" label="Min isabet" step={0.05} value={String(p.minHitRate)} small
                    onChange={e => setCfg({ ...cfg, promotion: { ...cfg.promotion, [d.id]: { ...p, minHitRate: Number(e.target.value) } } })} />
                  <Field type="number" label="Min örnek" value={String(p.minSamples)} small
                    onChange={e => setCfg({ ...cfg, promotion: { ...cfg.promotion, [d.id]: { ...p, minSamples: Number(e.target.value) } } })} />
                </div>
              );
            })}
          </div>
          </div>))}

          {/* v0.10.1886 — ifadeler, eşikler ve kalite çerçevesi varsayılan KAPALI
              tek bölümde: gündelik kurulum yukarıdaki anahtarlarla biter. Alanlar
              kapalıyken de bağlı kalır (Save hepsini gönderir). */}
          <details data-testid="ml-advanced" className="mt-4 rounded-md border border-border px-3 py-2">
          <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-(--text2)">
            Gelişmiş ayarlar — MetricsQL ifadeleri, eşikler, kalite çerçevesi
          </summary>
          <p className={`${NOTE} mt-2`}>Varsayılanlar çoğu kurulum için yeterlidir; değiştirmeniz gerekmez.</p>
          <h3 className={H3}>Metrik MetricsQL ifadeleri</h3>
          <p className={NOTE}>Varsayılanlar OTel/Prometheus adlarıdır; kendi metrik adlarınla düzenle. <code>{'{svc}'}</code> servis etiketi, <code>{'{win}'}</code> kova penceresi. Pod etiketi ifadede servis düzeyine toplanır.</p>
          <div className="mt-2 grid gap-2">
            {ML_METRIC_DETECTORS.map(f => (cfg.metricFamilies?.[f.id] ?? []).map((e, i) => (
              <div key={`${f.id}:${e.id}`}>
                <Field label={`${f.label} · ${e.label || e.id}`} hint={`yön: ${e.direction}${e.unit ? ` · ${e.unit}` : ''}`} small
                  value={e.expr} onChange={ev => setCfg({ ...cfg, metricFamilies: { ...cfg.metricFamilies, [f.id]: cfg.metricFamilies[f.id].map((x, j) => (j === i ? { ...x, expr: ev.target.value } : x)) } })} />
                <CheckboxField className="mt-1" checked={!!e.load}
                  onCheckedChange={v => setCfg({ ...cfg, metricFamilies: { ...cfg.metricFamilies, [f.id]: cfg.metricFamilies[f.id].map((x, j) => (j === i ? { ...x, load: v } : x)) } })}
                  label="Yük serisi"
                  hint="İstek/mesaj hızı gibi servisin yükünü ölçer; adı batch kalıbına giren serviste değerlendirilmez (Settings › Anomaly › Batch servis ad kalıpları)." />
              </div>
            )))}
          </div>

          <h3 className={H3}>Eşikler</h3>
          <div className="grid gap-3 md:grid-cols-2">
            {num('Taban (gün)', 'baselineDays', 'Mevsimsel taban için geriye bakış (3–28); 21 = her takvim sınıfından ≥3 gün')}
            {num('Servis tavanı', 'maxServices', 'ES servis terms tavanı; bundan fazlası izlenmez (10–5000)')}
            {num('Hacim tabanı (satır/kova)', 'minBaselineRate', 'Tabanı bunun altında kalan servis için hacim/susma sessiz', 1)}
            {num('Dwell (kova)', 'dwellBuckets', 'Olay açılmadan art arda sapmalı kova (1–12)')}
            {num('Günlük tavan', 'dailyEventCap', 'Dedektör başına günlük yeni olay (10–10000)')}
            {num('ES tik tavanı', 'maxEsQueriesPerTick', 'Tik başına en çok ES sorgusu (1–20); log RED 1 + şablon 1 + kod/pod 1 + seyrek geçmiş 1 = 4')}
            {num('Kova (sn)', 'bucketSec', 'date_histogram kovası (60–3600)')}
            {num('Tik (sn)', 'tickSec', 'Alt sistem tiki (60–3600)')}
            {num('ES zaman aşımı (ms)', 'timeoutMs', 'Tik içi istek (1000–60000)')}
            {num('Yavaş istek eşiği (sn)', 'slowRequestSec', 'op_latency: bu süreden uzun istek "yavaş"; oranı değerlendirilir (0.05–60)', 0.1)}
            {num('Asgari etkilenen istek', 'minAffectedRequests', 'Kovada yavaş istek sayısı bundan azsa olay yok (1–10000)')}
            {num('Kronik eşiği (epizot / 7 g)', 'chronicEpisodes', 'Bu kadar epizottan sonra yeni olay yerine tek kronik kaydı (2–100)')}
            {num('VM hedef tavanı (servis başına)', 'vmTargetCardinality', 'Bağımlılık gecikmesi: servis başına en çok K hedef, topk (1–200)')}
            {num('VM servis grubu', 'vmServiceBatch', 'Ham histogram sorgusunda bir gruptaki servis sayısı (5–500)')}
            {num('Geçmiş doldurma zaman aşımı (ms)', 'backfillTimeoutMs', 'Saatlik geçmiş isteği için ES zaman aşımı (5000–120000)')}
            {num('Geçmiş doldurma aralığı (ms)', 'backfillIntervalMs', 'İki geçmiş isteği arası bekleme (200–60000)')}
            {num('ES ingest gecikme payı (sn)', 'ingestLagSec', 'Son kova ancak kova sonu + bu pay geçince değerlendirilir (0–3600)')}
            {num('Asgari taban örneği', 'minBaselineSamples', 'Eksik veri kuralı: taban örneği bundan azsa sessiz (5–500)')}
            {num('Kritik z', 'criticalZ', 'Robust z eşiği (1–20)', 0.5)}
            <Field label="Influencer alanı (kanal kodu)" hint="ES keyword alanı; boş = adaylardan keşfet" controlClassName="max-w-[260px]"
              value={cfg.influencerField ?? ''} onChange={e => setCfg({ ...cfg, influencerField: e.target.value })} />
          </div>

          <h3 className={H3}>Kalite çerçevesi</h3>
          <p className={NOTE}>Log, trace ve metrik için ortak. Olay yazılmadan önce etki eşiği, geri bildirim ve rejim kapılarından geçer; bastırılanlar /admin/stats › ml sayaçlarında. Aynı servisin başka sinyaldeki olayı teyit sayılır.</p>
          <div className="grid gap-3 md:grid-cols-2" data-testid="ml-quality">
            {qnum('Etki eşiği (göreli)', 'impactMinRel', '|gözlenen − taban| / taban en az (0.2 = %20)', 0.05)}
            {qnum('Etki eşiği (mutlak)', 'impactMinAbs', 'Serinin biriminde en az fark (0 = yok)', 1)}
            {qnum('Rejim değişimi (saat)', 'regimeHours', 'Bu kadar kesintisiz süren sapma yeni normal sayılır, uzatılmaz (1–168)')}
            {qnum('Teyit penceresi (dk)', 'confirmWindowMin', 'Aynı servisin başka dedektör/sinyal olayı ± bu sürede (1–120)')}
            {qnum('Geri bildirim: asgari "değil"', 'feedbackMinNegatives', 'Dedektör × servis bu kadar "değil" kararından sonra eşik yükselir (1–100)')}
            {qnum('Geri bildirim: z adımı', 'feedbackZStep', 'Net "değil" kararı başına z artışı (0–5)', 0.1)}
            {qnum('Geri bildirim: z tavanı', 'feedbackMaxBoost', 'Toplam z artışı en çok (0–20)', 0.5)}
          </div>
          <CheckboxField className="mt-2" checked={!cfg.quality?.hygieneOff}
            onCheckedChange={v => setCfg({ ...cfg, quality: { ...cfg.quality, hygieneOff: !v } })}
            label="Taban hijyeni" hint="Gerçek anomali diye işaretlenen kovalar tabana girmez" />
          <CheckboxField className="mt-2" checked={!cfg.quality?.feedbackOff}
            onCheckedChange={v => setCfg({ ...cfg, quality: { ...cfg.quality, feedbackOff: !v } })}
            label="Geri bildirim ayarı" hint={'"Değil" kararları o dedektör × servis için eşiği yükseltir'} />
          </details>

          <div className="mt-[18px]">
            <ActionRow confirm={<Button onClick={save} loading={busy}>Save</Button>} />
            {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
          </div>
        </>
      )}
      <MLBaselineSections />
    </div>
  );
}
