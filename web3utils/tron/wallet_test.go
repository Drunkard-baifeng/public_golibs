package tron

import (
	"math/big"
	"strings"
	"testing"

	"github.com/fbsobreira/gotron-sdk/pkg/proto/core"
)

func TestRecoverWalletRoundTrip(t *testing.T) {
	wallet := &TronWallet{}
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

	ok, recovered, message := wallet.RecoverWalletFromMnemonic(mnemonic, 0)
	if !ok {
		t.Fatalf("RecoverWalletFromMnemonic failed: %s", message)
	}
	if !strings.HasPrefix(recovered.Address, "T") {
		t.Fatalf("unexpected TRON address: %s", recovered.Address)
	}

	ok, fromPrivateKey, message := wallet.RecoverWalletFromPrivateKey(recovered.PrivateKey)
	if !ok {
		t.Fatalf("RecoverWalletFromPrivateKey failed: %s", message)
	}
	if fromPrivateKey.Address != recovered.Address {
		t.Fatalf("address mismatch: mnemonic=%s private_key=%s", recovered.Address, fromPrivateKey.Address)
	}
}

func TestSignAndVerifyMessage(t *testing.T) {
	wallet := &TronWallet{}
	privateKey := "0x8185ae198672f631cf1d2c9447404353ff4597eff0603134d73918594f7424b4"

	ok, recovered, message := wallet.RecoverWalletFromPrivateKey(privateKey)
	if !ok {
		t.Fatalf("RecoverWalletFromPrivateKey failed: %s", message)
	}
	ok, signature, message := wallet.SignMessage(privateKey, "hello tron")
	if !ok {
		t.Fatalf("SignMessage failed: %s", message)
	}
	verified, message := wallet.VerifySignature("hello tron", signature.Signature, recovered.Address)
	if !verified {
		t.Fatalf("VerifySignature failed: %s", message)
	}
	verified, _ = wallet.VerifySignature("changed", signature.Signature, recovered.Address)
	if verified {
		t.Fatal("signature unexpectedly verified for a different message")
	}
}

func TestParseAndFormatUnits(t *testing.T) {
	value, err := parseUnits("12.345678", 6)
	if err != nil {
		t.Fatal(err)
	}
	if value.Cmp(big.NewInt(12_345_678)) != 0 {
		t.Fatalf("unexpected value: %s", value)
	}
	if formatted := formatUnits(value, 6); formatted != "12.345678" {
		t.Fatalf("unexpected formatted value: %s", formatted)
	}
	if _, err = parseUnits("1.0000001", 6); err == nil {
		t.Fatal("expected precision error")
	}
}

func TestGenerateWallet(t *testing.T) {
	wallet := &TronWallet{}
	ok, generated, message := wallet.GenerateWallet(3)
	if !ok {
		t.Fatalf("GenerateWallet failed: %s", message)
	}
	if generated.Index != 3 {
		t.Fatalf("unexpected index: %d", generated.Index)
	}
	if words := len(strings.Fields(generated.Mnemonic)); words != 12 {
		t.Fatalf("expected 12 words, got %d", words)
	}
}

func TestTransactionInfoSucceededForTRXDefaultReceipt(t *testing.T) {
	info := &core.TransactionInfo{
		Result: core.TransactionInfo_SUCESS,
		Receipt: &core.ResourceReceipt{
			Result: core.Transaction_Result_DEFAULT,
		},
	}
	if !transactionInfoSucceeded(info) {
		t.Fatal("successful TRX transfer with DEFAULT receipt was treated as failed")
	}
	if status := transactionInfoMap("test", info)["status"]; status != "SUCCESS" {
		t.Fatalf("unexpected status: %v", status)
	}
}

func TestTransactionInfoFailedForContractFailure(t *testing.T) {
	info := &core.TransactionInfo{
		Result: core.TransactionInfo_SUCESS,
		Receipt: &core.ResourceReceipt{
			Result: core.Transaction_Result_REVERT,
		},
	}
	if transactionInfoSucceeded(info) {
		t.Fatal("reverted contract transaction was treated as successful")
	}
}
