import { useMemo, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft } from 'lucide-react';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { Badge } from '@/components/ui';
import { Button } from '@/components/ui/Button';
import { PageShell } from '@/components/ui/PageShell';
import { ShareButton } from '@/components/ShareButton';
import { RootCauseRibbon } from '@/components/RootCauseRibbon';
import { AIExplainButton } from '@/components/ai/AIExplainButton';
import { IconSparkles } from '@/components/icons';
import { CosreChart } from '@/components/CosreChart';
import { ProblemVerdictActions } from '@/components/ProblemVerdictActions';
import { api } from '@/lib/api';
import { keys, useAnomalyEventByID, useLogPatternSeries } from '@/lib/queries';
import { useEscLayer } from '@/lib/escLayer';
import { DEFAULT_DURATIONS } from '@/lib/actions';
import { anomalyEventSilenceBody } from '@/lib/inboxDrawer';
import { anomalyVerdictSubject } from '@/lib/problemVerdict';
import { tsLong } from '@/lib/utils';
import type { AnomalyEvent } from '@/lib/types';
import { Sect, SignalLink, DeployBox, DetailSummary } from './detailSections';
import { anomalySummary, anomalyWhenLine } from './detailSummary';
import {
  ANOMALY_CHART_EMPTY, ANOMALY_KIND_PLAIN, anomalyChart, anomalyChartWindow, anomalySignalHrefs,
  behaviorDetailsOf, findAnomalyEventInCache, isLogAnomalyKind, sampleTraceHref,
} from './anomalyDetail';
import { AnomalyLogVolume, AnomalySample, BehaviorDetailsBox, SpikeFacts } from './anomalyDetailParts';
import { hasLogPatternSeries, logPatternSeriesArgs, patternLogsPivot } from './logPatternSeries';
import { LogPatternCountSection } from './LogPatternCountSection';

// AnomalyEventDetail — anomali olayının TAM SAYFA detayı (v0.10.1032).
//
// Operatör (prod): "Anomali ve alert rule'lara girdiğimde drawer çıkıyor.
// Exception gibi detay gözükmüyor." Problems kuyruğunda (/inbox) exception
// satırı v0.9.341'den beri tam sayfa açıyordu; anomali satırı ise 560px'lik
// triyaj çekmecesini (başlık + kök-neden şeridi + birkaç düğme). Anomali
// olayının çekmeceden zengin bir sayfası HİÇ yoktu — yalnız /anomalies
// üstündeki AnomalyDetailDrawer. Bu sayfa o boşluğu kapatıyor ve kuyruğun
// içinde, ?anomaly=<id> ile YERİNDE açılıyor (AlertProblemHost'un ?problem=
// deseninin aynısı: kuyruk display:none ile MOUNT'lu kalır, "← Problems"
// süzgeç / seçim / kaydırma hâline geri döner).
//
// İKİNCİ operatör kararı, aynı sürüm: "Anlaşılır olsun. Çok detay verince
// daha anlaşılır olmuyor — alert ve anomaliler de." Sayfa YALIN: varsayılan
// görünümde sırayla
//   1. tek cümlelik özet + "ne zaman" satırı (./detailSummary, tablo testli),
//      hemen altında TEK satırlık triyaj (Mute… + "Gerçek problem mi?"),
//   2. türün TEK grafiği (log deseni: desen sayısı barları — v0.10.1060;
//      diğer log türleri: log hacmi; trace / gecikme / davranış: Seyir) ve
//      kapalı kök-neden şeridi (açılınca çeker),
//   3. "Ne yapabilirim" (log / trace / servis sayfası pivotları; servissiz
//      log deseni olayında desene uyan loglar — v0.10.1062).
// Geri kalan her şey — olgu ızgarası, robust z, kıyas saati, ham örnek,
// cluster'lar, deploy — TEK kapalı "Teknik ayrıntı" bölümünde (kalıcı değil).
// Özetteki sayı görünür bloklarda tekrar basılmaz.
//
// Görsel dil AlertProblemDetail'inki: rb-bar şeridi + pd-cols iki kolon Sect
// blokları (./detailSections). Anlatım parçaları /anomalies çekmecesiyle ORTAK
// (./anomalyDetailParts, ./anomalyDetail) — aynı olay iki ekranda ayrışamaz.
//
// ES-maliyet disiplini korunur: log türlerinde tek sınırlı log hacmi / desen
// sayısı çağrısı, diğerlerinde tek Seyir grafiği (CosreChart, span rollup —
// ES değil) — hepsi YALNIZ sayfa açıkken, yoklama yok (tek istisna: aktif log
// deseni olayında desen sayısı 60 s, gizli sekmede durur); kök neden ve Teknik ayrıntı
// açılmadan hiçbir şey çekmez. Tekil okuma (useAnomalyEventByID)
// placeholderData: undefined: başka bir olayın gövdesi bu adresin altında
// görünemez.

