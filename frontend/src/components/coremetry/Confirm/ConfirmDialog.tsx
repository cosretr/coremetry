// coremetry/Confirm/ConfirmDialog — Faz 2 dilim 1 R3 (plan G2). ConfirmGate'in
// TEMBEL çizicisi (Radix burada; kapı eager ve Radix'siz). Default export:
// React.lazy. Odak İPTALDE (Radix AlertDialog'un açılış odağı Cancel);
// Esc / Cancel = false, Action = true; dış tık kapatmaz.
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '../overlay/AlertDialog';
import { CM_STRINGS } from '../strings';
import type { ConfirmOptions } from './context';

export default function ConfirmDialog({ opts, onSettle }: { opts: ConfirmOptions; onSettle(ok: boolean): void }) {
  return (
    <AlertDialog open onOpenChange={o => { if (!o) onSettle(false); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="text-[length:var(--fs-lg)]">{opts.title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="text-[length:var(--fs-md)] leading-relaxed text-(--text2)">{opts.body}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel size="sm" data-confirm-cancel="" onClick={() => onSettle(false)}>
            {opts.cancelLabel ?? CM_STRINGS.cancel}
          </AlertDialogCancel>
          <AlertDialogAction size="sm" variant={opts.danger ? 'destructive' : 'default'} onClick={() => onSettle(true)}>
            {opts.confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
