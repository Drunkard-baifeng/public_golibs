package web3utils

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	hdwallet "github.com/miguelmota/go-ethereum-hdwallet"
)

var chainNativeTokens = map[int64]string{
	1:        "ETH",
	5:        "ETH",
	11155111: "ETH",
	56:       "BNB",
	97:       "BNB",
	137:      "MATIC",
	80001:    "MATIC",
	42161:    "ETH",
	421613:   "ETH",
	10:       "ETH",
	420:      "ETH",
	43114:    "AVAX",
	43113:    "AVAX",
	250:      "FTM",
	4002:     "FTM",
	25:       "CRO",
	338:      "CRO",
	8453:     "ETH",
	84531:    "ETH",
}

var chainNames = map[int64]string{
	1:        "Ethereum",
	5:        "Goerli",
	11155111: "Sepolia",
	56:       "BSC",
	97:       "BSC Testnet",
	137:      "Polygon",
	80001:    "Polygon Mumbai",
	42161:    "Arbitrum One",
	421613:   "Arbitrum Goerli",
	10:       "Optimism",
	420:      "Optimism Goerli",
	43114:    "Avalanche",
	43113:    "Avalanche Fuji",
	250:      "Fantom",
	4002:     "Fantom Testnet",
	25:       "Cronos",
	338:      "Cronos Testnet",
	8453:     "Base",
	84531:    "Base Goerli",
}

const standardERC20ABI = `[
  {"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"},
  {"constant":true,"inputs":[],"name":"symbol","outputs":[{"name":"","type":"string"}],"type":"function"},
  {"constant":true,"inputs":[],"name":"name","outputs":[{"name":"","type":"string"}],"type":"function"},
  {"constant":true,"inputs":[{"name":"_owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"balance","type":"uint256"}],"type":"function"},
  {"constant":false,"inputs":[{"name":"_to","type":"address"},{"name":"_value","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"type":"function"}
]`

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
	FeeEther     string
	GasLimit     uint64
	GasPriceWei  string
	GasPriceGwei string
}

type TransferCoinOptions struct {
	GasPriceGwei         int64
	GasLimit             uint64
	Data                 string
	GasPriceBuffer       float64
	GasLimitBuffer       float64
	WaitForConfirmation  bool
	ConfirmationTimeoutS int
}

type TransferTokenOptions struct {
	ContractABIJSON      string
	GasPriceGwei         int64
	GasLimit             uint64
	GasPriceBuffer       float64
	GasLimitBuffer       float64
	WaitForConfirmation  bool
	ConfirmationTimeoutS int
}

type EVMWallet struct {
	rpcURL      string
	client      *ethclient.Client
	chainID     int64
	NativeToken string
	ChainName   string
}

func DefaultTransferCoinOptions() *TransferCoinOptions {
	return &TransferCoinOptions{
		GasPriceBuffer:       1.2,
		GasLimitBuffer:       1.2,
		ConfirmationTimeoutS: 120,
	}
}

func DefaultTransferTokenOptions() *TransferTokenOptions {
	return &TransferTokenOptions{
		GasPriceBuffer:       1.2,
		GasLimitBuffer:       1.2,
		ConfirmationTimeoutS: 120,
	}
}

func NewEVMWallet(rpcURL string, chainID int64) *EVMWallet {
	w := &EVMWallet{
		rpcURL:  rpcURL,
		chainID: chainID,
	}

	if rpcURL != "" {
		client, err := ethclient.Dial(rpcURL)
		if err == nil {
			w.client = client
			if w.chainID == 0 {
				id, chainErr := client.ChainID(context.Background())
				if chainErr == nil {
					w.chainID = id.Int64()
				}
			}
		}
	}

	if w.chainID == 0 {
		w.chainID = 1
	}
	w.NativeToken = chainNativeTokens[w.chainID]
	if w.NativeToken == "" {
		w.NativeToken = "ETH"
	}
	w.ChainName = chainNames[w.chainID]
	if w.ChainName == "" {
		w.ChainName = fmt.Sprintf("Chain %d", w.chainID)
	}
	return w
}

func (w *EVMWallet) Close() error {
	if w.client == nil {
		return nil
	}
	w.client.Close()
	w.client = nil
	return nil
}

func (w *EVMWallet) Connected() bool {
	return w.client != nil
}

