// KafkaClientsSection.tsx — v0.10.551 (Messaging Kafka Faz 2; operatör mockup
// onayı 2026-09-08). Çekmecede Consumers'tan SONRA, Top operations'tan ÖNCE:
// kaynak notu + iki CorePanelMulti (gönderim hatası/sn — servis; en yüksek lag —
// servis·istemci) + iki "son değer" tablosu (useDataTable). available=false →
// TEK satır soluk not (bölüm gizlenmez, sebep title'da). Liste sayfasına grafik
// KONMAZ (v0.9.834 kararı) — yalnız çekmece.
//
// v0.10.1097 — `set="clients"` (topic sayfasının "Kafka istemcileri" sekmesi)
// artık KafkaClientsTab'a devredilir: süzgeç/görünüm URL durumu, bağlantı
// paneli ve kısa kaynak satırı orada. Çekmece yolu (set yok / 'topic') bayt
// bayt aynı; "son değer" tablosu iki yolun ortak dosyasına (KafkaLastTable)
// taşındı.
import { lazy, Suspense, useMemo, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { KafkaAlertModal } from '@/pages/alerts/KafkaAlertModal'; // v0.10.554
import { Spinner } from '@/components/Spinner';
import { LazyMount } from '@/components/LazyMount';
import type { TimeRange } from '@/lib/types';
import { timeRangeToNs } from '@/lib/utils';
import { useMessagingClients } from '@/lib/queries/messaging';
import {
  kafkaBlockItems, kafkaDegradeTR, kafkaLastRows, kafkaPanelUnit, kafkaScopeNoteTR,
  KAFKA_CONSUMER_COLS, KAFKA_PRODUCER_COLS,
} from './kafkaClients';
import { KafkaLastTable } from './KafkaLastTable';
import { KafkaClientsTab } from './KafkaClientsTab';

const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

interface KafkaClientsSectionProps {
  system: string; cluster: string; destination: string; range: TimeRange;
  xRange: { from: number; to: number }; syncKey?: string;
  /**
   * v0.10.575 — istenecek SORU KÜMESİ. Verilmezse tel bugünkü hâlinde kalır
   * (çekmece hiçbirini geçmiyor, davranışı bayt-bayt aynı).
   *   • 'topic'   → çekmecenin beş sorusu, açıkça istenmiş hâli
   *   • 'clients' → bağlantı/gecikme/rebalance; v0.10.1097'ten beri
   *                 KafkaClientsTab çizer (blok adları sunucudan, genel ızgara).
   */
  set?: 'topic' | 'clients';
  /** Başlık metni (sayfa "Kafka istemcileri" sekmesinde kendi başlığını verir). */
  title?: string;
  /** Sorgu kapısı — sekme seçili değilken VM'e hiç gidilmez (ES/VM maliyet disiplini). */
  enabled?: boolean;
}

export function KafkaClientsSection(props: KafkaClientsSectionProps) {
  // Kapı kapalıyken sorgu HİÇ koşmaz; RQ bunu 'pending' diye raporlar ve
  // spinner dalı sonsuz bir "yükleniyor" çizerdi (v0.9.748 sınıfı).
  if (props.enabled === false) return null;
  if (props.set === 'clients') {
    return (
      <KafkaClientsTab system={props.system} cluster={props.cluster} destination={props.destination}
        range={props.range} xRange={props.xRange} syncKey={props.syncKey} title={props.title} />
    );
  }
  return <KafkaClientsDrawerSection {...props} />;
}

function KafkaClientsDrawerSection({ system, cluster, destination, range, xRange, syncKey, set, title }: KafkaClientsSectionProps) {
  // timeRangeToNs MEMO içinde (v0.5.184 sonsuz refetch sınıfı).
  const win = useMemo(() => timeRangeToNs(range), [range]);
  const q = useMessagingClients({ system, cluster, destination, fromNs: win.from, toNs: win.to, set });
  const [alertOpen, setAlertOpen] = useState(false); // v0.10.554 — lag alarmı modalı
  if (q.isPending) {
    return <div className="kc-line" role="status" aria-busy="true"><Spinner /> Kafka istemci metrikleri…</div>;
  }
  const data = q.isError ? null : (q.data ?? null);
  const degrade = kafkaDegradeTR(data);
  if (degrade || !data) {
    return <div className="kc-line" title={data?.note}>◌ {degrade}</div>;
  }
  const err = kafkaBlockItems(data.blocks.producer_error_rate);
  const lag = kafkaBlockItems(data.blocks.consumer_lag_max);
  const producers = kafkaLastRows(data.blocks, KAFKA_PRODUCER_COLS.map(c => c.id));
  const consumers = kafkaLastRows(data.blocks, KAFKA_CONSUMER_COLS.map(c => c.id));
  const errBlock = data.blocks.producer_error_rate;
  const lagBlock = data.blocks.consumer_lag_max;
  const scopeNote = kafkaScopeNoteTR(data.scope);
  return (
    <section className="kc-sec" aria-label="Kafka istemcileri (metrik)">
      <div className="kc-head">
        <span aria-hidden className="kc-dot" />
        {title ?? 'Kafka istemcileri'} · METRİK ({data.source})
        {data.consumers.length > 0 && (
          <Button variant="secondary" size="sm" style={{ marginLeft: 'auto' }} onClick={() => setAlertOpen(true)}
            title="Bu topic için tüketici lag alarmı (istemcinin gördüğü lag; consumer group lag'i değil)">
            Lag alarmı
          </Button>
        )}
      </div>
      {alertOpen && (
        <KafkaAlertModal open onClose={() => setAlertOpen(false)} services={data.consumers}
          target={{ topic: destination }} defaultMetric="kafka_lag_max" />
      )}
      {/* v0.10.575 — KAPSAM BEYANI notun ÜSTÜNDE. `set=clients` aileleri Kafka
          istemcisinde topic etiketi taşımıyor: seriler bu topic'e dokunan
          SERVİSLERİN tamamı. Beyansız bir panel, başka bir topic'in yükünü
          buranınmış gibi okutur. */}
      {scopeNote && <div className="kc-scope">{scopeNote}</div>}
      <div className="kc-note">
        {data.note}
      </div>
      <div className="kc-grid">
        <LazyMount minHeight={170}>
          <Suspense fallback={<div className="kc-fallback"><Spinner /></div>}>
            <CorePanelMultiLazy
              title="Gönderim hatası/sn — servis"
              storageKey="msg-drawer-kafka-err"
              height={150} unit={kafkaPanelUnit(errBlock?.unit)} xRange={xRange} syncKey={syncKey}
              note={errBlock?.error ? `sorgu hatası: ${errBlock.error}` : err.truncated ? `+${err.truncated} seri gösterilmiyor` : undefined}
              items={err.items} />
          </Suspense>
        </LazyMount>
        <LazyMount minHeight={170}>
          <Suspense fallback={<div className="kc-fallback"><Spinner /></div>}>
            <CorePanelMultiLazy
              title="İstemcinin gördüğü en yüksek lag — servis · istemci"
              storageKey="msg-drawer-kafka-lag"
              height={150} unit={kafkaPanelUnit(lagBlock?.unit)} xRange={xRange} syncKey={syncKey}
              note={lagBlock?.error ? `sorgu hatası: ${lagBlock.error}` : lag.truncated ? `+${lag.truncated} seri gösterilmiyor` : 'partition başına, bu istemcinin gördüğü; group lag değil'}
              items={lag.items} />
          </Suspense>
        </LazyMount>
      </div>
      <div className="kc-grid">
        <KafkaLastTable storageKey="deps-kafka-producers" title="Üreticiler (son değer)" keyLabel="Servis"
          rows={producers} cols={KAFKA_PRODUCER_COLS} />
        <KafkaLastTable storageKey="deps-kafka-consumers" title="Tüketiciler (son değer)" keyLabel="Servis · istemci"
          rows={consumers} cols={KAFKA_CONSUMER_COLS} />
      </div>
    </section>
  );
}
