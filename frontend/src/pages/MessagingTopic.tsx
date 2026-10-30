// MessagingTopic.tsx — /messaging/topic, tek bir topic'in tam sayfa detayı
// (v0.10.575, operatör mockup onayı 2026-09-09).
//
// NEDEN SAYFA. /messaging satırının çekmecesi bugün BEŞ tabloyu ve iki grafiği
// tablonun altındaki 12px dolgulu tek bir hücrede taşıyor: yayıncılar,
// tüketiciler, diğer istemciler, operasyon kırılımı, Kafka istemci blokları ve
// span adları. Aynı geçişi /databases v0.9.840'ta, /endpoints bir sürüm
// öncesinde yaptı. FARK: burada ÇEKMECE KALIYOR (operatör kararı) — satır
// tıklaması bugünkü gibi çalışıyor, sayfa ikinci bir kapı. İkisi aynı anda
// açılabildiği için ortak gövde PAYLAŞILIYOR, kopyalanmıyor:
// `features/dependencies/CallerSection.tsx` v0.10.575'te tam bu yüzden dışarı
// çıkarıldı (pages/databases/detailSections.tsx başlığındaki "iki kabuk, tek
// bölüm" sözleşmesi).
//
// YENİ UÇ YOK. İki okuma da mevcut: /api/messaging/detail (skor şeridi, e2e,
// çağıranlar, operasyonlar, span adları — TEK çağrı, açılışta) ve
// /api/messaging/clients (VM seam). MALİYET DİSİPLİNİ ikincisinde: açılışta
// yalnız `set=chart` (İKİ soru — gönderim ve tüketim hızı), `set=clients`
// (bağlantı/gecikme/rebalance) YALNIZ o sekme seçilince. Kalan üç sekme ek
// istek YAPMAZ; verileri açılıştaki detay yükünden geliyor.
//
// KİMLİK ÜÇLÜ ve URL'de AÇIK: `?system=&cluster=&destination=`. Cluster
// eksikse sayfa "linkte cluster yok" der — sessizce "(default)" VARSAYMAZ
// (v0.9.973: varsayılan cluster, çok-cluster kurulumda canlı bir topic için
// sıfırlanmış sayı üretiyordu ve sıfır "topic boşta" ile ayırt edilemiyordu).
import { lazy, Suspense, useMemo } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/coremetry/PageShell';
import { TabStrip } from '@/components/ui';
import { Spinner, Empty } from '@/components/Spinner';
import { LazyMount } from '@/components/LazyMount';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { CallerSection } from '@/features/dependencies/CallerSection';
import { KafkaTopicMetrics } from '@/features/dependencies/KafkaTopicMetrics'; // v0.10.1539
import { kindSeries } from '@/features/dependencies/msgSeries';
import { opLabelTR, isOpMissing, msgOperationRows, OP_MISSING_TITLE } from '@/features/dependencies/msgOperations';
import { useMessagingTopicDetail } from '@/lib/queries/messaging';
import { messagingTracesHref } from '@/lib/pivotHref';
import { traceHref } from '@/lib/traceHref';
import { navHref } from '@/lib/navHref';
import { msSyncKey } from '@/lib/chart/syncNamespace';
import { fmtNum, fmtNs, timeRangeToNs } from '@/lib/utils';
import { useUrlRange, DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { parseTopicRef, parseTopicTab, type MsgTopicTab } from './messaging/topicHref';
import type { DBOpStat, MsgOperationStat, TimeRange } from '@/lib/types';

// Çekmecedeki gerekçenin aynısı (v0.9.814): @grafana paketleri statik bağlanırsa bu
// rotanın chunk'ı ~1 MB büyür ve grafik GÖRÜNMESE de ödenir.
const CorePanelMultiLazy = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

export default function MessagingTopicPage() {
  const [params, setParams] = useSearchParams();
  const [range, setRange] = useUrlRange(DEFAULT_RANGE_PRESET);
  // `search` bir STRING: memo kimliği ancak URL gerçekten değişince değişir
  // (useSearchParams her render'da yeni bir nesne verir).
  const search = params.toString();
  const ref = useMemo(() => parseTopicRef(search), [search]);
  const tab = parseTopicTab(params.get('tab'));

  // URL = tek doğruluk kaynağı. Yazım `prev`den TÜRETİLİYOR (yabancı param —
  // env, gelecekteki filtreler — korunur) ve `replace: true` (sekme gezinmesi
  // geçmişe yığılmaz; geri tuşu ÖNCEKİ SAYFAYA döner). Varsayılan sekme
  // param'ı SİLER: "tab yok" ile "tab=producers" tek adres olur.
  const setTab = (next: MsgTopicTab) => setParams(prev => {
    const p = new URLSearchParams(prev);
    if (next === 'producers') p.delete('tab'); else p.set('tab', next);
    return p;
  }, { replace: true });

  // timeRangeToNs YALNIZ memo içinde — çıplak JSX'te sonsuz refetch (v0.5.184).
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);
  const xRange = useMemo(() => ({ from: from / 1e9, to: to / 1e9 }), [from, to]);
  const win = useMemo(() => ({ from, to }), [from, to]);
  const system = ref?.system ?? '';
  const cluster = ref?.cluster ?? '';
  const destination = ref?.destination ?? '';
  // Sync grubu topic BAŞINA + '-ms' motor ad alanı (v0.9.789): iki farklı
  // topic'in panelleri aynı gruba düşerse imleç komşunun grafiğinde gezer.
  const syncKey = msSyncKey(ref ? `msg-topic:${system}|${cluster}|${destination}` : '');

  const detailQ = useMessagingTopicDetail({
    system, cluster, destination, fromNs: from, toNs: to, enabled: !!ref,
  });
  if (!ref) {
    return (
      <>
        <Topbar title="Topic" range={range} onRangeChange={setRange} />
        <PageShell>
          <Empty icon="⚠" title="Bu linkte topic yok">
            Adres <code>system</code>, <code>cluster</code> ya da{' '}
            <code>destination</code> taşımıyor. Cluster BİLEREK zorunlu:
            varsayılan bir cluster, çok-cluster bir kurulumda canlı bir topic
            için sıfır gösterir.
            {' '}<Link to={navHref('/messaging', search)}>Messaging listesine dön →</Link>
          </Empty>
        </PageShell>
      </>
    );
  }

  const d = detailQ.isPending ? undefined : detailQ.isError ? null : (detailQ.data ?? null);
  const callers = d?.callers ?? [];
  const producers = callers.filter(c => c.role === 'producer');
  const consumers = callers.filter(c => c.role === 'consumer');
  const others = callers.filter(c => c.role && c.role !== 'producer' && c.role !== 'consumer');
  const msgOps = msgOperationRows(d?.operations);
  const topOps = d?.topOps ?? [];
  const e2e = d?.e2e;

  return (
    <>
      {/* Env "uygulanıyor" bayrağı BİLEREK verilmiyor: messaging okumaları
          `?env=` uygulamıyor ve uygulanmayan bir kapsamı aktif göstermek,
          operatöre olmayan bir daraltma vaat eder (§4.3(b) dürüstlük
          sözleşmesi; kapısı lib/ altındaki env bayrağı testi). Bayrağın ADI hiç
          geçmiyor: o kapı kaynağı YORUMLARIYLA tarıyor, yani bir açıklama
          cümlesi bile iddia sayılır ("gate kendi metnini ısırır"). */}
      <Topbar title="Topic" range={range} onRangeChange={setRange} />
      <PageShell>
        <div className="mtp-crumb">
          {/* Geri linki PENCEREYİ TAŞIR (navHref): bağlam değiştiren bir geri
              linki, geri linki olmaktan çıkar (Pod.tsx:206-209, v0.9.965). */}
          <Link to={navHref('/messaging', search)}>Messaging</Link> › topic detayı
        </div>

        {/* KİMLİK: sistem ve cluster soluk, destination vurgulu — operatörün
            aradığı sözcük, adresin taşıdığı üç alandan biri. */}
        <div className="mtp-id">
          <span className="mtp-id-dim mono" title="messaging.system">{system}</span>
          <span aria-hidden className="mtp-id-sep">›</span>
          <span className="mtp-id-dim mono" title="messaging.kafka.cluster.name">{cluster}</span>
          <span aria-hidden className="mtp-id-sep">›</span>
          <span className="mtp-id-name mono">{destination}</span>
          <Link className="ud-pill mtp-id-act"
            to={messagingTracesHref({ window: range, system, destination })}
            title="Bu topic'in trace'leri — aynı pencere, süzgeçler düzenlenebilir çip olarak">
            Traces →
          </Link>
        </div>

        {/* v0.10.1539 — ÖNCE METRİK (operatör: "topic'e girince kullanıcı önce
            metrikleri görsün; ClickHouse'a gerek yok, doğrudan VM"): topic
            etiketli Kafka istemci metrikleri, span detayından BAĞIMSIZ. */}
        <KafkaTopicMetrics system={system} cluster={cluster} topic={destination} range={range} win={win} />

        <h3 className="mtp-kafka-h mtp-span-h">Trace tarafı (span)</h3>
        {detailQ.isPending && <Spinner label="Span detayı yükleniyor…" />}
        {d === null && (
          <Empty icon="⚠" title="Span detayı alınamadı">
            /api/messaging/detail isteği hata döndü; metrikler yukarıda.
          </Empty>
        )}

        {d && (
          <>
            <div className="mtp-cap">
              span üretici / tüketici <b>{producers.length}</b> / <b>{consumers.length}</b>
              {others.length > 0 && <> · diğer istemci <b>{others.length}</b></>}
              {' '}· {fmtNum(d.spanCount)} span · hata {d.errorRate.toFixed(2)} %
            </div>
            <div className="mtp-card">
              <E2EPanel e2e={e2e} range={range} xRange={xRange} syncKey={syncKey} />
            </div>

            <TabStrip ariaLabel="Topic sekmeleri" value={tab} onChange={setTab} tabs={[
              { key: 'producers', label: <>Üreticiler<span className="tab-count">{producers.length}</span></> },
              { key: 'consumers', label: <>Tüketiciler<span className="tab-count">{consumers.length}</span></> },
              { key: 'operations', label: <>Operasyonlar<span className="tab-count">{msgOps.length}</span></> },
              { key: 'spannames', label: <>Span adları<span className="tab-count">{topOps.length}</span></> },
            ]} />

            {tab === 'producers' && (
              <CallerSection
                title={`Üreticiler · ${producers.length} satır`}
                rows={producers}
                emptyMessage="Bu pencerede bu destination'a üretici span'i yok."
                tone="producer" range={range}
                storageKey="msg-topic-producers" />
            )}
            {tab === 'consumers' && (
              <CallerSection
                title={`Tüketiciler · ${consumers.length} satır`}
                rows={consumers}
                emptyMessage="Bu pencerede bu destination'a tüketici span'i yok."
                tone="consumer" range={range}
                storageKey="msg-topic-consumers" />
            )}
            {tab === 'operations' && <OperationsTable rows={msgOps} />}
            {tab === 'spannames' && (
              <SpanNamesTable rows={topOps} range={range} system={system} destination={destination} />
            )}
          </>
        )}
      </PageShell>
    </>
  );
}

