package httpclient_test

import (
	"fmt"
	"time"

	"github.com/Drunkard-baifeng/public_golibs/httpclient"
)

func Example_basic() {
	// 创建客户端
	client := httpclient.New()

	// GET请求
	resp, err := client.Get("https://httpbin.org/get", nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.StatusCode)
	fmt.Println(resp.Text())
}

func Example_withOptions() {
	client := httpclient.New()

	// 带参数的GET请求
	resp, err := client.Get("https://httpbin.org/get", &httpclient.Options{
		Params: map[string]string{
			"name": "test",
			"age":  "18",
		},
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
		},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text())
}

func Example_postJSON() {
	client := httpclient.New()

	// POST JSON
	resp, err := client.PostJSON("https://httpbin.org/post", map[string]interface{}{
		"name": "张三",
		"age":  18,
	}, nil)
	if err != nil {
		panic(err)
	}

	// 解析JSON响应
	var result map[string]interface{}
	resp.JSON(&result)
	fmt.Println(result)
}

func Example_postForm() {
	client := httpclient.New()

	// POST表单
	resp, err := client.PostForm("https://httpbin.org/post", map[string]string{
		"username": "admin",
		"password": "123456",
	}, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text())
}

func Example_uploadFile() {
	client := httpclient.New()

	// 上传文件
	resp, err := client.PostMultipart("https://httpbin.org/post",
		map[string]string{"field1": "value1"},
		[]httpclient.FileField{
			{FieldName: "file", FilePath: "test.txt"},
			{FieldName: "file2", FileName: "data.bin", Data: []byte("binary data")},
		}, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text())
}

func Example_withProxy() {
	// 使用HTTP代理
	client := httpclient.New().
		SetProxy("127.0.0.1:7890", "http").
		SetTimeout(30 * time.Second).
		SetVerify(false)

	resp, _ := client.Get("https://httpbin.org/ip", nil)
	fmt.Println(resp.Text())

	// 使用SOCKS5代理
	client2 := httpclient.New().
		SetProxy("127.0.0.1:1080", "socks5")

	resp, _ = client2.Get("https://httpbin.org/ip", nil)
	fmt.Println(resp.Text())
}

func Example_withConfig() {
	// 使用配置创建客户端
	client := httpclient.NewWithConfig(httpclient.Config{
		Timeout:      60 * time.Second,
		MaxRedirects: 10,
		Verify:       false,
		Proxy:        "127.0.0.1:7890",
		ProxyType:    "http",
	})

	resp, _ := client.Get("https://httpbin.org/get", nil)
	fmt.Println(resp.Text())
}

func Example_chainMethods() {
	// 链式调用
	client := httpclient.New().
		SetTimeout(30*time.Second).
		SetMaxRedirects(5).
		SetVerify(false).
		AddHeader("User-Agent", "MyClient/1.0").
		AddHeader("Accept", "application/json").
		AddCookie("session", "abc123")

	resp, _ := client.Get("https://httpbin.org/get", nil)
	fmt.Println(resp.Text())
}

func Example_sessionCookies() {
	client := httpclient.New()

	// 第一次请求，服务器设置Cookie
	client.Get("https://httpbin.org/cookies/set/session/abc123", nil)

	// 后续请求自动携带Cookie
	resp, _ := client.Get("https://httpbin.org/cookies", nil)
	fmt.Println(resp.Text())

	// 查看当前Cookie
	fmt.Println(client.GetCookies())
}

func Example_responseHelpers() {
	client := httpclient.New()
	resp, _ := client.Get("https://httpbin.org/get", nil)

	// 响应辅助方法
	fmt.Println(resp.IsSuccess())     // true/false
	fmt.Println(resp.StatusCode)      // 200
	fmt.Println(resp.ContentType())   // application/json
	fmt.Println(resp.ContentLength()) // 响应体长度
	fmt.Println(resp.GetHeader("Content-Type"))
	fmt.Println(resp.GetAllHeaders())
	fmt.Println(resp.GetAllCookies())
}
