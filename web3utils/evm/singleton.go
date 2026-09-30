package evm

import "sync"

var (
	defaultEVMMu     sync.Mutex
	defaultEVM       *EVMWallet
	defaultRPCURL    string
	defaultChainID   int64
	defaultInitCount int64
)

// InitDefaultEVM 初始化/重建默认 EVM 钱包实例（单例）。
// 后续 DefaultEVM() 会返回这个实例。
func InitDefaultEVM(rpcURL string, chainID int64) *EVMWallet {
	defaultEVMMu.Lock()
	defer defaultEVMMu.Unlock()

	if defaultEVM != nil {
		_ = defaultEVM.Close()
	}
	defaultRPCURL = rpcURL
	defaultChainID = chainID
	defaultEVM = NewEVMWallet(rpcURL, chainID)
	defaultInitCount++
	return defaultEVM
}

// DefaultEVM 获取默认 EVM 钱包实例（单例）。
// 如果未初始化，则按最后一次配置创建；首次默认使用 ("", 0)。
func DefaultEVM() *EVMWallet {
	defaultEVMMu.Lock()
	defer defaultEVMMu.Unlock()

	if defaultEVM == nil {
		defaultEVM = NewEVMWallet(defaultRPCURL, defaultChainID)
	}
	return defaultEVM
}

// CloseDefaultEVM 关闭并清空默认 EVM 钱包实例。
func CloseDefaultEVM() error {
	defaultEVMMu.Lock()
	defer defaultEVMMu.Unlock()

	if defaultEVM == nil {
		return nil
	}
	err := defaultEVM.Close()
	defaultEVM = nil
	return err
}

// DefaultInitCount 返回默认实例被 InitDefaultEVM 重建的次数（用于调试/观测）。
func DefaultInitCount() int64 {
	defaultEVMMu.Lock()
	defer defaultEVMMu.Unlock()
	return defaultInitCount
}

// GenerateWalletDefault 使用默认实例生成钱包。
func GenerateWalletDefault(index int) (bool, *Wallet, string) {
	return DefaultEVM().GenerateWallet(index)
}

// RecoverWalletFromMnemonicDefault 使用默认实例通过助记词恢复钱包。
func RecoverWalletFromMnemonicDefault(mnemonic string, index int) (bool, *Wallet, string) {
	return DefaultEVM().RecoverWalletFromMnemonic(mnemonic, index)
}

// RecoverWalletFromPrivateKeyDefault 使用默认实例通过私钥恢复钱包。
func RecoverWalletFromPrivateKeyDefault(privateKey string) (bool, *Wallet, string) {
	return DefaultEVM().RecoverWalletFromPrivateKey(privateKey)
}

// SignMessageDefault 使用默认实例签名消息。
func SignMessageDefault(privateKey string, message string) (bool, *SignMessage, string) {
	return DefaultEVM().SignMessage(privateKey, message)
}

// VerifySignatureDefault 使用默认实例验签。
func VerifySignatureDefault(message string, signature string, address string) (bool, string) {
	return DefaultEVM().VerifySignature(message, signature, address)
}

// EstimateGasFeeDefault 使用默认实例估算 Gas。
func EstimateGasFeeDefault(fromAddress string, toAddress string, amountEther string, data string, gasPriceBuffer float64) (bool, *GasEstimate, string) {
	return DefaultEVM().EstimateGasFee(fromAddress, toAddress, amountEther, data, gasPriceBuffer)
}

// GetBalanceDefault 使用默认实例查询主币余额。
func GetBalanceDefault(address string) (bool, string, string) {
	return DefaultEVM().GetBalance(address)
}

// TransferCoinDefault 使用默认实例转主币或合约调用。
func TransferCoinDefault(privateKey string, toAddress string, amountEther string, options *TransferCoinOptions) (bool, string, string) {
	return DefaultEVM().TransferCoin(privateKey, toAddress, amountEther, options)
}

// TransferTokenDefault 使用默认实例转 ERC20 代币。
func TransferTokenDefault(privateKey string, toAddress string, amount string, contractAddress string, options *TransferTokenOptions) (bool, string, string) {
	return DefaultEVM().TransferToken(privateKey, toAddress, amount, contractAddress, options)
}

// GetTokenBalanceDefault 使用默认实例查询代币余额。
func GetTokenBalanceDefault(address string, contractAddress string, contractABIJSON string) (bool, string, string) {
	return DefaultEVM().GetTokenBalance(address, contractAddress, contractABIJSON)
}

// WaitForTransactionReceiptDefault 使用默认实例等待交易确认。
func WaitForTransactionReceiptDefault(txHash string, timeoutSeconds int) (bool, map[string]any, string) {
	return DefaultEVM().WaitForTransactionReceipt(txHash, timeoutSeconds)
}
