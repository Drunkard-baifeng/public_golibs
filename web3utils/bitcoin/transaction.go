package bitcoin

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/mempool"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/wallet/txauthor"
	"github.com/btcsuite/btcwallet/wallet/txsizes"
)

const (
	defaultFeeTargetBlocks             = 3
	defaultFeeRateBuffer               = 1.10
	defaultConfirmationTimeoutS        = 1800
	defaultConfirmationPollS           = 10
	defaultMaxInputs                   = 100
	defaultRelayFeeSatPerKVByte        = 1000
	maximumFeeRateSatPerVByte          = 100000
	optInRBFSequence            uint32 = wire.MaxTxInSequenceNum - 2
)

type TransferCoinOptions struct {
	AddressType          string
	FeeRateSatPerVByte   float64
	FeeTargetBlocks      int
	FeeRateBuffer        float64
	ChangeAddress        string
	IncludeUnconfirmed   bool
	VerifyUTXOs          bool
	EnableRBF            bool
	RandomizeChange      bool
	MaxInputs            int
	WaitForConfirmation  bool
	ConfirmationTimeoutS int
	ConfirmationPollS    int
}

type FeeEstimate struct {
	FeeBTC                    string
	FeeSatoshi                int64
	FeeRateSatPerVByte        float64
	EstimatedVirtualSize      int64
	InputCount                int
	OutputCount               int
	ChangeBTC                 string
	ChangeSatoshi             int64
	UsesUnconfirmedInputs     bool
	SelectedInputTotalBTC     string
	SelectedInputTotalSatoshi int64
}

type esploraUTXO struct {
	TxID   string `json:"txid"`
	Vout   uint32 `json:"vout"`
	Value  int64  `json:"value"`
	Status struct {
		Confirmed   bool   `json:"confirmed"`
		BlockHeight int64  `json:"block_height"`
		BlockHash   string `json:"block_hash"`
		BlockTime   int64  `json:"block_time"`
	} `json:"status"`
}

type esploraTransactionStatus struct {
	Confirmed   bool   `json:"confirmed"`
	BlockHeight int64  `json:"block_height"`
	BlockHash   string `json:"block_hash"`
	BlockTime   int64  `json:"block_time"`
}

type preparedTransaction struct {
	authored              *txauthor.AuthoredTx
	feeRateSatPerVByte    float64
	usesUnconfirmedInputs bool
}

type singleKeySecrets struct {
	privateKey   *btcec.PrivateKey
	params       *chaincfg.Params
	redeemScript []byte
	compressed   bool
}

func (s *singleKeySecrets) GetKey(btcutil.Address) (*btcec.PrivateKey, bool, error) {
	if s == nil || s.privateKey == nil {
		return nil, false, errors.New("private key is unavailable")
	}
	return s.privateKey, s.compressed, nil
}

func (s *singleKeySecrets) GetScript(btcutil.Address) ([]byte, error) {
	if s == nil || len(s.redeemScript) == 0 {
		return nil, errors.New("redeem script is unavailable")
	}
	return append([]byte(nil), s.redeemScript...), nil
}

func (s *singleKeySecrets) ChainParams() *chaincfg.Params {
	return s.params
}

type insufficientUTXOError struct {
	required  int64
	available int64
}

func (e *insufficientUTXOError) Error() string {
	return fmt.Sprintf("insufficient balance: need at least %s BTC, available %s BTC",
		formatSatoshi(e.required), formatSatoshi(e.available))
}

func (e *insufficientUTXOError) InputSourceError() {}

func DefaultTransferCoinOptions() *TransferCoinOptions {
	return &TransferCoinOptions{
		AddressType:          defaultAddressType,
		FeeTargetBlocks:      defaultFeeTargetBlocks,
		FeeRateBuffer:        defaultFeeRateBuffer,
		VerifyUTXOs:          true,
		EnableRBF:            true,
		RandomizeChange:      true,
		MaxInputs:            defaultMaxInputs,
		ConfirmationTimeoutS: defaultConfirmationTimeoutS,
		ConfirmationPollS:    defaultConfirmationPollS,
	}
}

func (w *BTCWallet) EstimateFee(fromAddress string, toAddress string, amountBTC string, options *TransferCoinOptions) (bool, *FeeEstimate, string) {
	normalized, err := normalizeTransferCoinOptions(options)
	if err != nil {
		return false, nil, err.Error()
	}
	prepared, err := w.prepareTransaction(fromAddress, toAddress, amountBTC, normalized)
	if err != nil {
		return false, nil, err.Error()
	}
	return true, feeEstimateFromPrepared(prepared), ""
}

