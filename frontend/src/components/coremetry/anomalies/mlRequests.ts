// mlRequests — v0.10.2380 (triage mockup dilim 1; operatör 2026-11-19: "hata
// oranı artan problemler için de başarılı / başarısız gösterebilirsin", "ilk
// grafikte timeseries gibi olan çizgiler"). Hata oranı kaydından dakikada
// başarılı / başarısız istek çizgilerinin ve "Şimdi ve normalde" rakamlarının
// SAF girdisi. Render'da ek istek yok: seri kaydın snapshot'ından türetilir
// (başarısız = oran × hacim; oran yüzde ve hacimle AYNI 1 dakikalık pencerede),
// "şimdi" rakamları kaydın `requests` bölümünden (tikin ölçtüğü tam dakika;
// Problem başlığı ve liste satırıyla aynı sayılar).
import { okErrorPoints } from '@/lib/chart/redBands';
import type { MLRecordView } from '@/lib/types';

/** SAF: kayıttan istek çizgileri çizilebilir mi (Go ml.RequestsCapable ikizi).
 *  Eski svc_error_rate kaydı (kayan 5 dk oran) ve op_error_rate sağlamaz. */
export function requestsCapable(rec: MLRecordView | null | undefined): rec is MLRecordView {
  if (!rec || rec.detector !== 'svc_error_rate') return false;
  const { series: s, snapshot: sn } = rec;
  return s.aggregation === 'err_ratio' && s.unit === 'percent' && s.stepSec === 60 && sn.step === 60 &&
    sn.v.length > 0 && !!sn.n && sn.n.length === sn.v.length;
}

export interface RequestSeries {
  /** dakika başı, unix sn */
  t: number[];
  ok: number[];
  failed: number[];
}

/** SAF: dakikada başarılı / başarısız istek (Servis genel görünümündeki OK /
 *  Errors ayrımıyla aynı işlev: okErrorPoints). AÇIK olayda ölçülen son tam
 *  dakikadan sonraki kovalar ÇİZİLMEZ: o dakikalar henüz dolmadı (ingest
 *  gecikmesi), çizilirse grafik sağ uçta "trafik düştü" derdi. */
export function requestSeries(rec: MLRecordView): RequestSeries | null {
  if (!requestsCapable(rec)) return null;
  const sn = rec.snapshot;
  let n = sn.v.length;
  const until = !rec.endedAt && rec.requests?.minute ? rec.requests.minute + sn.step : 0;
  if (until) {
    while (n > 1 && sn.t0 + (n - 1) * sn.step > until) n--;
  }
  const at = (i: number) => (sn.t0 + i * sn.step) * 1e9;
  const { ok, err } = okErrorPoints(
    sn.n!.slice(0, n).map((v, i) => ({ time: at(i), value: v })),
    sn.v.slice(0, n).map((v, i) => ({ time: at(i), value: v })),
  );
  return { t: ok.map(p => p.time / 1e9), ok: ok.map(p => p.value), failed: err.map(p => p.value) };
}

export interface RequestFigures {
  /** now = ölçülen son tam dakika (açık olay); detection = tespit dakikası (biten olay / bölümsüz kayıt). */
  at: 'now' | 'detection';
  failed: number;
  ok: number;
  total: number;
  /** Olağan düzey; taban trafiği bilinmiyorsa null. */
  normalFailed: number | null;
  normalOk: number | null;
  normalTotal: number | null;
}

/** SAF: üç rakam. Açık olayda kaydın `requests` bölümü; biten olayda (ya da
 *  bölümsüz kayıtta) tespit dakikası seriden. İstek yoksa null (sayı uydurulmaz). */
export function requestFigures(rec: MLRecordView): RequestFigures | null {
  if (!requestsCapable(rec)) return null;
  const q = rec.requests;
  const nf = q?.normalFailedPerMin;
  const no = q?.normalOkPerMin;
  const normal = nf != null && no != null
    ? { normalFailed: nf, normalOk: no, normalTotal: nf + no }
    : { normalFailed: null, normalOk: null, normalTotal: null };
  if (q && !rec.endedAt) {
    const total = q.failedPerMin + q.okPerMin;
    return total > 0 ? { at: 'now', failed: q.failedPerMin, ok: q.okPerMin, total, ...normal } : null;
  }
  const sn = rec.snapshot;
  const i = Math.min(Math.max(Math.floor((Math.floor(rec.detectedAt / 1e9) - sn.t0) / sn.step), 0), sn.v.length - 1);
  const total = Math.round(sn.n![i]);
  if (!(total > 0)) return null;
  const failed = Math.min(Math.round((Math.min(Math.max(sn.v[i], 0), 100) / 100) * sn.n![i]), total);
  return { at: 'detection', failed, ok: total - failed, total, ...normal };
}
