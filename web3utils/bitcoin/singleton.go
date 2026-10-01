package bitcoin

import "sync"

var (
	defaultBTCMu     sync.Mutex
	defaultBTC       *BTCWallet
	defaultNetwork   = "bitcoin"
	defaultAPIURLSet []string
	defaultInitCount int64
)

func InitDefaultBTC(network string, apiURL ...string) *BTCWallet {
	defaultBTCMu.Lock()
	defer defaultBTCMu.Unlock()
	if defaultBTC != nil {
		_ = defaultBTC.Close()
	}
	defaultNetwork = network
	defaultAPIURLSet = append([]string(nil), apiURL...)
	defaultBTC = NewBTCWallet(network, apiURL...)
	defaultInitCount++
	return defaultBTC
}

func DefaultBTC() *BTCWallet {
	defaultBTCMu.Lock()
	defer defaultBTCMu.Unlock()
	if defaultBTC == nil {
		defaultBTC = NewBTCWallet(defaultNetwork, defaultAPIURLSet...)
	}
	return defaultBTC
}

func CloseDefaultBTC() error {
	defaultBTCMu.Lock()
	defer defaultBTCMu.Unlock()
	if defaultBTC == nil {
		return nil
	}
	err := defaultBTC.Close()
	defaultBTC = nil
	return err
}

func DefaultInitCount() int64 {
	defaultBTCMu.Lock()
	defer defaultBTCMu.Unlock()
	return defaultInitCount
}

func GenerateWalletDefault(index int) (bool, *Wallet, string) {
	return DefaultBTC().GenerateWallet(index)
}

func GenerateWalletWithAddressTypeDefault(index int, addressType string) (bool, *Wallet, string) {
	return DefaultBTC().GenerateWalletWithAddressType(index, addressType)
}

func RecoverWalletFromMnemonicDefault(mnemonic string, index int) (bool, *Wallet, string) {
	return DefaultBTC().RecoverWalletFromMnemonic(mnemonic, index)
}

func RecoverWalletFromMnemonicWithAddressTypeDefault(mnemonic string, index int, addressType string) (bool, *Wallet, string) {
	return DefaultBTC().RecoverWalletFromMnemonicWithAddressType(mnemonic, index, addressType)
}

func RecoverWalletFromPrivateKeyDefault(privateKey string) (bool, *Wallet, string) {
	return DefaultBTC().RecoverWalletFromPrivateKey(privateKey)
}

func RecoverWalletFromPrivateKeyWithAddressTypeDefault(privateKey string, addressType string) (bool, *Wallet, string) {
	return DefaultBTC().RecoverWalletFromPrivateKeyWithAddressType(privateKey, addressType)
}

func SignMessageDefault(privateKey string, message string) (bool, *SignMessage, string) {
	return DefaultBTC().SignMessage(privateKey, message)
}

func VerifySignatureDefault(message string, signature string, address string) (bool, string) {
	return DefaultBTC().VerifySignature(message, signature, address)
}

func ValidateAddressDefault(address string) (bool, string) {
	return DefaultBTC().ValidateAddress(address)
}

func GetBalanceDefault(address string) (bool, string, string) {
	return DefaultBTC().GetBalance(address)
}

func GetBalanceInfoDefault(address string) (bool, *BalanceInfo, string) {
	return DefaultBTC().GetBalanceInfo(address)
}

func EstimateFeeDefault(fromAddress string, toAddress string, amountBTC string, options *TransferCoinOptions) (bool, *FeeEstimate, string) {
	return DefaultBTC().EstimateFee(fromAddress, toAddress, amountBTC, options)
}

func TransferCoinDefault(privateKey string, toAddress string, amountBTC string, options *TransferCoinOptions) (bool, string, string) {
	return DefaultBTC().TransferCoin(privateKey, toAddress, amountBTC, options)
}

func TransferAllDefault(privateKey string, toAddress string, options *TransferCoinOptions) (bool, string, string) {
	return DefaultBTC().TransferAll(privateKey, toAddress, options)
}

func WaitForTransactionReceiptDefault(txHash string, timeoutSeconds int) (bool, map[string]any, string) {
	return DefaultBTC().WaitForTransactionReceipt(txHash, timeoutSeconds)
}