func (w *BTCWallet) TransferCoin(privateKey string, toAddress string, amountBTC string, options *TransferCoinOptions) (bool, string, string) {
	normalized, err := normalizeTransferCoinOptions(options)
	if err != nil {
		return false, "", err.Error()
	}
	if w == nil || w.params == nil {
		return false, "", "bitcoin wallet is not initialized"
	}
	if !w.Connected() {
		return false, "", "bitcoin API URL is empty"
	}

	w.transferMu.Lock()
	txID, err := w.buildSignAndBroadcast(privateKey, toAddress, amountBTC, normalized)
	w.transferMu.Unlock()
	if err != nil {
		return false, "", err.Error()
	}

	if normalized.WaitForConfirmation {
		ok, _, waitMessage := w.waitForTransactionReceipt(
			txID,
			time.Duration(normalized.ConfirmationTimeoutS)*time.Second,
			time.Duration(normalized.ConfirmationPollS)*time.Second,
		)
		if !ok {
			return true, txID, "transaction sent but confirmation failed: " + waitMessage
		}
		return true, txID, "transaction confirmed"
	}
	return true, txID, ""
}

// TransferAll sends every spendable UTXO from the private key's address to
// toAddress. It creates no change output and deducts the fee from the amount.
func (w *BTCWallet) TransferAll(privateKey string, toAddress string, options *TransferCoinOptions) (bool, string, string) {
	normalized, err := normalizeTransferCoinOptions(options)
	if err != nil {
		return false, "", err.Error()
	}
	if w == nil || w.params == nil {
		return false, "", "bitcoin wallet is not initialized"
	}
	if !w.Connected() {
		return false, "", "bitcoin API URL is empty"
	}

	w.transferMu.Lock()
	txID, err := w.buildSignAndBroadcastSweep(privateKey, toAddress, normalized)
	w.transferMu.Unlock()
	if err != nil {
		return false, "", err.Error()
	}
	if normalized.WaitForConfirmation {
		ok, _, waitMessage := w.waitForTransactionReceipt(
			txID,
			time.Duration(normalized.ConfirmationTimeoutS)*time.Second,
			time.Duration(normalized.ConfirmationPollS)*time.Second,
		)
		if !ok {
			return true, txID, "transaction sent but confirmation failed: " + waitMessage
		}
		return true, txID, "transaction confirmed"
	}
	return true, txID, ""
}

func (w *BTCWallet) WaitForTransactionReceipt(txHash string, timeoutSeconds int) (bool, map[string]any, string) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultConfirmationTimeoutS
	}
	return w.waitForTransactionReceipt(
		txHash,
		time.Duration(timeoutSeconds)*time.Second,
		time.Duration(defaultConfirmationPollS)*time.Second,
	)
}

func (w *BTCWallet) buildSignAndBroadcast(privateKey string, toAddress string, amountBTC string, options *TransferCoinOptions) (string, error) {
	wif, err := btcutil.DecodeWIF(strings.TrimSpace(privateKey))
	if err != nil {
		return "", err
	}
	if !wif.IsForNet(w.params) {
		return "", fmt.Errorf("private key is not for %s", w.network)
	}
	_, addressType, err := addressPurpose(options.AddressType)
	if err != nil {
		return "", err
	}
	if !wif.CompressPubKey && addressType != AddressTypeLegacy {
		return "", errors.New("segwit and taproot require a compressed public key")
	}

	sourceAddress, err := addressFromPublicKey(wif.PrivKey.PubKey(), wif.CompressPubKey, addressType, w.params)
	if err != nil {
		return "", err
	}
	prepared, err := w.prepareTransaction(sourceAddress.EncodeAddress(), toAddress, amountBTC, options)
	if err != nil {
		return "", err
	}

	redeemScript, err := nestedWitnessRedeemScript(wif.PrivKey, addressType, w.params)
	if err != nil {
		return "", err
	}
	secrets := &singleKeySecrets{
		privateKey:   wif.PrivKey,
		params:       w.params,
		redeemScript: redeemScript,
		compressed:   wif.CompressPubKey,
	}
	if options.VerifyUTXOs {
		if err = w.verifySelectedPrevOutputs(context.Background(), prepared.authored); err != nil {
			return "", fmt.Errorf("verify UTXOs: %w", err)
		}
	}
	if err = prepared.authored.AddAllInputScripts(secrets); err != nil {
		return "", fmt.Errorf("sign transaction: %w", err)
	}
	if err = verifyAuthoredTransaction(prepared.authored); err != nil {
		return "", fmt.Errorf("verify signed transaction: %w", err)
	}

	fee := transactionFee(prepared.authored)
	virtualSize := mempool.GetTxVirtualSize(btcutil.NewTx(prepared.authored.Tx))
	minimumFee := int64(math.Ceil(prepared.feeRateSatPerVByte * float64(virtualSize)))
	if fee < minimumFee {
		return "", fmt.Errorf("signed transaction fee is too low: %d sat, need at least %d sat", fee, minimumFee)
	}

	var raw bytes.Buffer
	if err = prepared.authored.Tx.Serialize(&raw); err != nil {
		return "", fmt.Errorf("serialize transaction: %w", err)
	}
	expectedTxID := prepared.authored.Tx.TxHash().String()
	responseTxID, err := w.postText(context.Background(), "/tx", hex.EncodeToString(raw.Bytes()))
	if err != nil {
		return "", fmt.Errorf("broadcast transaction: %w", err)
	}
	responseTxID = strings.TrimSpace(responseTxID)
	if !isTransactionID(responseTxID) {
		return "", fmt.Errorf("bitcoin API returned an invalid transaction ID %q", responseTxID)
	}
	if !strings.EqualFold(responseTxID, expectedTxID) {
		return "", fmt.Errorf("bitcoin API returned transaction ID %s, expected %s", responseTxID, expectedTxID)
	}
	return expectedTxID, nil
}

