package main

import (
	"fmt"
	"log"

	"github.com/Drunkard-baifeng/public_golibs/proxypool"
)

func ProxypoolCall1() {

	cfg := proxypool.Config{
		APIURL:        "http://your-proxy-api.com/get",
		MaxUseCount:   5,
		ExpireSeconds: 180,
		MinPoolSize:   3,
		FetchFunc:     proxypool.SimpleFetchFunc,
	}

	proxypool.InitDefault(cfg)

	proxy, err := proxypool.Get()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(proxy.String())

	fmt.Println(proxypool.GetStats())

	proxy, err = proxypool.Get()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(proxy.String())

	fmt.Println(proxypool.GetStats())
}

func InitProxyManager() *proxypool.Proxy {
	// mode: 不换IP, 虚拟IP, 代理池IP, 账密IP
	// type: http, socks5
	// poolAPI: 代理池API地址
	// poolMaxUseCount: 代理池最大使用次数
	// poolExpireSeconds: 代理池过期时间
	// poolMinSize: 代理池最小数量

	// 创建代理管理器
	proxymanager := proxypool.NewProxy()

	// 设置代理模式
	proxyMode := proxypool.ModeAuth
	proxyType := proxypool.TypeSocks5

	switch proxyMode {
	case proxypool.ModeNone:
		proxymanager.SetMode(proxypool.ModeNone)
	case proxypool.ModeVirtual:
		proxymanager.SetMode(proxypool.ModeVirtual)
	case proxypool.ModePool:
		proxymanager.SetMode(proxypool.ModePool)
		proxymanager.SetPoolAPI("http://ecs.hailiangip.com:8422/api/getIpEncrypt?dataType=1&encryptParam=MBftJikKr3D5USLE1py4JK%2FfT7GfQ3maxDbxprdoOkzMHdUepM8Iw2ZmIWFtsSmJQEC8Eq1Zc2rsCypXjs5JZxefAKQWIBgbFD8MqY%2BnbFBsG7lku0h22vyr2kCgbo%2F6QKA1VpnunhXUNfZrno7hJ0eQ512UY6nMAfHhl4RAsbkCI4n3zNJRvRomO6LLTrnlarf5KKS7AFD8J5Z1OJJ%2FWfUil3KO10lMkIvLEBHnQaJfbjUcgj5d2L2WS%2F4B8XQR")
		proxymanager.SetPoolMaxUseCount(5)
		proxymanager.SetPoolExpireSeconds(180)
		proxymanager.SetPoolMinSize(3)
	case proxypool.ModeAuth:
		proxymanager.SetMode(proxypool.ModeAuth)
		proxymanager.SetAuthProxy("192.168.1.1:8080:user:pass")
	default:
		proxymanager.SetMode(proxypool.ModeNone)
	}

	// 设置代理类型
	switch proxyType {
	case proxypool.TypeHTTP, proxypool.TypeSocks5:
		proxymanager.SetType(proxyType)
	default:
		proxymanager.SetType(proxypool.TypeHTTP)
	}

	proxypool.InitDefaultProxy(proxymanager)
	return proxymanager
}

func ProxypoolCall2() {

	InitProxyManager()

	proxy, err := proxypool.DefaultProxy().GetProxy()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(proxy.Type)
	fmt.Println(proxy.Proxy)
	fmt.Println(proxy.IP)
	fmt.Println(proxy.Headers)

	proxy, err = proxypool.DefaultProxy().GetProxy()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(proxy.Type)
	fmt.Println(proxy.Proxy)
	fmt.Println(proxy.IP)
	fmt.Println(proxy.Headers)

	proxy, err = proxypool.DefaultProxy().GetProxy()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(proxy.Type)
	fmt.Println(proxy.Proxy)
	fmt.Println(proxy.IP)
	fmt.Println(proxy.Headers)
}
