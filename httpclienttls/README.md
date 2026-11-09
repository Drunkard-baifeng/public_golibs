# httpclienttls

带浏览器 TLS/HTTP/2 profile 的 Go HTTP 客户端。每个 Client 创建时选择一次 profile，后续请求复用该身份和连接池。

```sh
go get github.com/Drunkard-baifeng/public_golibs/httpclienttls@v1.2.0
```

要求 Go 1.24.1 或更高版本。

## 浏览器身份配置（工作树新增，尚未发布）

`Profile` 选择 TLS/HTTP2 传输预设；`BrowserIdentity` 配置与其配套的 HTTP 身份。
两层分别配置：选择 macOS 会同步 UA 与平台 Client Hints，但不会更换底层 TLS 预设，
也不表示完整浏览器运行时或逐字节浏览器指纹已经得到验证。

```go
client, err := httpclienttls.NewClient(httpclienttls.Config{
    Profile: "chrome_146",
    BrowserIdentity: &httpclienttls.BrowserIdentity{
        Platform: "macOS",
    },
    AcceptLanguage: "ja-JP,ja;q=0.9,en-US;q=0.8,en;q=0.7",
    StrictFingerprint: true,
})
if err != nil {
    return err
}
defer client.Close()
```

- 现有桌面 Chrome/Brave/Firefox 预设支持 `Windows`、`macOS`、`Linux` 身份，平台名称不区分大小写。桌面 Safari 保持 macOS，iOS Safari 保持 iOS；不兼容的组合在构造时返回错误。
- 不配置这些新字段时，既有 UA、Client Hints、默认 profile 池和请求头覆盖行为保持不变。
- `BrowserIdentity` 的内容在构造时复制；`Profile().Identity` 返回生效信息的值副本。
- 这里只使用当前已注册的真实上游预设。新增历史桌面 `safari_15_6_1`、`safari_16_0`，映射固定依赖中的同名传输预设，需显式选择，不进入默认随机池。它们不代表新版 Safari；Edge 和 Android 浏览器预设仍未增加。`AvailableProfiles()` 查看当前列表。

### 高熵 Client Hints

`HighEntropyClientHints: true` 是显式发送开关，默认关闭，仅适用于 Chrome/Brave。
开启前必须在 `BrowserIdentity` 填写以下实际值，不会读取本机信息或猜测版本：

| 字段 | 约束 |
| --- | --- |
| `PlatformVersion` | 点分数字系统版本 |
| `Architecture` | `x86` 或 `arm` |
| `Bitness` | 当前桌面画像支持 `64` |
| `FullVersion` | 四段数字浏览器版本，主版本必须与所选 profile 一致 |

开启后生成 `Sec-CH-UA-Full-Version-List`、`Sec-CH-UA-Platform-Version`、`Sec-CH-UA-Arch`、
`Sec-CH-UA-Bitness`、`Sec-CH-UA-Model`、`Sec-CH-UA-Full-Version`、`Sec-CH-UA-WoW64`、
`Sec-CH-UA-Form-Factors`。当前支持 64 位桌面 Chromium，Model 为空字符串、WoW64 为 `?0`、
Form-Factors 为 `"Desktop"`；品牌顺序沿用 profile，
完整版本不会替换 Chromium 的缩减 UA。Firefox/Safari 不自动生成 Chromium Client Hints。

该开关是主动发送模式；需要按响应协商时，使用下节的 `ClientHints`，两种模式互斥。
自动字段仅在 HTTPS 或可信本地 HTTP 地址发送，并随重定向更新；显式空值仍抑制默认值。
`BrowserModeOff` 或 `BrowserHeaders: BoolPtr(false)` 关闭自动生成。
默认请求头顺序包含新增字段；显式 `HeaderOrder`（包括空切片）优先。

### 按响应协商 Client Hints（工作树新增，尚未发布）