// ── uçtan uca gecikme (SPAN, span_links korelasyonu) ────────────────────────

function E2EPanel({ e2e, range, xRange, syncKey }: {
  e2e: import('@/lib/types').MsgE2E | undefined;
  range: TimeRange;
  xRange: { from: number; to: number };
  syncKey?: string;
}) {
  const series = e2e?.series ?? [];
  // linkless = HİÇ çift korele olmadı. Bu 0 ms DEĞİL: SDK'lar messaging span
  // link'i yaymıyor demek, ve ikisini aynı ekrana yazmak yanlış teşhis üretir.
  if (!e2e || e2e.linkless) {
    return (
      <div className="kc-line">
        ◌ Bu pencerede üretici→tüketici span link&#39;i yok — SDK&#39;lar messaging
        span link&#39;i yaymıyor, uçtan uca gecikme korele edilemiyor.
      </div>
    );
  }
  return (
    <>
      {series.length > 1 ? (
        <LazyMount minHeight={190}>
          <Suspense fallback={<div className="kc-fallback"><Spinner /></div>}>
            <CorePanelMultiLazy
              title="Uçtan uca gecikme · kova ortalaması"
              storageKey="msg-topic-e2e"
              height={170} unit="ms" xRange={xRange} syncKey={syncKey}
              note="produce→consume, span_links korelasyonu; kova başına ORTALAMA (p50/p95 aşağıda, pencere geneli)"
              items={[{ name: 'E2E lag', role: 'data', series: kindSeries(series, p => p.avgMs, 'E2E lag') }]} />
          </Suspense>
        </LazyMount>
      ) : (
        <div className="kc-line">◌ Seri tek kovada — eğri çizilemiyor, aşağıdaki pencere değerleri geçerli.</div>
      )}
      <div className="mtp-e2e">
        <span className="badge b-gray mono">p50 {fmtNs(e2e.p50Ms * 1e6)}</span>
        <span className="badge b-gray mono">p95 {fmtNs(e2e.p95Ms * 1e6)}</span>
        <span className="badge b-gray mono">p99 {fmtNs(e2e.p99Ms * 1e6)}</span>
        <span className="mtp-e2e-count">{fmtNum(e2e.count)} korele çift</span>
        {e2e.slowestConsumerTraceId && (
          <Link to={traceHref(e2e.slowestConsumerTraceId, { pageRange: range })}
            title={e2e.slowestProducerTraceId
              ? `En yavaş korele çift — tüketicinin trace'ini açar (üretici trace ${e2e.slowestProducerTraceId})`
              : "En yavaş korele çift — tüketicinin trace'ini açar"}
            className="mtp-e2e-link">
            en yavaş {fmtNs((e2e.slowestLagMs ?? 0) * 1e6)} → trace
          </Link>
        )}
      </div>
    </>
  );
}