func (w *BTCWallet) buildSignAndBroadcastSweep(privateKey string, toAddress string, options *TransferCoinOptions) (string, error) {
	wif, err := btcutil.DecodeWIF(strings.TrimSpace(privateKey))
	if err != nil {
		return "", err
	}
	if !wif.IsForNet(w.params) {
		return "", fmt.Errorf("private key is not for %s", w.network)
	}
	_, addressType, err := addressPurpose(options.AddressType)
	if err != nil {
		return "", err
	}
	if !wif.CompressPubKey && addressType != AddressTypeLegacy {
		return "", errors.New("segwit and taproot require a compressed public key")
	}

	sourceAddress, err := addressFromPublicKey(wif.PrivKey.PubKey(), wif.CompressPubKey, addressType, w.params)
	if err != nil {
		return "", err
	}
	destination, err := btcutil.DecodeAddress(strings.TrimSpace(toAddress), w.params)
	if err != nil || !destination.IsForNet(w.params) {
		return "", errors.New("invalid to address")
	}
	destinationScript, err := txscript.PayToAddrScript(destination)
	if err != nil {
		return "", err
	}
	sourceScript, err := txscript.PayToAddrScript(sourceAddress)
	if err != nil {
		return "", err
	}

	utxos, err := w.listUnspent(context.Background(), sourceAddress.EncodeAddress(), options.IncludeUnconfirmed)
	if err != nil {
		return "", err
	}
	if len(utxos) == 0 {
		return "", errors.New("no spendable UTXOs found")
	}
	if options.MaxInputs > 0 && len(utxos) > options.MaxInputs {
		return "", fmt.Errorf("sweep requires %d inputs, exceeding max_inputs=%d", len(utxos), options.MaxInputs)
	}
	feeRate, err := w.resolveFeeRate(context.Background(), options)
	if err != nil {
		return "", err
	}
	sequence := uint32(wire.MaxTxInSequenceNum)
	if options.EnableRBF {
		sequence = optInRBFSequence
	}

	inputs := make([]*wire.TxIn, 0, len(utxos))
	inputValues := make([]btcutil.Amount, 0, len(utxos))
	prevScripts := make([][]byte, 0, len(utxos))
	var totalInput int64
	for _, utxo := range utxos {
		hash, hashErr := chainhash.NewHashFromStr(utxo.TxID)
		if hashErr != nil {
			return "", fmt.Errorf("invalid UTXO transaction ID %q", utxo.TxID)
		}
		input := wire.NewTxIn(wire.NewOutPoint(hash, utxo.Vout), nil, nil)
		input.Sequence = sequence
		inputs = append(inputs, input)
		inputValues = append(inputValues, btcutil.Amount(utxo.Value))
		prevScripts = append(prevScripts, append([]byte(nil), sourceScript...))
		if utxo.Value > math.MaxInt64-totalInput {
			return "", errors.New("UTXO total overflows int64")
		}
		totalInput += utxo.Value
	}

	counts := transactionInputCounts(sourceScript, len(inputs))
	feeVSize := txsizes.EstimateVirtualSize(
		counts.p2pkh,
		counts.p2tr,
		counts.p2wpkh,
		counts.nested,
		[]*wire.TxOut{wire.NewTxOut(0, destinationScript)},
		0,
	)
	feeSatoshi := int64(math.Ceil(feeRate * float64(feeVSize)))
	if feeSatoshi < 1 {
		feeSatoshi = 1
	}

	redeemScript, err := nestedWitnessRedeemScript(wif.PrivKey, addressType, w.params)
	if err != nil {
		return "", err
	}
	secrets := &singleKeySecrets{
		privateKey:   wif.PrivKey,
		params:       w.params,
		redeemScript: redeemScript,
		compressed:   wif.CompressPubKey,
	}
	var signed *txauthor.AuthoredTx
	for attempt := 0; attempt < 4; attempt++ {
		amount := totalInput - feeSatoshi
		if amount <= 0 {
			return "", fmt.Errorf("balance %s BTC is not enough to pay the transaction fee", formatSatoshi(totalInput))
		}
		output := wire.NewTxOut(amount, destinationScript)
		if mempool.IsDust(output, btcutil.Amount(defaultRelayFeeSatPerKVByte)) {
			return "", fmt.Errorf("remaining amount %s BTC is below the destination dust threshold", formatSatoshi(amount))
		}
		tx := wire.NewMsgTx(wire.TxVersion)
		for _, input := range inputs {
			copyInput := wire.NewTxIn(&input.PreviousOutPoint, nil, nil)
			copyInput.Sequence = input.Sequence
			tx.AddTxIn(copyInput)
		}
		tx.AddTxOut(output)
		signed = &txauthor.AuthoredTx{
			Tx:              tx,
			PrevScripts:     prevScripts,
			PrevInputValues: inputValues,
			TotalInput:      btcutil.Amount(totalInput),
			ChangeIndex:     -1,
		}
		if options.VerifyUTXOs && attempt == 0 {
			if err = w.verifySelectedPrevOutputs(context.Background(), signed); err != nil {
				return "", fmt.Errorf("verify UTXOs: %w", err)
			}
		}
		if err = signed.AddAllInputScripts(secrets); err != nil {
			return "", fmt.Errorf("sign sweep transaction: %w", err)
		}
		if err = verifyAuthoredTransaction(signed); err != nil {
			return "", fmt.Errorf("verify sweep transaction: %w", err)
		}
		actualFee := transactionFee(signed)
		actualVSize := mempool.GetTxVirtualSize(btcutil.NewTx(signed.Tx))
		requiredFee := int64(math.Ceil(feeRate * float64(actualVSize)))
		if actualFee >= requiredFee {
			break
		}
		feeSatoshi += requiredFee - actualFee
		if attempt == 3 {
			return "", fmt.Errorf("unable to converge sweep fee: actual=%d required=%d", actualFee, requiredFee)
		}
	}
	if signed == nil {
		return "", errors.New("sweep transaction was not created")
	}
	var raw bytes.Buffer
	if err = signed.Tx.Serialize(&raw); err != nil {
		return "", fmt.Errorf("serialize sweep transaction: %w", err)
	}
	expectedTxID := signed.Tx.TxHash().String()
	responseTxID, err := w.postText(context.Background(), "/tx", hex.EncodeToString(raw.Bytes()))
	if err != nil {
		return "", fmt.Errorf("broadcast sweep transaction: %w", err)
	}
	responseTxID = strings.TrimSpace(responseTxID)
	if !isTransactionID(responseTxID) {
		return "", fmt.Errorf("bitcoin API returned an invalid transaction ID %q", responseTxID)
	}
	if !strings.EqualFold(responseTxID, expectedTxID) {
		return "", fmt.Errorf("bitcoin API returned transaction ID %s, expected %s", responseTxID, expectedTxID)
	}
	return expectedTxID, nil
}