func (w *EVMWallet) GenerateWallet(index int) (bool, *Wallet, string) {
	mnemonic, err := hdwallet.NewMnemonic(128)
	if err != nil {
		return false, nil, err.Error()
	}
	return w.RecoverWalletFromMnemonic(mnemonic, index)
}

func (w *EVMWallet) RecoverWalletFromMnemonic(mnemonic string, index int) (bool, *Wallet, string) {
	if strings.TrimSpace(mnemonic) == "" {
		return false, nil, "mnemonic is empty"
	}
	hd, err := hdwallet.NewFromMnemonic(mnemonic)
	if err != nil {
		return false, nil, err.Error()
	}

	path, err := accounts.ParseDerivationPath(fmt.Sprintf("m/44'/60'/0'/0/%d", index))
	if err != nil {
		return false, nil, err.Error()
	}
	account, err := hd.Derive(path, false)
	if err != nil {
		return false, nil, err.Error()
	}
	privateKey, err := hd.PrivateKey(account)
	if err != nil {
		return false, nil, err.Error()
	}

	return true, &Wallet{
		Address:    account.Address.Hex(),
		PrivateKey: hexutil.Encode(crypto.FromECDSA(privateKey)),
		Mnemonic:   mnemonic,
		Index:      index,
	}, ""
}

func (w *EVMWallet) RecoverWalletFromPrivateKey(privateKey string) (bool, *Wallet, string) {
	privateKey = strings.TrimPrefix(strings.TrimSpace(privateKey), "0x")
	key, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return false, nil, err.Error()
	}

	address := crypto.PubkeyToAddress(key.PublicKey).Hex()
	return true, &Wallet{
		Address:    address,
		PrivateKey: "0x" + privateKey,
		Mnemonic:   "",
		Index:      0,
	}, ""
}

func (w *EVMWallet) SignMessage(privateKey string, message string) (bool, *SignMessage, string) {
	privateKey = strings.TrimPrefix(strings.TrimSpace(privateKey), "0x")
	key, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return false, nil, err.Error()
	}

	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		return false, nil, err.Error()
	}
	if len(sig) != 65 {
		return false, nil, "invalid signature size"
	}

	// 与 Python eth_account 对齐：输出 v=27/28 的签名
	signatureWithEthV := make([]byte, 65)
	copy(signatureWithEthV, sig)
	signatureWithEthV[64] = signatureWithEthV[64] + 27

	rInt := new(big.Int).SetBytes(signatureWithEthV[0:32])
	sInt := new(big.Int).SetBytes(signatureWithEthV[32:64])

	return true, &SignMessage{
		Signature:  hex.EncodeToString(signatureWithEthV), // 不带0x，与 python 输出一致
		SignatureR: "0x" + rInt.Text(16),
		SignatureS: "0x" + sInt.Text(16),
		SignatureV: int(signatureWithEthV[64]),
	}, ""
}

func (w *EVMWallet) VerifySignature(message string, signature string, address string) (bool, string) {
	if !common.IsHexAddress(address) {
		return false, "invalid address"
	}
	signature = strings.TrimSpace(signature)
	if !strings.HasPrefix(signature, "0x") {
		signature = "0x" + signature
	}

	sig, err := hexutil.Decode(signature)
	if err != nil {
		return false, err.Error()
	}
	if len(sig) != 65 {
		return false, "invalid signature length"
	}

	copySig := make([]byte, 65)
	copy(copySig, sig)
	if copySig[64] >= 27 {
		copySig[64] -= 27
	}
	if copySig[64] > 1 {
		return false, "invalid recovery id"
	}

	hash := accounts.TextHash([]byte(message))
	pubKey, err := crypto.SigToPub(hash, copySig)
	if err != nil {
		return false, err.Error()
	}

	recovered := crypto.PubkeyToAddress(*pubKey).Hex()
	return strings.EqualFold(recovered, address), ""
}

