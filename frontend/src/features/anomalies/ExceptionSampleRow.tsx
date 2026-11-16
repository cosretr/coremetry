// ExceptionSampleRow — v0.10.1104 (operatör: "Exceptions sayfasındaki traceidler
// mouse orta clickle yeni sekmede açmıyorum. Bu arada bazı traceidler de aslında
// coremetry üzerinde olmayabilir").
//
// Exception detayının "Sample traces" satırı ProblemDetail'den buraya taşındı
// (render testi için). İki düzeltme:
//   1. Trace id artık GERÇEK bir <a href="/trace?id=…"> (react-router Link):
//      orta tık / Ctrl-tık / ⌘-tık tarayıcının kendi yeni-sekme davranışı.
//      Eskiden id bir <span>'dı ve satır yalnız navigate() çağırıyordu — orta
//      tık hiçbir şey yapmıyordu. Satırın rowActivation'ı (düz tık + Enter/Boşluk)
//      kalır; Link'in tıkı satıra ÇIKMAZ (stopRowClick) → düz tık bir kez gider,
//      değiştirici tuşlu tık aynı sekmede de gitmez (a11y.ts sözleşmesi:
//      satırın kendi <a>'sı kendi olayını keser).
//   2. Oracle grubunda (`ora:`) trace id Oracle satırından gelir ve Coremetry'de
//      olmayabilir: sunucu `traceInCoremetry` ile işaretler (chstore
//      oracle_sample_traces.go). false → soluk düz metin + "Coremetry'de yok",
//      link YOK, satır tıklanmaz (tablo standardı: satır yalnız açılıyorsa
//      tıklanır görünür). true / undefined → link.
//
// v0.10.1369 (operatör, prod: "Sample traces kısmında trace'ler Coremetry'de yok
// diyor, sample etmediği için. Ama sanki Tempo üzerinden search yapsa güzel
// olmaz mı. Bir de traceId'nin yarısını yazmış, oraya onu kopyalayıp kendimde
// aratamıyorum.") — iki düzeltme, TEK bileşende (SampleTraceId; örnek satırı,
// Oracle panelinin "Trace'ler" satırı ve Anomalies örnek kartı aynısını kullanır):
//   1. DOM'da TAM id: eskiden `traceId.slice(0, 16)…` basılıyordu, seçip
//      kopyalayan yarım id alıyordu. Artık tam 32 hex DOM'da, görsel kısaltma
//      CSS ellipsis (maxWidth ch), title = tam id + kopyala düğmesi (CopyButton:
//      lib/clipboard yedekli, ✓ + "Kopyalandı"; tık satıra çıkmaz).
//   2. Sunucu CH'de olmayan id'yi Tempo'da arar (api/exception_sample_tempo.go):
//      bulunursa traceSource="tempo" → gerçek link + nötr "Tempo" rozeti; hâlâ
//      yoksa gri "Coremetry'de yok" (artık tam, kopyalanabilir id ile).
import type { CSSProperties } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { stopRowClick } from '@/lib/a11y';
import { traceHref } from '@/lib/traceHref';
import { rowClickHandlers, tsLong } from '@/lib/utils';
import type { ExceptionSample } from '@/lib/types';
import { CopyButton } from '@/components/CopyButton';
import { sampleTraceFromTempo, sampleTraceLinkable } from './sampleTrace';
import { t, useLang } from '@/lib/i18n'; // v0.10.2287 — metinler arayüz dilinde (TR aynı)

/** v0.10.1369 — tam id DOM'da; görünen ~`chars` karakter + "…" CSS ile (mono ch). */
const idClip = (chars: number): CSSProperties => ({
  display: 'inline-block', maxWidth: `${chars + 1}ch`, overflow: 'hidden',
  textOverflow: 'ellipsis', whiteSpace: 'nowrap', verticalAlign: 'bottom',
});

/** v0.10.1369 — örnek trace id'si: açılabiliyorsa gerçek <Link> (Tempo'dan
 *  açılıyorsa "Tempo" rozeti), değilse soluk metin + "Coremetry'de yok"; her
 *  iki dalda TAM id + kopyala. `linkTitle` link title'ına tam id'den sonra eklenir. */
export function SampleTraceId({ s, chars = 16, accent = false, linkTitle }: {
  s: Pick<ExceptionSample, 'traceId' | 'traceInCoremetry' | 'traceSource'>;
  chars?: number;
  accent?: boolean;
  linkTitle?: string;
}) {
  const lang = useLang();
  if (!s.traceId) return null;
  const linkable = sampleTraceLinkable(s);
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, minWidth: 0, maxWidth: '100%' }}>
      {linkable ? (
        <Link to={traceHref(s.traceId)} className="mono" onClick={stopRowClick}
          title={linkTitle ? `${s.traceId} — ${linkTitle}` : s.traceId}
          style={{ ...(accent ? { color: 'var(--accent2)' } : {}), ...idClip(chars) }}>
          {s.traceId}
        </Link>
      ) : (
        <span className="mono cell-faint" title={s.traceId} style={idClip(chars)}>{s.traceId}</span>
      )}
      <CopyButton value={s.traceId} title={t('exc.trace.copy', lang)} copiedTitle={t('exc.trace.copied', lang)} />
      {linkable && sampleTraceFromTempo(s) && <span className="badge b-gray" title={t('exc.trace.tempoTitle', lang)}>Tempo</span>}
      {!linkable && <span className="badge b-gray" title={t('exc.trace.missingTitle', lang)}>{t('exc.trace.missing', lang)}</span>}
    </span>
  );
}

export function ExceptionSampleRow({ s, isEv }: { s: ExceptionSample; isEv: boolean }) {
  const navigate = useNavigate();
  const lang = useLang();
  const linkable = sampleTraceLinkable(s);
  return (
    // data-trace-id (v0.9.477): AI çekmecesindeki kanıt satırı
    // tıklanınca buraya kaydırılır.
    // v0.10.1356 (R4, gezinen satır gerçek bağlantı) — satır rowActivation
    // (role=button, her tıkta navigate) DEĞİL rowClickHandlers: düz tık aynı
    // sekmede, Ctrl/⌘/Shift/orta tık yeni sekme; klavye durağı trace id Link'i.
    <tr data-trace-id={s.traceId || undefined}
      className={isEv ? 'wf-evidence' : undefined}
      {...(linkable ? rowClickHandlers(traceHref(s.traceId), () => navigate(traceHref(s.traceId))) : {})}>
      {/* v0.10.977 — uç hücrelerin 14px kenar dolgusu kart kenarıyla hizalanır
          (başlıksız liste; §2b özel dolgu, sınıf karşılığı yok). */}
      <td className="mono" style={{ paddingLeft: 14 }}>
        {/* v0.10.1369 — tam id + kopyala + Tempo / "Coremetry'de yok" rozeti (SampleTraceId). */}
        {s.traceId ? <SampleTraceId s={s} accent /> : '—'}
      </td>
      {/* v0.10.922 (sade palet adım 1) — her satırdaki kırmızı
          "ERROR" rozeti KALKTI: tablo yalnız hata örneklerini
          listeliyor, sabit rozet bilgi taşımıyordu. "kanıt"
          kelimesi kalır ama nötr — satırın rengini zaten
          .wf-evidence veriyor (bir olgu = bir sinyal). */}
      <td title={isEv ? t('exc.ev.title', lang) : undefined}>
        {isEv && <span className="badge b-gray">{t('exc.ev', lang)}</span>}
      </td>
      <td className="mono cell-faint" style={{ textAlign: 'right', paddingRight: 14 }}>{tsLong(s.time)}</td>
    </tr>
  );
}