type transactionInputCount struct {
	p2pkh  int
	p2tr   int
	p2wpkh int
	nested int
}

func transactionInputCounts(pkScript []byte, count int) transactionInputCount {
	switch {
	case txscript.IsPayToScriptHash(pkScript):
		return transactionInputCount{nested: count}
	case txscript.IsPayToWitnessPubKeyHash(pkScript):
		return transactionInputCount{p2wpkh: count}
	case txscript.IsPayToTaproot(pkScript):
		return transactionInputCount{p2tr: count}
	default:
		return transactionInputCount{p2pkh: count}
	}
}

func (w *BTCWallet) prepareTransaction(fromAddress string, toAddress string, amountBTC string, options *TransferCoinOptions) (*preparedTransaction, error) {
	if w == nil || w.params == nil {
		return nil, errors.New("bitcoin wallet is not initialized")
	}
	if !w.Connected() {
		return nil, errors.New("bitcoin API URL is empty")
	}

	from, err := btcutil.DecodeAddress(strings.TrimSpace(fromAddress), w.params)
	if err != nil || !from.IsForNet(w.params) {
		return nil, errors.New("invalid from address")
	}
	to, err := btcutil.DecodeAddress(strings.TrimSpace(toAddress), w.params)
	if err != nil || !to.IsForNet(w.params) {
		return nil, errors.New("invalid to address")
	}
	amountSatoshi, err := parseBTCAmount(amountBTC)
	if err != nil {
		return nil, err
	}
	if amountSatoshi <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}

	toScript, err := txscript.PayToAddrScript(to)
	if err != nil {
		return nil, err
	}
	output := wire.NewTxOut(amountSatoshi, toScript)
	if mempool.IsDust(output, btcutil.Amount(defaultRelayFeeSatPerKVByte)) {
		return nil, errors.New("amount is below the destination dust threshold")
	}

	changeAddress := from
	if strings.TrimSpace(options.ChangeAddress) != "" {
		changeAddress, err = btcutil.DecodeAddress(strings.TrimSpace(options.ChangeAddress), w.params)
		if err != nil || !changeAddress.IsForNet(w.params) {
			return nil, errors.New("invalid change address")
		}
	}
	changeScript, err := txscript.PayToAddrScript(changeAddress)
	if err != nil {
		return nil, err
	}
	fromScript, err := txscript.PayToAddrScript(from)
	if err != nil {
		return nil, err
	}

	utxos, err := w.listUnspent(context.Background(), from.EncodeAddress(), options.IncludeUnconfirmed)
	if err != nil {
		return nil, err
	}
	if len(utxos) == 0 {
		return nil, errors.New("no spendable UTXOs found")
	}
	feeRate, err := w.resolveFeeRate(context.Background(), options)
	if err != nil {
		return nil, err
	}
	feeRatePerKB := btcutil.Amount(math.Ceil(feeRate * 1000))
	sequence := uint32(wire.MaxTxInSequenceNum)
	if options.EnableRBF {
		sequence = optInRBFSequence
	}

	inputSource := newEsploraInputSource(utxos, fromScript, sequence, options.MaxInputs)
	changeSource := &txauthor.ChangeSource{
		ScriptSize: len(changeScript),
		NewScript: func() ([]byte, error) {
			return append([]byte(nil), changeScript...), nil
		},
	}
	authored, err := txauthor.NewUnsignedTransaction(
		[]*wire.TxOut{output},
		feeRatePerKB,
		inputSource,
		changeSource,
	)
	if err != nil {
		return nil, err
	}
	if options.RandomizeChange && authored.ChangeIndex >= 0 {
		authored.RandomizeChangePosition()
	}

	usesUnconfirmed := false
	selected := make(map[wire.OutPoint]bool, len(authored.Tx.TxIn))
	for _, input := range authored.Tx.TxIn {
		selected[input.PreviousOutPoint] = true
	}
	for _, utxo := range utxos {
		hash, hashErr := chainhash.NewHashFromStr(utxo.TxID)
		if hashErr == nil && selected[wire.OutPoint{Hash: *hash, Index: utxo.Vout}] && !utxo.Status.Confirmed {
			usesUnconfirmed = true
			break
		}
	}

	return &preparedTransaction{
		authored:              authored,
		feeRateSatPerVByte:    float64(feeRatePerKB) / 1000,
		usesUnconfirmedInputs: usesUnconfirmed,
	}, nil
}

