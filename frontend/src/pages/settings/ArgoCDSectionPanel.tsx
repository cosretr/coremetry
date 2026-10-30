import type { ReactNode } from 'react';

// ArgoCDSectionPanel — v0.10.974 — Ayarlar › Argo CD bölümlerinin ortak
// kabuğu (mockup Main: başlık + soluk sayaç + açıklama paragrafı). Hub'lar,
// Instance'lar, Ortamlar ve keşif paneli aynı başlık ritmini kullanır; beş
// dosyada aynı statik stili tekrarlamak yerine tek yer. Bu iş akışı
// styles/**'a dokunmuyor. Faz 2 dilim 1 R15: CSSProperties sabitleri →
// Tailwind utility (aynı token'lar).

export function ArgoCDSectionPanel({ id, title, meta, desc, children }: {
  id: string;
  title: ReactNode;
  meta?: ReactNode;
  desc?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-4">
        <h3 id={id} className="m-0 text-[length:var(--fs-md)] font-semibold text-(--text)">{title}</h3>
        {meta != null && meta !== '' && <span className="text-[length:var(--fs-sm)] text-(--text3)">{meta}</span>}
      </div>
      {desc && <p className="m-0 max-w-[760px] text-[length:var(--fs-sm)] leading-[18px] text-(--text2)">{desc}</p>}
      {children}
    </section>
  );
}

/** Bölüm altı dipnotu (11px, --text3) — `id` aria-describedby hedefi olabilir. */
export function ArgoCDNote({ id, children }: { id?: string; children: ReactNode }) {
  return <p id={id} className="m-0 max-w-[760px] text-[length:var(--fs-xs)] leading-4 text-(--text3)">{children}</p>;
}
