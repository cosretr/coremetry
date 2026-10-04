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
import { Link, useNavigate } from 'react-router-dom';
import { rowActivation } from '@/lib/a11y';
import { traceHref } from '@/lib/traceHref';
import { tsLong } from '@/lib/utils';
import type { ExceptionSample } from '@/lib/types';
import { TRACE_MISSING_LABEL, TRACE_MISSING_TITLE, sampleTraceLinkable, stopRowClick } from './sampleTrace';

/** Coremetry'de olmayan trace id: soluk mono metin + gri rozet, link yok. */
export function TraceMissingId({ traceId, chars = 16 }: { traceId: string; chars?: number }) {
  return (
    <span title={TRACE_MISSING_TITLE} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
      <span className="mono cell-faint">{traceId.slice(0, chars)}…</span>
      <span className="badge b-gray">{TRACE_MISSING_LABEL}</span>
    </span>
  );
}

export function ExceptionSampleRow({ s, isEv }: { s: ExceptionSample; isEv: boolean }) {
  const navigate = useNavigate();
  const linkable = sampleTraceLinkable(s);
  return (
    // data-trace-id (v0.9.477): AI çekmecesindeki kanıt satırı
    // tıklanınca buraya kaydırılır.
    <tr data-trace-id={s.traceId || undefined}
      className={isEv ? 'wf-evidence' : undefined}
      {...(linkable ? rowActivation(() => navigate(traceHref(s.traceId))) : {})}>
      {/* v0.10.977 — uç hücrelerin 14px kenar dolgusu kart kenarıyla hizalanır
          (başlıksız liste; §2b özel dolgu, sınıf karşılığı yok). */}
      <td className="mono" style={{ paddingLeft: 14 }}>
        {linkable ? (
          <Link to={traceHref(s.traceId)} className="mono" onClick={stopRowClick} title={s.traceId}
            style={{ color: 'var(--accent2)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'inline-block', maxWidth: 150 }}>
            {s.traceId.slice(0, 16)}…
          </Link>
        ) : s.traceId ? <TraceMissingId traceId={s.traceId} /> : '—'}
      </td>
      {/* v0.10.922 (sade palet adım 1) — her satırdaki kırmızı
          "ERROR" rozeti KALKTI: tablo yalnız hata örneklerini
          listeliyor, sabit rozet bilgi taşımıyordu. "kanıt"
          kelimesi kalır ama nötr — satırın rengini zaten
          .wf-evidence veriyor (bir olgu = bir sinyal). */}
      <td title={isEv ? 'Explain kanıtı — kök neden bu trace üzerinden soruşturuldu' : undefined}>
        {isEv && <span className="badge b-gray">kanıt</span>}
      </td>
      <td className="mono cell-faint" style={{ textAlign: 'right', paddingRight: 14 }}>{tsLong(s.time)}</td>
    </tr>
  );
}