func newEsploraInputSource(utxos []esploraUTXO, pkScript []byte, sequence uint32, maxInputs int) txauthor.InputSource {
	return func(target btcutil.Amount) (btcutil.Amount, []*wire.TxIn, []btcutil.Amount, [][]byte, error) {
		selected := selectUTXOs(utxos, int64(target), maxInputs)
		inputs := make([]*wire.TxIn, 0, len(selected))
		values := make([]btcutil.Amount, 0, len(selected))
		scripts := make([][]byte, 0, len(selected))
		var total int64
		for _, utxo := range selected {
			hash, err := chainhash.NewHashFromStr(utxo.TxID)
			if err != nil || len(utxo.TxID) != 64 {
				return 0, nil, nil, nil, fmt.Errorf("invalid UTXO transaction ID %q", utxo.TxID)
			}
			input := wire.NewTxIn(wire.NewOutPoint(hash, utxo.Vout), nil, nil)
			input.Sequence = sequence
			inputs = append(inputs, input)
			values = append(values, btcutil.Amount(utxo.Value))
			scripts = append(scripts, append([]byte(nil), pkScript...))
			total += utxo.Value
		}
		if total < int64(target) {
			return btcutil.Amount(total), inputs, values, scripts, &insufficientUTXOError{
				required:  int64(target),
				available: total,
			}
		}
		return btcutil.Amount(total), inputs, values, scripts, nil
	}
}

