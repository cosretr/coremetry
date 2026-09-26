import { useEffect, useMemo, useRef, useState } from 'react';
import { useEscLayer } from '@/lib/escLayer';
import { Button, MenuItem } from '@/components/ui';
import { historyItemView, type QueryHistoryEntry } from './useQueryHistory';

// RecentQueries (v0.9.849) — "Son sorgular ▾".
//
// useQueryHistory v0.9.562'den beri halkayı YAZIYORDU ama hiçbir yer
// OKUMUYORDU: tek tüketicisi giriş ekranının soru kartlarıydı ve o ekran
// kaldırılınca hook öksüz kaldı. Yani her düzenleme localStorage'a bir kayıt
// düşürüyor, kayıt hiçbir zaman geri gösterilmiyordu.
//
// 4 SLOT — halkanın kendi kapasitesi (MAX_HISTORY). Daha uzun bir liste
// "arama" ister; bu düğmenin işi arama değil, "az önce neye bakıyordum".
//
// UYGULAMA = NAVİGASYON. Kayıt Phase-1'den beri tam arama dizesini ('?…')
// saklıyor, o yüzden geri dönüş builder state'ini elle kurmak değil o URL'e
// gitmektir; ExplorePage'in imza-anahtarı (v0.9.805) dışarıdan gelen bu
// URL'i kendi yazımızdan ayırıp remount tetikliyor ve ExploreInner URL'i
// mount'ta bir kez okuyor. Yani mekanizma zaten yerinde — eksik olan tek şey
// listeyi göstermekti.
//
// PUSH, replace DEĞİL: sayfanın kendi state→URL yazımı replace kullanır
// (geçmişi kirletmemek için), ama bu bir operatör NAVİGASYONUDUR ve
// tarayıcının geri düğmesi onu geri alabilmeli.
export function RecentQueries({ history, onApply }: {
  history: QueryHistoryEntry[];
  onApply: (search: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // Click-outside + Esc. Dinleyici YALNIZ açıkken bağlı (FacetMultiSelect
  // deseni) — kapalı bir düğme sayfaya global dinleyici bırakmaz.
  useEffect(() => {
    if (!open) return;
    const onDown = (ev: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(ev.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [open]);
  // v0.9.950 (E2/Ö28) — Esc KATMAN (FacetMultiSelect deseni).
  useEscLayer(open, () => setOpen(false));

  // Göreli zaman AÇILIŞTA bir kez donuyor: liste açıkken tik tik güncellemek
  // bir zamanlayıcı + render döngüsü demek olurdu ve "3 dk önce"nin 4'e
  // dönmesini kimse beklemiyor. now, panel her açıldığında tazeleniyor.
  const items = useMemo(
    () => (open ? history.map(h => historyItemView(h, Date.now())) : []),
    [open, history]);

  // Boşsa düğme HİÇ çizilmez — tıklandığında boş bir kutu açan düğme,
  // olmayan düğmeden kötüdür.
  if (history.length === 0) return null;

  return (
    <div ref={rootRef} style={{ position: 'relative' }}>
      <Button variant="secondary" size="sm"
        aria-haspopup="menu" aria-expanded={open}
        onClick={() => setOpen(o => !o)}
        title="Bu tarayıcıda en son çalıştırdığın sorgular — tıkla, aynen geri yükle">
        ⟲ Son sorgular ▾
      </Button>
      {/* v0.10.927 — satırlar SEÇİM değil EYLEM (tık = sorguyu geri yükle):
          listbox/option değil menu/MenuItem. Hover + klavye odağı `.menuitem`
          CSS'inden (JS-hover kalktı), geri yüklenemeyen kayıt `:disabled`
          (soluk + not-allowed). İki sütun `.menuitem-label` içinde. */}
      {open && (
        <div role="menu" aria-label="Son sorgular"
          style={{
            position: 'absolute', top: '100%', right: 0, zIndex: 'var(--z-dropdown)', marginTop: 4,
            minWidth: 320, maxWidth: 560,
            background: 'var(--bg1)', border: '1px solid var(--border)',
            borderRadius: 8, padding: 4,
            boxShadow: '0 8px 24px rgba(0,0,0,.28)',
          }}>
          {items.map((it, i) => (
            <MenuItem key={`${it.text}:${i}`}
              disabled={!it.search}
              title={it.search
                ? it.title
                : `${it.title}\n(bu kayıt geri yüklenemiyor — eski/bozuk biçim)`}
              onClick={() => { if (it.search) { setOpen(false); onApply(it.search); } }}>
              <span style={{ display: 'flex', alignItems: 'baseline', gap: 8, minWidth: 0 }}>
                <span style={{
                  flex: 1, minWidth: 0,
                  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                }}>{it.text}</span>
                <span style={{ flexShrink: 0, fontSize: 10.5, color: 'var(--text3)' }}>
                  {it.when}
                </span>
              </span>
            </MenuItem>
          ))}
        </div>
      )}
    </div>
  );
}
