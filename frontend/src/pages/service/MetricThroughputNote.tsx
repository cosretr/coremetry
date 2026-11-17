import type { MetricSourceKind, ServiceMetricThroughput } from '@/lib/types';
import { Badge } from '@/components/ui';
import { METRIC_SOURCE_LABELS } from '@/lib/metricSource';
import { t as tl, tf, useLang } from '@/lib/i18n'; // v0.10.2315 — not metni arayüz dilinde (mtn.*; TR aynı)

// v0.9.665 — metrik türevli throughput ÇİZİLEMEDİĞİNDE nedenini söyler.
//
// Operatör: "metricten okusun bakalım bir. Oluştursun görelim."
//
// Grafana o metriği PROMETHEUS'tan okuyor. Coremetry'ye de girip
// girmediği buradan görülemez — collector onu OTLP ile iletiyor mu, adı
// çeviride korunuyor mu, bilinmiyor. Bu yüzden özellik sessizce boş bir
// grafik göstermiyor.
//
// Ayırt edilen üç durum, çünkü ÜÇÜ DE FARKLI EYLEM istiyor:
//   1. Metrik kurulumda YOK        → collector'ı düzelt (ya da adı ara).
//   2. Metrik var, `job` eşleşmedi → deseni/etiketi düzelt. Gerçek job
//      değerleri gösteriliyor ki operatör kıyaslasın.
//   3. Metrik var, job etiketi yok → başka bir etiket taşıyor olabilir.
//
// Boş bir grafik üçünü de aynı gösterirdi ve teşhis operatöre kalırdı.

// v0.9.1268 — HANGİ DEPODA ARANDI.
//
// Operatör-bildirimi: bu not "bu servise eşleşen seri yok" diyordu, oysa
// metrik VictoriaMetrics'te VARDI ve aynı metriği okuyan komşu panel
// çiziyordu. Eşleyici ClickHouse'a çakılıydı — cevap doğruydu ama YANLIŞ
// deponun hakkındaydı. Backend düzeltildi; rozet, aynı körlüğün bir daha
// SESSİZ kalmamasını sağlıyor. "Seri yok" ile "yanlış yerde arandı"
// arasındaki farkı ekranda gösteren tek şey bu satır.
//
// Üç dalın ÜÇÜNDE de basılıyor. Not zaten yalnız seri boşken çiziliyor,
// yani rozetin göründüğü her an tam olarak teşhisin gerektiği an.
//
// `source` yoksa hiç çizilmiyor: alanı bilmeyen bir sunucuya bakan arayüz
// (rolling deploy) "ClickHouse'ta arandı" diye VARSAYMAMALI — varsayılanı
// basmak, düzeltmenin yalan söyleyebildiği tek yol olurdu.
function SearchedIn({ source }: { source?: MetricSourceKind }) {
  const lang = useLang();
  if (!source) return null;
  const label = METRIC_SOURCE_LABELS[source] ?? source;
  return (
    <Badge
      tone={source === 'vm' ? 'info' : 'neutral'}
      title={tf('mtn.searchedTip', lang, { label })}
    >
      {tf('mtn.searched', lang, { label })}
    </Badge>
  );
}

