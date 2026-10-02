import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowDownToLine } from 'lucide-react';
import { DisclosureButton } from '@/components/ui/DisclosureButton';

// detailSections — tam sayfa problem detaylarının ortak yapı taşları
// (v0.10.1032). Sect / SignalLink / DeployBox ProblemDetail.tsx'te yerel
// tanımlardı; operatör "Anomali ve alert rule'lara girdiğimde drawer çıkıyor.
// Exception gibi detay gözükmüyor." deyince anomali olayının da tam sayfası
// doğdu (AnomalyEventDetail) ve aynı görsel dili konuşması gerekti. Kopya
// yerine buraya taşındılar: iki detay aynı bölüm başlığını, aynı sinyal
// bağlantısını, aynı deploy kutusunu basar — biri değişince öteki de değişir.

// Sect — detay sayfasının bölüm kartı.
//
// v0.10.1032 (operatör: "Anlaşılır olsun. Çok detay verince daha anlaşılır
// olmuyor — alert ve anomaliler de.") — `collapsible`: ikincil bölümler
// KAPALI gelir, başlık tek tıkla açar. Kapalıyken gövde MOUNT EDİLMEZ, yani
// içinde sorgu atan bir panel (bildirim geçmişi, runbook koşuları) ancak
// açılınca çeker — sayfa açılışının istek bütçesi küçülür. Açıklık yerel
// durum, kalıcı değil (her açılışta kapalı; paylaşılan link bölüm durumunu
// taşımaz — taşıdığı şey olayın kendisi). Başlık ui/DisclosureButton
// (aria-expanded + tek ▸/▾ glif dili); iki detay sayfası da bu TEK
// uygulamayı kullanır.
export function Sect({ title, accent, sub, children, collapsible, defaultOpen }: {
  title: string; accent?: boolean; sub?: React.ReactNode; children: React.ReactNode;
  collapsible?: boolean; defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(!collapsible || !!defaultOpen);
  const subNode = sub && <span className="pb-sect-sub">{sub}</span>;
  if (!collapsible) {
    return (
      <div className="pb-sect">
        <div className={accent ? 'h accent' : 'h'}>
          {title}
          {subNode}
        </div>
        <div className="b">{children}</div>
      </div>
    );
  }
  return (
    <div className={open ? 'pb-sect' : 'pb-sect pb-sect--closed'}>
      <div className={accent ? 'h accent' : 'h'}>
        <DisclosureButton expanded={open} onClick={() => setOpen(o => !o)} className="pb-sect-toggle">
          {title}
        </DisclosureButton>
        {subNode}
      </div>
      {open && <div className="b">{children}</div>}
    </div>
  );
}

// DetailSummary — v0.10.1032 (operatör: "Anlaşılır olsun. Çok detay verince
// daha anlaşılır olmuyor"). Detay şeridinin hemen altında, nöbetçinin üç
// saniyede okuduğu TEK cümle ("ne oldu") + bir "ne zaman" satırı. Cümleler
// saf kurucularda (./detailSummary — tablo testli); burası yalnız çizer.
// `detail`: isteğe bağlı tek satırlık kimlik (log deseni gibi) — cümle "bu
// desen" diyorsa hangi desen olduğu görünmeli.
export function DetailSummary({ sentence, when, detail }: {
  sentence: string; when: string; detail?: string;
}) {
  return (
    <div className="pd-summary">
      <div className="pd-summary__what">{sentence}</div>
      {detail && <div className="pd-summary__detail mono" title={detail}>{detail}</div>}
      <div className="pd-summary__when">{when}</div>
    </div>
  );
}

export function SignalLink({ to, label, sub }: { to: string; label: string; sub?: string }) {
  return (
    <Link to={to} style={{
      display: 'flex', alignItems: 'baseline', gap: 8,
      padding: '7px 10px', marginBottom: 6,
      border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)',
      background: 'var(--bg2)', textDecoration: 'none',
      color: 'var(--accent2)', fontSize: 12,
    }}>
      <span style={{ fontWeight: 600 }}>{label} ↗</span>
      {sub && <span style={{ color: 'var(--text3)', fontSize: 11 }}>{sub}</span>}
    </Link>
  );
}

// DeployBox — renders ONLY when the row carries a recentDeploy (spec:
// no placeholder, no "no deploy detected", no extra fetch).
// v0.10.1032 — `before`: neyin ÖNCESİNE indiği. Varsayılan alarm problemi
// cümlesi ("this problem opened"); anomali sayfası "the spike started" verir.
export function DeployBox({ version, ageSeconds, before = 'this problem opened' }: {
  version: string; ageSeconds: number; before?: string;
}) {
  return (
    <div style={{
      fontSize: 12, padding: '8px 12px', marginTop: 10,
      borderRadius: 'var(--radius-sm)',
      background: 'color-mix(in srgb, var(--warn) 10%, transparent)',
      border: '1px solid color-mix(in srgb, var(--warn) 35%, transparent)',
    }}>
      <span style={{ fontWeight: 600, display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        <ArrowDownToLine size={13} strokeWidth={1.75} /> Deploy correlation
      </span>{' — '}
      <code className="mono">{version}</code> landed{' '}
      <b>{Math.max(1, Math.round(ageSeconds / 60))}m before</b> {before}.
    </div>
  );
}
