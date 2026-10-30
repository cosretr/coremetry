import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

// cn — v0.10.1613 (shadcn Faz 1 R5, plan §5.2/§7.4). shadcn'in `utils`
// takma adı. Kayıt defteri (new-york-v4, shadcn@4.21.5) üretilen dosyalara
// `import { cn } from "cn"` yazıyor ve npm'deki `cn` paketini kuruyor; plan
// §5.5'in bağımlılık tablosunda o paket yok. Çıplak `cn` kimliği
// vite.config.ts + vitest.config.ts + tsconfig.json'da BU dosyaya takma
// adlı (cnAlias.pin.test.ts) → üretilen dosyalar harfi harfine kalır,
// davranış twMerge(clsx(…)).
// Kural (§7.4): yalnız components/shadcn, components/coremetry,
// components/ai-elements ve geçirilmiş sayfalar; eski sınıf dizisi
// (`cn('badge', 'b-' + tone)`) kurmak için asla.
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
