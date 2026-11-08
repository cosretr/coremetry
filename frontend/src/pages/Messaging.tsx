import { useCallback, useEffect, useMemo } from 'react';
import { useEscLayer } from '@/lib/escLayer';
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { Empty } from '@/components/Spinner';
import { DependenciesTable, type DepRow } from '@/components/DependenciesTable';
import type { DataTableStateProps } from '@/components/ui/DataTable';
import { api } from '@/lib/api';
import { timeRangeToNs } from '@/lib/utils';
import { useUrlRange, DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { encodeDestinationParam, decodeDestinationParam } from './messaging/destinationParam';
import { depRowKey } from '@/lib/depsTable';
import type { MessagingInstance, MessagingOverview } from '@/lib/types';
import { PageShell } from '@/components/coremetry/PageShell';
import { useHttpPromql } from '@/lib/queries/httpMetrics';
import { topicLagMap, sortByLag } from './messaging/topicLag'; // v0.10.1959
import { KAFKA_LIST_ITEMS, kafkaTopicStats, kafkaListSummary, type KafkaListSummary } from './messaging/kafkaOverview'; // v0.10.1961
import { KafkaConsumersTable } from './messaging/KafkaConsumersTable';
import { Tabs, TabsList, TabsTrigger } from '@/components/shadcn/tabs';
import { HttpStat } from '@/pages/service/httpMetricsViz';
import { fmtSmart } from '@/lib/chartFmt';
import { fmtNum } from '@/lib/utils';

// /messaging — top-level queue / topic technologies overview.
// Kafka brokers, RabbitMQ vhosts, IBM MQ queues, NATS subjects,
// SQS / Kinesis streams — anything OTel's messaging.system
// semconv touches.
//
// useQuery + keepPreviousData: revisits land instantly, range
// changes paint the old data underneath while the new payload
// fetches. staleTime aligns with the backend's 30s cache TTL
// so most refetches hit the warm slot.
//
// v0.8.364 (Stage-2 M1):
//   • Produce/min + Consume/min + P50 columns (producer/consumer
//     split off messaging_caller_summary_5m; p50 off the existing
//     TDigest state).
//   • "Compare vs prior" toggle → TrendDelta badges (the endpoints
//     v0.5.404 pattern; opt-in, doubles the backend scan).
//   • ?destination= URL param drives the topic detail drawer
//     (URL-first house rule; replace:true, Esc/✕ clears).
type MsgView = 'topics' | 'consumers';

export default function MessagingPage() {
  const [range, setRange] = useUrlRange(DEFAULT_RANGE_PRESET);
  const [params, setParams] = useSearchParams();
  // Prior-window comparison — off by default (second CH scan);
  // session-local like the endpoints toggle.
  // v0.9.399 (desen paritesi) — compare oturum state'inden URL'e
  // (?compare=prior, Endpoints deseniyle birebir): copy-link ve saved
  // view artık compare durumunu da taşıyor.
  // v0.10.574 — TEK useSearchParams örneği. İkinci bir örnek (mparams)
  // aynı URL durumunu ayrı bir isimle okuyordu: hata değildi (iki yazıcı da
  // fonksiyonel biçimi kullanıyor, yani birbirini ezmiyor) ama okuyan
  // "iki ayrı durum mu" diye duraklıyordu ve fonksiyonel biçimden sapan
  // ileride bir düzenleme sessizce ezme üretirdi.
  const compare = params.get('compare') === 'prior';
  const setCompare = (v: boolean) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v) next.set('compare', 'prior'); else next.delete('compare');
    return next;
  }, { replace: true });
  // Memoize on range identity — without this, a relative range
  // resolved fresh every render reshuffles the useQuery key
  // and the table refetches on every paint.
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const q = useQuery({
    queryKey: ['messaging', from, to, compare],
    queryFn: () => api.messaging(from, to, compare ? 'prior' : undefined).then(r => r ?? []),
    staleTime: 30_000,
    placeholderData: keepPreviousData,
  });

  // v0.8.364 — URL-first topic detail drawer. ?destination=
  // encodes the row's full identity (system|cluster|destination,
  // each field URI-encoded — see destinationParam.ts); a copied
  // link reopens the same drawer. replace:true so drawer churn
  // doesn't pile history entries.
  const destRef = useMemo(
    () => decodeDestinationParam(params.get('destination')),
    [params],
  );
  const setOpenRow = useCallback((row: DepRow | null) => {
    setParams(prev => {
      const next = new URLSearchParams(prev);
      if (row) {
        next.set('destination', encodeDestinationParam({
          system: row.system,
          cluster: row.cluster ?? '(default)',
          destination: row.destination ?? '',
        }));
      } else {
        next.delete('destination');
      }
      return next;
    }, { replace: true });
  }, [setParams]);
  // v0.9.821 — anahtar TEK NÜSHADAN (depRowKey). Elle yazılmış kopya
  // DependenciesTable'ın ürettiği anahtarla ayrışabilirdi ve ayrışsa
  // drawer HİÇ AÇILMAZDI: sessiz, tip hatası vermeyen bir kırılma.
  // (Anahtar db.name alanı kazandı; messaging satırları onu taşımadığı
  // için burada boş kalıyor ve şekil eskisiyle uyumlu.)
  const openRowKey = destRef
    ? depRowKey({
        system: destRef.system,
        cluster: destRef.cluster,
        destination: destRef.destination,
      })
    : null;

  // Esc clears the drawer param (✕ inside the drawer does the same
  // through onOpenRowChange(null)).
  // v0.9.950 (E2/Ö28) — KATMAN. Çekmecenin İÇİNDE bir popover açıkken
  // ilk Esc ona ait; çekmece ancak en üstteyken kapanır.
  useEscLayer(!!destRef, () => setOpenRow(null));

  // The backend ships raw window counts (window-independent,
  // compare-safe); the page owns the window so it derives the
  // /min rates here. Prior windows are equal-length by
  // construction, so the same divisor applies.
  const windowMins = Math.max((to - from) / 60e9, 1 / 60);
  // v0.10.1959 — Kafka lag VM'den (servis Messaging sekmesiyle aynı metrik); anlık, 30 sn önbellek.
  // v0.10.1961 (Kafka UI düzeni) — aynı anlık okumaya Partitions / Producers / Consumers / Produce/s /
  // Consume/s / gönderim hatası eklendi (topic etiketli seriler; sorgular kafkaOverview'da testli).
  const win = useMemo(() => ({ from, to }), [from, to]);
  const kq = useHttpPromql(KAFKA_LIST_ITEMS, win);
  const lagMap = useMemo(() => topicLagMap(kq.data.lag), [kq.data.lag]);
  const kStats = useMemo(() => kafkaTopicStats(kq.data),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- veri kimliği sorgu sonuçları
    [kq.data.produce, kq.data.consume, kq.data.sendErr, kq.data.parts, kq.data.prodSvc, kq.data.consSvc]);
  const kSummary = useMemo(() => kafkaListSummary(kStats, lagMap), [kStats, lagMap]);
  const view: MsgView = kSummary && params.get('view') === 'consumers' ? 'consumers' : 'topics';
  const setView = (v: MsgView) => setParams(prev => {
    const next = new URLSearchParams(prev);
    if (v === 'consumers') next.set('view', 'consumers'); else next.delete('view');
    return next;
  }, { replace: true });
  // v0.9.813 — zarf. `?? []` HER İKİ eksikliği karşılar: yanıt yok ve
  // rows yok. Tavan bilgisi ayrı okunur — şerit tabloyu değil listeyi
  // niteliyor, bu yüzden filtrelenmiş satır sayısına DEĞİL sunucunun
  // döndürdüğü ham sayıya bakar.
  const ov = q.data as MessagingOverview | undefined;
  const list = useMemo(() => ov?.rows ?? [], [ov]);
  const rowsCapped = ov?.rowsCapped === true;
  const rowLimit = ov?.rowLimit ?? 0;
  const rows = useMemo<DepRow[]>(() => {
    const mapped = list.map((d: MessagingInstance): DepRow => {
      const hasPrior = d.priorSpanCount !== undefined;
      return {
        system: d.system,
        cluster: d.cluster,
        destination: d.destination,
        spanCount: d.spanCount,
        errorCount: d.errorCount,
        errorRate: d.errorRate,
        avgDurationMs: d.avgDurationMs,
        p99DurationMs: d.p99DurationMs,
        p50DurationMs: d.p50DurationMs,
        p95DurationMs: d.p95DurationMs,
        producePerMin: (d.produceCount ?? 0) / windowMins,
        consumePerMin: (d.consumeCount ?? 0) / windowMins,
        produceCount: d.produceCount,
        consumeCount: d.consumeCount,
        produceErrors: d.produceErrors,
        consumeErrors: d.consumeErrors,
        // v0.9.816 — gecikme ayrışması (publish vs process p95).
        produceP95Ms: d.produceP95Ms,
        consumeP95Ms: d.consumeP95Ms,
        // Prior fields only when the row had a prior twin —
        // otherwise the delta badges must stay hidden (a zeroed
        // prior would render a bogus NEW badge). omitempty on the
        // backend guarantees priorSpanCount is present iff matched.
        priorSpanCount: d.priorSpanCount,
        priorErrorCount: hasPrior ? (d.priorErrorCount ?? 0) : undefined,
        priorProducePerMin: hasPrior ? (d.priorProduceCount ?? 0) / windowMins : undefined,
        priorConsumePerMin: hasPrior ? (d.priorConsumeCount ?? 0) / windowMins : undefined,
        priorAvgMs: d.priorAvgMs,
        priorP50Ms: d.priorP50Ms,
        priorP99Ms: d.priorP99Ms,
        callers: d.callers ?? [],
        lag: d.system === 'kafka' ? lagMap.get(d.destination) : undefined,
        kafka: d.system === 'kafka' ? kStats.get(d.destination) : undefined,
      };
    });
    // v0.10.1959 — lag'i olan topic'ler üstte (azalan), gerisi çağrı hacmine göre.
    return sortByLag(mapped);
  }, [list, windowMins, lagMap, kStats]);
  // Consumers sekmesinde topic bağlantısı: span tarafında topic tek cluster'daysa o cluster.
  const clusterOf = useMemo(() => {
    const m = new Map<string, string | null>();
    for (const d of list) {
      if (d.system !== 'kafka') continue;
      const prev = m.get(d.destination);
      m.set(d.destination, prev === undefined || prev === d.cluster ? d.cluster : null);
    }
    return (topic: string) => m.get(topic) ?? undefined;
  }, [list]);

  // v0.10.954 — tablo standardı T12 (P6): yükleniyor / hata / boş artık
  // DependenciesTable'ın İÇİNDE (başlık durur); tablo her durumda çizilir.
  // Sıra eskisiyle aynı. Eski hata metninin çaresi (ClickHouse bağlantısı,
  // aralık seçici yeniden çalıştırır) korunur; yeniden dene düğmesi yoktu,
  // yok. Satır VARKEN hata (eski veriyle başarısız yenileme) eskisi gibi
  // tablonun üstünde kutu — satırlar kalır, hata gizlenmez.
  const depState: Omit<DataTableStateProps<DepRow>, 'dt' | 'leading' | 'trailing'> =
    q.isPending ? { kind: 'loading', skeletonRows: 8 }
    : q.isError ? {
        kind: 'error',
        message: 'Mesajlaşma özeti okunamadı — ClickHouse bağlantısını kontrol edip yeniden dene; üstteki aralık seçici sorguyu yeniden çalıştırır.',
      }
    : {
        kind: 'empty',
        message: "Bu pencerede mesajlaşma etkinliği yok — seçili aralıkta messaging.system özniteliği taşıyan span gelmedi. "
          + "Zaman aralığını genişlet ya da bir üretici / tüketiciyi OTel messaging semconv ile enstrümante et; kuyruklar ve topic'ler burada görünür.",
      };

  return (
    <>
      <Topbar title="Messaging" range={range} onRangeChange={setRange} />
      <PageShell>
        <div style={{ marginBottom: 14, fontSize: 12, color: 'var(--text2)' }}>
          Every queue / topic the platform's services produced to or consumed from
          in the selected window. Derived from spans with a populated
          {' '}<code>messaging.system</code> attribute. {/* v0.9.256 — eski metin
          "Click a row to drill into matching traces" diyordu; satır DRAWER
          açıyor, trace'e gitmiyor. Yanlış vaat, operatörün "trace'e
          erişemiyorum" şikayetinin bir parçasıydı. */}
          Satıra tıkla → yayıncılar, tüketiciler ve uçtan uca gecikme;
          trace'ler için satırdaki destination bağlantısını kullan.
          {/* v0.9.405 — çakışan yüzey artık birbirine bağlı: consumer
              span'leri endpoint (giriş) perspektifiyle de listelenir. */}
          {' '}Consumer'ları giriş-noktası görünümünde incelemek için{' '}
          <Link to="/endpoints?entry=rpc">Endpoints · RPC →</Link>
        </div>
        {/* v0.9.405 — URL-state taşıyan sayfa görünüm kaydedebilmeli
            (saved_views şeması hazır; Endpoints emsali). */}
        <SavedViewsBar page="messaging" />
        {/* v0.9.834 — v0.9.814'ün KPI şeridi + üç grafiği KALDIRILDI
            (operatör: bu metrikler gereksiz). Sayfa tabloya döndü;
            /api/messaging/series ve okuyucusu da silindi — tüketicisi
            kalmayan bir uç, ödenen ama okunmayan bir CH taramasıdır. */}
        {q.isError && list.length > 0 && (
          <Empty icon="⚠" title="Couldn't load messaging overview">
            The messaging query failed. Check ClickHouse connectivity and retry —
            the range selector above re-runs the fetch.
          </Empty>
        )}
        {/* v0.9.813 — TAVAN ŞERİDİ. Sunucu LIMIT'e dayandığında liste
            EKSİK ve bunu söylemeyen bir tablo "estate'in tamamı bu"
            diye okunur. Şerit yalnız kesme GERÇEKTEN olduğunda çıkar;
            eylem de veriyor (arama daraltır → sunucu değil istemci
            filtresi, ama operatörün gördüğü küme netleşir). */}
        {rowsCapped && (
          <div className="badge b-warn" style={{
            display: 'block', marginBottom: 10, padding: '7px 11px',
            fontSize: 11.5, lineHeight: 1.45, textTransform: 'none', letterSpacing: 0,
          }}>
            ⚠ Yalnız en yoğun {rowLimit} destination gösteriliyor — bu pencerede
            daha fazlası var. Liste çağrı hacmine göre kesildi; aradığın topic
            listede yoksa aramayı daralt ya da pencereyi kısalt.
          </div>
        )}
        {kSummary && <KafkaStrip s={kSummary} />}
        {kSummary && (
          <Tabs value={view} onValueChange={v => setView(v as MsgView)} className="msg-kafka-tabs">
            <TabsList variant="line" aria-label="Messaging görünümü">
              <TabsTrigger value="topics">Topics<span className="tab-count">{fmtNum(rows.length)}</span></TabsTrigger>
              <TabsTrigger value="consumers">Consumers<span className="tab-count">{fmtNum(kSummary.consumerServices)}</span></TabsTrigger>
            </TabsList>
          </Tabs>
        )}
        {view === 'consumers' && <KafkaConsumersTable range={range} win={win} clusterOf={clusterOf} />}
        {view === 'topics' && <DependenciesTable
          rows={rows}
          kind="queue"
          range={range}
          compare={compare}
          openRowKey={openRowKey}
          onOpenRowChange={setOpenRow}
          state={depState}
          extraControls={
            // v0.10.1025 (R1) — /databases ile aynı ipucu: süren pencerede
            // prior sayaçları (çağrı, hata, üretim/tüketim) dolu kısma oranlı.
            <label style={{ fontSize: 11, display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer' }}
              title="Bu pencereyi bir önceki pencereyle karşılaştırır. Süren pencerede önceki pencerenin sayıları (çağrı, hata, üretim/tüketim), süren pencerenin dolu kısmına oranlanır. İkinci bir sunucu okuması ekler; varsayılan kapalı.">
              <input type="checkbox"
                checked={compare}
                onChange={e => setCompare(e.target.checked)} />
              Compare vs prior
            </label>
          } />}
      </PageShell>
    </>
  );
}

// v0.10.1961 — Kafka UI'deki özet şeridi: yalnız topic etiketli Kafka metriği varsa, anlık (pencere sonu).
// Karo topic sayfasının karosuyla aynı (HttpStat, .mtp-kafka-stats ızgarası).
function KafkaStrip({ s }: { s: KafkaListSummary }) {
  const hint = 'Kafka istemci metrikleri (VictoriaMetrics, topic etiketli), pencere sonundaki değer';
  return (
    <div className="mtp-kafka-stats msg-kafka-strip">
      <HttpStat label="Topics" title={hint} value={fmtNum(s.topics)} />
      <HttpStat label="Lag ≥ 1.000" title={`${hint}. kafka_consumer_records_lag_max, topic başına en kötü partition`} value={fmtNum(s.lagged)} tone={s.lagged > 0 ? 'warn' : undefined} />
      <HttpStat label="Produce/s" title={`${hint}. kafka_producer_record_send_rate`} value={fmtSmart(s.produce, 'reqps')} />
      <HttpStat label="Consume/s" title={`${hint}. kafka_consumer_records_consumed_rate`} value={fmtSmart(s.consume, 'reqps')} />
      <HttpStat label="Send errors/s" title={`${hint}. kafka_producer_record_error_rate`} value={fmtSmart(s.sendErr, 'reqps')} tone={s.sendErr > 0 ? 'err' : undefined} />
      <HttpStat label="Consumers" title={`${hint}. Tüketen tekil servis`} value={fmtNum(s.consumerServices)} />
    </div>
  );
}
