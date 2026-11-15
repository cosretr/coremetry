package api

// topology_entity_id.go — v0.10.2240 (Entity S5; plan §4 "Topoloji"):
// servis grafiği düğümünün EK `entityId` alanı. Düğüm id'si DEĞİŞMEZ
// (mevcut tüketiciler aynen); entity kimliği tek kuraldan türetilir
// (entity.TopologyEntityID) — syncer'ın calls geçişi aynı fonksiyonla yazar,
// gidiş-dönüş testi ikisini bağlar. Okuma maliyeti yok (saf türetim).

import "github.com/cilcenk/coremetry/internal/entity"

// graphNodeEntityID — SAF: yalnız entity karşılığı olan türler
// (service | database | queue | external); diğerleri "".
func graphNodeEntityID(id, kind string) string {
	switch kind {
	case "service", "database", "queue", "external":
		return entity.TopologyEntityID(id)
	}
	return ""
}
