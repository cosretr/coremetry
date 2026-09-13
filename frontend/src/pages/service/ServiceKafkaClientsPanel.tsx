// ServiceKafkaClientsPanel.tsx — v0.10.552 (Messaging Kafka Faz 3; operatör
// yer kararı 2026-09-08: "önerin" = Infra sekmesi). Servisin Kafka İSTEMCİ
// sağlığı (topic'e değil istemciye ait metrikler): StatTile şeridi + üç
// MetricArea (sekmenin CPU/Mem panelleriyle aynı bileşen, aynı imleç senkronu
// `infra:<service>`). Thanos'tan BAĞIMSIZ: sekme Thanos yokken Empty çizse de
// bu panel kendi verisiyle çizilir. Metrik yoksa tek satır soluk not.
import { useMemo, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { LinkButton } from '@/components/ui/LinkButton'; // v0.10.719
import { SectionHead } from '@/components/ui/SectionHead'; // v0.10.718
import { KafkaAlertModal } from '@/pages/alerts/KafkaAlertModal'; // v0.10.554
import { StatTile } from '@/components/ui/StatTile';
import { Spinner } from '@/components/Spinner';
import { MetricArea } from '@/pages/clusters/MetricArea';
import { useUrlEnv } from '@/lib/useUrlEnv';
import { fmtNum, timeRangeToNs } from '@/lib/utils';
import { useServiceKafkaClients } from '@/lib/queries/messaging';
import { kafkaDegradeTR } from '@/features/dependencies/kafkaClients';
import { kafkaStrip, kafkaStripEmpty, kafkaToNamedSeries } from './serviceKafkaClients';
import type { TimeRange } from '@/lib/types';

const fmtOr = (v: number | null, f: (n: number) => string = n => fmtNum(n)) => (v === null ? '—' : f(v));
const fmtMs = (n: number) => `${fmtNum(Math.round(n * 10) / 10)} ms`;
const KAFKA_PRIMARY_TILES = 4; // v0.10.719

export function ServiceKafkaClientsPanel({ service, range, onZoom, onZoomReset }: {
  service: string; range: TimeRange;
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
}) {
  const [env] = useUrlEnv();
  const win = useMemo(() => timeRangeToNs(range), [range]); // v0.5.184: memo içinde
  const q = useServiceKafkaClients({ service, fromNs: win.from, toNs: win.to, env: env || undefined });
  const [alertOpen, setAlertOpen] = useState(false); // v0.10.554
  const [showAll, setShowAll] = useState(false); // v0.10.719 — 4 + "tümü ▸"
  if (q.isPending) {
    return <div className="kc-line" role="status" aria-busy="true"><Spinner /> Kafka istemci metrikleri…</div>;
  }
  const data = q.isError ? null : (q.data ?? null);
  const degrade = kafkaDegradeTR(data);
  if (degrade || !data) {
    return <div className="kc-line" title={data?.note}>◌ {degrade}</div>;
  }
  const strip = kafkaStrip(data.blocks);
  const err = kafkaToNamedSeries(data.blocks.producer_error_rate);
  const lag = kafkaToNamedSeries(data.blocks.consumer_lag_max);
  const lat = kafkaToNamedSeries(data.blocks.producer_request_latency_avg);
  const sync = `infra:${service}`;
  return (
    <section className="kc-sec" aria-label="Kafka client (metrik)" style={{ marginTop: 14 }}>
      {/* v0.10.718 — ortak bölüm başlığı atomu (Infra'nın diğer bölümleriyle
          aynı anatomi: ad · kaynak rozeti · rozetler · sağda eylem). */}
      <SectionHead id="infra-kafka" title="Kafka client" source={`${data.source} · OTel Java agent`}
        badges={<>
          <span className="badge b-gray mono">{service}</span>
          {data.envAmbiguous && (
            <span className="badge b-warn"
              title="env filtresi bu depoda ifade edilemiyor; seriler TÜM ortamları kapsıyor.">
              env uygulanmadı
            </span>
          )}
        </>}
        actions={<Button variant="secondary" size="sm" onClick={() => setAlertOpen(true)}
          title="Bu servisin Kafka istemcisi için alarm kuralı (lag ya da gönderim hatası)">
          Alarm kur
        </Button>} />
      {alertOpen && (
        <KafkaAlertModal open onClose={() => setAlertOpen(false)} target={{ service }} />
      )}
      <div className="kc-note">{data.note}</div>
      {/* v0.10.719 (mockup 3b03fe22 Infra şerh 5) — 9 tile → 4 birincil + "tümü ▸".
          Gizlenen bir ölçü uyarı/hata tonu taşıyorsa şerit AÇIK başlar ve
          toggle çizilmez: uyarı katlanıp saklanmaz (dürüstlük). */}
      {!kafkaStripEmpty(strip) && (() => {
        const tiles: { key: string; label: string; tone?: 'warn' | 'err'; value: string }[] = [
          { key: 'pc', label: 'Açık bağlantı · üretici', value: fmtOr(strip.producerConnections) },
          { key: 'cc', label: 'Açık bağlantı · tüketici', value: fmtOr(strip.consumerConnections) },
          { key: 'lmax', label: 'Broker gecikme · maks.', tone: strip.latencyMaxMs !== null && strip.latencyMaxMs > 1000 ? 'warn' : undefined, value: fmtOr(strip.latencyMaxMs, fmtMs) },
          { key: 'reb', label: 'Rebalance / saat', tone: strip.rebalancePerHour !== null && strip.rebalancePerHour > 0 ? 'warn' : undefined, value: fmtOr(strip.rebalancePerHour) },
          // ── ikincil (tümü ▸) ──
          { key: 'lavg', label: 'Broker gecikme · ort.', value: fmtOr(strip.latencyAvgMs, fmtMs) },
          { key: 'poll', label: 'Son poll · maks.', tone: strip.lastPollSecMax !== null && strip.lastPollSecMax > 300 ? 'err' : undefined, value: fmtOr(strip.lastPollSecMax, n => `${fmtNum(Math.round(n))} s`) },
          // v0.10.583 — 582'nin üç "neden" metriği. Eşikler son değere:
          // poll aralığı Kafka'nın varsayılan max.poll.interval.ms'inin
          // (300 s) %80'ine yaklaşınca err — tüketici gruptan atılmak
          // üzere; her türlü kota kısıtı warn — yavaşlığın sebebi
          // uygulama değil broker.
          { key: 'gap', label: 'Poll aralığı · maks.', tone: strip.pollGapMaxMs !== null && strip.pollGapMaxMs > 240_000 ? 'err' : strip.pollGapMaxMs !== null && strip.pollGapMaxMs > 60_000 ? 'warn' : undefined, value: fmtOr(strip.pollGapMaxMs, n => `${fmtNum(Math.round(n / 1000))} s`) },
          { key: 'thr', label: 'Fetch kota kısıtı · ort.', tone: strip.fetchThrottleAvgMs !== null && strip.fetchThrottleAvgMs > 0 ? 'warn' : undefined, value: fmtOr(strip.fetchThrottleAvgMs, fmtMs) },
          { key: 'rebl', label: 'Rebalance süresi · ort.', tone: strip.rebalanceLatencyAvgMs !== null && strip.rebalanceLatencyAvgMs > 10_000 ? 'warn' : undefined, value: fmtOr(strip.rebalanceLatencyAvgMs, fmtMs) },
        ];
        const primary = tiles.slice(0, KAFKA_PRIMARY_TILES);
        const rest = tiles.slice(KAFKA_PRIMARY_TILES);
        const restFlagged = rest.some(t => t.tone);
        const expanded = showAll || restFlagged;
        return (
          <>
            <div className="kc-grid" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))' }}>
              {(expanded ? tiles : primary).map(t => (
                <StatTile key={t.key} label={t.label} tone={t.tone}>{t.value}</StatTile>
              ))}
            </div>
            <div className="kc-note">
              {restFlagged
                ? <span className="badge b-warn" title="İkincil ölçülerden en az biri uyarı taşıyor; şerit katlanmaz.">ikincil ölçülerde uyarı · tümü açık</span>
                : <LinkButton onClick={() => setShowAll(v => !v)} title={expanded ? 'Yalnız dört birincil ölçü' : `${rest.length} ikincil ölçüyü göster`}>
                    {expanded ? 'daha az ▴' : `tümü (${rest.length}) ▸`}
                  </LinkButton>}
            </div>
          </>
        );
      })()}
      <div className="grid-2" style={{ display: 'grid', gap: 14 }}>
        <MetricArea title="Gönderim hatası (kayıt/sn) · topic" subtitle="kafka.producer.record_error_rate · by topic"
          series={err.series} seriesName="hata/sn" totalSeries={err.total} maxSeries={12}
          onZoom={onZoom} onZoomReset={onZoomReset} syncKey={sync} />
        <MetricArea title="İstemcinin gördüğü en yüksek lag · topic · istemci" subtitle="kafka.consumer.records_lag_max · partition maks., group lag değil"
          series={lag.series} seriesName="lag" totalSeries={lag.total} maxSeries={12}
          onZoom={onZoom} onZoomReset={onZoomReset} syncKey={sync} />
        <MetricArea title="Broker istek gecikmesi (ms) · istemci" subtitle="kafka.producer.request_latency_avg · by client_id"
          series={lat.series} seriesName="gecikme" unit="ms" totalSeries={lat.total} maxSeries={12}
          onZoom={onZoom} onZoomReset={onZoomReset} syncKey={sync} />
      </div>
    </section>
  );
}