func selectUTXOs(utxos []esploraUTXO, target int64, maxInputs int) []esploraUTXO {
	singleIndex := -1
	for index, utxo := range utxos {
		if utxo.Value >= target {
			singleIndex = index
			continue
		}
		break
	}
	if singleIndex >= 0 {
		return []esploraUTXO{utxos[singleIndex]}
	}
	limit := len(utxos)
	if maxInputs > 0 && limit > maxInputs {
		limit = maxInputs
	}
	selected := make([]esploraUTXO, 0, limit)
	var total int64
	for i := 0; i < limit; i++ {
		selected = append(selected, utxos[i])
		total += utxos[i].Value
		if total >= target {
			break
		}
	}
	return selected
}

func (w *BTCWallet) listUnspent(ctx context.Context, address string, includeUnconfirmed bool) ([]esploraUTXO, error) {
	var response []esploraUTXO
	if err := w.getJSON(ctx, "/address/"+address+"/utxo", &response); err != nil {
		return nil, err
	}
	filtered := make([]esploraUTXO, 0, len(response))
	seen := make(map[string]struct{}, len(response))
	for _, utxo := range response {
		if utxo.Value <= 0 || !isTransactionID(utxo.TxID) {
			continue
		}
		if !includeUnconfirmed && !utxo.Status.Confirmed {
			continue
		}
		key := utxo.TxID + ":" + strconv.FormatUint(uint64(utxo.Vout), 10)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, utxo)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Value == filtered[j].Value {
			if filtered[i].TxID == filtered[j].TxID {
				return filtered[i].Vout < filtered[j].Vout
			}
			return filtered[i].TxID < filtered[j].TxID
		}
		return filtered[i].Value > filtered[j].Value
	})
	return filtered, nil
}

func (w *BTCWallet) resolveFeeRate(ctx context.Context, options *TransferCoinOptions) (float64, error) {
	if options.FeeRateSatPerVByte > 0 {
		return validateFeeRate(options.FeeRateSatPerVByte)
	}
	var estimates map[string]float64
	if err := w.getJSON(ctx, "/fee-estimates", &estimates); err != nil {
		return 0, fmt.Errorf("get fee estimates: %w", err)
	}
	target := options.FeeTargetBlocks
	bestTarget := -1
	bestRate := 0.0
	for textTarget, rate := range estimates {
		blockTarget, err := strconv.Atoi(textTarget)
		if err != nil || blockTarget <= 0 || rate <= 0 {
			continue
		}
		if blockTarget <= target && blockTarget > bestTarget {
			bestTarget = blockTarget
			bestRate = rate
		}
	}
	if bestTarget < 0 {
		for textTarget, rate := range estimates {
			blockTarget, err := strconv.Atoi(textTarget)
			if err != nil || blockTarget <= 0 || rate <= 0 {
				continue
			}
			if bestTarget < 0 || blockTarget < bestTarget {
				bestTarget = blockTarget
				bestRate = rate
			}
		}
	}
	if bestTarget < 0 {
		return 0, errors.New("bitcoin API returned no usable fee estimate")
	}
	return validateFeeRate(bestRate * options.FeeRateBuffer)
}

func validateFeeRate(rate float64) (float64, error) {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return 0, errors.New("fee rate must be greater than zero")
	}
	if rate > maximumFeeRateSatPerVByte {
		return 0, fmt.Errorf("fee rate %.8f sat/vB exceeds safety limit", rate)
	}
	if rate < 1 {
		rate = 1
	}
	return rate, nil
}

func normalizeTransferCoinOptions(options *TransferCoinOptions) (*TransferCoinOptions, error) {
	if options == nil {
		return DefaultTransferCoinOptions(), nil
	}
	copyOptions := *options
	_, addressType, err := addressPurpose(copyOptions.AddressType)
	if err != nil {
		return nil, err
	}
	copyOptions.AddressType = addressType
	if copyOptions.FeeTargetBlocks <= 0 {
		copyOptions.FeeTargetBlocks = defaultFeeTargetBlocks
	}
	if copyOptions.FeeRateBuffer <= 0 {
		copyOptions.FeeRateBuffer = defaultFeeRateBuffer
	}
	if copyOptions.MaxInputs <= 0 {
		copyOptions.MaxInputs = defaultMaxInputs
	}
	if copyOptions.ConfirmationTimeoutS <= 0 {
		copyOptions.ConfirmationTimeoutS = defaultConfirmationTimeoutS
	}
	if copyOptions.ConfirmationPollS <= 0 {
		copyOptions.ConfirmationPollS = defaultConfirmationPollS
	}
	if copyOptions.FeeRateSatPerVByte < 0 {
		return nil, errors.New("fee rate cannot be negative")
	}
	return &copyOptions, nil
}