`ClientHints: &httpclienttls.ClientHintsConfig{}` 开启 `Accept-CH` 协商，默认关闭。
它与主动发送模式一样要求完整 Chromium `BrowserIdentity`，但第一次请求不主动携带高熵字段。
以下为函数内配置片段：版本、架构变量由调用方提供，浏览器完整版本的主版本需匹配 `chrome_146`。

```go
client, err := httpclienttls.NewClient(httpclienttls.Config{
    Profile: "chrome_146",
    BrowserIdentity: &httpclienttls.BrowserIdentity{
        Platform:        "macOS",
        PlatformVersion: platformVersion,
        Architecture:    architecture,
        Bitness:         "64",
        FullVersion:     fullBrowserVersion,
    },
    ClientHints: &httpclienttls.ClientHintsConfig{
        CacheTTL:   30 * time.Minute,
        MaxOrigins: 256,
    },
    StrictFingerprint: true,
})
if err != nil {
    return err
}
defer client.Close()
```

- **范围**：仅处理 HTTPS 响应的 `Accept-CH`，包括回环地址也要求 HTTPS。旧有低熵/主动发送模式对可信本地 HTTP 的行为保持不变。
- **缓存**：按精确 origin（协议、主机、端口）隔离；合法 `Accept-CH` 替换前次偏好，空字段或仅含未知 hint 的合法列表清空偏好。无效结构化字段保留前次偏好。TTL 默认 30 分钟，容量默认 256，超额淘汰最早观察的条目；两个限制均支持配置，负值报错。
- **检查与清除**：`AcceptedClientHints(url)` 返回未过期字段名副本；`ClearClientHints()` 清空偏好；`ClearCookies()` 同时清空 Cookie 和协商偏好。在途响应只更新旧快照，不重新填充已清空的存储。
- **有限重试**：成功/重定向响应（200–399）的 `Accept-CH` 与 `Critical-CH` 同时请求尚未发送且实际可发送的字段时，对当前 URL 的无请求体 GET 最多重试一次，每个 `Do`/`DoStream` 共用一次额度。POST、PUT、HEAD、带 body 的 GET、4xx/5xx 均不自动重试；`DisableCriticalRetry: true` 可关闭。
- **资源生命周期**：重试前关闭原响应体，不等其流结束；保留响应 Cookie，复用业务总时限和父 context，每次交换重新计 `HeaderTimeout`。这不是浏览器完整导航重启算法，不回放此前的重定向链。
- **显式值优先**：用户设置的请求头及空值抑制优先，严格模式仍验证非空身份值。生成字段随重定向重新检查 origin；`BrowserModeOff` / 关闭 `BrowserHeaders` 时不学习、不重试、不自动发送。出口地区探测不参与 hint 协商。

#### 文档上下文与跨源委派

子资源请求使用文档自己的 `Accept-CH` 集合，再按 `Permissions-Policy` 过滤目标；它不依赖 CDN 是否先被访问过。
使用文档最终响应的 URL 和 Headers 创建不可变快照：

```go
document, err := client.Get(documentURL, &httpclienttls.Options{
    BrowserMode: httpclienttls.BrowserModeNavigation,
})
if err != nil {
    return err
}
hintContext, err := httpclienttls.NewClientHintContext(document.URL, document.Headers)
if err != nil {
    return err
}
resource, err := client.Get(resourceURL, &httpclienttls.Options{
    BrowserMode:       httpclienttls.BrowserModeFetch,
    ClientHintContext: hintContext,
    Headers:          map[string]string{"Referer": document.URL},
})
_ = resource
return err
```

