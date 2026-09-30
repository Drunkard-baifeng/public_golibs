// Package tron provides wallet, signing, balance, and transfer utilities for
// the TRON network and TRC20 tokens.
package tron

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/fbsobreira/go-bip39"
	"github.com/fbsobreira/gotron-sdk/pkg/address"
	tronclient "github.com/fbsobreira/gotron-sdk/pkg/client"
	"github.com/fbsobreira/gotron-sdk/pkg/client/transaction"
	"github.com/fbsobreira/gotron-sdk/pkg/keys"
	tronkeystore "github.com/fbsobreira/gotron-sdk/pkg/keystore"
	tronmnemonic "github.com/fbsobreira/gotron-sdk/pkg/mnemonic"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/core"
	"github.com/fbsobreira/gotron-sdk/pkg/signer"
)

const (
	SunPerTRX             int64 = 1_000_000
	defaultEnergyPriceSUN int64 = 420
	defaultFeeLimitSUN    int64 = 30_000_000
)

var networkConfigs = map[string]NetworkConfig{
	"mainnet": {
		Name:      "mainnet",
		GRPCURL:   "grpc.trongrid.io:50051",
		HTTPURL:   "https://api.trongrid.io",
		ChainName: "TRON Mainnet",
	},
	"nile": {
		Name:      "nile",
		GRPCURL:   "grpc.nile.trongrid.io:50051",
		HTTPURL:   "https://nile.trongrid.io",
		ChainName: "TRON Nile Testnet",
	},
	"shasta": {
		Name:      "shasta",
		GRPCURL:   "grpc.shasta.trongrid.io:50051",
		HTTPURL:   "https://api.shasta.trongrid.io",
		ChainName: "TRON Shasta Testnet",
	},
}

type NetworkConfig struct {
	Name      string
	GRPCURL   string
	HTTPURL   string
	ChainName string
}

type Wallet struct {
	Address    string
	PrivateKey string
	Mnemonic   string
	Index      int
}

type SignMessage struct {
	Signature  string
	SignatureR string
	SignatureS string
	SignatureV int
}

type GasEstimate struct {
	FeeTRX         string
	FeeLimitSUN    int64
	EnergyUsed     int64
	EnergyPriceSUN int64
	Bandwidth      int64
}

type AccountInfo struct {
	Address          string // TRON Base58Check 账户地址，通常以 T 开头
	Balance          string // TRX 余额，已按 1 TRX = 1,000,000 SUN 转换为十进制字符串
	BalanceSUN       int64  // TRX 原始余额，单位 SUN；1 TRX = 1,000,000 SUN
	CreateTime       int64  // 账户在链上创建的时间，Unix 毫秒时间戳；未激活账户通常为 0
	LatestOperation  int64  // 账户最近一次链上操作时间，Unix 毫秒时间戳
	FreeNetUsed      int64  // 已使用的每日免费带宽点数
	FreeNetLimit     int64  // 每日免费带宽点数上限
	FreeNetRemaining int64  // 剩余免费带宽点数，即 FreeNetLimit - FreeNetUsed，最小为 0
	NetUsed          int64  // 已使用的质押或委托带宽点数，不包含每日免费带宽
	NetLimit         int64  // 通过质押 TRX 或接收委托获得的带宽点数上限
	NetRemaining     int64  // 剩余质押或委托带宽点数，即 NetLimit - NetUsed，最小为 0
	EnergyUsed       int64  // 已使用的能量点数，主要由智能合约和 TRC20 调用消耗
	EnergyLimit      int64  // 通过质押 TRX 或接收委托获得的能量点数上限
	EnergyRemaining  int64  // 剩余能量点数，即 EnergyLimit - EnergyUsed，最小为 0
}

type TokenInfo struct {
	ContractAddress string
	Name            string
	Symbol          string
	Decimals        int
	TotalSupply     string
}

type TransferCoinOptions struct {
	WaitForConfirmation  bool
	ConfirmationTimeoutS int
}

type TransferTokenOptions struct {
	FeeLimitSUN          int64
	AutoFeeLimit         bool
	FeeLimitBuffer       float64
	WaitForConfirmation  bool
	ConfirmationTimeoutS int
}

