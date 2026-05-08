package main

import (
	"fmt"
	"log"

	"github.com/Drunkard-baifeng/public_golibs/proxypool"
)

func ProxypoolCall() {

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