func feeEstimateFromPrepared(prepared *preparedTransaction) *FeeEstimate {
	fee := transactionFee(prepared.authored)
	change := int64(0)
	if prepared.authored.ChangeIndex >= 0 && prepared.authored.ChangeIndex < len(prepared.authored.Tx.TxOut) {
		change = prepared.authored.Tx.TxOut[prepared.authored.ChangeIndex].Value
	}
	estimatedVSize := int64(0)
	if prepared.feeRateSatPerVByte > 0 {
		estimatedVSize = int64(math.Ceil(float64(fee) / prepared.feeRateSatPerVByte))
	}
	return &FeeEstimate{
		FeeBTC:                    formatSatoshi(fee),
		FeeSatoshi:                fee,
		FeeRateSatPerVByte:        prepared.feeRateSatPerVByte,
		EstimatedVirtualSize:      estimatedVSize,
		InputCount:                len(prepared.authored.Tx.TxIn),
		OutputCount:               len(prepared.authored.Tx.TxOut),
		ChangeBTC:                 formatSatoshi(change),
		ChangeSatoshi:             change,
		UsesUnconfirmedInputs:     prepared.usesUnconfirmedInputs,
		SelectedInputTotalBTC:     formatSatoshi(int64(prepared.authored.TotalInput)),
		SelectedInputTotalSatoshi: int64(prepared.authored.TotalInput),
	}
}

func transactionFee(authored *txauthor.AuthoredTx) int64 {
	if authored == nil || authored.Tx == nil {
		return 0
	}
	var outputs int64
	for _, output := range authored.Tx.TxOut {
		outputs += output.Value
	}
	return int64(authored.TotalInput) - outputs
}

func nestedWitnessRedeemScript(privateKey *btcec.PrivateKey, addressType string, params *chaincfg.Params) ([]byte, error) {
	if addressType != AddressTypeP2SHSegWit {
		return nil, nil
	}
	publicKeyHash := btcutil.Hash160(privateKey.PubKey().SerializeCompressed())
	witnessAddress, err := btcutil.NewAddressWitnessPubKeyHash(publicKeyHash, params)
	if err != nil {
		return nil, err
	}
	return txscript.PayToAddrScript(witnessAddress)
}

func verifyAuthoredTransaction(authored *txauthor.AuthoredTx) error {
	if authored == nil || authored.Tx == nil {
		return errors.New("transaction is empty")
	}
	if len(authored.Tx.TxIn) != len(authored.PrevScripts) || len(authored.Tx.TxIn) != len(authored.PrevInputValues) {
		return errors.New("previous outputs do not match transaction inputs")
	}
	prevOutFetcher, err := txauthor.TXPrevOutFetcher(authored.Tx, authored.PrevScripts, authored.PrevInputValues)
	if err != nil {
		return err
	}
	hashCache := txscript.NewTxSigHashes(authored.Tx, prevOutFetcher)
	for index, previousScript := range authored.PrevScripts {
		engine, err := txscript.NewEngine(
			previousScript,
			authored.Tx,
			index,
			txscript.StandardVerifyFlags,
			nil,
			hashCache,
			int64(authored.PrevInputValues[index]),
			prevOutFetcher,
		)
		if err != nil {
			return fmt.Errorf("input %d: %w", index, err)
		}
		if err = engine.Execute(); err != nil {
			return fmt.Errorf("input %d: %w", index, err)
		}
	}
	return nil
}

func (w *BTCWallet) verifySelectedPrevOutputs(ctx context.Context, authored *txauthor.AuthoredTx) error {
	if authored == nil || authored.Tx == nil {
		return errors.New("transaction is empty")
	}
	if len(authored.Tx.TxIn) != len(authored.PrevScripts) || len(authored.Tx.TxIn) != len(authored.PrevInputValues) {
		return errors.New("previous outputs do not match transaction inputs")
	}
	transactions := make(map[string]*wire.MsgTx)
	for index, input := range authored.Tx.TxIn {
		txID := input.PreviousOutPoint.Hash.String()
		previousTransaction := transactions[txID]
		if previousTransaction == nil {
			rawHex, err := w.getText(ctx, "/tx/"+txID+"/hex")
			if err != nil {
				return fmt.Errorf("get previous transaction %s: %w", txID, err)
			}
			previousTransaction, err = deserializeTransaction(rawHex)
			if err != nil {
				return fmt.Errorf("decode previous transaction %s: %w", txID, err)
			}
			if previousTransaction.TxHash().String() != txID {
				return fmt.Errorf("previous transaction %s hash mismatch", txID)
			}
			transactions[txID] = previousTransaction
		}
		vout := int(input.PreviousOutPoint.Index)
		if vout < 0 || vout >= len(previousTransaction.TxOut) {
			return fmt.Errorf("previous transaction %s has no output %d", txID, vout)
		}
		previousOutput := previousTransaction.TxOut[vout]
		if previousOutput.Value != int64(authored.PrevInputValues[index]) {
			return fmt.Errorf("previous output %s:%d value mismatch", txID, vout)
		}
		if !bytes.Equal(previousOutput.PkScript, authored.PrevScripts[index]) {
			return fmt.Errorf("previous output %s:%d script mismatch", txID, vout)
		}
	}
	return nil
}