type TronWallet struct {
	network     string
	grpcURL     string
	httpURL     string
	apiKey      string
	client      *tronclient.GrpcClient
	httpClient  *http.Client
	connectErr  error
	NativeToken string
	ChainName   string
}

func DefaultTransferCoinOptions() *TransferCoinOptions {
	return &TransferCoinOptions{
		ConfirmationTimeoutS: 120,
	}
}

func DefaultTransferTokenOptions() *TransferTokenOptions {
	return &TransferTokenOptions{
		FeeLimitSUN:          defaultFeeLimitSUN,
		AutoFeeLimit:         true,
		FeeLimitBuffer:       1.5,
		ConfirmationTimeoutS: 120,
	}
}

// NewTronWallet creates a TRON wallet client using the default endpoints for
// mainnet, nile, or shasta. An empty network selects mainnet.
func NewTronWallet(network string, apiKey string) *TronWallet {
	cfg := resolveNetworkConfig(network)
	return NewTronWalletWithEndpoints(cfg.Name, cfg.GRPCURL, cfg.HTTPURL, apiKey)
}

// NewTronWalletWithEndpoints creates a TRON wallet client using custom gRPC
// and HTTP endpoints. Empty endpoint values fall back to the selected network.
func NewTronWalletWithEndpoints(network, grpcURL, httpURL, apiKey string) *TronWallet {
	cfg := resolveNetworkConfig(network)
	if strings.TrimSpace(grpcURL) == "" {
		grpcURL = cfg.GRPCURL
	}
	if strings.TrimSpace(httpURL) == "" {
		httpURL = cfg.HTTPURL
	}

	w := &TronWallet{
		network:     cfg.Name,
		grpcURL:     strings.TrimSpace(grpcURL),
		httpURL:     strings.TrimRight(strings.TrimSpace(httpURL), "/"),
		apiKey:      strings.TrimSpace(apiKey),
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		NativeToken: "TRX",
		ChainName:   cfg.ChainName,
	}

	c := tronclient.NewGrpcClientWithTimeout(w.grpcURL, 15*time.Second)
	if w.apiKey != "" {
		_ = c.SetAPIKey(w.apiKey)
	}
	if err := c.Start(tronclient.GRPCInsecure()); err != nil {
		w.connectErr = err
		return w
	}
	w.client = c
	return w
}

func (w *TronWallet) Close() error {
	if w.client != nil {
		w.client.Stop()
		w.client = nil
	}
	return nil
}

func (w *TronWallet) Connected() bool {
	return w != nil && w.client != nil && w.connectErr == nil
}

func (w *TronWallet) Network() string {
	if w == nil {
		return ""
	}
	return w.network
}

func (w *TronWallet) GenerateWallet(index int) (bool, *Wallet, string) {
	if index < 0 {
		return false, nil, "index cannot be negative"
	}
	mnemonic, err := tronmnemonic.Generate(128)
	if err != nil {
		return false, nil, err.Error()
	}
	return w.RecoverWalletFromMnemonic(mnemonic, index)
}

func (w *TronWallet) RecoverWalletFromMnemonic(mnemonic string, index int) (bool, *Wallet, string) {
	mnemonic = strings.TrimSpace(mnemonic)
	if mnemonic == "" {
		return false, nil, "mnemonic is empty"
	}
	if index < 0 {
		return false, nil, "index cannot be negative"
	}
	if !bip39.IsMnemonicValid(mnemonic) {
		return false, nil, "invalid mnemonic"
	}

	privateKey, publicKey := tronmnemonic.FromSeedAndPassphrase(mnemonic, "", index)
	if privateKey == nil || publicKey == nil {
		return false, nil, "failed to derive wallet"
	}
	defer keys.ZeroPrivateKey(privateKey)

	return true, &Wallet{
		Address:    address.BTCECPubkeyToAddress(publicKey).String(),
		PrivateKey: "0x" + hex.EncodeToString(privateKey.Serialize()),
		Mnemonic:   mnemonic,
		Index:      index,
	}, ""
}

