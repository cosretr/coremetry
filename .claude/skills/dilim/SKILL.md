---
name: dilim
description: 'Onaylı bir audit dilimini ajana devretmek için brif kalıbı. WHEN: bir audit''in "Dilim planı"ndan tek dilim uygulanacak ve iş ajana verilecek. WHEN NOT: audit yazmak (/audit), tek dosyalık düzeltme, operatör kararı bekleyen dilim.'
---

# /dilim — tek dilim, tek ajan, tek commit

Çağrı: `/dilim <audit yolu> <dilim no> [taban etiket]`. Ana oturum aşağıdaki
brifi doldurur ve işi uygulama ajanına (`opus`, worktree yalıtımıyla) verir.
`.claude/rules/` içindeki kurallar brife YAZILMAZ; brif onlara işaret eder.

## Brif kalıbı

```
Coremetry reposunda <audit adı> denetiminin <N>. dilimini uygula. Kendi worktree'nde çalış.
İlk iş: git fetch --tags origin && git reset --hard <taban etiket>.
Sonra oku: CLAUDE.md, .claude/rules/*.md, <audit yolu> (§<bölümler>, dilim tablosunun <N>. satırı, "Kararlar").

Kapsam: <dilim satırından: ne yapılacak, çıkış ölçütü, ratchet>
Kapsam dışı: <sonraki dilimler; operatör kararı bekleyenler; bildirim metni, dedektör mantığı gibi yasaklar>
Bu dilime özel dikkat: <paket bütçesi, davranış değişikliği, veri riski>

Bitiş: TZ=UTC scripts/gate.sh all; Go değiştiyse golangci-lint; make audit (çıkış 0).
Tek commit: `v0.10.<X> — <başlık> (<audit> dilim <N>)`. Tag, push, deploy yok. docs/DECISIONS.md'ye dilim girişi.

Rapor: commit sha ve worktree yolu; çıkış ölçütünün önce/sonra sayıları; ratchet tabanları; paket ve CSS
önce/sonra; kaldırılan ya da değişen davranışlar (beklenen: yok); açık kararlar; kapıların gerçek son satırları.
```

## Ana oturumun işi

1. Dilimin giriş ölçütü karşılanmış mı, operatör kararı bekliyor mu: bekliyorsa başlatma, sor.
2. Taban etiket yeşil mi (CI + Release); kırmızı etiketin üstüne dilim kurulmaz.
3. Ajan raporu gelince: kapı sonucunu ajanın sözünden değil `verifier` ya da sürüm betiğinden al.
4. Davranış değişikliği varsa operatöre sürümden önce düz cümleyle söyle.
5. Tur başına tek sürüm; yerel deploy yalnız istenirse (`scripts/dev-deploy.sh`).
