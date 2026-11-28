---
paths:
  - "internal/**"
  - "cmd/**"
  - "migrations/**"
---
# Arka uç kuralları (yalnız `internal/`, `cmd/`, `migrations/` altında çalışırken yüklenir)

- `internal/api/api.go` büyümez; yeni uç kendi dosyasında (`/api-route`).
- `make audit` CHECK 5: tek satırda `IN (SELECT` ya da `JOIN (SELECT` yazılmaz (`GLOBAL IN`; `JOIN (` + yeni satır + `SELECT`).
- `spans` / `metric_points` sorgusu: `LIMIT` + `SETTINGS max_execution_time` + zaman sınırlı WHERE; toplam MV'den okunur.
- Pod / servis / deployment / cluster bağı entity modeliyle kurulur; ad eşleştirmesi yapılmaz.
- Oylar ve triage düğmeleri çalışma zamanı kararı ve eval etiketi sürmez (tek onaylı istisna: gölge kuralı).
- Prod LLM yalnız yerel ağ geçidi; tek modele bağlı kod yolu yazılmaz. PII maskeleme özelliği yok.
- Go değişikliğinde kapıya ek olarak `golangci-lint run ./... --timeout 15m` (kapıda yok, CI'da var).