- 支持受管理 hint 的 `self`、`*`、精确 HTTPS origin、`"https://*.example.com"` 子域通配和 `()` 禁止；字符串 origin 需写在列表内，例如 `ch-ua-arch=(self "https://cdn.example.com")`。端口纳入匹配，通配子域不含根域。
- 未声明策略时，高熵字段默认仅限同源；三个低熵 UA 字段使用其默认 allowlist。显式手工请求头优先于自动策略过滤。
- 快照复制本次文档响应的接受集合和策略；缺少 `Accept-CH` 时高熵集合为空，不继承此前文档/目标缓存。修改原 Header 不影响快照。使用该上下文的子资源响应不修改导航偏好、不触发 Critical-CH 重试。
- 上下文只影响生成的 Client Hints，不自动填写 Origin/Referer，不实现 DOM、iframe 继承、HTML meta、策略报告、Clear-Site-Data 或 JS 环境。`ClearClientHints`/`ClearCookies` 不修改调用方保存的不可变文档快照；切换会话应同时丢弃旧快照。
- 字段解析使用 `github.com/dunglas/httpsfv v1.1.2`，hint 列表和策略分别限制为 8 KiB；非法上下文在发送前报错。

### 稳定选择与一致性校验

- `FingerprintSeed`：固定种子与相同的候选集合可重建相同的 profile；别名、重复项及池顺序不影响结果。显式 `Profile` 优先。它只影响 profile 选择，不固定 TLS 随机数、密钥、扩展洗牌、Cookie 或设备 ID。
- 默认池会先过滤与 `BrowserIdentity` / 高熵设置不兼容的条目，再进行选择。显式 `ProfilePool` 则逐项校验，任一不兼容项直接报错，避免构造随机失败。
- 种子映射使用 SHA-256(`"httpclienttls/profile/v1\x00" + seed`) 的大端整数对排序后的候选数取模；候选集合变化可能改变结果。需要跨版本稳定时，显式固定 `ProfilePool` 或保存最终 `Profile().Name`。
- `StrictFingerprint: true`：对 UA 和本节管理的 11 个 `Sec-CH-UA*` 字段（3 个低熵、8 个高熵）做精确匹配检查，涵盖客户端默认头和请求级覆盖。高熵值需开启主动发送或协商模式；其余未建模的非空 `Sec-CH-UA-*` 也返回错误。冲突或多值在网络发送前返回错误；缺省/显式空值仍允许。构造时自定义 `UserAgent` 也必须匹配生成的身份。它不是线上指纹检测器，也不校验非 UA 类扩展请求头。
- 严格模式默认关闭，保留旧版任意请求头覆盖能力；更换 UA 本身不会联动更换 TLS 预设。

语言可以通过 `AcceptLanguage` 手动设置，也可启用下节的出口地区联动。屏幕、WebGL、JS navigator 和运行时执行仍由调用方的独立环境层负责。

### 出口 IP 自动地区适配（工作树新增，尚未发布）

`ExitLocale: &httpclienttls.ExitLocaleConfig{}` 开启自动探测；`nil`（默认）关闭，保持既有行为。
构造客户端不联网。开启后，首次业务请求先沿当前传输/代理查询出口地区，再自动填充 `Accept-Language`；
`Locale()` 返回最近一次结果，包括 IP、国家代码、城市、IANA 时区、语言、检测/过期时间及 `LastError`。
这不会更换 TLS profile、操作系统或 UA，也不会模拟 JS 时区。

```go
client, err := httpclienttls.NewClient(httpclienttls.Config{
    Profile: "chrome_146",
    // Proxy: "socks5://127.0.0.1:1080", // 已有代理配置；探测沿同一路线
    ExitLocale: &httpclienttls.ExitLocaleConfig{
        Timeout:    6 * time.Second,
        CacheTTL:   15 * time.Minute,
        FailureTTL: 30 * time.Second,
    },
})
if err != nil {
    return err
}
defer client.Close()

// 可选预热：避免首次短超时业务请求同时承担地区查询的等待。
info, lookupErr := client.RefreshLocale(context.Background())
if lookupErr != nil {
    // 记录诊断；普通请求仍沿原路线使用手动或 profile 默认语言。
    fmt.Println(lookupErr)
}
fmt.Println(info.Country, info.Timezone, info.AcceptLanguage)
```

