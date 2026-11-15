package logstore

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// v0.10.2233 (Entity S3) — /api/logs?entity=pod:… ES'de ALAN süzgecidir
// (pod + namespace + cluster rolleri, exactTermsBothShapes), gövde araması
// değil; zaman penceresi range olarak iner; PIT yalnız sayfalama niyetinde.
func TestESEntityScopeIsFieldFilter(t *testing.T) {
	s := &ESStore{}
	s.cfg.defaults()
	s.fields = s.cfg.Fields
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(s.buildQuery(Filter{Pod: "web-5c7dbf9b8d-x2b9z", Namespace: "shop", Cluster: "east", From: t0.Add(-time.Hour), To: t0}))
	if err != nil {
		t.Fatal(err)
	}
	q := string(raw)
	for _, want := range []string{
		`"k8s.pod.name.keyword":"web-5c7dbf9b8d-x2b9z"`,
		`"k8s.namespace.name.keyword":"shop"`,
		`"range"`,
		`"minimum_should_match":1`,
	} {
		if !strings.Contains(q, want) {
			t.Errorf("entity kapsamı %s içermeli: %s", want, q)
		}
	}
	if !strings.Contains(q, `"east"`) {
		t.Errorf("cluster değeri süzgeçte yok: %s", q)
	}
	for _, banned := range []string{`"query_string"`, `"pit"`} {
		if strings.Contains(q, banned) {
			t.Errorf("entity kapsamı %s üretmemeli: %s", banned, q)
		}
	}
}