func (w *TronWallet) RecoverWalletFromPrivateKey(privateKey string) (bool, *Wallet, string) {
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return false, nil, err.Error()
	}
	defer keys.ZeroPrivateKey(key)

	return true, &Wallet{
		Address:    address.BTCECPrivkeyToAddress(key).String(),
		PrivateKey: "0x" + hex.EncodeToString(key.Serialize()),
		Mnemonic:   "",
		Index:      0,
	}, ""
}

func (w *TronWallet) SignMessage(privateKey string, message string) (bool, *SignMessage, string) {
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return false, nil, err.Error()
	}
	defer keys.ZeroPrivateKey(key)

	sig, err := crypto.Sign(tronkeystore.TextHash([]byte(message)), key.ToECDSA())
	if err != nil {
		return false, nil, err.Error()
	}
	if len(sig) != 65 {
		return false, nil, "invalid signature size"
	}

	sig[64] += 27
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:64])
	return true, &SignMessage{
		Signature:  hex.EncodeToString(sig),
		SignatureR: "0x" + r.Text(16),
		SignatureS: "0x" + s.Text(16),
		SignatureV: int(sig[64]),
	}, ""
}

func (w *TronWallet) VerifySignature(message string, signature string, expectedAddress string) (bool, string) {
	if _, err := address.Base58ToAddress(expectedAddress); err != nil {
		return false, "invalid address"
	}

	signature = strings.TrimPrefix(strings.TrimSpace(signature), "0x")
	sig, err := hex.DecodeString(signature)
	if err != nil {
		return false, err.Error()
	}
	if len(sig) != 65 {
		return false, "invalid signature length"
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	if sig[64] > 1 {
		return false, "invalid recovery id"
	}

	publicKey, err := crypto.SigToPub(tronkeystore.TextHash([]byte(message)), sig)
	if err != nil {
		return false, err.Error()
	}
	recovered := address.PubkeyToAddress(*publicKey).String()
	return recovered == expectedAddress, ""
}

func (w *TronWallet) GetBalance(accountAddress string) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "0", err.Error()
	}
	if _, err := address.Base58ToAddress(accountAddress); err != nil {
		return false, "0", "invalid address"
	}

	account, err := w.client.GetAccount(accountAddress)
	if err != nil {
		return false, "0", err.Error()
	}
	return true, formatUnits(big.NewInt(account.GetBalance()), 6), ""
}

func (w *TronWallet) GetAccountInfo(accountAddress string) (bool, *AccountInfo, string) {
	if err := w.requireClient(); err != nil {
		return false, nil, err.Error()
	}
	if _, err := address.Base58ToAddress(accountAddress); err != nil {
		return false, nil, "invalid address"
	}

	account, err := w.client.GetAccount(accountAddress)
	if err != nil {
		return false, nil, err.Error()
	}
	resource, err := w.client.GetAccountResource(accountAddress)
	if err != nil {
		return false, nil, err.Error()
	}

	return true, &AccountInfo{
		Address:          accountAddress,
		Balance:          formatUnits(big.NewInt(account.GetBalance()), 6),
		BalanceSUN:       account.GetBalance(),
		CreateTime:       account.GetCreateTime(),
		LatestOperation:  account.GetLatestOprationTime(),
		FreeNetUsed:      resource.GetFreeNetUsed(),
		FreeNetLimit:     resource.GetFreeNetLimit(),
		FreeNetRemaining: maxInt64(resource.GetFreeNetLimit()-resource.GetFreeNetUsed(), 0),
		NetUsed:          resource.GetNetUsed(),
		NetLimit:         resource.GetNetLimit(),
		NetRemaining:     maxInt64(resource.GetNetLimit()-resource.GetNetUsed(), 0),
		EnergyUsed:       resource.GetEnergyUsed(),
		EnergyLimit:      resource.GetEnergyLimit(),
		EnergyRemaining:  maxInt64(resource.GetEnergyLimit()-resource.GetEnergyUsed(), 0),
	}, ""
}

