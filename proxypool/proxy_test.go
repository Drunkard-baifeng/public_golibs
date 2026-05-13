package proxypool

import "testing"

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
