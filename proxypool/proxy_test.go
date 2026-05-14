package proxypool

import (
	"errors"
	"testing"
	"time"
)

func resetDefaultProxyForTest() {
	defaultProxyMu.Lock()
	defer defaultProxyMu.Unlock()
	defaultProxy = nil
}

func TestSetAuthProxyPasswordWithColon(t *testing.T) {
	p := NewProxy()
	p.SetAuthProxy("10.0.0.1:8080:user:pa:ss:word")

	if p.authHost != "10.0.0.1" {
		t.Fatalf("host mismatch, got=%q", p.authHost)
	}
	if p.authPort != "8080" {
		t.Fatalf("port mismatch, got=%q", p.authPort)
	}
	if p.authUsername != "user" {
		t.Fatalf("username mismatch, got=%q", p.authUsername)
	}
	if p.authPassword != "pa:ss:word" {
		t.Fatalf("password mismatch, got=%q want=%q", p.authPassword, "pa:ss:word")
	}
}

func TestSetAuthProxyClearsOldValues(t *testing.T) {
	p := NewProxy()
	p.SetAuthProxy("10.0.0.1:8080:user:pass")
	p.SetAuthProxy("invalid")

	if p.authHost != "" || p.authPort != "" || p.authUsername != "" || p.authPassword != "" {
		t.Fatalf("expected auth config cleared, got host=%q port=%q user=%q pass=%q",
			p.authHost, p.authPort, p.authUsername, p.authPassword)
	}
}

func TestInitDefaultProxyRebuildsSingleton(t *testing.T) {
	resetDefaultProxyForTest()
	defer resetDefaultProxyForTest()

	p1 := DefaultProxy()
	p2 := InitDefaultProxy(NewProxy().SetMode(ModePool).SetType(TypeSocks5))
	p3 := DefaultProxy()

	if p1 == nil || p2 == nil || p3 == nil {
		t.Fatalf("default proxy should not be nil")
	}
	if p1 == p2 {
		t.Fatalf("InitDefaultProxy should rebuild singleton")
	}
	if p2 != p3 {
		t.Fatalf("DefaultProxy should return rebuilt singleton")
	}
	if p3.GetMode() != ModePool {
		t.Fatalf("mode mismatch, got=%q want=%q", p3.GetMode(), ModePool)
	}
	if p3.GetType() != TypeSocks5 {
		t.Fatalf("type mismatch, got=%q want=%q", p3.GetType(), TypeSocks5)
	}
}

func TestGetProxyOnceReturnsRefreshError(t *testing.T) {
	fetchErr := errors.New("fetch failed")
	p := NewProxy().SetMode(ModePool)
	p.SetPoolAPI("http://example.com/get")

	calls := 0
	pool := p.GetPool().SetFetchFunc(func(apiURL string) ([]ProxyAddr, error) {
		calls++
		return nil, fetchErr
	})

	_, err := p.GetProxy()
	if !errors.Is(err, fetchErr) {
		t.Fatalf("expected fetch error, got=%v", err)
	}
	if calls != 1 {
		t.Fatalf("once mode should fetch once, got=%d", calls)
	}
	if !errors.Is(pool.LastRefreshError(), fetchErr) {
		t.Fatalf("last refresh error mismatch, got=%v", pool.LastRefreshError())
	}
}

func TestGetProxyMustSuccessCanBeStopped(t *testing.T) {
	fetchErr := errors.New("fetch failed")
	p := NewProxy().SetMode(ModePool)
	p.SetPoolAPI("http://example.com/get")

	calls := 0
	p.GetPool().SetFetchFunc(func(apiURL string) ([]ProxyAddr, error) {
		calls++
		return nil, fetchErr
	})
	p.ResumeGetProxy()

	resultCh := make(chan error, 1)
	go func() {
		_, err := p.GetProxy(GetProxyModeMustSuccess)
		resultCh <- err
	}()

	time.Sleep(100 * time.Millisecond)
	p.StopGetProxy()

	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrGetProxyStopped) {
			t.Fatalf("expected stop error, got=%v", err)
		}
		if !errors.Is(err, fetchErr) {
			t.Fatalf("expected joined fetch error, got=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("must-success mode was not stopped in time")
	}

	if calls == 0 {
		t.Fatal("must-success mode should attempt to fetch before stop")
	}
}