func (w *TronWallet) GetTokenInfo(contractAddress string) (bool, *TokenInfo, string) {
	if err := w.requireClient(); err != nil {
		return false, nil, err.Error()
	}
	if _, err := address.Base58ToAddress(contractAddress); err != nil {
		return false, nil, "invalid contract address"
	}

	name, err := w.client.TRC20GetName(contractAddress)
	if err != nil {
		return false, nil, err.Error()
	}
	symbol, err := w.client.TRC20GetSymbol(contractAddress)
	if err != nil {
		return false, nil, err.Error()
	}
	decimalsValue, err := w.client.TRC20GetDecimals(contractAddress)
	if err != nil {
		return false, nil, err.Error()
	}
	decimals := int(decimalsValue.Int64())
	totalSupply, err := w.readTRC20Uint256(contractAddress, "0x18160ddd")
	if err != nil {
		return false, nil, err.Error()
	}

	return true, &TokenInfo{
		ContractAddress: contractAddress,
		Name:            name,
		Symbol:          symbol,
		Decimals:        decimals,
		TotalSupply:     formatUnits(totalSupply, decimals),
	}, ""
}

func (w *TronWallet) GetTokenBalance(accountAddress string, contractAddress string) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "0", err.Error()
	}
	if _, err := address.Base58ToAddress(accountAddress); err != nil {
		return false, "0", "invalid address"
	}
	if _, err := address.Base58ToAddress(contractAddress); err != nil {
		return false, "0", "invalid contract address"
	}

	balance, err := w.client.TRC20ContractBalance(accountAddress, contractAddress)
	if err != nil {
		return false, "0", err.Error()
	}
	decimalsValue, err := w.client.TRC20GetDecimals(contractAddress)
	if err != nil {
		return false, "0", err.Error()
	}
	return true, formatUnits(balance, int(decimalsValue.Int64())), ""
}

// EstimateGasFee estimates a TRC20 transfer fee. contractAddress is required;
// TRX transfers consume bandwidth and do not use the TRC20 energy model.
func (w *TronWallet) EstimateGasFee(fromAddress string, toAddress string, amount string, contractAddress string, feeBuffer float64) (bool, *GasEstimate, string) {
	if err := w.requireClient(); err != nil {
		return false, nil, err.Error()
	}
	if _, err := address.Base58ToAddress(fromAddress); err != nil {
		return false, nil, "invalid from address"
	}
	if _, err := address.Base58ToAddress(toAddress); err != nil {
		return false, nil, "invalid to address"
	}
	if _, err := address.Base58ToAddress(contractAddress); err != nil {
		return false, nil, "invalid contract address"
	}
	if feeBuffer <= 0 {
		feeBuffer = 1.5
	}

	decimalsValue, err := w.client.TRC20GetDecimals(contractAddress)
	if err != nil {
		return false, nil, err.Error()
	}
	amountUnits, err := parseUnits(amount, int(decimalsValue.Int64()))
	if err != nil {
		return false, nil, err.Error()
	}

	estimate, err := w.client.TRC20Send(fromAddress, toAddress, contractAddress, amountUnits, 0, tronclient.WithEstimate())
	if err != nil {
		return false, nil, err.Error()
	}
	energyUsed := estimate.GetEnergyUsed()
	energyPrice := w.getEnergyPriceSUN()
	feeLimit := multiplyInt64ByFloat(energyUsed*energyPrice, feeBuffer)
	return true, &GasEstimate{
		FeeTRX:         formatUnits(big.NewInt(feeLimit), 6),
		FeeLimitSUN:    feeLimit,
		EnergyUsed:     energyUsed,
		EnergyPriceSUN: energyPrice,
	}, ""
}