- **优先级**：请求级 `HeaderValues` / `Headers` → 客户端自定义请求头 → `Config.AcceptLanguage` → 出口地区偏好 → profile 默认语言。显式空请求头仍表示抑制。`BrowserHeaders: BoolPtr(false)` / `BrowserModeOff` 不会因地区适配而重新开启自动请求头，但仍可查询地区元数据。
- **查询源**：默认顺序为 `https://ipwho.is/`、`https://ipapi.co/json/`。支持这两类 JSON 的国家代码与字符串/对象时区格式，也兼容含 `ip` / `country` / `timezone` 的简单自有接口。通过 `Endpoints` 替换；要求 HTTPS，或可信本地 HTTP，排除 URL 用户名密码和 fragment；非 nil 空列表视为配置错误。
- **隐私与路线**：服务商会看到探测连接的出口 IP。探测仅发送配置的 UA 和基础 JSON/identity Accept 头，不携带业务 Authorization、自定义头或 Cookie，也不收集响应 Cookie。查询失败不会切换直连；查询接口重定向不跟随。可使用自有接口避免向默认服务商查询。
- **预算与回退**：`Timeout` 是所有查询源共用的总预算；每个源分得剩余预算的一部分，失败后尝试下一个。响应上限 64 KiB。无效 JSON、地区/时区、失败状态或查询超时，回退至手动/profile 默认语言，原因见 `Locale().LastError`，不写入 `ConfigError()`。父 context / 业务总超时若已结束，则整个业务请求结束，不继续发送。
- **缓存与并发**：成功默认缓存 15 分钟，失败默认退避 30 秒；同一客户端/传输的并发请求合并查询，等待者可单独取消。过期后下次请求重查；`RefreshLocale(ctx)` 可主动刷新并返回查询错误。缓存不跨客户端共享，调用方取消不缓存为服务商失败。
- **代理变更**：重新配置代理或重建传输时清空对应地区缓存，旧查询结果不覆盖新路线；已开始的请求继续使用原先的传输/地区快照。对每连接轮换或按目的地选择出口的代理，探测出口与业务出口仍可能不同；需要会话固定出口，或按需缩短 TTL / 主动刷新。
- **元数据范围**：内置常见国家的语言偏好，未知映射使用 `en-US`；它不是用户真实语言或设备类型的证明。`Locale()` / `Profile().AcceptLanguage` 反映客户端默认语言，不包含请求级覆盖。时区只返回有效 IANA 名称，不猜测缺失时区、不缓存固定夏令时偏移；按使用日期通过 `time.LoadLocation(info.Timezone)` 计算，并先检查时区非空。内置 Go tzdata 以支持 Windows 等环境。

查询源结构参考其官方文档：`https://ipwhois.io/documentation`、`https://ipapi.co/api/`。
外部服务可用性、配额及出口定位准确度由服务商决定；回归测试使用本地 HTTP/1.1、HTTP/2 和代理夹具，不调用公网查询服务。

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
- `HeaderTimeout`：每次请求交换的时限，包含建连、上传和等待响应头；每次重定向或 Critical-CH 重试重新计时。
- `ReadIdleTimeout`：限制一次阻塞的 `Body.Read`，不计算调用方处理数据的暂停时间。它不是每个底层网络包的空闲计时器。
- 超时错误可用 `errors.Is(err, httpclienttls.ErrHeaderTimeout)`、`errors.Is(err, httpclienttls.ErrReadIdleTimeout)` 判断，也匹配 `context.DeadlineExceeded`。
- `MaxResponseBodyBytes` 只约束内存缓冲 API。流式响应需要调用方设定上限；`CopyTo` 最多写入指定字节数，超限时返回错误。写文件时建议使用临时文件，成功后再重命名。

## 上传与重定向

