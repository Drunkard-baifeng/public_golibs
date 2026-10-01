// Package bitcoin provides wallet, signing, address, and balance utilities for
// Bitcoin-compatible networks.
package bitcoin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	bip39 "github.com/fbsobreira/go-bip39"
)

const (
	AddressTypeLegacy     = "legacy"
	AddressTypeP2SHSegWit = "p2sh-segwit"
	AddressTypeSegWit     = "segwit"
	AddressTypeTaproot    = "taproot"

	defaultAddressType  = AddressTypeSegWit
	bitcoinMessageMagic = "Bitcoin Signed Message:\n"
)

type Wallet struct {
	Address        string
	PrivateKey     string
	Mnemonic       string
	Index          int
	DerivationPath string
	AddressType    string
}

type SignMessage struct {
	Signature  string
	SignatureR string
	SignatureS string
	SignatureV int
}

type BalanceInfo struct {
	ConfirmedSatoshi   int64
	UnconfirmedSatoshi int64
	TotalSatoshi       int64
	ConfirmedBTC       string
	UnconfirmedBTC     string
	TotalBTC           string
}

type BTCWallet struct {
	network    string
	params     *chaincfg.Params
	apiURL     string
	httpClient *http.Client
	transferMu sync.Mutex
}

type esploraAddressResponse struct {
	ChainStats struct {
		FundedTXOSum int64 `json:"funded_txo_sum"`
		SpentTXOSum  int64 `json:"spent_txo_sum"`
	} `json:"chain_stats"`
	MempoolStats struct {
		FundedTXOSum int64 `json:"funded_txo_sum"`
		SpentTXOSum  int64 `json:"spent_txo_sum"`
	} `json:"mempool_stats"`
}

// NewBTCWallet creates a Bitcoin wallet helper. apiURL is optional; when it is
// omitted, a public Esplora-compatible endpoint is selected for mainnet,
// testnet, or signet. Pass an empty apiURL to create an offline-only helper.
func NewBTCWallet(network string, apiURL ...string) *BTCWallet {
	normalized, params := resolveNetwork(network)
	endpoint := defaultAPIURL(normalized)
	if len(apiURL) > 0 {
		endpoint = strings.TrimRight(strings.TrimSpace(apiURL[0]), "/")
	}

	return &BTCWallet{
		network: normalized,
		params:  params,
		apiURL:  endpoint,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (w *BTCWallet) Close() error {
	if w.httpClient != nil {
		w.httpClient.CloseIdleConnections()
	}
	return nil
}

func (w *BTCWallet) Connected() bool {
	return w != nil && w.httpClient != nil && w.apiURL != ""
}

func (w *BTCWallet) Network() string {
	if w == nil {
		return ""
	}
	return w.network
}

func (w *BTCWallet) GenerateWallet(index int) (bool, *Wallet, string) {
	return w.GenerateWalletWithAddressType(index, defaultAddressType)
}

func (w *BTCWallet) GenerateWalletWithAddressType(index int, addressType string) (bool, *Wallet, string) {
	if index < 0 {
		return false, nil, "index must be non-negative"
	}
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return false, nil, err.Error()
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return false, nil, err.Error()
	}
	return w.RecoverWalletFromMnemonicWithAddressType(mnemonic, index, addressType)
}

func (w *BTCWallet) RecoverWalletFromMnemonic(mnemonic string, index int) (bool, *Wallet, string) {
	return w.RecoverWalletFromMnemonicWithAddressType(mnemonic, index, defaultAddressType)
}

func (w *BTCWallet) RecoverWalletFromMnemonicWithAddressType(mnemonic string, index int, addressType string) (bool, *Wallet, string) {
	if w == nil || w.params == nil {
		return false, nil, "bitcoin wallet is not initialized"
	}
	if index < 0 {
		return false, nil, "index must be non-negative"
	}
	mnemonic = strings.TrimSpace(mnemonic)
	if !bip39.IsMnemonicValid(mnemonic) {
		return false, nil, "invalid mnemonic"
	}

	purpose, normalizedType, err := addressPurpose(addressType)
	if err != nil {
		return false, nil, err.Error()
	}
	seed := bip39.NewSeed(mnemonic, "")
	master, err := hdkeychain.NewMaster(seed, w.params)
	if err != nil {
		return false, nil, err.Error()
	}
	path := derivationPath(purpose, coinType(w.network), index)
	key, err := deriveKey(master, purpose, coinType(w.network), index)
	if err != nil {
		return false, nil, err.Error()
	}
	defer key.Zero()

	privateKey, err := key.ECPrivKey()
	if err != nil {
		return false, nil, err.Error()
	}
	return w.walletFromPrivateKey(privateKey, mnemonic, index, path, normalizedType)
}

func (w *BTCWallet) RecoverWalletFromPrivateKey(privateKey string) (bool, *Wallet, string) {
	return w.RecoverWalletFromPrivateKeyWithAddressType(privateKey, defaultAddressType)
}

func (w *BTCWallet) RecoverWalletFromPrivateKeyWithAddressType(privateKey string, addressType string) (bool, *Wallet, string) {
	if w == nil || w.params == nil {
		return false, nil, "bitcoin wallet is not initialized"
	}
	wif, err := btcutil.DecodeWIF(strings.TrimSpace(privateKey))
	if err != nil {
		return false, nil, err.Error()
	}
	if !wif.IsForNet(w.params) {
		return false, nil, fmt.Sprintf("private key is not for %s", w.network)
	}
	_, normalizedType, err := addressPurpose(addressType)
	if err != nil {
		return false, nil, err.Error()
	}
	return w.walletFromPrivateKey(wif.PrivKey, "", 0, "", normalizedType)
}

// SignMessage creates a Bitcoin compact message signature encoded as base64.
func (w *BTCWallet) SignMessage(privateKey string, message string) (bool, *SignMessage, string) {
	wif, err := btcutil.DecodeWIF(strings.TrimSpace(privateKey))
	if err != nil {
		return false, nil, err.Error()
	}
	if w == nil || w.params == nil || !wif.IsForNet(w.params) {
		return false, nil, "private key network does not match wallet network"
	}

	hash := bitcoinMessageHash(message)
	signature := ecdsa.SignCompact(wif.PrivKey, hash, wif.CompressPubKey)
	if len(signature) != 65 {
		return false, nil, "invalid compact signature size"
	}
	return true, &SignMessage{
		Signature:  base64.StdEncoding.EncodeToString(signature),
		SignatureR: "0x" + hex.EncodeToString(signature[1:33]),
		SignatureS: "0x" + hex.EncodeToString(signature[33:65]),
		SignatureV: int(signature[0]),
	}, ""
}

func (w *BTCWallet) VerifySignature(message string, signature string, address string) (bool, string) {
	if w == nil || w.params == nil {
		return false, "bitcoin wallet is not initialized"
	}
	decodedAddress, err := btcutil.DecodeAddress(strings.TrimSpace(address), w.params)
	if err != nil || !decodedAddress.IsForNet(w.params) {
		return false, "invalid address"
	}
	compact, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return false, err.Error()
	}
	if len(compact) != 65 {
		return false, "invalid compact signature length"
	}
	publicKey, compressed, err := ecdsa.RecoverCompact(compact, bitcoinMessageHash(message))
	if err != nil {
		return false, err.Error()
	}

	addressType, err := inferAddressType(decodedAddress)
	if err != nil {
		return false, err.Error()
	}
	derived, err := addressFromPublicKey(publicKey, compressed, addressType, w.params)
	if err != nil {
		return false, err.Error()
	}
	return derived.EncodeAddress() == decodedAddress.EncodeAddress(), ""
}