// ── Host: ?anomaly=<id> → olay ────────────────────────────────────────────
//
// Tekil okuma (GET /api/anomalies/event?id=) HER ZAMAN koşar ve gerçeğin
// kaynağıdır. v0.10.1032 (inceleme) iki düzeltme:
//   • uç artık liste ucunun zenginleştirme zincirinden geçiyor (cluster,
//     son deploy, kök-neden özeti, karar — internal/api/anomaly_event_get.go):
//     /inbox liste önbelleğini hiç doldurmadığı için ana yol BU okuma ve kök-
//     neden çipi hipotez varken "no clear cause yet" diyordu;
//   • önbellek yalnız ANINDA İLK BOYAMA: /anomalies ya da servis Overview'u
//     açılmışsa olay listesindeki satır hemen çizilir, tekil okuma gelince
//     onun yerini alır. Eskiden önbellek bulunursa okuma hiç koşmuyordu ve
//     dakikalar önce bitmiş bir olay "ACTIVE · sürüyor" diye donuk kalıyordu.
// Liste ucu BİLEREK çağrılmıyor (60 s yoklamalı sorgu = yeni yoklama döngüsü).
//
// "Kayıt yok" YALNIZ sunucu 404 dediğinde (data === null) — önbellekte satır
// olsa bile sunucunun cevabı kazanır; ağ / sunucu hatası, önbellekte satır
// yoksa, ayrı ekran ve yeniden dene düğmesi taşır.
export function AnomalyEventHost({ id, isAdmin, onBack, backLabel = '← Problems' }: {
  id: string;
  isAdmin: boolean;
  onBack: () => void;
  backLabel?: string;
}) {
  const qc = useQueryClient();
  const cached = useMemo(
    () => findAnomalyEventInCache(
      qc.getQueryData<{ items?: AnomalyEvent[] | null }>(keys.anomalies.events), id),
    [qc, id]);
  const byID = useAnomalyEventByID(id);
  // Kimlik denetimi kemer + askı: placeholderData kapalı olsa da dönen kayıt
  // bu adresin olayı değilse ÇİZİLMEZ.
  const fromRead = byID.data && byID.data.id === id ? byID.data : undefined;
  const gone = byID.isSuccess && byID.data === null;
  const ev = gone ? undefined : (fromRead ?? cached);

  if (ev) {
    return (
      <>
        <Topbar title="Anomali" />
        <AnomalyEventDetail event={ev} isAdmin={isAdmin} onBack={onBack} />
      </>
    );
  }
  if (gone) {
    return (
      <>
        <Topbar title="Anomali" />
        <PageShell>
          <Empty icon="❓" title="Anomali kaydı yok">
            Bu kimlikle bir anomali olayı bulunamadı — kayıt saklama penceresini
            aşmış ya da bağlantı eksik kopyalanmış olabilir.{' '}
            <Button variant="secondary" size="sm" onClick={onBack}>{backLabel}</Button>
          </Empty>
        </PageShell>
      </>
    );
  }
  if (byID.isError) {
    return (
      <>
        <Topbar title="Anomali" />
        <PageShell>
          <Empty icon="⚠" title="Anomali yüklenemedi">
            <Button variant="secondary" size="sm" onClick={() => { void byID.refetch(); }}>Tekrar dene</Button>{' '}
            <Button variant="secondary" size="sm" onClick={onBack}>{backLabel}</Button>
          </Empty>
        </PageShell>
      </>
    );
  }
  return (
    <>
      <Topbar title="Anomali" />
      <PageShell><Spinner /></PageShell>
    </>
  );
}

