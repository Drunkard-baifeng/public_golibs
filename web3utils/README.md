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
├── solana/              # Solana/SPL Token/Token-2022
│   ├── wallet.go        # 钱包、签名、余额、SOL/SPL 转账和交易确认
│   ├── singleton.go     # 默认 Solana 客户端及便捷方法
│   └── wallet_test.go   # 离线钱包、签名和金额转换测试
├── bitcoin/             # Bitcoin
│   ├── wallet.go        # BIP32/39/44/49/84/86、WIF、地址、签名和余额
│   ├── singleton.go     # 默认 Bitcoin 客户端及便捷方法
│   └── wallet_test.go   # 四种地址、恢复、签名和余额测试
├── go.mod
└── go.sum
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

Solana：

```go
import "github.com/Drunkard-baifeng/public_golibs/web3utils/solana"

wallet := solana.NewSolanaWallet("https://api.mainnet-beta.solana.com")
defer wallet.Close()

ok, account, message := wallet.GenerateWallet(0)
```

Bitcoin（默认产生 BIP84 Native SegWit 地址）：

```go
import "github.com/Drunkard-baifeng/public_golibs/web3utils/bitcoin"

wallet := bitcoin.NewBTCWallet("bitcoin")
defer wallet.Close()

ok, account, message := wallet.GenerateWallet(0)

// 指定 Legacy / P2SH-SegWit / Native SegWit / Taproot
ok, taproot, message := wallet.GenerateWalletWithAddressType(0, bitcoin.AddressTypeTaproot)
```

BTC 转账（UTXO 选择、手续费、找零、本地签名和广播）：

```go
opts := bitcoin.DefaultTransferCoinOptions()
opts.AddressType = bitcoin.AddressTypeSegWit
opts.FeeTargetBlocks = 3
opts.WaitForConfirmation = false

ok, txID, message := wallet.TransferCoin(wifPrivateKey, toAddress, "0.0001", opts)
```

`TransferCoin` 默认只使用已确认 UTXO，私钥仅在本地签名，不会发给
Esplora 服务。`AddressType` 必须与付款 UTXO 所在地址类型一致。广播前会
下载并校验被消费的原始交易，再使用 btcd 脚本引擎验证每个已签名输入。

转出付款地址的全部可花余额（手续费从转出金额中扣除）：

```go
opts := bitcoin.DefaultTransferCoinOptions()
opts.AddressType = bitcoin.AddressTypeTaproot
opts.WaitForConfirmation = false

ok, txID, message := wallet.TransferAll(wifPrivateKey, toAddress, opts)
```

`TransferAll` 不会生成找零输出，会使用全部可用 UTXO，动态计算实际虚拟大小后从金额中扣除手续费。

`bitcoin` 的钱包与地址底层使用 `btcsuite/btcd`、`btcutil`、
`hdkeychain`、`txscript` 和 `btcwallet/txauthor`，助记词复用 `go-bip39`。`GetBalance`
默认通过 Blockstream Esplora API 查询已确认余额；也可在
`NewBTCWallet(network, apiURL)` 中传入自建 Esplora 地址，或传空字符串仅使用离线功能。

`solana` 使用 `github.com/solana-foundation/solana-go/v2`，支持 SOL、SPL
Token 和普通 Token-2022 转账。Token-2022 的 confidential transfer、transfer
hook 等需要额外账户或零知识证明的扩展不包含在通用 `TransferToken` 中。

> 当前模块最低要求 Go 1.25。