export function MetricThroughputNote({ d }: { d: ServiceMetricThroughput }) {
  const lang = useLang();
  const box: React.CSSProperties = {
    marginTop: 8, padding: '8px 10px', borderRadius: 6,
    background: 'var(--bg2)', border: '1px solid var(--border)',
    fontSize: 11.5, lineHeight: 1.5, color: 'var(--text2)',
  };
  const searched = <SearchedIn source={d.source} />;

  if (d.unsupportedInstrument) {
    return (
      <div style={box}>
        <b>{tl('mtn.unsup', lang)}</b> {searched}
        <div style={{ marginTop: 4 }}>
          <code>{d.metric}</code> · instrument <code>{d.instrument || '?'}</code>
        </div>
        <div style={{ color: 'var(--text3)', marginTop: 4 }}>
          {tl('mtn.unsupWhy', lang)}
        </div>
      </div>
    );
  }

  if (!d.metricExists) {
    return (
      <div style={box}>
        <b>{tl('mtn.absent', lang)}</b> {searched}
        {(d.tried?.length ?? 0) > 0 && (
          <div style={{ marginTop: 4 }}>
            {tl('mtn.tried', lang)}{' '}
            {d.tried!.map((t, i) => (
              <span key={t}>{i > 0 && ', '}<code>{t}</code></span>
            ))}
          </div>
        )}
        <div style={{ color: 'var(--text3)', marginTop: 4 }}>
          {tl('mtn.absentA', lang)}<code>?metric=</code>{tl('mtn.absentB', lang)}
        </div>
        {(d.suggestions?.length ?? 0) > 0 && (
          <div style={{ marginTop: 6 }}>
            <div style={{ color: 'var(--text3)' }}>
              {tl('mtn.similar', lang)}
            </div>
            <ul style={{ margin: '4px 0 0 16px', padding: 0 }}>
              {d.suggestions!.map(n => (
                <li key={n} className="mono" style={{ fontSize: 11 }}>{n}</li>
              ))}
            </ul>
          </div>
        )}
      </div>
    );
  }

  const hasJobs = (d.sampleJobs?.length ?? 0) > 0;
  return (
    <div style={box}>
      <b>{tl('mtn.noMatch', lang)}</b> {searched}
      <div style={{ marginTop: 4 }}>
        <code>{d.metric}</code>
        {d.instrument && <> · instrument <code>{d.instrument}</code></>}
        {' '}{tl('mtn.pattern', lang)}<code>{d.pattern}</code>
      </div>
      {(d.triedLabels?.length ?? 0) > 0 && (
        <div style={{ marginTop: 4, color: 'var(--text3)' }}>
          {tl('mtn.triedLabels', lang)}{' '}
          {d.triedLabels!.map((l, i) => (
            <span key={l}>{i > 0 && ', '}<code>{l}</code></span>
          ))}
        </div>
      )}
      {/* v0.9.682 — VARLIK, denemeden ayrı. "Anahtar yok" ise
          collector'ı düzeltmek gerekiyor; "anahtar var ama değer
          tutmadı" ise deseni. Boş grafik ikisini de aynı gösterirdi. */}
      {(d.candidateErrors?.length ?? 0) > 0 && (
        <div style={{ marginTop: 6, color: 'var(--err)' }}>
          <b>{tl('mtn.candErr', lang)}</b>{tl('mtn.candErrB', lang)}
          <ul style={{ margin: '2px 0 0 16px', padding: 0 }}>
            {d.candidateErrors!.map(e => (
              <li key={e} style={{ fontSize: 11 }}>{e}</li>
            ))}
          </ul>
        </div>
      )}
      {/* v0.10.1358 — üçüncü dal: tarama hata verdi / kısmi döndü. Boş
          liste burada "anahtar yok" DEĞİL — collector'ı düzelt demek yanlış
          eylem olurdu. Görülen anahtarlar (kısmi listede) yine VAR. Eski
          gövdede alan yok ⇒ known okuması (eski iki dal aynen). */}
      <div style={{ marginTop: 4 }}>
        {d.presentKeysState === 'unknown' ? (
          <span style={{ color: 'var(--warn)' }}>
            {tl('mtn.unkA', lang)}<b>{tl('mtn.unkB', lang)}</b>
            {d.presentKeysReason ? <> ({d.presentKeysReason})</> : null}{tl('mtn.unkC', lang)}
            {(d.presentKeys?.length ?? 0) > 0 && (
              <>
                {' '}{tl('mtn.seen', lang)}{' '}
                {d.presentKeys!.map((k, i) => (
                  <span key={k}>{i > 0 && ', '}<code>{k}</code></span>
                ))}
              </>
            )}
          </span>
        ) : (d.presentKeys?.length ?? 0) > 0 ? (
          <>
            <span style={{ color: 'var(--text3)' }}>{tl('mtn.present', lang)}
              {d.presentKeysScope === 'fleet' && tl('mtn.fleet', lang)}:</span>{' '}
            {d.presentKeys!.map((k, i) => (
              <span key={k}>{i > 0 && ', '}<code>{k}</code></span>
            ))}
            <div style={{ color: 'var(--text3)', marginTop: 2 }}>
              {tl('mtn.keyNoMatch', lang)}
            </div>
          </>
        ) : (
          <span style={{ color: 'var(--text3)' }}>
            {tl('mtn.noneA', lang)}<b>{tl('mtn.noneB', lang)}</b>{tl('mtn.noneC', lang)}
            {d.presentKeysScope === 'fleet' && tl('mtn.fleet', lang) /* v0.10.1358 */}{tl('mtn.noneD', lang)}
          </span>
        )}
      </div>
      {hasJobs ? (
        <div style={{ marginTop: 6 }}>
          <div style={{ color: 'var(--text3)' }}>
            {tl('mtn.jobsA', lang)}<code>{d.jobLabel}</code>{tl('mtn.jobsB', lang)}
          </div>
          <ul style={{ margin: '4px 0 0 16px', padding: 0 }}>
            {d.sampleJobs!.map((j, i) => (
              <li key={j} className="mono" style={{ fontSize: 11 }}>
                {j}
                {d.sampleServices?.[i] && (
                  <span style={{ color: 'var(--text3)' }}> → {d.sampleServices[i]}</span>
                )}
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <div style={{ color: 'var(--text3)', marginTop: 6 }}>
          {tl('mtn.noJobA', lang)}<code>{d.jobLabel}</code>{tl('mtn.noJobB', lang)}
        </div>
      )}
    </div>
  );
}
