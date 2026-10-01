package bitcoin

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/mempool"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func TestTransferCoinAllAddressTypes(t *testing.T) {
	offline := NewBTCWallet("bitcoin", "")
	_, destination, message := offline.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 1, AddressTypeSegWit)
	if destination == nil {
		t.Fatalf("derive destination failed: %s", message)
	}

	for testIndex, addressType := range []string{
		AddressTypeLegacy,
		AddressTypeP2SHSegWit,
		AddressTypeSegWit,
		AddressTypeTaproot,
	} {
		t.Run(addressType, func(t *testing.T) {
			ok, source, recoverMessage := offline.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 0, addressType)
			if !ok {
				t.Fatalf("derive source failed: %s", recoverMessage)
			}
			sourceAddress, err := btcutil.DecodeAddress(source.Address, offline.params)
			if err != nil {
				t.Fatal(err)
			}
			sourceScript, err := txscript.PayToAddrScript(sourceAddress)
			if err != nil {
				t.Fatal(err)
			}

			const fundingValue = int64(500000)
			fundingTx, fundingHex := makeFundingTransaction(t, sourceScript, fundingValue, uint32(testIndex+1))
			fundingTxID := fundingTx.TxHash().String()
			var broadcastTx *wire.MsgTx

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/address/"+source.Address+"/utxo":
					writer.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(writer).Encode([]map[string]any{{
						"txid":  fundingTxID,
						"vout":  0,
						"value": fundingValue,
						"status": map[string]any{
							"confirmed":    true,
							"block_height": 800000,
						},
					}})
				case request.Method == http.MethodGet && request.URL.Path == "/tx/"+fundingTxID+"/hex":
					_, _ = writer.Write([]byte(fundingHex))
				case request.Method == http.MethodPost && request.URL.Path == "/tx":
					payload, readErr := io.ReadAll(request.Body)
					if readErr != nil {
						http.Error(writer, readErr.Error(), http.StatusBadRequest)
						return
					}
					broadcastTx, readErr = deserializeTransaction(string(payload))
					if readErr != nil {
						http.Error(writer, readErr.Error(), http.StatusBadRequest)
						return
					}
					_, _ = writer.Write([]byte(broadcastTx.TxHash().String()))
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			walletClient := NewBTCWallet("bitcoin", server.URL)
			opts := DefaultTransferCoinOptions()
			opts.AddressType = addressType
			opts.FeeRateSatPerVByte = 2
			opts.RandomizeChange = false
			opts.WaitForConfirmation = false

			ok, txID, transferMessage := walletClient.TransferCoin(source.PrivateKey, destination.Address, "0.001", opts)
			if !ok {
				t.Fatalf("TransferCoin failed: %s", transferMessage)
			}
			if broadcastTx == nil {
				t.Fatal("transaction was not broadcast")
			}
			if txID != broadcastTx.TxHash().String() {
				t.Fatalf("txid = %s, want %s", txID, broadcastTx.TxHash())
			}
			if len(broadcastTx.TxIn) != 1 || len(broadcastTx.TxOut) != 2 {
				t.Fatalf("unexpected transaction shape: inputs=%d outputs=%d", len(broadcastTx.TxIn), len(broadcastTx.TxOut))
			}
			if broadcastTx.TxIn[0].Sequence != optInRBFSequence {
				t.Fatalf("sequence = %d, want %d", broadcastTx.TxIn[0].Sequence, optInRBFSequence)
			}

			foundDestination := false
			var outputTotal int64
			for _, output := range broadcastTx.TxOut {
				outputTotal += output.Value
				if output.Value == 100000 {
					foundDestination = true
				}
			}
			if !foundDestination {
				t.Fatal("destination output was not found")
			}
			fee := fundingValue - outputTotal
			virtualSize := mempool.GetTxVirtualSize(btcutil.NewTx(broadcastTx))
			if fee < 2*virtualSize {
				t.Fatalf("fee %d is below 2 sat/vB for vsize %d", fee, virtualSize)
			}

			verifySingleInput(t, broadcastTx, sourceScript, fundingValue)
			switch addressType {
			case AddressTypeLegacy:
				if len(broadcastTx.TxIn[0].SignatureScript) == 0 || len(broadcastTx.TxIn[0].Witness) != 0 {
					t.Fatal("legacy input has unexpected script or witness")
				}
			case AddressTypeP2SHSegWit:
				if len(broadcastTx.TxIn[0].SignatureScript) == 0 || len(broadcastTx.TxIn[0].Witness) != 2 {
					t.Fatal("nested segwit input has unexpected script or witness")
				}
			case AddressTypeSegWit:
				if len(broadcastTx.TxIn[0].SignatureScript) != 0 || len(broadcastTx.TxIn[0].Witness) != 2 {
					t.Fatal("segwit input has unexpected script or witness")
				}
			case AddressTypeTaproot:
				if len(broadcastTx.TxIn[0].SignatureScript) != 0 || len(broadcastTx.TxIn[0].Witness) != 1 {
					t.Fatal("taproot input has unexpected script or witness")
				}
			}
		})
	}
}

