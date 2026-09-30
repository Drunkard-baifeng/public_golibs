package solana

import "sync"

var (
	defaultSolanaMu  sync.Mutex
	defaultSolana    *SolanaWallet
	defaultRPCURL    string
	defaultInitCount int64
)

// InitDefaultSolana initializes or rebuilds the default Solana wallet.
func InitDefaultSolana(rpcURL string) *SolanaWallet {
	defaultSolanaMu.Lock()
	defer defaultSolanaMu.Unlock()

	if defaultSolana != nil {
		_ = defaultSolana.Close()
	}
	defaultRPCURL = rpcURL
	defaultSolana = NewSolanaWallet(rpcURL)
	defaultInitCount++
	return defaultSolana
}

func DefaultSolana() *SolanaWallet {
	defaultSolanaMu.Lock()
	defer defaultSolanaMu.Unlock()

	if defaultSolana == nil {
		defaultSolana = NewSolanaWallet(defaultRPCURL)
	}
	return defaultSolana
}

func CloseDefaultSolana() error {
	defaultSolanaMu.Lock()
	defer defaultSolanaMu.Unlock()

	if defaultSolana == nil {
		return nil
	}
	err := defaultSolana.Close()
	defaultSolana = nil
	return err
}

func DefaultInitCount() int64 {
	defaultSolanaMu.Lock()
	defer defaultSolanaMu.Unlock()
	return defaultInitCount
}

func GenerateWalletDefault(index int) (bool, *Wallet, string) {
	return DefaultSolana().GenerateWallet(index)
}

func RecoverWalletFromMnemonicDefault(mnemonic string, index int) (bool, *Wallet, string) {
	return DefaultSolana().RecoverWalletFromMnemonic(mnemonic, index)
}

func RecoverWalletFromPrivateKeyDefault(privateKey string) (bool, *Wallet, string) {
	return DefaultSolana().RecoverWalletFromPrivateKey(privateKey)
}

func SignMessageDefault(privateKey string, message string) (bool, *SignMessage, string) {
	return DefaultSolana().SignMessage(privateKey, message)
}

func VerifySignatureDefault(message string, signature string, address string) (bool, string) {
	return DefaultSolana().VerifySignature(message, signature, address)
}

func EstimateGasFeeDefault(fromAddress string, toAddress string, amountSOL string, feeBuffer float64) (bool, *GasEstimate, string) {
	return DefaultSolana().EstimateGasFee(fromAddress, toAddress, amountSOL, feeBuffer)
}

func GetBalanceDefault(address string) (bool, string, string) {
	return DefaultSolana().GetBalance(address)
}

func TransferCoinDefault(privateKey string, toAddress string, amountSOL string, options *TransferCoinOptions) (bool, string, string) {
	return DefaultSolana().TransferCoin(privateKey, toAddress, amountSOL, options)
}

func TransferTokenDefault(privateKey string, toAddress string, amount string, mintAddress string, options *TransferTokenOptions) (bool, string, string) {
	return DefaultSolana().TransferToken(privateKey, toAddress, amount, mintAddress, options)
}

func GetTokenBalanceDefault(address string, mintAddress string) (bool, string, string) {
	return DefaultSolana().GetTokenBalance(address, mintAddress)
}

func WaitForTransactionReceiptDefault(signature string, timeoutSeconds int) (bool, map[string]any, string) {
	return DefaultSolana().WaitForTransactionReceipt(signature, timeoutSeconds)
}
