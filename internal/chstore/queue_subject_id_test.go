package chstore

import (
	"strings"
	"testing"
)

// TestParseQueueSubjectIDRoundTrip — v0.10.1802 (Davis dilim 3): Kafka lag
// öznesinin topic adı kimlikten geri okunur; gidiş-dönüş bozulmaz.
func TestParseQueueSubjectIDRoundTrip(t *testing.T) {
	for _, c := range []struct{ sys, dest string }{{"kafka", "orders"}, {"Kafka", "orders.v2"}, {"rabbitmq", "a@b"}} {
		id := QueueSubjectID(c.sys, c.dest)
		sys, dest, ok := ParseQueueSubjectID(id)
		if !ok || dest != c.dest || sys != strings.ToLower(c.sys) {
			t.Errorf("%q → %q %q %v", id, sys, dest, ok)
		}
	}
	for _, bad := range []string{"", "orders", DBSubjectID("postgresql", "db1"), QueueSubjectID("kafka", "x")[:len(QueueSubjectID("kafka", "x"))-1]} {
		if _, _, ok := ParseQueueSubjectID(bad); ok {
			t.Errorf("%q kabul edildi", bad)
		}
	}
}