// ── Detay ─────────────────────────────────────────────────────────────────

export function AnomalyEventDetail({ event, isAdmin, onBack }: {
  event: AnomalyEvent;
  isAdmin: boolean;
  onBack: () => void;
}) {
  // Esc = geri (AlertProblemDetail / ProblemDetail ile aynı kas hafızası);
  // üstte açılan bir modal Esc'i önce alır (katman yığını, v0.9.950).
  useEscLayer(true, onBack);

  // Pencere + kanıt + pivotlar memo'lu: her render'da yeni nesne, histogram /
  // grafik sorgularının anahtarını değiştirip yeniden çekerdi (v0.5.184).
  const win = useMemo(
    () => anomalyChartWindow({ startedAt: event.startedAt, lastSeen: event.lastSeen }),
    [event.startedAt, event.lastSeen]);
  const details = useMemo(
    () => behaviorDetailsOf({ kind: event.kind, sample: event.sample }),
    [event.kind, event.sample]);
  const hrefs = useMemo(
    () => anomalySignalHrefs({ kind: event.kind, service: event.service, pattern: event.pattern }, win),
    [event.kind, event.service, event.pattern, win]);
  const chart = useMemo(
    () => anomalyChart({ kind: event.kind, service: event.service, pattern: event.pattern }, win, details),
    [event.kind, event.service, event.pattern, win, details]);
  const chartPresentation = useMemo(
    () => (chart ? { title: chart.title, seriesName: chart.seriesName, emptyNote: ANOMALY_CHART_EMPTY } : undefined),
    [chart]);
  const sampleTrace = sampleTraceHref({ kind: event.kind, sample: event.sample });
  const isLog = isLogAnomalyKind(event.kind);
  const patternSeries = hasLogPatternSeries(event);
  const sentence = anomalySummary(event, details);
  // Bitmiş (cleared) olayda bitiş = son gözlem (durum last_seen tazeliğinden
  // türer: chstore GetAnomalyEvent). v0.10.1049 — yinelenen olayda satırın
  // sonunda tek ek: "yinelenen · bu N. kez · ilk kez <tarih>" (yeni bölüm yok).
  const when = anomalyWhenLine(event);

  return (
    <PageShell>
      <div className="rb-bar">
        <Button variant="secondary" onClick={onBack} leftIcon={<ArrowLeft size={14} strokeWidth={1.75} />}>
          Problems
        </Button>
        {/* v0.10.929 (K5) — çekmeceyle AYNI ton: ACTIVE normal durum → nötr;
            CLEARED bir geçiş, yeşil kalır. */}
        <Badge tone={event.status === 'active' ? 'neutral' : 'success'}>
          {event.status === 'active' ? 'ACTIVE' : 'CLEARED'}
        </Badge>
        {/* v0.10.1032 (inceleme) — tür rozeti düz Türkçe ("Operasyon hatası"). */}
        <span className="badge b-gray">{ANOMALY_KIND_PLAIN[event.kind] ?? String(event.kind)}</span>
        <span className="spacer" />
        <ShareButton copiedLabel="Copied" />
      </div>

      {/* Ne oldu + ne zaman. Log türlerinde cümle "bu desen" diyor — hangi
          desen olduğu hemen altında (sayı değil, kimlik). */}
      <DetailSummary sentence={sentence} when={when}
        detail={isLog && event.pattern ? event.pattern : undefined} />
      {/* Triyaj TEK satır, özetin hemen altında (alarm sayfasıyla aynı yer). */}
      <AnomalyTriage event={event} isAdmin={isAdmin} onBack={onBack} />

      <div className="pd-cols pd-cols-15">
        {/* ── Sol kolon: türün tek grafiği, kök neden, teknik ayrıntı ── */}
        <div style={{ minWidth: 0 }}>
          {/* v0.10.1060 (operatör: "artışın ne zaman başladığını
              göstermiyor. Elastic'e gidip bakınca barlardan net görüyorum")
              — log_pattern'ın tek grafiği desenin KENDİ sayısı (bar, olay
              başlangıcında işaret); servisin genel log hacmi bu türde
              çizilmez ("Logları aç" pivotu duruyor). */}
          {patternSeries && <LogPatternCountSection event={event} />}
          {/* Diğer log biçimli türler (yeni log biçimi, Elastic ML): servisin
              spike çevresindeki log hacmi — çekmeceyle aynı TEK sınırlı çağrı,
              yalnız sayfa açıkken. */}
          {isLog && !patternSeries && event.service && (
            <Sect title="Log hacmi" sub="servis · olay penceresi">
              {/* Bölüm başlığı zaten söylüyor: bileşenin İngilizce alt yazısı
                  burada basılmaz; boş / hata için sayfanın kendi notu. */}
              <AnomalyLogVolume service={event.service} win={win} bare />
            </Sect>
          )}
          {/* Trace / gecikme / davranış türleri: olayın KENDİ sinyali, olay
              penceresiyle, düz Türkçe başlıkla. Mevcut CosreChart: tek sınırlı
              span sorgusu, yoklama YOK, yeni uç YOK. Çizilemeyen tür (dış
              kaynak metriği, ayrıştırılamayan kanıt) için bölüm hiç çizilmez —
              ilgisiz bir seri "grafik var" güvenini yanlış yere taşırdı. */}
          {chart && (
            <Sect title="Seyir" sub={chart.spec.operation ? 'operasyon · olay penceresi' : 'servis · olay penceresi'}>
              <CosreChart spec={chart.spec} presentation={chartPresentation} />
            </Sect>
          )}

          <Sect title="Kök neden">
            {/* KAPALI şerit (operatör: "çok detay verince daha anlaşılır
                olmuyor"): çip sıfır istek; tık tam kök-neden dökümünü O ZAMAN
                çeker. AI açıklaması sağ AI çekmecesine açılır (?ai=anomaly:<id>);
                satır-içi CopilotExplain yalnız /anomalies çekmecesinde kalır
                (çekmece üstüne çekmece açılmasın, v0.9.477). */}
            <RootCauseRibbon anchor="anomaly" id={event.id} summary={event.rootCause}
              window={win}
              trailing={(
                <AIExplainButton subject={{ kind: 'anomaly', id: event.id }}
                  label={<><IconSparkles /> <span>Açıkla</span></>} />
              )} />
          </Sect>

          {/* Geri kalan her şey TEK kapalı bölümde; kapalıyken mount edilmez. */}
          <Sect title="Teknik ayrıntı" collapsible>
            <AnomalyTechnical event={event} win={win} hasDetails={!!details} sampleIsTrace={!!sampleTrace} />
            {details && <BehaviorDetailsBox d={details} />}
          </Sect>
        </div>

        {/* ── Sağ kolon: ne yapabilirim ── */}
        <div style={{ minWidth: 0 }}>
          <Sect title="Ne yapabilirim">
            {/* Servis adı gerektiren pivotlar servis yoksa hiç basılmaz —
                boş açılan bir liste "olay yok" diye okunur (v0.9.1331).
                v0.10.1062 — servissiz log deseni olayında servis gerekmez:
                desene uyan satırlar (PatternLogsAction). */}
            {hrefs ? (
              <>
                <SignalLink to={hrefs.logs} label="Logları aç"
                  sub={patternSeries ? 'servis + desen, olay penceresi' : 'servis, olay penceresi'} />
                {hrefs.operationTraces
                  ? <SignalLink to={hrefs.operationTraces}
                      label={event.kind === 'trace_op' ? "Operasyonun hatalı trace'leri" : "Operasyonun trace'leri"}
                      sub="olay penceresi" />
                  : <SignalLink to={hrefs.errorTraces} label="Hatalı trace'ler" sub="servis, olay penceresi" />}
                {sampleTrace && (
                  <SignalLink to={sampleTrace} label="Örnek trace" sub="tespit anındaki en çarpıcı istek" />
                )}
                <SignalLink to={hrefs.servicePage} label="Servis sayfası" sub="olay penceresiyle" />
              </>
            ) : patternSeries ? (
              <PatternLogsAction event={event} win={win} />
            ) : (
              <NoServiceNote />
            )}
          </Sect>
        </div>
      </div>
    </PageShell>
  );
}