func (w *BTCWallet) ValidateAddress(address string) (bool, string) {
	if w == nil || w.params == nil {
		return false, "bitcoin wallet is not initialized"
	}
	decoded, err := btcutil.DecodeAddress(strings.TrimSpace(address), w.params)
	if err != nil {
		return false, err.Error()
	}
	if !decoded.IsForNet(w.params) {
		return false, fmt.Sprintf("address is not for %s", w.network)
	}
	return true, ""
}

func (w *BTCWallet) GetBalance(address string) (bool, string, string) {
	ok, info, message := w.GetBalanceInfo(address)
	if !ok {
		return false, "0", message
	}
	return true, info.ConfirmedBTC, ""
}

func (w *BTCWallet) GetBalanceInfo(address string) (bool, *BalanceInfo, string) {
	if ok, message := w.ValidateAddress(address); !ok {
		return false, nil, message
	}
	if !w.Connected() {
		return false, nil, "bitcoin API URL is empty"
	}

	var response esploraAddressResponse
	if err := w.getJSON(context.Background(), "/address/"+strings.TrimSpace(address), &response); err != nil {
		return false, nil, err.Error()
	}
	confirmed := response.ChainStats.FundedTXOSum - response.ChainStats.SpentTXOSum
	unconfirmed := response.MempoolStats.FundedTXOSum - response.MempoolStats.SpentTXOSum
	total := confirmed + unconfirmed
	return true, &BalanceInfo{
		ConfirmedSatoshi:   confirmed,
		UnconfirmedSatoshi: unconfirmed,
		TotalSatoshi:       total,
		ConfirmedBTC:       formatSatoshi(confirmed),
		UnconfirmedBTC:     formatSatoshi(unconfirmed),
		TotalBTC:           formatSatoshi(total),
	}, ""
}

func (w *BTCWallet) walletFromPrivateKey(privateKey *btcec.PrivateKey, mnemonic string, index int, path string, addressType string) (bool, *Wallet, string) {
	address, err := addressFromPublicKey(privateKey.PubKey(), true, addressType, w.params)
	if err != nil {
		return false, nil, err.Error()
	}
	wif, err := btcutil.NewWIF(privateKey, w.params, true)
	if err != nil {
		return false, nil, err.Error()
	}
	return true, &Wallet{
		Address:        address.EncodeAddress(),
		PrivateKey:     wif.String(),
		Mnemonic:       mnemonic,
		Index:          index,
		DerivationPath: path,
		AddressType:    addressType,
	}, ""
}

