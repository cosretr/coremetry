// TriageTitleCell — v0.10.1084 (operatör: "tekilleştir, Exceptions'taki format
// güzel"). Triyaj satırının başlık hücresi: Exceptions listesi (AnomaliesPage)
// ile Problems kuyruğu (/inbox) AYNI bileşeni çizer — ikinci bir tasarım yok.
//
//   1. satır  kalın başlık (exception tipi ya da olay cümlesi) + satır içi çipler
//             (durum rozeti, "<1h", yinelenen, runbook, deploy …)
//   2. satır  soluk ayrıntı (exception mesajı / kural · ölçü · gerekçe)
//   3. satır  (varsa) AI özeti — ✨ köken işareti + yaş damgası; metin ÇAĞIRANDA
//             stripMarkdown'dan geçmiş gelir (kırpılmış yüzey, markdownSurfaces).
//
// Sınıflar globals.css `.triage-*`. Hücre (<td>) ve satır linki (<Link>)
// çağıranın: Exceptions hücresi tıklanabilir link, Problems satırı rowActivation.
import type { ReactNode } from 'react';
import { IconSparkles } from '@/components/icons';

export interface TriageTitleCellProps {
  /** Kalın başlık (title özniteliğinde tam hâli). */
  title: string;
  /** Exception tipi gibi kod kimliği: mono + hata rengi. Cümle başlığı düz metin. */
  code?: boolean;
  /** Başlığın yanındaki satır içi çipler. */
  chips?: ReactNode;
  /** Soluk ikinci satır; undefined = satır yok. */
  detail?: ReactNode;
  /** İkinci satırın title'ı (tam metin). */
  detailTitle?: string;
  /** İkinci satır mono (exception mesajı). */
  detailMono?: boolean;
  /** stripMarkdown'dan geçmiş AI özeti; boşsa hiçbir şey çizilmez. */
  ai?: string;
  /** AI özetinin yaşı ("3 dk önce"); varsa satır sonunda soluk. */
  aiAge?: string;
}

export function TriageTitleCell({ title, code, chips, detail, detailTitle, detailMono, ai, aiAge }: TriageTitleCellProps) {
  return (
    <>
      <div className={code ? 'triage-title triage-title--code mono' : 'triage-title'} title={title}>
        <span className="dt-trunc">{title}</span>
        {chips}
      </div>
      {detail !== undefined && (
        <div className={detailMono ? 'triage-sub mono' : 'triage-sub'} title={detailTitle}>
          {detail}
        </div>
      )}
      {ai && (
        <div className="triage-ai" title={aiAge ? `${ai}\n\nAI çıkarımı · ${aiAge}` : ai}>
          <IconSparkles size={10} />
          <span className="triage-ai__text">{ai}</span>
          {aiAge && <span className="triage-ai__age">· {aiAge}</span>}
        </div>
      )}
    </>
  );
}
