package mcptools

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/entity"
)

// v0.10.2240 (Entity S5) — db / queue / ext entity'leri resolve_entity
// adayıdır; tek aday çözülünce neighbours calls / called_by taşır.
func TestResolveEntitiesDependencies(t *testing.T) {
	idx := EntityCatalogIndex{
		Services: []string{"checkout-api"},
		Dependencies: []EntityCandidate{
			{Kind: entity.TypeDatabase, Name: "postgresql@orders-db", ID: "db:postgresql@orders-db"},
			{Kind: entity.TypeQueue, Name: "kafka:orders.created", ID: "queue:kafka:orders.created"},
		},
		EntityLayer: true,
	}
	got := ResolveEntities("orders-db", idx, 5)
	one := ResolvedOne(got)
	if one == nil || one.Kind != entity.TypeDatabase || one.ID != "db:postgresql@orders-db" {
		t.Fatalf("db adayı çözülmeli: %+v", got)
	}
}
