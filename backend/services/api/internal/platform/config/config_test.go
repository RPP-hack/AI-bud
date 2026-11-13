package config

import "testing"

func TestLoad(t *testing.T) {
	base := map[string]string{"DEMO_MODE": "true", "DATABASE_URL": "postgres://localhost/buddy", "MOCK_TOKEN": "test"}
	get := func(k string) string { return base[k] }
	c, err := Load(get)
	if err != nil || c.HTTPAddr != "127.0.0.1:8080" {
		t.Fatal(c, err)
	}
	for _, tc := range []struct{ k, v string }{{"DEMO_MODE", "false"}, {"DATABASE_URL", ""}, {"MOCK_TOKEN", ""}, {"MOCK_URL", "ftp://bad"}, {"MOCK_URL", "://"}, {"MOCK_GRANT_AFTER", "bad"}, {"MOCK_GRANT_AFTER", "-1s"}} {
		old, ok := base[tc.k]
		base[tc.k] = tc.v
		if _, err := Load(get); err == nil {
			t.Fatal(tc)
		}
		if ok {
			base[tc.k] = old
		} else {
			delete(base, tc.k)
		}
	}
	base["HTTP_ADDR"] = ":7777"
	base["MOCK_ADDR"] = ":7778"
	base["MOCK_URL"] = "https://example.test"
	base["MOCK_GRANT_AFTER"] = "0s"
	if c, err = Load(get); err != nil || c.GrantAfter != 0 || c.MockAddr != ":7778" {
		t.Fatal(c, err)
	}
}

func TestReconciliationConfiguration(t *testing.T) {
	base := map[string]string{"DEMO_MODE": "true", "DATABASE_URL": "postgres://localhost/buddy", "MOCK_TOKEN": "test"}
	get := func(key string) string { return base[key] }
	c, err := Load(get)
	if err != nil || !c.ReconcileEnabled || c.Reconciliation.BatchSize != 32 {
		t.Fatal(c, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"RECONCILE_ENABLED", "invalid"}, {"RECONCILE_INTERVAL", "bad"}, {"RECONCILE_INTERVAL", "0s"},
		{"RECONCILE_LEASE", "1s"}, {"RECONCILE_TIMEOUT", "-1s"}, {"RECONCILE_RETRY_MAX", "1s"},
		{"RECONCILE_BATCH_SIZE", "no"}, {"RECONCILE_BATCH_SIZE", "0"}, {"RECONCILE_BATCH_SIZE", "101"},
	} {
		base[tc.key] = tc.value
		if _, err := Load(get); err == nil {
			t.Fatal(tc)
		}
		delete(base, tc.key)
	}
	base["RECONCILE_ENABLED"] = "false"
	base["RECONCILE_BATCH_SIZE"] = "2"
	base["RECONCILE_INTERVAL"] = "2s"
	c, err = Load(get)
	if err != nil || c.ReconcileEnabled || c.Reconciliation.BatchSize != 2 {
		t.Fatal(c, err)
	}
}
