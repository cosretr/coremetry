import type { CSSProperties } from 'react';
import { DataTableState, type DataTableStateStaticProps } from '@/components/ui/DataTable';
import { fmtNum, fmtDurShort } from '@/lib/utils';
import type { ExternalPathRow } from '@/lib/types';

// ExternalPaths — v0.9.1255. Bir dış bağımlılığın "en çok çağrılan
// yollar" kırılımı. İKİ yerde çiziliyor: /external çekmecesi (tam RED
// kolonları) ve topolojinin pin'li düğüm kartı (dense, ilk 5 — kart
// 240px).
//
// Neden ortak bileşen: boş / hata / kırpılmış-pencere üçlüsünün
// SÖYLEDİĞİ şey iki yüzeyde de aynı olmalı. "Yol yok" ile "okuma
// başarısız oldu" ayrımı burada BİR kez yazılıyor; ikinci bir kopya
// yazılsaydı biri er ya da geç timeout'u boşluk gibi gösterirdi
// (v0.9.363'ün sınıfı).
//
// Yollar NORMALIZE: /orders/12345 ve /orders/67890 tek satır
// (/orders/{id}). Alttaki not bunu söylüyor — operatör ham url.full
// beklerse sayıların neden birleştiğini bilmeli.

/**
 * ellipsizePathMiddle — yolu ORTADAN kırpar, KUYRUĞU korur.
 *
 * Bu fonksiyon özelliğin bütün noktası. Operatörün şikâyeti tam olarak
 * ortak ön ekti: `/tibcoESB/ExternalServices/NVI/KPS/…` altındaki beş
 * uç, sondan kırpan bir hücrede BEŞİ DE aynı görünür
 * ("/tibcoESB/ExternalServic…") — yani kırılım eklenmiş ama hiçbir şey
 * ayırt edilemiyor olurdu. Ayırt edici parça SONDA, o yüzden bütçenin
 * 2/3'ü kuyruğa gider.
 *
 * bidi hilesi (direction:rtl) bilerek KULLANILMIYOR: yolun içindeki
 * '/' ve rakamlar görsel sırayı kaydırıyor ve sonuç tarayıcıdan
 * tarayıcıya değişiyor. Saf, test edilebilir bir kırpma tercih edildi;
 * tam değer her hâlükârda `title`da duruyor.
 */
export function ellipsizePathMiddle(p: string, max: number): string {
  if (max <= 1) return p.length <= max ? p : '…';
  if (p.length <= max) return p;
  const tail = Math.max(1, Math.ceil(((max - 1) * 2) / 3));
  const head = Math.max(0, max - 1 - tail);
  return p.slice(0, head) + '…' + p.slice(p.length - tail);
}

export function ExternalPaths({ paths, error, windowS, limit, dense }: {
  paths: ExternalPathRow[] | undefined;
  error?: string;
  windowS?: number;
  limit?: number;
  dense?: boolean;
}) {
  const muted: CSSProperties = { fontSize: dense ? 10 : 12, color: 'var(--text3)' };
  const rows = (paths ?? []).slice(0, limit ?? 10);
  // v0.10.967 — tablo standardı dilim 5 (P-2): hata erken dönüşü kalktı;
  // "okunamadı" da boş cümle gibi tablonun İÇİNDE, başlık durur. Sıra aynı:
  // hata, gelen yolları da gizler (eskiden erken dönüş satırlardan önceydi) —
  // bayat/kısmi satır hatanın yanında "geçerli" gibi okunmasın. Ham hata
  // metni eskisi gibi yalnız geniş yüzeyde (kart 240px).
  const showRows = !error && rows.length > 0;
  const state: Omit<DataTableStateStaticProps, 'colSpan'> = error
    ? { kind: 'error', message: dense
      ? 'Yol kırılımı okunamadı — çağıran listesi ve trend geçerli.'
      : `Yol kırılımı okunamadı: ${error} — çağıran listesi ve trend geçerli.` }
    : { kind: 'empty', message: "Bu pencerede URL taşıyan istemci span'i yok — yol kırılımı url.full / http.url / url.path attr'ından türer." };

  const total = rows.reduce((a, r) => a + r.calls, 0);
  const maxChars = dense ? 26 : 46;
  return (
    <>
      {/* v0.10.947 — statik tablo: en çok 10 sabit satır, sıralanmaz (T1).
          Kartta (dense) bilerek sıkışık tek punto; 12px tablonun tabanı. */}
      <table style={dense ? { fontSize: 10.5 } : undefined}>
        <thead>
          {/* v0.10.928 (Y3) — `tr`deki renk/boyut/hiza satır-içi stili
              silindi: `thead th` kuralı üçünü de kendisi bildirdiği için
              hiç uygulanmıyordu (dense 9.5px başlık hiç görünmedi). */}
          <tr>
            <th>Yol</th>
            <th className="num">Çağrı</th>
            {!dense && <th className="num">Hata %</th>}
            <th className="num">P99</th>
          </tr>
        </thead>
        <tbody>
          {/* v0.10.967 (P-2) — statik tablonun durum satırı; colSpan = thead'deki <th> sayısı (dense'te Hata % yok). */}
          {!showRows ? <DataTableState colSpan={dense ? 3 : 4} {...state} /> : rows.map(r => (
            <tr key={r.path}>
              <td>
                <span className="mono"
                  title={`${r.path}\n${fmtNum(r.calls)} çağrı · ${r.errorRate.toFixed(2)}% hata · p99 ${r.p99Ms.toFixed(0)}ms`}>
                  {ellipsizePathMiddle(r.path, maxChars)}
                </span>
              </td>
              <td className="num">{fmtNum(r.calls)}</td>
              {!dense && (
                <td className={`num ${r.errorRate > 5 ? 'cell-err' : r.errorRate > 1 ? 'cell-warn' : 'cell-faint'}`}>
                  {r.errorRate.toFixed(2)}
                </td>
              )}
              <td className={`num ${dense && r.errorRate > 5 ? 'cell-err' : ''}`}>{r.p99Ms.toFixed(0)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {/* Pencere kırpması BEYAN edilir: sunucu ham spans okumasını
          kısıtlıyor, yani bu sayılar çekmecenin üst yarısıyla AYNI
          aralığı kapsamayabilir. Gizlenirse operatör seçtiği aralığın
          tamamına ait bir toplam sanar. (Satır yokken "0 çağrı" demez;
          v0.10.967 — hata satırının altında bayat toplam da kalmaz.) */}
      {showRows && (
        <div style={{ ...muted, marginTop: 5 }}>
          {fmtNum(total)} çağrı{windowS ? ` · son ${fmtDurShort(windowS)}` : ''}
          {' · '}id'ler <span className="mono">{'{id}'}</span> olarak gruplandı
        </div>
      )}
    </>
  );
}
