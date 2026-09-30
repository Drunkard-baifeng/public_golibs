package solana

import "testing"

func TestGenerateRecoverAndSignWallet(t *testing.T) {
	walletClient := NewSolanaWallet("")

	ok, generated, message := walletClient.GenerateWallet(0)
	if !ok {
		t.Fatalf("GenerateWallet failed: %s", message)
	}
	if generated.Address == "" || generated.PrivateKey == "" || generated.Mnemonic == "" {
		t.Fatal("generated wallet has empty fields")
	}
	if generated.DerivationPath != "m/44'/501'/0'/0'" {
		t.Fatalf("unexpected derivation path: %s", generated.DerivationPath)
	}

	ok, recovered, message := walletClient.RecoverWalletFromMnemonic(generated.Mnemonic, 0)
	if !ok {
		t.Fatalf("RecoverWalletFromMnemonic failed: %s", message)
	}
	if recovered.Address != generated.Address || recovered.PrivateKey != generated.PrivateKey {
		t.Fatal("mnemonic recovery returned a different wallet")
	}

	ok, fromPrivateKey, message := walletClient.RecoverWalletFromPrivateKey(generated.PrivateKey)
	if !ok {
		t.Fatalf("RecoverWalletFromPrivateKey failed: %s", message)
	}
	if fromPrivateKey.Address != generated.Address {
		t.Fatal("private key recovery returned a different address")
	}

	ok, signed, message := walletClient.SignMessage(generated.PrivateKey, "hello solana")
	if !ok {
		t.Fatalf("SignMessage failed: %s", message)
	}
	verified, verifyMessage := walletClient.VerifySignature("hello solana", signed.Signature, generated.Address)
	if !verified {
		t.Fatalf("VerifySignature failed: %s", verifyMessage)
	}
	verified, _ = walletClient.VerifySignature("changed message", signed.Signature, generated.Address)
	if verified {
		t.Fatal("signature unexpectedly verified for a different message")
	}
}

func TestMnemonicIndexDerivation(t *testing.T) {
	walletClient := NewSolanaWallet("")
	ok, first, message := walletClient.GenerateWallet(0)
	if !ok {
		t.Fatalf("GenerateWallet failed: %s", message)
	}
	ok, second, message := walletClient.RecoverWalletFromMnemonic(first.Mnemonic, 1)
	if !ok {
		t.Fatalf("RecoverWalletFromMnemonic index 1 failed: %s", message)
	}
	if first.Address == second.Address {
		t.Fatal("different derivation indexes returned the same address")
	}
	if second.DerivationPath != "m/44'/501'/1'/0'" {
		t.Fatalf("unexpected index 1 derivation path: %s", second.DerivationPath)
	}
}

func TestAmountConversion(t *testing.T) {
	tests := []struct {
		amount   string
		decimals int
		want     uint64
	}{
		{"1", 9, 1_000_000_000},
		{"0.000000001", 9, 1},
		{"12.34", 6, 12_340_000},
	}
	for _, test := range tests {
		got, err := parseUnitsUint64(test.amount, test.decimals)
		if err != nil {
			t.Fatalf("parseUnitsUint64(%q) failed: %v", test.amount, err)
		}
		if got != test.want {
			t.Fatalf("parseUnitsUint64(%q) = %d, want %d", test.amount, got, test.want)
		}
	}
	if _, err := parseUnitsUint64("0.0000000001", 9); err == nil {
		t.Fatal("expected excessive decimal precision to fail")
	}
}

func TestOfflineClientState(t *testing.T) {
	walletClient := NewSolanaWallet("")
	if walletClient.Connected() {
		t.Fatal("wallet without RPC URL should not be connected")
	}
	ok, _, message := walletClient.GetBalance("11111111111111111111111111111111")
	if ok || message == "" {
		t.Fatal("GetBalance should fail without an RPC client")
	}
}