- `DoStream`/`Do` 接受 `io.Reader` 时直接上传，不预读整个请求体。传入 `io.ReadCloser` 后由客户端负责关闭，包括请求准备失败的情况。
- 阻塞式流上传应提供支持并发 `Read`/`Close` 的 `io.ReadCloser`，且 `Close` 应解除阻塞的 `Read`（例如 `io.PipeReader`）。超时、context 取消或关闭响应体会关闭仍在进行的上传；普通 `io.Reader` 的自定义阻塞逻辑仍由调用方负责结束。
- `Options.ContentLength` 可提供已知的 reader 上传长度；`nil` 自动判断，`-1` 表示未知。存在请求体时 `0` 按未知长度处理。
- 正数 `ContentLength` 必须匹配实际上传字节数。已知长度不符时在发送前报错；流式读取时检测短读或超长，HTTP/1.1 和 HTTP/2 均适用。
- 字符串、字节切片和底层支持 `GetBody` 的 reader 可在 307/308 后重放。普通单次 reader 返回原始 307/308 响应，由调用方决定下一步。
- 301/302/303 跟随后丢弃请求体，方法为 GET 或 HEAD。
- `PostMultipartStream` 从磁盘流式上传；`PostMultipartContext` 使用同一上传逻辑，再缓冲响应。与旧版缓冲 multipart 的方式相比，流式 multipart 不自动重放 307/308，未知大小上传通常使用分块传输。
- JSON、表单和 multipart 帮助方法会设置与编码一致的 `Content-Type`。需要完整自定义请求头时可使用 `Do`/`DoStream`。
- multipart 字段名和文件名保留 UTF-8；字段名、文件名和 part `Content-Type` 中的 CR/LF 会在上传前被拒绝。

### HTTP/2 提前响应与阻塞上传

已修复 `fhttp v0.6.8` 在请求体 reader 阻塞、服务端提前返回 3xx/4xx/5xx 时等待上传结束的问题。客户端先终止并关闭上传体，再返回响应头；无需依靠超时触发这一步。关闭引起的读取错误不会重置正在接收的响应流，响应体仍可流式读取，连接仍可复用。

未知长度上传结束发送侧；尚未发送完整的定长上传会在响应结束或关闭后清理，避免发送与 `Content-Length` 不符的结束帧。1xx/2xx 保留全双工上传能力。上传 reader 仍需满足上述并发关闭契约；普通阻塞 `io.Reader` 的结束由调用方负责。

成功的全双工响应在 EOF、主动关闭、连接断开或取消时，也会释放尚未完成的上传。上传协程先结束但尚未发出结束帧时，同样执行剩余流清理。

修复随模块内的传输层源码分发，无需调用方设置 `replace` 或修改模块缓存。所选 TLS profile、HTTP/2 SETTINGS/优先级/头顺序继续沿用原实现；来源、补丁范围和升级注意事项见 [internal/README.md](internal/README.md)。原先跳过的复现已纳入默认回归测试：

```powershell
go test -race -run 'TestHTTP2|TestEarly|TestRedirectClosesDiscardedUploadBeforeFinalResponse|TestPatchedTransportPreservesHTTP2Wire' ./...
```

## 可选 HTTP/3 协议竞速

`EnableHTTP3` 与 `EnableProtocolRacing` 同时开启且未配置代理时，仅无请求体的 GET/HEAD 参与竞速。POST/PUT/DELETE、携带请求体的 GET 及其他请求使用单个传输，避免重复操作或并发读取同一个上传体。首次竞速可能在两个协议上发送同一 GET/HEAD；这不是只竞速握手。

竞速遵守调用方取消，并为候选请求分别持有 context；落败候选会取消并关闭响应。胜出响应保持可读，直到 EOF、Close 或调用方截止时间。协议选择阶段最多等待 10 秒，该时限不限制已返回响应的流式读取。已缓存协议发生请求错误时返回该错误，不自动重放本次请求；后续调用重新选择协议。

TLS 协商后尚未交给 HTTP 传输的连接，在原请求取消或关闭空闲连接时释放。

## 浏览器请求头