func (w *TronWallet) TransferCoin(privateKey string, toAddress string, amountTRX string, options *TransferCoinOptions) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "", err.Error()
	}
	if options == nil {
		options = DefaultTransferCoinOptions()
	}
	if options.ConfirmationTimeoutS <= 0 {
		options.ConfirmationTimeoutS = 120
	}
	if _, err := address.Base58ToAddress(toAddress); err != nil {
		return false, "", "invalid to address"
	}

	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return false, "", err.Error()
	}
	defer keys.ZeroPrivateKey(key)
	fromAddress := address.BTCECPrivkeyToAddress(key).String()
	amountSUN, err := parseUnits(amountTRX, 6)
	if err != nil {
		return false, "", err.Error()
	}
	if !amountSUN.IsInt64() || amountSUN.Sign() <= 0 {
		return false, "", "amount is out of range"
	}

	account, err := w.client.GetAccount(fromAddress)
	if err != nil {
		return false, "", err.Error()
	}
	if big.NewInt(account.GetBalance()).Cmp(amountSUN) < 0 {
		return false, "", fmt.Sprintf("insufficient TRX balance, current: %s", formatUnits(big.NewInt(account.GetBalance()), 6))
	}

	tx, err := w.client.Transfer(fromAddress, toAddress, amountSUN.Int64())
	if err != nil {
		return false, "", err.Error()
	}
	signed, err := transaction.SignTransaction(tx.GetTransaction(), key)
	if err != nil {
		return false, "", err.Error()
	}
	if _, err = w.client.Broadcast(signed); err != nil {
		return false, "", err.Error()
	}
	txID := hex.EncodeToString(tx.GetTxid())

	if options.WaitForConfirmation {
		ok, receipt, message := w.WaitForTransactionReceipt(txID, options.ConfirmationTimeoutS)
		if !ok {
			return false, txID, message
		}
		return true, txID, fmt.Sprintf("transaction confirmed in block %v", receipt["block_number"])
	}
	return true, txID, "transfer sent"
}

func (w *TronWallet) TransferToken(privateKey string, toAddress string, amount string, contractAddress string, options *TransferTokenOptions) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "", err.Error()
	}
	if options == nil {
		options = DefaultTransferTokenOptions()
	}
	if options.FeeLimitBuffer <= 0 {
		options.FeeLimitBuffer = 1.5
	}
	if options.FeeLimitSUN <= 0 {
		options.FeeLimitSUN = defaultFeeLimitSUN
	}
	if options.ConfirmationTimeoutS <= 0 {
		options.ConfirmationTimeoutS = 120
	}
	if _, err := address.Base58ToAddress(toAddress); err != nil {
		return false, "", "invalid to address"
	}
	if _, err := address.Base58ToAddress(contractAddress); err != nil {
		return false, "", "invalid contract address"
	}

	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return false, "", err.Error()
	}
	defer keys.ZeroPrivateKey(key)
	fromAddress := address.BTCECPrivkeyToAddress(key).String()

	decimalsValue, err := w.client.TRC20GetDecimals(contractAddress)
	if err != nil {
		return false, "", err.Error()
	}
	decimals := int(decimalsValue.Int64())
	amountUnits, err := parseUnits(amount, decimals)
	if err != nil {
		return false, "", err.Error()
	}
	if amountUnits.Sign() <= 0 {
		return false, "", "amount must be greater than zero"
	}

	tokenBalance, err := w.client.TRC20ContractBalance(fromAddress, contractAddress)
	if err != nil {
		return false, "", err.Error()
	}
	if tokenBalance.Cmp(amountUnits) < 0 {
		return false, "", fmt.Sprintf("token balance is not enough, current: %s", formatUnits(tokenBalance, decimals))
	}

	feeLimit := options.FeeLimitSUN
	if options.AutoFeeLimit {
		if ok, estimate, _ := w.EstimateGasFee(fromAddress, toAddress, amount, contractAddress, options.FeeLimitBuffer); ok && estimate.FeeLimitSUN > 0 {
			feeLimit = estimate.FeeLimitSUN
		}
	}

	tx, err := w.client.TRC20Send(fromAddress, toAddress, contractAddress, amountUnits, feeLimit)
	if err != nil {
		return false, "", err.Error()
	}
	signed, err := signer.NewPrivateKeySignerFromBTCEC(key)
	if err != nil {
		return false, "", err.Error()
	}
	signedTx, err := signed.Sign(tx.GetTransaction())
	if err != nil {
		return false, "", err.Error()
	}
	if _, err = w.client.Broadcast(signedTx); err != nil {
		return false, "", err.Error()
	}
	txID := hex.EncodeToString(tx.GetTxid())

	if options.WaitForConfirmation {
		ok, receipt, message := w.WaitForTransactionReceipt(txID, options.ConfirmationTimeoutS)
		if !ok {
			return false, txID, message
		}
		return true, txID, fmt.Sprintf("token transfer confirmed in block %v", receipt["block_number"])
	}
	return true, txID, "token transfer sent"
}

