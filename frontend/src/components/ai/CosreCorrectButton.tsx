// CosreCorrectButton — CoSRE Faz E: cevabın eylem satırında "✎ Düzelt".
// AÇIK düzeltme yolu (👍/👎 değil): tık formu açar (CosreCorrectForm, tembel
// chunk), yalnız formdaki "Kaydet" ekip notu yazar. exchangeId yokken (akış
// bitmeden) kapalı. Viewer formu görür, kaydet gerekçesiyle kapalı.
import { Suspense, lazy, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { useAuthRole } from '@/components/AuthProvider';

const CosreCorrectForm = lazy(() => import('./CosreMemoryConfirm').then(m => ({ default: m.CosreCorrectForm })));

export function CosreCorrectButton({ exchangeId }: { exchangeId?: string }) {
  const [open, setOpen] = useState(false);
  const role = useAuthRole();
  if (!open) {
    return (
      <Button variant="ghost" size="xs" disabled={!exchangeId} onClick={() => setOpen(true)}
        title="Cevaptaki yanlış bilgiyi düzelt — ekip notu olarak saklanır, bu kapsamdaki sonraki cevaplara veri olarak eklenir"
        aria-label="Cevabı düzelt">✎ Düzelt</Button>
    );
  }
  return (
    <Suspense fallback={null}>
      <CosreCorrectForm exchangeId={exchangeId} canSave={role === 'admin' || role === 'editor'} onClose={() => setOpen(false)} />
    </Suspense>
  );
}
