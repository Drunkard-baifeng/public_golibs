# web3utils

`web3utils` 按链类型拆分子包，避免后续 EVM、Solana、Bitcoin 的类型和依赖混在一起。

```text
web3utils/
├── evm/                 # Ethereum/EVM 兼容链
│   ├── wallet.go        # 钱包、签名、余额、转账和交易回执
│   └── singleton.go     # 默认 EVM 客户端及便捷方法
├── tron/                # TRON/TRC20
│   ├── wallet.go        # 钱包、签名、余额、TRX/TRC20 和交易回执
│   ├── history.go       # TronGrid TRX/TRC20 历史记录查询
│   ├── singleton.go     # 默认 TRON 客户端及便捷方法
│   └── wallet_test.go   # 离线钱包及签名单元测试
├── go.mod
└── go.sum
```

后续建议按相同层级新增：

```text
web3utils/
├── evm/
├── tron/
├── solana/
└── bitcoin/
```

新代码推荐直接使用具体链的子包：

```go
import "github.com/Drunkard-baifeng/public_golibs/web3utils/evm"

wallet := evm.NewEVMWallet("", 1)
defer wallet.Close()
```

TRON：

```go
import "github.com/Drunkard-baifeng/public_golibs/web3utils/tron"

wallet := tron.NewTronWallet("mainnet", "TRON-PRO-API-KEY")
defer wallet.Close()

ok, account, message := wallet.GenerateWallet(0)
```

> `gotron-sdk v0.26.0` 要求 Go 1.24 或更高版本。仓库目前仍保留
> `go 1.23.2`，因此需要升级 Go 后才能直接构建 `tron` 子包。