func (w *EVMWallet) EstimateGasFee(fromAddress string, toAddress string, amountEther string, data string, gasPriceBuffer float64) (bool, *GasEstimate, string) {
	if err := w.requireClient(); err != nil {
		return false, nil, err.Error()
	}
	if !common.IsHexAddress(fromAddress) || !common.IsHexAddress(toAddress) {
		return false, nil, "invalid from/to address"
	}
	if gasPriceBuffer <= 0 {
		gasPriceBuffer = 1.2
	}

	valueWei, err := parseUnits(amountEther, 18)
	if err != nil {
		return false, nil, err.Error()
	}
	input, err := decodeHexData(data)
	if err != nil {
		return false, nil, err.Error()
	}

	from := common.HexToAddress(fromAddress)
	to := common.HexToAddress(toAddress)
	ctx := context.Background()

	gasLimit, err := w.client.EstimateGas(ctx, ethereum.CallMsg{
		From:  from,
		To:    &to,
		Value: valueWei,
		Data:  input,
	})
	if err != nil {
		return false, nil, err.Error()
	}

	gasPrice, err := w.client.SuggestGasPrice(ctx)
	if err != nil || gasPrice == nil || gasPrice.Sign() <= 0 {
		gasPrice = w.fallbackGasPriceWei()
	} else {
		gasPrice = multiplyBigIntByFloat(gasPrice, gasPriceBuffer)
	}

	feeWei := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)
	return true, &GasEstimate{
		FeeEther:     formatUnits(feeWei, 18),
		GasLimit:     gasLimit,
		GasPriceWei:  gasPrice.String(),
		GasPriceGwei: formatUnits(gasPrice, 9),
	}, ""
}

func (w *EVMWallet) GetBalance(address string) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "0", err.Error()
	}
	if !common.IsHexAddress(address) {
		return false, "0", "invalid address"
	}

	balance, err := w.client.BalanceAt(context.Background(), common.HexToAddress(address), nil)
	if err != nil {
		return false, "0", err.Error()
	}
	return true, formatUnits(balance, 18), ""
}

func (w *EVMWallet) TransferCoin(privateKey string, toAddress string, amountEther string, options *TransferCoinOptions) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "", err.Error()
	}
	if options == nil {
		options = DefaultTransferCoinOptions()
	}
	if options.GasPriceBuffer <= 0 {
		options.GasPriceBuffer = 1.2
	}
	if options.GasLimitBuffer <= 0 {
		options.GasLimitBuffer = 1.2
	}
	if options.ConfirmationTimeoutS <= 0 {
		options.ConfirmationTimeoutS = 120
	}
	if !common.IsHexAddress(toAddress) {
		return false, "", "invalid to address"
	}

	key, fromAddress, err := privateKeyToSender(privateKey)
	if err != nil {
		return false, "", err.Error()
	}
	valueWei, err := parseUnits(amountEther, 18)
	if err != nil {
		return false, "", err.Error()
	}
	input, err := decodeHexData(options.Data)
	if err != nil {
		return false, "", err.Error()
	}
	isContractCall := len(input) > 0

	ctx := context.Background()
	balanceWei, err := w.client.BalanceAt(ctx, fromAddress, nil)
	if err != nil {
		return false, "", err.Error()
	}
	if balanceWei.Cmp(valueWei) < 0 {
		return false, "", fmt.Sprintf("insufficient balance, current: %s", formatUnits(balanceWei, 18))
	}

	nonce, err := w.client.PendingNonceAt(ctx, fromAddress)
	if err != nil {
		return false, "", err.Error()
	}

	gasPrice := w.buildGasPriceWei(options.GasPriceGwei, options.GasPriceBuffer)
	to := common.HexToAddress(toAddress)

	gasLimit := options.GasLimit
	if gasLimit == 0 {
		estimated, estimateErr := w.client.EstimateGas(ctx, ethereum.CallMsg{
			From:     fromAddress,
			To:       &to,
			Value:    valueWei,
			GasPrice: gasPrice,
			Data:     input,
		})
		if estimateErr != nil {
			if isContractCall {
				return false, "", "contract call gas estimate failed, please check address/data or set gas_limit manually"
			}
			if w.chainID == 42161 {
				estimated = 100000
			} else {
				estimated = 21000
			}
		}
		gasLimit = multiplyUint64ByFloat(estimated, options.GasLimitBuffer)
	}

	feeWei := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)
	totalCost := new(big.Int).Add(valueWei, feeWei)
	if balanceWei.Cmp(totalCost) < 0 {
		return false, "", fmt.Sprintf("%s balance is not enough for amount + gas", w.NativeToken)
	}

	tx := types.NewTransaction(nonce, to, valueWei, gasLimit, gasPrice, input)
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(w.chainIDBig()), key)
	if err != nil {
		return false, "", err.Error()
	}
	if err = w.client.SendTransaction(ctx, signed); err != nil {
		return false, "", err.Error()
	}
	txHash := signed.Hash().Hex()

	if options.WaitForConfirmation {
		ok, receipt, receiptErr := w.waitReceipt(common.HexToHash(txHash), time.Duration(options.ConfirmationTimeoutS)*time.Second)
		if receiptErr != nil {
			return true, txHash, fmt.Sprintf("transaction sent but confirmation failed: %v", receiptErr)
		}
		if !ok || receipt.Status != types.ReceiptStatusSuccessful {
			return false, txHash, "transaction failed (status=0)"
		}
		return true, txHash, fmt.Sprintf("transaction confirmed, gas used: %d", receipt.GasUsed)
	}

	if isContractCall {
		return true, txHash, "contract call sent"
	}
	return true, txHash, "transfer sent"
}

