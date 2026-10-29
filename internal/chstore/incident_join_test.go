package chstore

// v0.10.1588 — Davis paritesi FAZ 0.5 / D: tekil exception problemi (ölümcül /
// paylaşılan) yalnız aynı servis ya da 1-hop komşuda AÇIK incident varsa
// katılır. Komşu adayları: servislerin komşularının birleşimi, tekrarsız,
// servislerin kendileri hariç, sağlayıcı yoksa boş.

import (
	"reflect"
	"testing"
)

func TestJoinCandidateNeighbours(t *testing.T) {
	np := staticNeighbours{
		"orders-api":   {"payments-api", "orders-db", "inventory-api"},
		"payments-api": {"orders-api", "bank-gw", "orders-db"},
	}
	cases := []struct {
		name     string
		services []string
		np       NeighborProvider
		want     []string
	}{
		{"sağlayıcı yok", []string{"orders-api"}, nil, nil},
		{"tek servis", []string{"orders-api"}, np, []string{"payments-api", "orders-db", "inventory-api"}},
		{"iki servis: birleşim, tekrarsız, kendileri hariç", []string{"orders-api", "payments-api"}, np, []string{"orders-db", "inventory-api", "bank-gw"}},
		{"boş ve tekrar eden girdi", []string{"", "orders-api", "orders-api"}, np, []string{"payments-api", "orders-db", "inventory-api"}},
		{"komşusuz servis", []string{"lonely-api"}, np, nil},
	}
	for _, c := range cases {
		if got := joinCandidateNeighbours(c.services, c.np); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

type staticNeighbours map[string][]string

func (n staticNeighbours) Neighbors(s string) []string { return n[s] }