- `Options.UserActivated` 配合 `BrowserModeNavigation`：`nil` 保留既有 `Sec-Fetch-User: ?1` 行为；`BoolPtr(false)` 表示程序触发的导航，省略该头，重定向保持一致。显式请求头（含空值）优先，fetch/script/off 模式不因该选项而新增用户激活头。
- 显式设置为空的请求头用于抑制相应默认值；`HeaderOrder: []string{}` 用于关闭自动头顺序，两者都在重定向中保留。
- 自动生成的 Client Hints 和 Fetch Metadata 仅用于 HTTPS 或可信本地 HTTP 地址，并随重定向目标重新判断。显式提供的值保持调用方设置。
- `Sec-Fetch-Site` 根据初始 `Origin`/`Referer` 及完整重定向链生成；跨站跳转后回到原站仍标为 `cross-site`，无来源的直接导航保持 `none`。

## 代理与 Cookie

支持带显式端口的 HTTP、HTTPS、SOCKS5、SOCKS5H 代理地址。HTTP(S) 代理使用 CONNECT；SOCKS 代理由代理端解析目标域名。`ProxyHandshakeTimeout` 默认 30 秒，只限制隧道建立，建立后的流式传输使用请求自己的时限。

HTTPS 代理证书始终校验；`Config.Verify` 只控制目标站点证书校验。配置代理时关闭 HTTP/3 和协议竞速，以保持请求走 TCP 代理隧道。

无效代理配置会阻止请求；切换 `SetVerify` 保留该错误，直到调用方通过 `ConfigureProxy` 修正或通过 `ClearProxy` 明确选择直连。代理 URL 解析错误不包含认证信息。CONNECT 的临时响应与最终响应共用 64 KiB 头部预算和握手时限。

响应 Cookie 按域、路径等规则存入 Cookie jar，并启用公共后缀校验。跨源重定向移除显式的 Authorization/Cookie；Cookie jar 中的 Cookie 仍按其自身作用域规则发送。

`ClearCookies` 创建新的 Cookie 存储，并清空协商 Client Hints 缓存。此前已发出的请求继续使用自己的旧存储，其迟到响应不会回写新存储、hint 缓存或 `GetCookies` 快照。

### 按 URL 读取 Cookie

- `CookiesForURL(url)` 返回 jar 中适用于该 URL 的 `[]*http.Cookie` 副本，按域、路径、Secure 和过期规则筛选，保留同名但不同路径的 Cookie 及 jar 的发送顺序。修改返回值不影响客户端。
- `CookieHeader(url)` 把同一份作用域结果序列化为 HTTP `Cookie` 头，正确处理带空格等需要引号的值。它不复原服务器原始引号或原始字节排列。
- 两者只包含 jar 数据，不合并 `SetCookies` / `AddCookie` 无作用域覆盖、原始 `Cookie` 请求头或 `Options.Cookies`；目标变更后应重新查询。HttpOnly Cookie 也会返回，因此这是 HTTP 请求接口，不是 JS `document.cookie`。
- 与 `net/http.CookieJar` 一致，返回的是请求 Cookie 的名称/值，原始 Set-Cookie 的域、路径等属性不作为存储导出接口暴露。
- `GetCookies()` 保留兼容的名称映射语义：它汇总历史响应与显式覆盖，可能保留已过期/删除的历史记录，且不区分域和路径。需要请求可用状态时使用上述 URL 接口。
- `SetCookie`、`CookieValue` 与新接口统一要求合法 HTTP(S) 目标，不接受 URL 用户名密码和越界端口；错误信息不回显 URL 中的敏感内容。

## 可选响应冷却（工作树新增，尚未发布）

`Config.Cooldown` 默认 `nil`，保持既有行为。启用后默认在 429/503 响应后记录目标 origin 的冷却：

```go
client, err := httpclienttls.NewClient(httpclienttls.Config{
    Profile: "chrome_146",
    Cooldown: &httpclienttls.CooldownConfig{
        DefaultDuration: time.Minute,
        MaxDuration:     time.Hour,
        MaxOrigins:      256,
    },
})
if err != nil {
    return err
}
defer client.Close()
```