func (w *TronWallet) GetTransactionInfo(txID string) (bool, map[string]any, string) {
	if err := w.requireClient(); err != nil {
		return false, map[string]any{}, err.Error()
	}
	txID = normalizeTransactionID(txID)
	if !isTransactionID(txID) {
		return false, map[string]any{}, "invalid transaction id"
	}

	info, err := w.client.GetTransactionInfoByID(txID)
	if err != nil {
		if tx, txErr := w.client.GetTransactionByID(txID); txErr == nil {
			status := "CONFIRMING"
			if len(tx.GetRet()) > 0 {
				status = tx.GetRet()[0].GetContractRet().String()
			}
			return true, map[string]any{
				"transaction_id": txID,
				"status":         status,
				"confirmed":      false,
			}, ""
		}
		return false, map[string]any{}, err.Error()
	}
	return true, transactionInfoMap(txID, info), ""
}

func (w *TronWallet) WaitForTransactionReceipt(txID string, timeoutSeconds int) (bool, map[string]any, string) {
	if err := w.requireClient(); err != nil {
		return false, map[string]any{}, err.Error()
	}
	txID = normalizeTransactionID(txID)
	if !isTransactionID(txID) {
		return false, map[string]any{}, "invalid transaction id"
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}

	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	for {
		info, err := w.client.GetTransactionInfoByID(txID)
		if err == nil {
			result := transactionInfoMap(txID, info)
			if !transactionInfoSucceeded(info) {
				return false, result, transactionFailureMessage(info)
			}
			return true, result, ""
		}
		if time.Now().After(deadline) {
			return false, map[string]any{"transaction_id": txID, "confirmed": false}, "transaction confirmation timeout"
		}
		time.Sleep(2 * time.Second)
	}
}

func (w *TronWallet) readTRC20Uint256(contractAddress, methodSignature string) (*big.Int, error) {
	result, err := w.client.TRC20Call("", contractAddress, methodSignature, true, 0)
	if err != nil {
		return nil, err
	}
	if len(result.GetConstantResult()) == 0 {
		return nil, errors.New("contract returned no result")
	}
	return new(big.Int).SetBytes(result.GetConstantResult()[0]), nil
}

func (w *TronWallet) requireClient() error {
	if w == nil {
		return errors.New("tron wallet is nil")
	}
	if w.connectErr != nil {
		return w.connectErr
	}
	if w.client == nil {
		return errors.New("tron client is not connected")
	}
	return nil
}

func resolveNetworkConfig(network string) NetworkConfig {
	network = strings.ToLower(strings.TrimSpace(network))
	if network == "" {
		network = "mainnet"
	}
	if cfg, ok := networkConfigs[network]; ok {
		return cfg
	}
	cfg := networkConfigs["mainnet"]
	cfg.Name = network
	cfg.ChainName = "TRON " + network
	return cfg
}

func parsePrivateKey(privateKey string) (*btcec.PrivateKey, error) {
	privateKey = strings.TrimPrefix(strings.TrimSpace(privateKey), "0x")
	if len(privateKey) != 64 {
		return nil, errors.New("invalid private key length")
	}
	return keys.GetPrivateKeyFromHex(privateKey)
}

func parseUnits(amount string, decimals int) (*big.Int, error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return nil, errors.New("amount is empty")
	}
	if decimals < 0 || decimals > 255 {
		return nil, errors.New("invalid decimals")
	}
	if strings.HasPrefix(amount, "+") {
		amount = amount[1:]
	}
	if strings.HasPrefix(amount, "-") {
		return nil, errors.New("amount cannot be negative")
	}

	parts := strings.Split(amount, ".")
	if len(parts) > 2 {
		return nil, errors.New("invalid amount")
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	if !isDigits(whole) {
		return nil, errors.New("invalid amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction != "" && !isDigits(fraction) {
			return nil, errors.New("invalid amount")
		}
	}
	if len(fraction) > decimals {
		return nil, fmt.Errorf("amount has more than %d decimal places", decimals)
	}
	fraction += strings.Repeat("0", decimals-len(fraction))

	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	wholeValue, ok := new(big.Int).SetString(whole, 10)
	if !ok {
		return nil, errors.New("invalid amount")
	}
	wholeValue.Mul(wholeValue, base)
	if fraction == "" {
		return wholeValue, nil
	}
	fractionValue, ok := new(big.Int).SetString(fraction, 10)
	if !ok {
		return nil, errors.New("invalid amount")
	}
	return wholeValue.Add(wholeValue, fractionValue), nil
}

