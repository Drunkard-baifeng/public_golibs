# httpclienttls

带浏览器 TLS/HTTP/2 profile 的 Go HTTP 客户端。每个 Client 创建时选择一次 profile，后续请求复用该身份和连接池。

```sh
go get github.com/Drunkard-baifeng/public_golibs/httpclienttls@v1.1.0
```

要求 Go 1.24.1 或更高版本。

## 普通请求与流式请求

`Get`、`Post`、`Do` 等方法返回已读入内存的 `Response`，默认响应体上限为 16 MiB。`DoStream` 收到响应头后返回 `StreamResponse`，适用于 SSE 和大文件下载；所有路径都需要关闭 `Body`，包括拒绝响应状态码的路径。

```go
client, err := httpclienttls.NewClient(httpclienttls.Config{
    Profile: "chrome_146", // 留空则从内置 profile 中随机选择
    Timeout: 30 * time.Second,
})
if err != nil {
    return err
}
defer client.Close()

ctx, cancel := context.WithCancel(context.Background())
defer cancel()
response, err := client.DoStream(ctx, http.MethodGet, targetURL, nil, &httpclienttls.Options{
    DisableTimeout:  true,
    HeaderTimeout:   20 * time.Second,
    ReadIdleTimeout: 60 * time.Second,
})
if err != nil {
    return err
}
defer response.Body.Close()
if response.StatusCode != http.StatusOK {
    return fmt.Errorf("unexpected HTTP status: %s", response.Status)
}
_, err = response.CopyTo(destination, 64<<20)
return err
```

上例为函数内片段：`targetURL` 是请求地址，`destination` 是调用方提供的 `io.Writer`。SSE 使用方可以直接逐条读取 `Body`。

- `Timeout`：整个请求的总时限，包含响应体读取；客户端默认 30 秒。
- `DisableTimeout`：仅关闭客户端总时限，调用方 `context` 的取消/截止时间仍然有效。
- `HeaderTimeout`：每次请求交换的时限，包含建连、上传和等待响应头；每次重定向重新计时。
- `ReadIdleTimeout`：限制一次阻塞的 `Body.Read`，不计算调用方处理数据的暂停时间。它不是每个底层网络包的空闲计时器。
- 超时错误可用 `errors.Is(err, httpclienttls.ErrHeaderTimeout)`、`errors.Is(err, httpclienttls.ErrReadIdleTimeout)` 判断，也匹配 `context.DeadlineExceeded`。
- `MaxResponseBodyBytes` 只约束内存缓冲 API。流式响应需要调用方设定上限；`CopyTo` 最多写入指定字节数，超限时返回错误。写文件时建议使用临时文件，成功后再重命名。

## 上传与重定向

- `DoStream`/`Do` 接受 `io.Reader` 时直接上传，不预读整个请求体。传入 `io.ReadCloser` 后由客户端负责关闭，包括请求准备失败的情况。
- `Options.ContentLength` 可提供已知的 reader 上传长度；`nil` 自动判断，`-1` 表示未知。存在请求体时 `0` 按未知长度处理。
- 字符串、字节切片和底层支持 `GetBody` 的 reader 可在 307/308 后重放。普通单次 reader 返回原始 307/308 响应，由调用方决定下一步。
- 301/302/303 跟随后丢弃请求体，方法为 GET 或 HEAD。
- `PostMultipartStream` 从磁盘流式上传；`PostMultipartContext` 使用同一上传逻辑，再缓冲响应。与旧版缓冲 multipart 的方式相比，流式 multipart 不自动重放 307/308，未知大小上传通常使用分块传输。
- JSON、表单和 multipart 帮助方法会设置与编码一致的 `Content-Type`。需要完整自定义请求头时可使用 `Do`/`DoStream`。

## 代理与 Cookie

支持带显式端口的 HTTP、HTTPS、SOCKS5、SOCKS5H 代理地址。HTTP(S) 代理使用 CONNECT；SOCKS 代理由代理端解析目标域名。`ProxyHandshakeTimeout` 默认 30 秒，只限制隧道建立，建立后的流式传输使用请求自己的时限。

HTTPS 代理证书始终校验；`Config.Verify` 只控制目标站点证书校验。配置代理时关闭 HTTP/3 和协议竞速，以保持请求走 TCP 代理隧道。

响应 Cookie 按域、路径等规则存入 Cookie jar，并启用公共后缀校验。跨源重定向移除显式的 Authorization/Cookie；Cookie jar 中的 Cookie 仍按其自身作用域规则发送。

## 验证

```sh
go test -race ./...
go vet ./...
go mod verify
```

公网指纹探针默认跳过。PowerShell 手动启用：

```powershell
$env:HTTPCLIENTTLS_LIVE_TEST = '1'
go test -run '^TestLiveFingerprint$' -count=1 -v
Remove-Item Env:HTTPCLIENTTLS_LIVE_TEST
```

探针检查 UA、TLS 指纹和 HTTP/2 指纹是否正常返回；完整浏览器行为还涉及 JS、网络环境等层面，这些不属于 HTTP 客户端的实现范围。
