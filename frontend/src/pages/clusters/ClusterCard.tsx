import type { ReactNode } from 'react';

// ClusterCard — Clusters sayfasının başlıklı kart kabuğu (Clusters S4,
// v0.10.2169; plan docs/frontend/clusters-shadcn-plan.md §5). Dondurulmuş
// `ui/Card`ın bu sayfadaki halefi: aynı `.card` yüzeyi (globals.css) + aynı
// başlık ayracı, satır içi stil yok (`.cl-card__head`).
//
// Neden shadcn `card` değil: depoda kurulu değil; shadcn-phase1-plan §12 Q2
// yeni shadcn bileşenini KENDİ sürümüyle (katalog + axe girdisi) ister.
// Geçirilmiş sayfalar da `.card` sınıfını elle yazıyor (pages/settings
// BackupTab, ArgoCDDiscoveryPanel). Tıklanabilir kart bu bileşen DEĞİL:
// gerçek bağlantı `<Link className="card card-link">` olarak yazılır.
export function ClusterCard({ header, title, className, children }: {
  header?: ReactNode;
  /** Kartın tamamı için ipucu (ölçümün sınırını anlatan açıklama). */
  title?: string;
  className?: string;
  children?: ReactNode;
}) {
  const cls = ['card', className].filter(Boolean).join(' ');
  return (
    <div className={cls} title={title}>
      {header != null && <div className="cl-card__head">{header}</div>}
      {children}
    </div>
  );
}