func TestTransferAllAddressTypes(t *testing.T) {
	offline := NewBTCWallet("bitcoin", "")
	_, destination, message := offline.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 2, AddressTypeSegWit)
	if destination == nil {
		t.Fatalf("derive destination failed: %s", message)
	}

	for testIndex, addressType := range []string{
		AddressTypeLegacy,
		AddressTypeP2SHSegWit,
		AddressTypeSegWit,
		AddressTypeTaproot,
	} {
		t.Run(addressType, func(t *testing.T) {
			ok, source, recoverMessage := offline.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 0, addressType)
			if !ok {
				t.Fatalf("derive source failed: %s", recoverMessage)
			}
			sourceScript := mustPayToAddressScript(t, offline, source.Address)
			firstFunding, firstHex := makeFundingTransaction(t, sourceScript, 120000, uint32(100+testIndex*2))
			secondFunding, secondHex := makeFundingTransaction(t, sourceScript, 80000, uint32(101+testIndex*2))
			firstTxID := firstFunding.TxHash().String()
			secondTxID := secondFunding.TxHash().String()
			var broadcastTx *wire.MsgTx

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/address/"+source.Address+"/utxo":
					writer.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(writer, `[
                        {"txid":%q,"vout":0,"value":120000,"status":{"confirmed":true}},
                        {"txid":%q,"vout":0,"value":80000,"status":{"confirmed":true}}
                    ]`, firstTxID, secondTxID)
				case request.Method == http.MethodGet && request.URL.Path == "/tx/"+firstTxID+"/hex":
					_, _ = writer.Write([]byte(firstHex))
				case request.Method == http.MethodGet && request.URL.Path == "/tx/"+secondTxID+"/hex":
					_, _ = writer.Write([]byte(secondHex))
				case request.Method == http.MethodPost && request.URL.Path == "/tx":
					payload, readErr := io.ReadAll(request.Body)
					if readErr != nil {
						http.Error(writer, readErr.Error(), http.StatusBadRequest)
						return
					}
					broadcastTx, readErr = deserializeTransaction(string(payload))
					if readErr != nil {
						http.Error(writer, readErr.Error(), http.StatusBadRequest)
						return
					}
					_, _ = writer.Write([]byte(broadcastTx.TxHash().String()))
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			walletClient := NewBTCWallet("bitcoin", server.URL)
			opts := DefaultTransferCoinOptions()
			opts.AddressType = addressType
			opts.FeeRateSatPerVByte = 2
			opts.WaitForConfirmation = false

			ok, txID, transferMessage := walletClient.TransferAll(source.PrivateKey, destination.Address, opts)
			if !ok {
				t.Fatalf("TransferAll failed: %s", transferMessage)
			}
			if broadcastTx == nil || txID != broadcastTx.TxHash().String() {
				t.Fatal("sweep transaction was not broadcast correctly")
			}
			if len(broadcastTx.TxIn) != 2 || len(broadcastTx.TxOut) != 1 {
				t.Fatalf("unexpected sweep shape: inputs=%d outputs=%d", len(broadcastTx.TxIn), len(broadcastTx.TxOut))
			}
			fee := int64(200000) - broadcastTx.TxOut[0].Value
			virtualSize := mempool.GetTxVirtualSize(btcutil.NewTx(broadcastTx))
			if fee < 2*virtualSize {
				t.Fatalf("fee %d is below 2 sat/vB for vsize %d", fee, virtualSize)
			}
			verifyInputs(t, broadcastTx, map[wire.OutPoint]*wire.TxOut{
				{Hash: firstFunding.TxHash(), Index: 0}:  firstFunding.TxOut[0],
				{Hash: secondFunding.TxHash(), Index: 0}: secondFunding.TxOut[0],
			})
		})
	}
}

