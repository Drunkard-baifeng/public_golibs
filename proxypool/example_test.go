package proxypool_test

import (
	"fmt"
	"log"

	"github.com/Drunkard-baifeng/public_golibs/proxypool"
)

func Example_basic() {
	// 创建代理池
	pool := proxypool.New(proxypool.Config{
		APIURL:        "http://your-proxy-api.com/get",
		MaxUseCount:   5,                         // 每个代理最多用5次
		ExpireSeconds: 180,                       // 180秒后过期
		MinPoolSize:   3,                         // 低于3个时自动刷新
		FetchFunc:     proxypool.SimpleFetchFunc, // 使用简单获取函数
	})

	// 手动添加代理
	pool.AddProxy("192.168.1.1", "8080")
	pool.AddProxy("192.168.1.2", "8080")

	// 获取代理
	proxy, err := pool.Get()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("代理地址:", proxy.String())
	fmt.Println("代理URL:", proxy.URL())

	// 获取统计信息
	stats := pool.GetStats()
	fmt.Printf("统计: 总数=%d, 可用=%d\n", stats.Total, stats.Available)
}

func Example_singleton() {
	// 初始化默认代理池（单例）
	proxypool.InitDefault(proxypool.Config{
		APIURL:        "http://your-proxy-api.com/get",
		MaxUseCount:   5,
		ExpireSeconds: 180,
		FetchFunc:     proxypool.SimpleFetchFunc,
	})

	// 在任何地方使用默认代理池
	pool := proxypool.Default()
	proxy, _ := pool.Get()
	fmt.Println(proxy)
}

func Example_withCallback() {
	pool := proxypool.New(proxypool.Config{
		APIURL:    "http://your-proxy-api.com/get",
		FetchFunc: proxypool.SimpleFetchFunc,
		OnProxyGet: func(proxy *proxypool.ProxyItem) {
			log.Printf("使用代理: %s (已用%d次)\n", proxy.String(), proxy.GetUsedCount())
		},
		OnRefresh: func(count int, err error) {
			if err != nil {
				log.Printf("刷新失败: %v\n", err)
			} else {
				log.Printf("刷新成功，新增 %d 个代理\n", count)
			}
		},
	})

	pool.Refresh()
	pool.Get()
}

func Example_withHttpClient() {
	// 使用自定义 HTTP 客户端（例如带代理的）
	// import "github.com/yourusername/httpclient"
	//
	// client := httpclient.New().SetTimeout(10 * time.Second)
	//
	// pool := proxypool.New(proxypool.Config{
	//     APIURL: "http://your-proxy-api.com/get",
	//     FetchFunc: func(apiURL string) ([]proxypool.ProxyAddr, error) {
	//         resp, err := client.Get(apiURL, nil)
	//         if err != nil {
	//             return nil, err
	//         }
	//         return proxypool.ExtractIPPort(resp.Text()), nil
	//     },
	// })
}

func Example_chainConfig() {
	// 链式配置
	pool := proxypool.New(proxypool.Config{}).
		SetAPIURL("http://your-proxy-api.com/get").
		SetMaxUseCount(10).
		SetExpireSeconds(300).
		SetMinPoolSize(5).
		SetFetchFunc(proxypool.SimpleFetchFunc)

	pool.Refresh()
}

func Example_manualProxy() {
	pool := proxypool.New(proxypool.Config{
		MaxUseCount:   10,
		ExpireSeconds: 600,
	})

	// 手动批量添加代理
	proxies := []struct {
		IP   string
		Port string
	}{
		{"192.168.1.1", "8080"},
		{"192.168.1.2", "8080"},
		{"192.168.1.3", "8080"},
	}

	for _, p := range proxies {
		pool.AddProxy(p.IP, p.Port)
	}

	fmt.Println("代理池大小:", pool.Size())
}

func Example_proxyItem() {
	// 直接使用 ProxyItem
	proxy := proxypool.NewProxyItemWithConfig("192.168.1.1", "8080", 5, 180)

	fmt.Println("可用:", proxy.IsAvailable())
	fmt.Println("剩余次数:", proxy.GetRemainingCount())
	fmt.Println("剩余时间:", proxy.GetRemainingTime())

	// 使用代理
	if proxy.IncrementUseCount() {
		fmt.Println("使用成功，已用次数:", proxy.GetUsedCount())
	}
}