function NoServiceNote() {
  return (
    <div className="pd-pivot-note">
      Bu olay bir servis adı taşımıyor — log / trace / servis pivotları bir servis adı gerektiriyor.
    </div>
  );
}

// PatternLogsAction — v0.10.1062 (operatör, prod ES: servissiz log deseni
// anomalisinde bu kart yalnız "servis adı yok" diyordu, operatör Kibana'ya elle
// gidiyordu). Servissiz log_pattern olayının tek eylemi: olay penceresinde
// desene uyan satırlarla "Logları aç" + biliniyorsa tek satır "En çok: …".
// v0.10.1071 — bağlantı `pattern=<desen adı>` (arama metni değil): /logs
// sunucusu dedektörün yüklemini uygular, grafikle aynı sayım. Ad olayın
// kendisinden, yani bağlantı okumayı BEKLEMEZ; "En çok" servisleri desen
// sayısı okumasından (grafik bölümüyle AYNI anahtar, ikinci istek yok;
// yoklamayı bölüm yapar, burada live: false). Okuma 404 (desenin tanımı
// artık yok) → sunucu /logs?pattern= isteğini de reddeder: sahte bağlantı
// yok, eski dürüst cümle.
function PatternLogsAction({ event, win }: {
  event: AnomalyEvent;
  win: { fromNs: number; toNs: number };
}) {
  const args = useMemo(
    () => logPatternSeriesArgs({ pattern: event.pattern, startedAt: event.startedAt, lastSeen: event.lastSeen, status: event.status }),
    [event.pattern, event.startedAt, event.lastSeen, event.status]);
  const q = useLogPatternSeries(args, { live: false });
  const pivot = useMemo(
    () => (q.data === null ? null : patternLogsPivot(event.pattern, q.data, win)),
    [event.pattern, q.data, win]);
  if (pivot) {
    return (
      <>
        <SignalLink to={pivot.href} label="Logları aç" sub="desene uyan satırlar, olay penceresi" />
        {pivot.topServices.length > 0 && (
          <div className="pd-pivot-note">En çok: {pivot.topServices.join(', ')}</div>
        )}
      </>
    );
  }
  return <NoServiceNote />;
}