func TestEstimateFeeUsesEsploraRate(t *testing.T) {
	offline := NewBTCWallet("bitcoin", "")
	_, source, _ := offline.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 0, AddressTypeSegWit)
	_, destination, _ := offline.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 1, AddressTypeSegWit)
	fundingTx, _ := makeFundingTransaction(t, mustPayToAddressScript(t, offline, source.Address), 500000, 20)
	fundingTxID := fundingTx.TxHash().String()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/address/" + source.Address + "/utxo":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `[{"txid":%q,"vout":0,"value":500000,"status":{"confirmed":true}}]`, fundingTxID)
		case "/fee-estimates":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"1":8,"3":2.5,"6":1.5}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	walletClient := NewBTCWallet("bitcoin", server.URL)
	opts := DefaultTransferCoinOptions()
	opts.FeeTargetBlocks = 3
	opts.FeeRateBuffer = 1.1
	ok, estimate, message := walletClient.EstimateFee(source.Address, destination.Address, "0.001", opts)
	if !ok {
		t.Fatalf("EstimateFee failed: %s", message)
	}
	if estimate.FeeRateSatPerVByte != 2.75 {
		t.Fatalf("fee rate = %v, want 2.75", estimate.FeeRateSatPerVByte)
	}
	if estimate.FeeSatoshi <= 0 || estimate.InputCount != 1 || estimate.OutputCount != 2 {
		t.Fatalf("unexpected estimate: %+v", estimate)
	}
}

func TestTransferCoinRejectsMismatchedPrevOutput(t *testing.T) {
	offline := NewBTCWallet("bitcoin", "")
	_, source, _ := offline.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 0, AddressTypeSegWit)
	_, destination, _ := offline.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 1, AddressTypeSegWit)
	sourceScript := mustPayToAddressScript(t, offline, source.Address)
	fundingTx, fundingHex := makeFundingTransaction(t, sourceScript, 499999, 30)
	fundingTxID := fundingTx.TxHash().String()
	broadcastCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/address/"+source.Address+"/utxo":
			_, _ = fmt.Fprintf(writer, `[{"txid":%q,"vout":0,"value":500000,"status":{"confirmed":true}}]`, fundingTxID)
		case request.Method == http.MethodGet && request.URL.Path == "/tx/"+fundingTxID+"/hex":
			_, _ = writer.Write([]byte(fundingHex))
		case request.Method == http.MethodPost && request.URL.Path == "/tx":
			broadcastCalled = true
			http.Error(writer, "must not broadcast", http.StatusBadRequest)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	walletClient := NewBTCWallet("bitcoin", server.URL)
	opts := DefaultTransferCoinOptions()
	opts.FeeRateSatPerVByte = 2
	ok, _, message := walletClient.TransferCoin(source.PrivateKey, destination.Address, "0.001", opts)
	if ok || !strings.Contains(message, "value mismatch") {
		t.Fatalf("expected value mismatch, got ok=%v message=%q", ok, message)
	}
	if broadcastCalled {
		t.Fatal("invalid transaction was broadcast")
	}
}

