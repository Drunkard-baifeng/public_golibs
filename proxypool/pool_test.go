package proxypool

import (
	"errors"
	"testing"
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