// AnomalyTechnical — "Teknik ayrıntı" gövdesi (yalnız bölüm açıkken mount):
// çekmecenin olgu ızgarası, ham örnek (yapılandırılmış kanıt ya da trace
// kimliği değilse), tam desen, cluster'lar, pencere, deploy, olay kimliği.
function AnomalyTechnical({ event, win, hasDetails, sampleIsTrace }: {
  event: AnomalyEvent;
  win: { fromNs: number; toNs: number };
  hasDetails: boolean;
  sampleIsTrace: boolean;
}) {
  return (
    <>
      <SpikeFacts event={event} />
      {event.sample && !hasDetails && !sampleIsTrace && <AnomalySample sample={event.sample} />}
      <div style={{ display: 'grid', gap: 4, fontSize: 12, color: 'var(--text2)', marginBottom: 12, minWidth: 0 }}>
        <div style={{ overflowWrap: 'anywhere' }}>desen <span className="mono">{event.pattern || '—'}</span></div>
        {(event.clusters ?? []).length > 0 && (
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'center' }}>
            cluster
            {(event.clusters ?? []).map(c => (
              <span key={c} className="pb-pill"><span className="dot" /> <span className="mono">{c}</span></span>
            ))}
          </div>
        )}
        <div>pencere <span className="mono">{tsLong(win.fromNs)} → {tsLong(win.toNs)}</span></div>
        <div>kimlik <span className="mono">{event.id}</span></div>
      </div>
      {event.recentDeploy && (
        <DeployBox version={event.recentDeploy.version} ageSeconds={event.recentDeploy.ageSeconds}
          before="the spike started" />
      )}
    </>
  );
}