func (w *EVMWallet) TransferToken(privateKey string, toAddress string, amount string, contractAddress string, options *TransferTokenOptions) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "", err.Error()
	}
	if options == nil {
		options = DefaultTransferTokenOptions()
	}
	if options.GasPriceBuffer <= 0 {
		options.GasPriceBuffer = 1.2
	}
	if options.GasLimitBuffer <= 0 {
		options.GasLimitBuffer = 1.2
	}
	if options.ConfirmationTimeoutS <= 0 {
		options.ConfirmationTimeoutS = 120
	}
	if !common.IsHexAddress(toAddress) || !common.IsHexAddress(contractAddress) {
		return false, "", "invalid to/contract address"
	}

	contractABI, err := parseTokenABI(options.ContractABIJSON)
	if err != nil {
		return false, "", err.Error()
	}

	key, fromAddress, err := privateKeyToSender(privateKey)
	if err != nil {
		return false, "", err.Error()
	}
	to := common.HexToAddress(toAddress)
	contract := common.HexToAddress(contractAddress)

	ctx := context.Background()
	tokenDecimals, _ := w.getTokenDecimals(ctx, contract, contractABI)
	tokenAmount, err := parseUnits(amount, tokenDecimals)
	if err != nil {
		return false, "", err.Error()
	}

	tokenBalance, err := w.readTokenBalance(ctx, contract, contractABI, fromAddress)
	if err != nil {
		return false, "", err.Error()
	}
	if tokenBalance.Cmp(tokenAmount) < 0 {
		return false, "", fmt.Sprintf("token balance is not enough, current: %s", formatUnits(tokenBalance, tokenDecimals))
	}

	nativeBalanceWei, err := w.client.BalanceAt(ctx, fromAddress, nil)
	if err != nil {
		return false, "", err.Error()
	}
	nonce, err := w.client.PendingNonceAt(ctx, fromAddress)
	if err != nil {
		return false, "", err.Error()
	}

	gasPrice := w.buildGasPriceWei(options.GasPriceGwei, options.GasPriceBuffer)
	input, err := contractABI.Pack("transfer", to, tokenAmount)
	if err != nil {
		return false, "", err.Error()
	}

	gasLimit := options.GasLimit
	if gasLimit == 0 {
		estimated, estimateErr := w.client.EstimateGas(ctx, ethereum.CallMsg{
			From:     fromAddress,
			To:       &contract,
			GasPrice: gasPrice,
			Value:    big.NewInt(0),
			Data:     input,
		})
		if estimateErr != nil {
			switch w.chainID {
			case 42161:
				estimated = 200000
			case 137:
				estimated = 150000
			default:
				estimated = 100000
			}
		}
		gasLimit = multiplyUint64ByFloat(estimated, options.GasLimitBuffer)
	}

	feeWei := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)
	if nativeBalanceWei.Cmp(feeWei) < 0 {
		return false, "", fmt.Sprintf("%s balance is not enough for gas", w.NativeToken)
	}

	tx := types.NewTransaction(nonce, contract, big.NewInt(0), gasLimit, gasPrice, input)
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(w.chainIDBig()), key)
	if err != nil {
		return false, "", err.Error()
	}
	if err = w.client.SendTransaction(ctx, signed); err != nil {
		return false, "", err.Error()
	}
	txHash := signed.Hash().Hex()

	if options.WaitForConfirmation {
		ok, receipt, receiptErr := w.waitReceipt(common.HexToHash(txHash), time.Duration(options.ConfirmationTimeoutS)*time.Second)
		if receiptErr != nil {
			return true, txHash, fmt.Sprintf("token transfer sent but confirmation failed: %v", receiptErr)
		}
		if !ok || receipt.Status != types.ReceiptStatusSuccessful {
			return false, txHash, "transaction failed (status=0)"
		}
		return true, txHash, fmt.Sprintf("token transfer confirmed, gas used: %d", receipt.GasUsed)
	}

	return true, txHash, "token transfer sent"
}