- **首次响应原样返回**，包含状态、响应头、Cookie、可读取的响应体；客户端不自动重试或睡眠。收到响应头即记录冷却，流式接口也生效。
- **后续请求本地返回 `*CooldownError`**，可用 `errors.Is(err, httpclienttls.ErrCooldown)` / `errors.As` 判断，并读取 `Origin`、`StatusCode`、`Until`。`client.Cooldown(url)` 返回值快照，零值表示未启用、无活动冷却或无效 URL。冷却错误不包含 URL 凭据、路径、查询参数。
- 支持 `Retry-After` 的整数秒和 HTTP 日期。缺失、无效或重复字段使用 `DefaultDuration`（默认 1 分钟）；合法零秒/过去日期不新增冷却；极大值截断到 `MaxDuration`（默认 1 小时），避免溢出。默认时长应不超过最大时长。
- `StatusCodes` 为 nil 时选择 429/503；非 nil 列表完整替换默认值，只接受 400–599，可显式配置 403 等状态。空列表或负配置报错。
- 按协议、主机、端口隔离，同域不同端口互不影响；客户端间不共享。`MaxOrigins` 默认 256，满额时淘汰最早到期的条目，因此超大目标集合中的活动冷却可能提前移出缓存。
- 已开始的请求继续执行；其迟到成功/较短等待响应不缩短已有冷却。`ClearCookies`、更改代理或重建传输不重置冷却。到期自动恢复。
- 业务请求在出口地区探测前和每次重定向/重试交换前检查冷却，被拦截的上传体会关闭；已取消的父 context 优先返回取消错误。地区查询自身的直接探测流量不更新这份业务冷却状态。

## 与 Python 协议客户端的能力对照

以下对照针对该项目 `BrowserSession` 的通用客户端部分，基于源码检查：

| 能力 | Python 协议客户端 | Go httpclienttls |
| --- | --- | --- |
| 传输 | curl_cffi / wreq 选择其一 | tls-client，固定的实际预设注册表 |
| UA/平台/基础 CH | 按后端预设同步 | BrowserIdentity + StrictFingerprint 校验 |
| 高熵 CH | 主动发送开关 | 主动发送或 Accept-CH 协商，两者互斥 |
| 出口地区 | 会话创建时查询并构建画像 | 可选懒加载、超时/TTL/刷新及代理路线隔离 |
| 用户触发导航 | user_initiated 参数 | Options.UserActivated，支持重定向保持 |
| Cookie 读取 | 业务域 Cookie 导出 | 通用目标 URL 作用域快照/序列化 |
| 失败冷却 | BrowserSession 收到 403/429 后按会话阻止请求 | 可选、按 origin 隔离，默认 429/503，状态可配置 |
| 设备与业务 ID、Node SDK | 注册业务层另外实现 | 属于上层业务/运行环境，未并入通用 HTTP 客户端 |

两种语言的默认行为并不完全相同；不要把 Python 业务画像里的字段存在，视为底层 HTTP 库具备浏览器运行时。

## 验证

```sh
go test -race ./...
go vet ./...
go mod verify
```

请求头构造及一次重定向更新的微基准（不包含网络 I/O）：

```sh
go test -run '^$' -bench '^BenchmarkBrowserHeaders$' -benchmem -count=3
```

静态检查需用兼容 Go 1.24 的工具构建环境；例如 PowerShell：

```powershell
$env:GOTOOLCHAIN = 'go1.24.1'
go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...
```

公网指纹探针默认跳过。PowerShell 手动启用：

```powershell
$env:HTTPCLIENTTLS_LIVE_TEST = '1'
go test -run '^TestLiveFingerprint$' -count=1 -v
Remove-Item Env:HTTPCLIENTTLS_LIVE_TEST
```

探针检查 UA、TLS 指纹和 HTTP/2 指纹是否正常返回；完整浏览器行为还涉及 JS、网络环境等层面，这些不属于 HTTP 客户端的实现范围。
