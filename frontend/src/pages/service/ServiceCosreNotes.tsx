// ServiceCosreNotes — CoSRE Faz E: Servis › Details › "CoSRE notları"
// bölümünün gövdesi (Service.tsx React.lazy ile yükler; bölüm kapalıyken ne
// chunk ne GET /api/ai/memory). Viewer notları görür, editör/admin düzenler.
import { useAuthRole } from '@/components/AuthProvider';
import { CosreMemoryPanel } from '@/components/ai/CosreMemoryPanel';

export default function ServiceCosreNotes({ service }: { service: string }) {
  const role = useAuthRole();
  return <CosreMemoryPanel service={service} canEdit={role === 'admin' || role === 'editor'} storageKey="svc-dtl-cosre-notes" />;
}