func (w *EVMWallet) GetTokenBalance(address string, contractAddress string, contractABIJSON string) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "0", err.Error()
	}
	if !common.IsHexAddress(address) || !common.IsHexAddress(contractAddress) {
		return false, "0", "invalid address/contract address"
	}

	contractABI, err := parseTokenABI(contractABIJSON)
	if err != nil {
		return false, "0", err.Error()
	}
	holder := common.HexToAddress(address)
	contract := common.HexToAddress(contractAddress)

	ctx := context.Background()
	tokenBalance, err := w.readTokenBalance(ctx, contract, contractABI, holder)
	if err != nil {
		return false, "0", err.Error()
	}
	tokenDecimals, _ := w.getTokenDecimals(ctx, contract, contractABI)
	return true, formatUnits(tokenBalance, tokenDecimals), ""
}

func (w *EVMWallet) WaitForTransactionReceipt(txHash string, timeoutSeconds int) (bool, map[string]any, string) {
	if err := w.requireClient(); err != nil {
		return false, map[string]any{}, err.Error()
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	if !isHexHash(txHash) {
		return false, map[string]any{}, "invalid tx hash"
	}

	ok, receipt, err := w.waitReceipt(common.HexToHash(txHash), time.Duration(timeoutSeconds)*time.Second)
	if err != nil {
		return false, map[string]any{}, err.Error()
	}
	receiptMap := map[string]any{
		"transactionHash": receipt.TxHash.Hex(),
		"status":          receipt.Status,
		"blockNumber":     receipt.BlockNumber.String(),
		"gasUsed":         receipt.GasUsed,
	}

	if !ok || receipt.Status != types.ReceiptStatusSuccessful {
		return false, receiptMap, "transaction failed"
	}
	return true, receiptMap, ""
}

func (w *EVMWallet) requireClient() error {
	if w.client == nil {
		return errors.New("not connected to blockchain network")
	}
	return nil
}

func (w *EVMWallet) chainIDBig() *big.Int {
	return big.NewInt(w.chainID)
}

func (w *EVMWallet) fallbackGasPriceWei() *big.Int {
	switch w.chainID {
	case 42161:
		return new(big.Int).Mul(big.NewInt(1), big.NewInt(1_000_000_000))
	case 137:
		return new(big.Int).Mul(big.NewInt(50), big.NewInt(1_000_000_000))
	default:
		return new(big.Int).Mul(big.NewInt(20), big.NewInt(1_000_000_000))
	}
}

func (w *EVMWallet) buildGasPriceWei(gasPriceGwei int64, gasPriceBuffer float64) *big.Int {
	if gasPriceGwei > 0 {
		return new(big.Int).Mul(big.NewInt(gasPriceGwei), big.NewInt(1_000_000_000))
	}

	gasPrice, err := w.client.SuggestGasPrice(context.Background())
	if err != nil || gasPrice == nil || gasPrice.Sign() <= 0 {
		return w.fallbackGasPriceWei()
	}
	return multiplyBigIntByFloat(gasPrice, gasPriceBuffer)
}

func (w *EVMWallet) waitReceipt(txHash common.Hash, timeout time.Duration) (bool, *types.Receipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		receipt, err := w.client.TransactionReceipt(ctx, txHash)
		if err == nil {
			return true, receipt, nil
		}
		if !errors.Is(err, ethereum.NotFound) {
			return false, nil, err
		}

		select {
		case <-ctx.Done():
			return false, nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *EVMWallet) readTokenBalance(ctx context.Context, contractAddress common.Address, tokenABI abi.ABI, holder common.Address) (*big.Int, error) {
	input, err := tokenABI.Pack("balanceOf", holder)
	if err != nil {
		return nil, err
	}
	output, err := w.client.CallContract(ctx, ethereum.CallMsg{
		To:   &contractAddress,
		Data: input,
	}, nil)
	if err != nil {
		return nil, err
	}
	values, err := tokenABI.Unpack("balanceOf", output)
	if err != nil || len(values) == 0 {
		return nil, errors.New("failed to decode balanceOf result")
	}

	value, ok := values[0].(*big.Int)
	if !ok {
		return nil, errors.New("invalid balanceOf return type")
	}
	return value, nil
}

func (w *EVMWallet) getTokenDecimals(ctx context.Context, contractAddress common.Address, tokenABI abi.ABI) (int, error) {
	input, err := tokenABI.Pack("decimals")
	if err != nil {
		return 18, err
	}
	output, err := w.client.CallContract(ctx, ethereum.CallMsg{
		To:   &contractAddress,
		Data: input,
	}, nil)
	if err != nil {
		return 18, err
	}
	values, err := tokenABI.Unpack("decimals", output)
	if err != nil || len(values) == 0 {
		return 18, errors.New("failed to decode decimals result")
	}

	switch v := values[0].(type) {
	case uint8:
		return int(v), nil
	case *big.Int:
		return int(v.Int64()), nil
	default:
		return 18, errors.New("invalid decimals return type")
	}
}

func parseTokenABI(contractABIJSON string) (abi.ABI, error) {
	abiJSON := strings.TrimSpace(contractABIJSON)
	if abiJSON == "" {
		abiJSON = standardERC20ABI
	}
	return abi.JSON(strings.NewReader(abiJSON))
}

func privateKeyToSender(privateKey string) (*ecdsa.PrivateKey, common.Address, error) {
	keyHex := strings.TrimPrefix(strings.TrimSpace(privateKey), "0x")
	key, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		return nil, common.Address{}, err
	}
	address := crypto.PubkeyToAddress(key.PublicKey)
	return key, address, nil
}

func isHexHash(s string) bool {
	if !strings.HasPrefix(s, "0x") {
		return false
	}
	data, err := hexutil.Decode(s)
	if err != nil {
		return false
	}
	return len(data) == common.HashLength
}

func decodeHexData(data string) ([]byte, error) {
	data = strings.TrimSpace(data)
	if data == "" || data == "0x" {
		return nil, nil
	}
	if !strings.HasPrefix(data, "0x") {
		data = "0x" + data
	}
	decoded, err := hexutil.Decode(data)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func parseUnits(amount string, decimals int) (*big.Int, error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return nil, errors.New("amount is empty")
	}
	if strings.HasPrefix(amount, "-") {
		return nil, errors.New("amount cannot be negative")
	}
	if decimals < 0 {
		return nil, errors.New("invalid decimals")
	}

	parts := strings.SplitN(amount, ".", 2)
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if whole == "" {
		whole = "0"
	}
	if !isDigits(whole) || (frac != "" && !isDigits(frac)) {
		return nil, errors.New("invalid amount format")
	}
	if len(frac) > decimals {
		frac = frac[:decimals]
	}
	if len(frac) < decimals {
		frac += strings.Repeat("0", decimals-len(frac))
	}
	number := strings.TrimLeft(whole+frac, "0")
	if number == "" {
		return big.NewInt(0), nil
	}

	value, ok := new(big.Int).SetString(number, 10)
	if !ok {
		return nil, errors.New("invalid amount")
	}
	return value, nil
}

func formatUnits(value *big.Int, decimals int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	raw := value.String()
	if decimals == 0 {
		return raw
	}

	if len(raw) <= decimals {
		raw = strings.Repeat("0", decimals-len(raw)+1) + raw
	}

	whole := raw[:len(raw)-decimals]
	frac := strings.TrimRight(raw[len(raw)-decimals:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func multiplyBigIntByFloat(v *big.Int, factor float64) *big.Int {
	if v == nil || v.Sign() <= 0 {
		return big.NewInt(0)
	}
	if factor <= 0 {
		return new(big.Int).Set(v)
	}
	f := new(big.Float).SetInt(v)
	f.Mul(f, big.NewFloat(factor))
	out, _ := f.Int(nil)
	if out.Sign() <= 0 {
		return big.NewInt(1)
	}
	return out
}

func multiplyUint64ByFloat(v uint64, factor float64) uint64 {
	if v == 0 {
		return 0
	}
	if factor <= 0 {
		return v
	}
	out := uint64(float64(v) * factor)
	if out == 0 {
		return 1
	}
	return out
}
