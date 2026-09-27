// usePanelNote — v0.10.968 — Trace › Metrics seçili pod paneli / odak
// görünümünün GÖRÜNÜR duyuru notu (inceleme turu MF-8; onaylı mockup
// `pn.hasLive`).
//
// Neden: panelin reddi ve onayları ("Son pod karşılaştırmadan çıkarılamaz.",
// "Pod adı kopyalandı: …") yalnız kabuğun sr-only canlı bölgesine
// yazılıyordu; gören kullanıcı son çipe tıklayınca HİÇBİR şey olmuyordu
// (v0.10.962 "sessiz yutma yok" kuralı). `say` hem canlı bölgeye duyurur
// hem aynı cümleyi not olarak tutar; çipler onu altında gösterir. Seçili
// pod değişince not silinir (görülen-durum kalıbı, efektsiz).
import { useCallback, useState } from 'react';

export function usePanelNote(pod: string, announce: (m: string) => void) {
  const [note, setNote] = useState('');
  const [seen, setSeen] = useState(pod);
  if (seen !== pod) { setSeen(pod); setNote(''); }
  const say = useCallback((m: string) => { announce(m); setNote(m); }, [announce]);
  return { note, setNote, say };
}