func TestWaitForTransactionReceipt(t *testing.T) {
	txID := strings.Repeat("ab", 32)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tx/"+txID+"/status" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"confirmed":true,"block_height":800001,"block_hash":"block","block_time":1700000000}`))
	}))
	defer server.Close()

	walletClient := NewBTCWallet("bitcoin", server.URL)
	ok, receipt, message := walletClient.waitForTransactionReceipt(txID, time.Second, time.Millisecond)
	if !ok {
		t.Fatalf("waitForTransactionReceipt failed: %s", message)
	}
	if receipt["confirmed"] != true || receipt["block_height"] != int64(800001) {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestParseBTCAmount(t *testing.T) {
	tests := map[string]int64{
		"1":          100000000,
		"0.00000001": 1,
		"12.34":      1234000000,
		"+0.1":       10000000,
	}
	for input, expected := range tests {
		actual, err := parseBTCAmount(input)
		if err != nil {
			t.Fatalf("parseBTCAmount(%q) failed: %v", input, err)
		}
		if actual != expected {
			t.Fatalf("parseBTCAmount(%q) = %d, want %d", input, actual, expected)
		}
	}
	for _, invalid := range []string{"", "-1", "1.000000001", "1.", "abc"} {
		if _, err := parseBTCAmount(invalid); err == nil {
			t.Fatalf("parseBTCAmount(%q) unexpectedly succeeded", invalid)
		}
	}
}

func makeFundingTransaction(t *testing.T, pkScript []byte, value int64, nonce uint32) (*wire.MsgTx, string) {
	t.Helper()
	var previousHash chainhash.Hash
	previousHash[0] = byte(nonce)
	previousHash[1] = byte(nonce >> 8)
	tx := wire.NewMsgTx(wire.TxVersion)
	input := wire.NewTxIn(wire.NewOutPoint(&previousHash, nonce), []byte{0x01, byte(nonce)}, nil)
	tx.AddTxIn(input)
	tx.AddTxOut(wire.NewTxOut(value, pkScript))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	return tx, hex.EncodeToString(raw.Bytes())
}

func mustPayToAddressScript(t *testing.T, walletClient *BTCWallet, address string) []byte {
	t.Helper()
	decoded, err := btcutil.DecodeAddress(address, walletClient.params)
	if err != nil {
		t.Fatal(err)
	}
	script, err := txscript.PayToAddrScript(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return script
}

func verifySingleInput(t *testing.T, tx *wire.MsgTx, previousScript []byte, previousValue int64) {
	t.Helper()
	prevOutFetcher := txscript.NewCannedPrevOutputFetcher(previousScript, previousValue)
	hashCache := txscript.NewTxSigHashes(tx, prevOutFetcher)
	engine, err := txscript.NewEngine(
		previousScript,
		tx,
		0,
		txscript.StandardVerifyFlags,
		nil,
		hashCache,
		previousValue,
		prevOutFetcher,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Execute(); err != nil {
		t.Fatalf("signed input validation failed: %v", err)
	}
}

func verifyInputs(t *testing.T, tx *wire.MsgTx, previousOutputs map[wire.OutPoint]*wire.TxOut) {
	t.Helper()
	fetcher := txscript.NewMultiPrevOutFetcher(previousOutputs)
	hashCache := txscript.NewTxSigHashes(tx, fetcher)
	for index, input := range tx.TxIn {
		previousOutput := previousOutputs[input.PreviousOutPoint]
		if previousOutput == nil {
			t.Fatalf("missing previous output for input %d", index)
		}
		engine, err := txscript.NewEngine(
			previousOutput.PkScript,
			tx,
			index,
			txscript.StandardVerifyFlags,
			nil,
			hashCache,
			previousOutput.Value,
			fetcher,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err = engine.Execute(); err != nil {
			t.Fatalf("signed input %d validation failed: %v", index, err)
		}
	}
}