// AnomalyTriage — çekmecenin anomali eylemleri tam sayfada (v0.10.1032):
// Mute… (süre + api.createAnomalySilence; gövde lib/inboxDrawer'ın ORTAK
// kurucusundan, çekmeceyle aynı parmak izi) ve öğretme düğmeleri ("Gerçek
// problem / Problem değil", imza a:<tür>|<servis>|<desen>). Başarılı susturma
// ya da "Problem değil" satırı varsayılan kuyruktan çıkarır → kuyruk ve
// anomali sorguları tazelenir, sayfa kuyruğa döner (çekmecenin kendini
// kapatmasının ikizi). Mute editor/admin'e; viewer kararı salt okunur görür.
// v0.10.1032 (inceleme: "Triyaj bloğu çok büyük") — kutu ve paragraf YOK:
// özetin altında TEK satır, "[1h ▾] [Mute…] · Gerçek problem mi? …".
function AnomalyTriage({ event, isAdmin, onBack }: {
  event: AnomalyEvent;
  isAdmin: boolean;
  onBack: () => void;
}) {
  const qc = useQueryClient();
  const [durationSec, setDurationSec] = useState<number>(60 * 60);
  const muteMut = useMutation({
    mutationFn: () => {
      const body = anomalyEventSilenceBody(
        { id: event.id, kind: event.kind, pattern: event.pattern, service: event.service }, durationSec);
      if (!body) throw new Error('anomaly id missing');
      return api.createAnomalySilence(body);
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.inbox.all });
      void qc.invalidateQueries({ queryKey: keys.anomalies.all });
      onBack();
    },
  });
  return (
    <div className="pd-triage" role="group" aria-label="Triyaj">
      {isAdmin && (
        <>
          <select value={durationSec}
            onChange={e => setDurationSec(Number(e.target.value))}
            aria-label="Susturma süresi"
            title="Susturma süresi"
            style={{ fontSize: 12, padding: '4px 8px' }}>
            {DEFAULT_DURATIONS.map(d => (
              <option key={d.seconds} value={d.seconds}>{d.label}</option>
            ))}
          </select>
          <Button variant="secondary" size="sm" loading={muteMut.isPending} onClick={() => muteMut.mutate()}>
            Mute…
          </Button>
          {muteMut.isError && <span role="alert" className="field-error">Susturma yazılamadı — yeniden deneyin.</span>}
          <span className="pd-triage__sep" aria-hidden="true">·</span>
        </>
      )}
      <ProblemVerdictActions subject={anomalyVerdictSubject(event)} compact
        onDone={next => { if (next === 'noise') onBack(); }} />
    </div>
  );
}