func formatUnits(value *big.Int, decimals int) string {
	if value == nil {
		return "0"
	}
	negative := value.Sign() < 0
	abs := new(big.Int).Abs(new(big.Int).Set(value))
	digits := abs.Text(10)
	if decimals == 0 {
		if negative {
			return "-" + digits
		}
		return digits
	}
	if len(digits) <= decimals {
		digits = strings.Repeat("0", decimals-len(digits)+1) + digits
	}
	whole := digits[:len(digits)-decimals]
	fraction := strings.TrimRight(digits[len(digits)-decimals:], "0")
	result := whole
	if fraction != "" {
		result += "." + fraction
	}
	if negative {
		result = "-" + result
	}
	return result
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func multiplyInt64ByFloat(value int64, factor float64) int64 {
	if factor <= 0 {
		factor = 1
	}
	r := new(big.Rat).SetInt64(value)
	f := new(big.Rat)
	if _, ok := f.SetString(fmt.Sprintf("%.8f", factor)); !ok {
		return value
	}
	r.Mul(r, f)
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if new(big.Int).Mod(r.Num(), r.Denom()).Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return value
	}
	return q.Int64()
}

func normalizeTransactionID(txID string) string {
	return strings.TrimPrefix(strings.TrimSpace(txID), "0x")
}

func isTransactionID(txID string) bool {
	if len(txID) != 64 {
		return false
	}
	_, err := hex.DecodeString(txID)
	return err == nil
}

func transactionInfoMap(txID string, info *core.TransactionInfo) map[string]any {
	receipt := info.GetReceipt()
	status := "SUCCESS"
	if info.GetResult() == core.TransactionInfo_FAILED {
		status = "FAILED"
	}
	if receipt != nil && receipt.GetResult() != core.Transaction_Result_DEFAULT {
		status = receipt.GetResult().String()
	}
	return map[string]any{
		"transaction_id":       txID,
		"status":               status,
		"confirmed":            true,
		"block_number":         info.GetBlockNumber(),
		"block_timestamp":      info.GetBlockTimeStamp(),
		"fee_sun":              info.GetFee(),
		"fee_trx":              formatUnits(big.NewInt(info.GetFee()), 6),
		"energy_usage":         receipt.GetEnergyUsage(),
		"energy_usage_total":   receipt.GetEnergyUsageTotal(),
		"energy_fee":           receipt.GetEnergyFee(),
		"energy_penalty_total": receipt.GetEnergyPenaltyTotal(),
		"net_usage":            receipt.GetNetUsage(),
		"net_fee":              receipt.GetNetFee(),
		"message":              string(info.GetResMessage()),
	}
}

func transactionInfoSucceeded(info *core.TransactionInfo) bool {
	if info == nil || info.GetResult() == core.TransactionInfo_FAILED {
		return false
	}
	receipt := info.GetReceipt()
	if receipt == nil {
		return true
	}

	// 普通 TRX 转账成功时，TransactionInfo.Result 为 SUCESS，
	// Receipt.Result 可能仍是 DEFAULT。只有智能合约明确返回了非默认失败状态，
	// 才应该判定交易失败。
	contractResult := receipt.GetResult()
	return contractResult == core.Transaction_Result_DEFAULT || contractResult == core.Transaction_Result_SUCCESS
}

func transactionFailureMessage(info *core.TransactionInfo) string {
	if message := strings.TrimSpace(string(info.GetResMessage())); message != "" {
		return message
	}
	if info.GetReceipt() != nil && info.GetReceipt().GetResult() != core.Transaction_Result_DEFAULT {
		return "transaction failed: " + info.GetReceipt().GetResult().String()
	}
	if info.GetResult() == core.TransactionInfo_FAILED {
		return "transaction failed"
	}
	return "transaction failed"
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
