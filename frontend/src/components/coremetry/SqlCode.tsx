// coremetry/SqlCode — v0.10.1859 (operatör: "SQL düz metin yazıyor; Toad'daki
// gibi renkli gösterilsin" + "SQL için panoya kopyala da olsun").
//
// SQL metni bloğu: sözdizimi renkli + sağ üstte kopyala düğmesi. Renk
// belirteçleri lib/sqlHighlight (saf, kayıpsız); ekranda görünen ve kopyalanan
// metin her zaman `sql` prop'unun KENDİSİ.
//
// Renkler yalnız mevcut tokenlardan (yeni renk yok) ve hepsi metin olarak
// kontrastı ölçülmüş tokenlar: anahtar sözcük = bağlantı/vurgu metni
// (--accent2, ayrıca kalın — renk tek taşıyıcı değil), fonksiyon / metin /
// sayı = --sig-* tonları (altı temada bg0–bg2 üzerinde çivili), yorum = soluk
// metin + italik. Sınıf haritası ./sqlCodeClass.ts içinde (components/coremetry
// altında çünkü Tailwind yalnız @source dizinlerini tarar).
//
// Tablolarda KULLANILMAZ: liste hücresi tek satır düz kimliktir (tablo
// standardı: renk yalnız sapan değerde). Bu bileşen tam ifade bloğu içindir.
import { useEffect, useMemo, useRef, useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { cn } from '@/lib/cn';
import { copyToClipboard } from '@/lib/clipboard';
import { tokenizeSql } from '@/lib/sqlHighlight';
import { SQL_TOKEN_CLASS, SQL_CODE_STRINGS } from './sqlCodeClass';

export interface SqlCodeProps {
  sql: string;
  /** Küçük yazı + daha alçak kutu (örnek ifade, satır altı ayrıntı). */
  dense?: boolean;
  className?: string;
}

export function SqlCode({ sql, dense, className }: SqlCodeProps) {
  const tokens = useMemo(() => tokenizeSql(sql), [sql]);
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  const copy = async () => {
    if (await copyToClipboard(sql)) {
      setCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    }
  };
  const label = copied ? SQL_CODE_STRINGS.copied : SQL_CODE_STRINGS.copy;
  return (
    <div data-sql-code="" className={cn('relative', className)}>
      <pre
        className={cn(
          'm-0 overflow-y-auto rounded-md border border-border bg-[color:var(--bg1)] pr-10 font-mono leading-relaxed break-words whitespace-pre-wrap text-foreground',
          dense ? 'max-h-[200px] p-2 text-xs' : 'max-h-[260px] p-3 text-sm',
        )}
      >
        <code>
          {tokens.map((t, i) => (t.kind === 'plain'
            ? t.text
            : <span key={i} data-sql={t.kind} className={SQL_TOKEN_CLASS[t.kind]}>{t.text}</span>))}
        </code>
      </pre>
      <Button
        variant="ghost" size="icon-xs" aria-label={label} title={label}
        data-copied={copied ? '' : undefined}
        className="absolute top-1.5 right-1.5 text-muted-foreground data-[copied]:text-status-ok"
        onClick={() => void copy()}
      >
        {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
      </Button>
    </div>
  );
}