func (w *BTCWallet) getJSON(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, w.apiURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := w.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("bitcoin API returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err = json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode bitcoin API response: %w", err)
	}
	return nil
}

func resolveNetwork(network string) (string, *chaincfg.Params) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "testnet", "testnet3":
		return "testnet", &chaincfg.TestNet3Params
	case "regtest", "regression":
		return "regtest", &chaincfg.RegressionNetParams
	case "signet":
		return "signet", &chaincfg.SigNetParams
	default:
		return "bitcoin", &chaincfg.MainNetParams
	}
}

func defaultAPIURL(network string) string {
	switch network {
	case "bitcoin":
		return "https://blockstream.info/api"
	case "testnet":
		return "https://blockstream.info/testnet/api"
	case "signet":
		return "https://blockstream.info/signet/api"
	default:
		return ""
	}
}

func addressPurpose(addressType string) (uint32, string, error) {
	switch strings.ToLower(strings.TrimSpace(addressType)) {
	case AddressTypeLegacy:
		return 44, AddressTypeLegacy, nil
	case AddressTypeP2SHSegWit:
		return 49, AddressTypeP2SHSegWit, nil
	case "", AddressTypeSegWit:
		return 84, AddressTypeSegWit, nil
	case AddressTypeTaproot:
		return 86, AddressTypeTaproot, nil
	default:
		return 0, "", fmt.Errorf("unsupported address type %q", addressType)
	}
}

func coinType(network string) uint32 {
	if network == "bitcoin" {
		return 0
	}
	return 1
}

func derivationPath(purpose, coin uint32, index int) string {
	return fmt.Sprintf("m/%d'/%d'/0'/0/%d", purpose, coin, index)
}

func deriveKey(master *hdkeychain.ExtendedKey, purpose, coin uint32, index int) (*hdkeychain.ExtendedKey, error) {
	if uint64(index) >= uint64(hdkeychain.HardenedKeyStart) {
		return nil, errors.New("index is too large")
	}
	parts := []uint32{
		hdkeychain.HardenedKeyStart + purpose,
		hdkeychain.HardenedKeyStart + coin,
		hdkeychain.HardenedKeyStart,
		0,
		uint32(index),
	}
	key := master
	for _, childNumber := range parts {
		child, err := key.Derive(childNumber)
		if err != nil {
			return nil, err
		}
		if key != master {
			key.Zero()
		}
		key = child
	}
	return key, nil
}

func addressFromPublicKey(publicKey *btcec.PublicKey, compressed bool, addressType string, params *chaincfg.Params) (btcutil.Address, error) {
	serialized := publicKey.SerializeCompressed()
	if !compressed && addressType == AddressTypeLegacy {
		serialized = publicKey.SerializeUncompressed()
	}
	publicKeyHash := btcutil.Hash160(serialized)

	switch addressType {
	case AddressTypeLegacy:
		return btcutil.NewAddressPubKeyHash(publicKeyHash, params)
	case AddressTypeP2SHSegWit:
		witnessAddress, err := btcutil.NewAddressWitnessPubKeyHash(publicKeyHash, params)
		if err != nil {
			return nil, err
		}
		witnessProgram, err := txscript.PayToAddrScript(witnessAddress)
		if err != nil {
			return nil, err
		}
		return btcutil.NewAddressScriptHash(witnessProgram, params)
	case AddressTypeSegWit:
		return btcutil.NewAddressWitnessPubKeyHash(publicKeyHash, params)
	case AddressTypeTaproot:
		outputKey := txscript.ComputeTaprootKeyNoScript(publicKey)
		return btcutil.NewAddressTaproot(outputKey.SerializeCompressed()[1:], params)
	default:
		return nil, fmt.Errorf("unsupported address type %q", addressType)
	}
}

func inferAddressType(address btcutil.Address) (string, error) {
	switch address.(type) {
	case *btcutil.AddressPubKeyHash:
		return AddressTypeLegacy, nil
	case *btcutil.AddressScriptHash:
		return AddressTypeP2SHSegWit, nil
	case *btcutil.AddressWitnessPubKeyHash:
		return AddressTypeSegWit, nil
	case *btcutil.AddressTaproot:
		return AddressTypeTaproot, nil
	default:
		return "", fmt.Errorf("unsupported address type %T", address)
	}
}

func bitcoinMessageHash(message string) []byte {
	var buffer bytes.Buffer
	_ = wire.WriteVarString(&buffer, 0, bitcoinMessageMagic)
	_ = wire.WriteVarString(&buffer, 0, message)
	first := sha256.Sum256(buffer.Bytes())
	second := sha256.Sum256(first[:])
	return second[:]
}

func formatSatoshi(satoshi int64) string {
	negative := satoshi < 0
	if negative {
		satoshi = -satoshi
	}
	whole := satoshi / 100_000_000
	fraction := satoshi % 100_000_000
	value := fmt.Sprintf("%d.%08d", whole, fraction)
	value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	if value == "" {
		value = "0"
	}
	if negative {
		return "-" + value
	}
	return value
}
