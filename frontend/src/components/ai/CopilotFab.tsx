import { IconButton } from '@/components/ui/IconButton';

// v0.10.1599 — CoSRE başlatıcısı (FAB) + marka işareti CopilotChat'ten
// buraya taşındı (birebir; davranış/görünüm değişmedi). Sebep: kabuk sohbeti
// artık tembel yüklüyor (CopilotChatGate) ve sohbet yığını gelmeden ÖNCE de
// aynı FAB çizilmeli — iki kopya yerine tek bileşen, iki kullanıcı (kapı ve
// sohbetin kendisi). Bu dosya hafif kalmalı: ana yığına girer.

// CoSRE markası — v0.10.498 (operatör: "direkt OpenTelemetry iconu olsun";
// teleskop mockup'ı reddedildi): resmî OpenTelemetry ikonu, repodaki
// /favicon.svg (CNCF artwork'ten birebir kopya, v0.9.495) — ikinci kopya
// yok, favicon güncellenince düğme de güncellenir. Eski gradient sparkline
// (v0.9.163, varyant B) ve .cm-ai-spark çizim animasyonu kaldırıldı.
export function AiMark({ size = 26 }: { size?: number }) {
  return (
    <img src="/favicon.svg" width={size} height={size} alt="" aria-hidden="true" draggable={false}
      style={{ display: 'block', width: size, height: size }} />
  );
}

// Launcher — Yuvarlak FAB kendi anatomisi; shared <Button> atomu uygulanmaz
// (U1 batch-2 kararı). v0.10.924 — buton bütünlüğü Faz 2: kabuk glif-only
// IconButton (bare; aria-label sözleşmesi + odak halkası). 48px yuvarlak
// gradyan anatomisi atomda bir rung değil, satır-içi kalır; `bare` zemin boyamaz.
// v0.10.926 — title kalır: ipucu kutusu FAB'ın KARDEŞİ olarak `--z-tooltip`
// (30) ile çizilir, FAB ise `--z-fab` (80). Sağ kenardaki çekmeceler (60/61),
// span paneli (40) ve üstteki nudge baloncuğu (80) açıkken ipucu onların
// ALTINDA kalıp görünmez; yerel title üstte.
export function CopilotFab({ criticalOpen, onClick }: { criticalOpen: number; onClick: () => void }) {
  return (
    <IconButton variant="bare" data-tour="cosre-fab"
      className={criticalOpen > 0 ? 'cm-ai-fab is-alert' : 'cm-ai-fab'}
      onClick={onClick}
      title={criticalOpen > 0 ? `CoSRE — ${criticalOpen} açık kritik problem` : "CoSRE'ye sor"}
      aria-label={criticalOpen > 0 ? `CoSRE, ${criticalOpen} açık kritik problem` : 'CoSRE'}
      style={{
        position: 'fixed', right: 18, bottom: 18, zIndex: 'var(--z-fab)',
        width: 48, height: 48, borderRadius: 24,
        background: 'linear-gradient(135deg, var(--accent-soft), var(--bg1))',
        border: '1px solid var(--accent2)',
        display: 'grid', placeItems: 'center',
        cursor: 'pointer', boxShadow: '0 2px 14px rgba(0,0,0,0.3)',
      }}
      icon={<>
        <AiMark size={26} />
        {criticalOpen > 0 && (
          <span aria-hidden="true" style={{
            position: 'absolute', top: -3, right: -3,
            minWidth: 18, height: 18, padding: '0 5px', boxSizing: 'border-box',
            borderRadius: 9, background: 'var(--err-solid)', color: 'var(--on-accent)',
            fontSize: 10, fontWeight: 700, lineHeight: '14px',
            display: 'grid', placeItems: 'center',
            border: '2px solid var(--bg1)',
          }}>{criticalOpen > 9 ? '9+' : criticalOpen}</span>
        )}
      </>} />
  );
}
