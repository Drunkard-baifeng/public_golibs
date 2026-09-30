package tron

import "sync"

var (
	defaultTronMu        sync.Mutex
	defaultTron          *TronWallet
	defaultNetwork       = "mainnet"
	defaultAPIKey        string
	defaultGRPCURL       string
	defaultHTTPURL       string
	defaultTronInitCount int64
)

func InitDefaultTron(network string, apiKey string) *TronWallet {
	defaultTronMu.Lock()
	defer defaultTronMu.Unlock()

	if defaultTron != nil {
		_ = defaultTron.Close()
	}
	defaultNetwork = network
	defaultAPIKey = apiKey
	defaultGRPCURL = ""
	defaultHTTPURL = ""
	defaultTron = NewTronWallet(network, apiKey)
	defaultTronInitCount++
	return defaultTron
}

func InitDefaultTronWithEndpoints(network, grpcURL, httpURL, apiKey string) *TronWallet {
	defaultTronMu.Lock()
	defer defaultTronMu.Unlock()

	if defaultTron != nil {
		_ = defaultTron.Close()
	}
	defaultNetwork = network
	defaultGRPCURL = grpcURL
	defaultHTTPURL = httpURL
	defaultAPIKey = apiKey
	defaultTron = NewTronWalletWithEndpoints(network, grpcURL, httpURL, apiKey)
	defaultTronInitCount++
	return defaultTron
}

func DefaultTron() *TronWallet {
	defaultTronMu.Lock()
	defer defaultTronMu.Unlock()

	if defaultTron == nil {
		if defaultGRPCURL != "" || defaultHTTPURL != "" {
			defaultTron = NewTronWalletWithEndpoints(defaultNetwork, defaultGRPCURL, defaultHTTPURL, defaultAPIKey)
		} else {
			defaultTron = NewTronWallet(defaultNetwork, defaultAPIKey)
		}
	}
	return defaultTron
}

func CloseDefaultTron() error {
	defaultTronMu.Lock()
	defer defaultTronMu.Unlock()

	if defaultTron == nil {
		return nil
	}
	err := defaultTron.Close()
	defaultTron = nil
	return err
}

func DefaultInitCount() int64 {
	defaultTronMu.Lock()
	defer defaultTronMu.Unlock()
	return defaultTronInitCount
}

func GenerateWalletDefault(index int) (bool, *Wallet, string) {
	return DefaultTron().GenerateWallet(index)
}

func RecoverWalletFromMnemonicDefault(mnemonic string, index int) (bool, *Wallet, string) {
	return DefaultTron().RecoverWalletFromMnemonic(mnemonic, index)
}

func RecoverWalletFromPrivateKeyDefault(privateKey string) (bool, *Wallet, string) {
	return DefaultTron().RecoverWalletFromPrivateKey(privateKey)
}

func SignMessageDefault(privateKey string, message string) (bool, *SignMessage, string) {
	return DefaultTron().SignMessage(privateKey, message)
}

func VerifySignatureDefault(message string, signature string, accountAddress string) (bool, string) {
	return DefaultTron().VerifySignature(message, signature, accountAddress)
}

func EstimateGasFeeDefault(fromAddress string, toAddress string, amount string, contractAddress string, feeBuffer float64) (bool, *GasEstimate, string) {
	return DefaultTron().EstimateGasFee(fromAddress, toAddress, amount, contractAddress, feeBuffer)
}

func GetBalanceDefault(accountAddress string) (bool, string, string) {
	return DefaultTron().GetBalance(accountAddress)
}

func GetAccountInfoDefault(accountAddress string) (bool, *AccountInfo, string) {
	return DefaultTron().GetAccountInfo(accountAddress)
}

func GetTokenInfoDefault(contractAddress string) (bool, *TokenInfo, string) {
	return DefaultTron().GetTokenInfo(contractAddress)
}

func TransferCoinDefault(privateKey string, toAddress string, amountTRX string, options *TransferCoinOptions) (bool, string, string) {
	return DefaultTron().TransferCoin(privateKey, toAddress, amountTRX, options)
}

func TransferTokenDefault(privateKey string, toAddress string, amount string, contractAddress string, options *TransferTokenOptions) (bool, string, string) {
	return DefaultTron().TransferToken(privateKey, toAddress, amount, contractAddress, options)
}

func GetTokenBalanceDefault(accountAddress string, contractAddress string) (bool, string, string) {
	return DefaultTron().GetTokenBalance(accountAddress, contractAddress)
}

func GetTransactionInfoDefault(txID string) (bool, map[string]any, string) {
	return DefaultTron().GetTransactionInfo(txID)
}

func GetTRXTransfersDefault(accountAddress string, limit int, onlyConfirmed bool) (bool, []TransferRecord, string) {
	return DefaultTron().GetTRXTransfers(accountAddress, limit, onlyConfirmed)
}

func GetTokenTransfersDefault(accountAddress string, contractAddress string, limit int, onlyConfirmed bool) (bool, []TransferRecord, string) {
	return DefaultTron().GetTokenTransfers(accountAddress, contractAddress, limit, onlyConfirmed)
}

func WaitForTransactionReceiptDefault(txID string, timeoutSeconds int) (bool, map[string]any, string) {
	return DefaultTron().WaitForTransactionReceipt(txID, timeoutSeconds)
}