// ── Operasyonlar (messaging_summary_5m'in operation boyutu) ─────────────────

function OperationsTable({ rows }: { rows: MsgOperationStat[] }) {
  // v0.10.943 — tablo standardı dilim 3: kimlik kolonu `mono` bayrağında;
  // sayılar arayüz fontunda (S2), >100 satır `cv-row`.
  const cols = useMemo<ColumnDef<MsgOperationStat>[]>(() => [
    { id: 'operation', label: 'Operasyon', sortValue: o => o.operation, naturalDir: 'asc', flex: true, minWidth: 140, mono: true },
    { id: 'count',   label: 'Calls', sortValue: o => o.spanCount,     numeric: true, naturalDir: 'desc', width: 90 },
    { id: 'errRate', label: 'Err %', sortValue: o => o.errorRate,     numeric: true, naturalDir: 'desc', width: 90 },
    { id: 'avg',     label: 'Avg',   sortValue: o => o.avgDurationMs, numeric: true, naturalDir: 'desc', width: 84 },
    { id: 'p50',     label: 'P50',   sortValue: o => o.p50DurationMs, numeric: true, naturalDir: 'desc', width: 84 },
    { id: 'p95',     label: 'P95',   sortValue: o => o.p95DurationMs, numeric: true, naturalDir: 'desc', width: 84 },
    { id: 'p99',     label: 'P99',   sortValue: o => o.p99DurationMs, numeric: true, naturalDir: 'desc', width: 84 },
  ], []);
  const dt = useDataTable<MsgOperationStat>({
    storageKey: 'msg-topic-ops', columns: cols, rows,
    initialSort: { id: 'count', dir: 'desc' },
  });
  return (
    <div className="mtp-sec">
      <div className="mtp-sec-head">
        <span aria-hidden className="mtp-dot mtp-dot--op" />
        Operasyonlar · MV · {rows.length} satır
      </div>
      {/* TRACE PİVOTU YOK — bilerek (çekmecedeki gerekçenin aynısı):
          messagingTracesHref'in `operation` parametresi span ADINA çevriliyor,
          buradaki değer ise operasyon TÜRÜ (messaging.operation.type coalesce).
          İkisini eşitleyen link, var olamayacak satırlara giden ölü bir link
          olurdu — v0.9.256'nın kapattığı sınıf. */}
      {/* Bölüm GİZLENMİYOR: yokluğu söylemek, bakılmamış gibi görünmekten iyi.
          v0.10.954 — tablo standardı T12: soluk satır tablonun İÇİNDE, başlık durur. */}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0 ? (
              <DataTableState dt={dt} kind="empty" message="Bu pencerede MV'de operasyon satırı yok" />
            ) : dt.sortedRows.map((o, i) => {
              // v0.10.929 (K5) — %0 hata sağlıklı durum: nötr rozet, eşikler aynı.
              const errCls = o.errorRate > 5 ? 'err' : o.errorRate > 0 ? 'warn' : 'gray';
              const missing = isOpMissing(o.operation);
              return (
                <tr key={`${o.operation}|${i}`} className="cv-row">
                  <DataTableCell dt={dt} col="operation" row={o}
                      className={missing ? 'mtp-op-missing' : 'mtp-op'}
                      title={missing ? OP_MISSING_TITLE : o.operation}>
                    {opLabelTR(o.operation)}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="count" row={o} value={fmtNum(o.spanCount)} />
                  <DataTableCell dt={dt} col="errRate" row={o}>
                    <span className={`badge b-${errCls}`} style={{ fontSize: 9 }}
                          title={`${fmtNum(o.errorCount)} hatalı span`}>
                      {o.errorRate.toFixed(2)}%
                    </span>
                  </DataTableCell>
                  <DataTableCell dt={dt} col="avg" row={o} value={`${o.avgDurationMs.toFixed(1)}ms`} />
                  <DataTableCell dt={dt} col="p50" row={o} value={`${o.p50DurationMs.toFixed(1)}ms`} />
                  <DataTableCell dt={dt} col="p95" row={o} value={`${o.p95DurationMs.toFixed(1)}ms`} />
                  <DataTableCell dt={dt} col="p99" row={o} value={`${o.p99DurationMs.toFixed(1)}ms`} />
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ── Span adları (topOps) ───────────────────────────────────────────────────
//
// Çekmecedeki "Top operations" tablosunun İKİZİ DEĞİL, kardeşi: oradaki tablo
// İKİ KİPLİ (db → statement + LIKE-önekli pivot / queue → span adı) ve kendi
// iç kaydırma kabında yaşıyor. Burada kip TEK ve tablo sayfa boyunda; ortak
// bir gövdeye indirmek, `kind` propuyla iki kabuğu tek bileşende birleştirmek
// olurdu — pages/databases/detailSections.tsx başlığının DÖRT ısırıkla
// reddettiği şey.

function SpanNamesTable({ rows, range, system, destination }: {
  rows: DBOpStat[]; range: TimeRange; system: string; destination: string;
}) {
  const cols = useMemo<ColumnDef<DBOpStat>[]>(() => [
    { id: 'statement', label: 'Span adı', sortValue: o => o.statement, naturalDir: 'asc', flex: true, minWidth: 200, mono: true },
    { id: 'op', label: 'Type', sortValue: o => o.operation ?? '', naturalDir: 'asc', width: 96, mono: true },
    { id: 'count', label: 'Count', sortValue: o => o.count, numeric: true, naturalDir: 'desc', width: 110 },
    { id: 'avg', label: 'Avg', sortValue: o => o.avgDurationMs, numeric: true, naturalDir: 'desc', width: 110 },
  ], []);
  const dt = useDataTable<DBOpStat>({
    storageKey: 'msg-topic-spannames', columns: cols, rows,
    initialSort: { id: 'count', dir: 'desc' },
  });
  return (
    <div className="mtp-sec">
      <div className="mtp-sec-head">
        <span aria-hidden className="mtp-dot mtp-dot--span" />
        Span adları · {rows.length} satır
      </div>
      {/* v0.10.954 — tablo standardı T12: boş durum tablonun İÇİNDE, başlık durur. */}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0 ? (
              <DataTableState dt={dt} kind="empty" message="Bu pencerede span adı satırı yok" />
            ) : dt.sortedRows.map((o, i) => (
              <tr key={`${o.statement}|${i}`} className="cv-row">
                <DataTableCell dt={dt} col="statement" row={o} className="mtp-span-name">
                  {o.statement
                    ? (
                      <>
                        {o.statement}
                        {/* Span ADINA göre pivot — destination + name TAM
                            EŞLEŞME (v0.9.256'nın onardığı link). */}
                        <Link to={messagingTracesHref({
                          window: range, system, destination, operation: o.statement,
                        })}
                          title="Bu span adının trace'lerini aç"
                          className="mtp-row-link">
                          → traces
                        </Link>
                      </>
                    )
                    : <span className="mtp-op-missing">(boş)</span>}
                </DataTableCell>
                <DataTableCell dt={dt} col="op" row={o} className="mtp-op">{o.operation ?? '—'}</DataTableCell>
                <DataTableCell dt={dt} col="count" row={o} value={fmtNum(o.count)} />
                <DataTableCell dt={dt} col="avg" row={o} value={`${o.avgDurationMs.toFixed(1)}ms`} />
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

