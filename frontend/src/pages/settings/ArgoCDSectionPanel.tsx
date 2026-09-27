import type { CSSProperties, ReactNode } from 'react';

// ArgoCDSectionPanel — v0.10.974 — Ayarlar › Argo CD bölümlerinin ortak
// kabuğu (mockup Main: başlık + soluk sayaç + açıklama paragrafı). Hub'lar,
// Instance'lar, Ortamlar ve keşif paneli aynı başlık ritmini kullanır; beş
// dosyada aynı statik stili tekrarlamak yerine tek yer. Bu iş akışı
// styles/**'a dokunmuyor (paylaşılan globals.css eşzamanlı düzenleniyor):
// statik değerler dosya düzeyinde adlandırılmış sabitlerde, token'larla.

const TITLE: CSSProperties = { margin: 0, fontSize: 'var(--fs-md)', fontWeight: 600, color: 'var(--text)' };
const META: CSSProperties = { fontSize: 'var(--fs-sm)', color: 'var(--text3)' };
const DESC: CSSProperties = { margin: 0, maxWidth: 760, fontSize: 'var(--fs-sm)', lineHeight: '18px', color: 'var(--text2)' };
const NOTE: CSSProperties = { margin: 0, maxWidth: 760, fontSize: 'var(--fs-xs)', lineHeight: '16px', color: 'var(--text3)' };

export function ArgoCDSectionPanel({ id, title, meta, desc, children }: {
  id: string;
  title: ReactNode;
  meta?: ReactNode;
  desc?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="stack gap-3">
      <div className="row gap-4 row-wrap">
        <h3 id={id} style={TITLE}>{title}</h3>
        {meta != null && meta !== '' && <span style={META}>{meta}</span>}
      </div>
      {desc && <p style={DESC}>{desc}</p>}
      {children}
    </section>
  );
}

/** Bölüm altı dipnotu (11px, --text3) — `id` aria-describedby hedefi olabilir. */
export function ArgoCDNote({ id, children }: { id?: string; children: ReactNode }) {
  return <p id={id} style={NOTE}>{children}</p>;
}
