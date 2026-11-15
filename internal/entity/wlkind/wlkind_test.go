package wlkind

import "testing"

func TestFromPod(t *testing.T) {
	cases := []struct{ pod, kind, name string }{
		{"payment-api-7d9f8c6b5-x2k9q", Deployment, "payment-api"},
		{"shop-login-prep-6bd9df6c4d-x2b1z", Deployment, "shop-login-prep"},
		{"api-gateway-5c7dbf9b8d-7xq2p", Deployment, "api-gateway"},
		{"db-0", StatefulSet, "db"},
		{"kafka-12", StatefulSet, "kafka"},
		{"node-exporter-x2b1z", Derived, "node-exporter"},
		{"my-app-canary-x2b9z", Derived, "my-app-canary"}, // "canary" rs-hash değil
		{"svc-audio", "", ""},                             // sesli harf: rand5 değil
		{"standalone", "", ""},
		{"trailing-", "", ""},
	}
	for _, c := range cases {
		kind, name, ok := FromPod(c.pod)
		if (c.kind == "") != !ok || kind != c.kind || name != c.name {
			t.Errorf("%q → %s/%s/%v; want %s/%s", c.pod, kind, name, ok, c.kind, c.name)
		}
	}
}

func TestCanonical(t *testing.T) {
	cases := []struct{ kind, name, wantKind, wantName string }{
		{ReplicaSet, "checkout-7d9f8c6b5", Deployment, "checkout"},
		{ReplicaSet, "checkout-5c7dbf9b8d", Deployment, "checkout"},
		{ReplicaSet, "standalone-rs", ReplicaSet, "standalone-rs"},
		{Deployment, "checkout", Deployment, "checkout"},
		{"Rollout", "checkout", "Rollout", "checkout"},
		{"CronJob", "nightly", "CronJob", "nightly"},
	}
	for _, c := range cases {
		k, n := Canonical(c.kind, c.name)
		if k != c.wantKind || n != c.wantName {
			t.Errorf("Canonical(%s,%s) = %s/%s; want %s/%s", c.kind, c.name, k, n, c.wantKind, c.wantName)
		}
	}
	if !Replaceable(Derived, "x") || !Replaceable(ReplicaSet, "checkout-7d9f8c6b5") || Replaceable(ReplicaSet, "standalone-rs") || Replaceable(Deployment, "x") {
		t.Error("Replaceable")
	}
}