func (w *BTCWallet) waitForTransactionReceipt(txHash string, timeout time.Duration, pollInterval time.Duration) (bool, map[string]any, string) {
	if w == nil || !w.Connected() {
		return false, nil, "bitcoin API URL is empty"
	}
	if !isTransactionID(txHash) {
		return false, nil, "invalid transaction ID"
	}
	if timeout <= 0 {
		timeout = time.Duration(defaultConfirmationTimeoutS) * time.Second
	}
	if pollInterval <= 0 {
		pollInterval = time.Duration(defaultConfirmationPollS) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastStatus *esploraTransactionStatus
	var lastErr error
	for {
		var status esploraTransactionStatus
		err := w.getJSON(ctx, "/tx/"+strings.ToLower(txHash)+"/status", &status)
		if err == nil {
			lastStatus = &status
			lastErr = nil
			if status.Confirmed {
				return true, transactionStatusMap(txHash, &status), ""
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			result := transactionStatusMap(txHash, lastStatus)
			if lastErr != nil {
				return false, result, lastErr.Error()
			}
			return false, result, "confirmation timeout"
		case <-ticker.C:
		}
	}
}

func transactionStatusMap(txHash string, status *esploraTransactionStatus) map[string]any {
	result := map[string]any{
		"transaction_hash": strings.ToLower(txHash),
		"confirmed":        false,
	}
	if status == nil {
		return result
	}
	result["confirmed"] = status.Confirmed
	if status.BlockHeight > 0 {
		result["block_height"] = status.BlockHeight
	}
	if status.BlockHash != "" {
		result["block_hash"] = status.BlockHash
	}
	if status.BlockTime > 0 {
		result["block_time"] = status.BlockTime
	}
	return result
}

func (w *BTCWallet) postText(ctx context.Context, path string, body string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, w.apiURL+path, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Accept", "text/plain")
	response, err := w.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
	if readErr != nil {
		return "", readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("bitcoin API returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return string(payload), nil
}

func (w *BTCWallet) getText(ctx context.Context, path string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, w.apiURL+path, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "text/plain")
	response, err := w.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if readErr != nil {
		return "", readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("bitcoin API returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return strings.TrimSpace(string(payload)), nil
}

func parseBTCAmount(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("amount is empty")
	}
	if strings.HasPrefix(value, "+") {
		value = strings.TrimPrefix(value, "+")
	}
	if strings.HasPrefix(value, "-") {
		return 0, errors.New("amount cannot be negative")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return 0, errors.New("invalid BTC amount")
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	if !digitsOnly(whole) {
		return 0, errors.New("invalid BTC amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || !digitsOnly(fraction) {
			return 0, errors.New("invalid BTC amount")
		}
		if len(fraction) > 8 {
			return 0, errors.New("BTC amount has more than 8 decimal places")
		}
	}
	fraction += strings.Repeat("0", 8-len(fraction))
	wholeInt, ok := new(big.Int).SetString(whole, 10)
	if !ok {
		return 0, errors.New("invalid BTC amount")
	}
	wholeInt.Mul(wholeInt, big.NewInt(100000000))
	if fraction != "" {
		fractionInt, ok := new(big.Int).SetString(fraction, 10)
		if !ok {
			return 0, errors.New("invalid BTC amount")
		}
		wholeInt.Add(wholeInt, fractionInt)
	}
	if wholeInt.Sign() < 0 || wholeInt.Cmp(big.NewInt(btcutil.MaxSatoshi)) > 0 || !wholeInt.IsInt64() {
		return 0, errors.New("BTC amount is out of range")
	}
	return wholeInt.Int64(), nil
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isTransactionID(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func deserializeTransaction(rawHex string) (*wire.MsgTx, error) {
	data, err := hex.DecodeString(strings.TrimSpace(rawHex))
	if err != nil {
		return nil, err
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err = tx.Deserialize(bytes.NewReader(data)); err != nil {
		return nil, err
	}
	return tx, nil
}

var _ txauthor.SecretsSource = (*singleKeySecrets)(nil)
