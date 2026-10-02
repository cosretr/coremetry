import { useEffect, useLayoutEffect, useRef, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import { placeTip } from '@/lib/tipPlacement';

// SparkReadout — v0.10.1059 (operatör, prod, servis → Operations: "bir servisin
// herhangi birinin üzerine gelince bir şey çıkıyor ama anlaşılmıyor").
//
// Tablo içi mini grafiklerin (TrendSpark, Sparkline `readout`) kova okuması.
// Eskiden TrendSpark okumayı hücrenin İÇİNDE `position: absolute;
// bottom: calc(100% + 4px)` çiziyordu: `tbody td { overflow: hidden }` onu
// hücrenin üst kenarında kesiyordu (yalnız harflerin alt yarısı görünüyordu)
// ve düğmenin `title`ı aynı anda ikinci, başka şey söyleyen yerel bir ipucu
// açıyordu. Şimdi:
//   • body'ye PORTAL, `position: fixed` — hücrenin overflow'u, satırın
//     content-visibility'si ve tablo kabının kaydırması kırpamaz. ui/Tooltip
//     bilerek portal değildir (çekmece/modal içinde arkada kalmasın diye);
//     bu okuma yalnız sayfa tablolarında yaşar ve `pointer-events: none`dur,
//     o gerekçe burada geçerli değil — ama yerleşim aynı saf çekirdekten
//     (lib/tipPlacement.placeTip): üstte, sığmazsa alta çevrilir, yatayda
//     viewport'a kıstırılır.
//   • Çapa üzerine gelinen KOVA: kutu imleçle çubuklar boyunca yürür.
//   • Görünüm grafik ipuçlarının evdeki şablonu `.ov-tt` (zaman başlığı,
//     renk lekeli satırlar, soluk ipucu satırı) — yeni tooltip stili yok.
//   • Durum çağıranda ve YEREL (hover edilen hücre); tabloyu yeniden
//     çizdirmez. Kaydırmada kapanır (imleç hareket etmeden satır kayar).

export interface SparkReadoutRow {
  label: string;
  /** Seri rengi (CSS değeri; tablo lejantıyla aynı token). */
  color: string;
  value: string;
}

export function SparkReadout({ anchorRef, at, title, rows, hint, onDismiss }: {
  /** Grafiğin svg'si — ölçüm ondan alınır (yerleşim anında okunur). */
  anchorRef: RefObject<SVGSVGElement | null>;
  /** Kovanın grafik genişliği içindeki yeri, 0..1. */
  at: number;
  /** Zaman başlığı (fmtBucketWindow). */
  title: string;
  rows: SparkReadoutRow[];
  /** Son, soluk satır — ör. "tıkla: grafik". */
  hint?: string;
  onDismiss: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);

  // Her çizimde yeniden yerleştir (içerik ve kova değişir); DOM'a doğrudan
  // yazılır — konum için ikinci bir React çizimi yok, boyamadan önce oturur.
  useLayoutEffect(() => {
    const el = ref.current;
    const anchor = anchorRef.current;
    if (!el || !anchor) return;
    const r = anchor.getBoundingClientRect();
    const pos = placeTip(
      { left: r.left + at * r.width, top: r.top, width: 0, height: r.height },
      { width: el.offsetWidth, height: el.offsetHeight },
      { width: window.innerWidth, height: window.innerHeight },
      'top',
    );
    el.style.left = `${pos.left}px`;
    el.style.top = `${pos.top}px`;
    el.dataset.side = pos.side;
  });

  useEffect(() => {
    const close = () => onDismiss();
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
    };
  }, [onDismiss]);

  return createPortal(
    <div ref={ref} className="ov-tt spark-readout" role="tooltip">
      <div className="ov-tt-t">{title}</div>
      {rows.map(r => (
        <div key={r.label} className="ov-tt-r">
          <span className="ov-lbl"><i className="ov-sw" style={{ background: r.color }} />{r.label}</span>
          <b>{r.value}</b>
        </div>
      ))}
      {hint && <div className="ov-tt-hint">{hint}</div>}
    </div>,
    document.body,
  );
}
