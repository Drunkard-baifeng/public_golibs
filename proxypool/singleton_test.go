package proxypool

import "testing"

func resetDefaultPoolForTest() {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultPool = nil
	defaultCfg = Config{}
}

func TestDefaultReturnsSingleton(t *testing.T) {
	resetDefaultPoolForTest()
	defer resetDefaultPoolForTest()

	p1 := Default()
	p2 := Default()

	if p1 == nil || p2 == nil {
		t.Fatalf("default pool should not be nil")
	}
	if p1 != p2 {
		t.Fatalf("Default should return same instance")
	}
}

func TestInitDefaultRecreatesWithConfig(t *testing.T) {
	resetDefaultPoolForTest()
	defer resetDefaultPoolForTest()

	old := Default()

	cfg := Config{
		APIURL:        "http://example.com/get",
		MaxUseCount:   9,
		ExpireSeconds: 99,
		MinPoolSize:   7,
		FetchFunc: func(apiURL string) ([]ProxyAddr, error) {
			return nil, nil
		},
	}
	newPool := InitDefault(cfg)
	if newPool == nil {
		t.Fatalf("InitDefault should not return nil")
	}
	if old == newPool {
		t.Fatalf("InitDefault should recreate default pool")
	}

	got := Default()
	if got != newPool {
		t.Fatalf("Default should return latest initialized pool")
	}

	if newPool.apiURL != cfg.APIURL {
		t.Fatalf("apiURL mismatch, got=%q want=%q", newPool.apiURL, cfg.APIURL)
	}
	if newPool.maxUseCount != cfg.MaxUseCount {
		t.Fatalf("maxUseCount mismatch, got=%d want=%d", newPool.maxUseCount, cfg.MaxUseCount)
	}
	if newPool.expireSeconds != cfg.ExpireSeconds {
		t.Fatalf("expireSeconds mismatch, got=%d want=%d", newPool.expireSeconds, cfg.ExpireSeconds)
	}
	if newPool.minPoolSize != cfg.MinPoolSize {
		t.Fatalf("minPoolSize mismatch, got=%d want=%d", newPool.minPoolSize, cfg.MinPoolSize)
	}
	if newPool.fetchFunc == nil {
		t.Fatalf("fetchFunc should be set")
	}
}

func TestPackageGetAndGetStats(t *testing.T) {
	resetDefaultPoolForTest()
	defer resetDefaultPoolForTest()

	pool := InitDefault(Config{
		MaxUseCount:   5,
		ExpireSeconds: 180,
		MinPoolSize:   1,
	})
	if !pool.AddProxy("127.0.0.1", "8080") {
		t.Fatalf("AddProxy failed")
	}

	stats := GetStats()
	if stats.Total != 1 {
		t.Fatalf("stats total mismatch, got=%d want=1", stats.Total)
	}
	if stats.Available != 1 {
		t.Fatalf("stats available mismatch, got=%d want=1", stats.Available)
	}

	proxy, err := Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if proxy.String() != "127.0.0.1:8080" {
		t.Fatalf("proxy mismatch, got=%q", proxy.String())
	}
}
