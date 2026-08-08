package proxypool

import (
	"errors"
	"testing"
	"time"
)

func TestRefreshStoresLastError(t *testing.T) {
	fetchErr := errors.New("load proxy failed")
	pool := New(Config{
		APIURL: "http://example.com/get",
		FetchFunc: func(apiURL string) ([]ProxyAddr, error) {
			return nil, fetchErr
		},
	})

	err := pool.Refresh()
	if !errors.Is(err, fetchErr) {
		t.Fatalf("expected fetch error, got=%v", err)
	}
	if !errors.Is(pool.LastRefreshError(), fetchErr) {
		t.Fatalf("last refresh error mismatch, got=%v", pool.LastRefreshError())
	}
}

func TestRefreshClearsLastErrorAfterSuccess(t *testing.T) {
	fetchErr := errors.New("load proxy failed")
	pool := New(Config{
		APIURL: "http://example.com/get",
		FetchFunc: func(apiURL string) ([]ProxyAddr, error) {
			return nil, fetchErr
		},
	})

	_ = pool.Refresh()

	pool.SetFetchFunc(func(apiURL string) ([]ProxyAddr, error) {
		return []ProxyAddr{
			{IP: "127.0.0.1", Port: "8080"},
		}, nil
	})

	if err := pool.Refresh(); err != nil {
		t.Fatalf("refresh should succeed, got=%v", err)
	}
	if pool.LastRefreshError() != nil {
		t.Fatalf("last refresh error should be cleared, got=%v", pool.LastRefreshError())
	}
}

func TestRefreshRequiresAPIURL(t *testing.T) {
	pool := New(Config{
		FetchFunc: func(apiURL string) ([]ProxyAddr, error) {
			return []ProxyAddr{{IP: "127.0.0.1", Port: "8080"}}, nil
		},
	})

	if err := pool.Refresh(); !errors.Is(err, ErrAPIURLEmpty) {
		t.Fatalf("expected ErrAPIURLEmpty, got=%v", err)
	}
}

func TestOnProxyGetRunsOutsidePoolLock(t *testing.T) {
	pool := New(Config{
		APIURL: "http://example.com/get",
		FetchFunc: func(apiURL string) ([]ProxyAddr, error) {
			return nil, nil
		},
	})
	if !pool.AddProxy("127.0.0.1", "8080") {
		t.Fatal("AddProxy failed")
	}

	done := make(chan struct{}, 1)
	pool.SetOnProxyGet(func(proxy *ProxyItem) {
		_ = pool.Size()
		_ = pool.GetStats()
		done <- struct{}{}
	})

	resultCh := make(chan error, 1)
	go func() {
		_, err := pool.Get()
		resultCh <- err
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnProxyGet callback appears to be blocked")
	}

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get appears to be blocked")
	}
}
