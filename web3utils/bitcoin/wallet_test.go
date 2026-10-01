package bitcoin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testMnemonic = "swing kitchen wreck slim filter name tennis spell garden fog seek science"

func TestMnemonicAddressTypes(t *testing.T) {
	walletClient := NewBTCWallet("bitcoin", "")
	tests := []struct {
		addressType string
		address     string
		privateKey  string
		path        string
	}{
		{AddressTypeLegacy, "17DNjRDbdAjjTS8w4fT7MdZgSXHhfSaVQe", "L4yWrKhVK9QzTnRJ21vodTUnzWwtHGtcdVnDM883uGUvVLEQaPpi", "m/44'/0'/0'/0/0"},
		{AddressTypeP2SHSegWit, "36hynDoe6w2EJnRsDvnAv7kZvN5Tw2EXKK", "KzntKXANZpb9VZTM87Ewea2P9kapviKpMoyWYMRmmkorH4MARJUy", "m/49'/0'/0'/0/0"},
		{AddressTypeSegWit, "bc1qr6hkxdel6h7ww3y6f0lwt8rs8kvaw2m3hmzykx", "L3nt3dzC6t84PkKj6Rx7rWD56gcX9YS2H3jZ4NNv5Es1fL7aHGgx", "m/84'/0'/0'/0/0"},
		{AddressTypeTaproot, "bc1psce69jzm8x9za2d68fyr3y9cesewxuqq0ue2m70ywlt4vg0ev86spw53l3", "KwoquikkhFXnA2V5i5NCC2c6sYtJa8nE8qgii7xmvZfYTTV97NHV", "m/86'/0'/0'/0/0"},
	}
	for _, test := range tests {
		t.Run(test.addressType, func(t *testing.T) {
			ok, wallet, message := walletClient.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 0, test.addressType)
			if !ok {
				t.Fatalf("RecoverWalletFromMnemonicWithAddressType failed: %s", message)
			}
			if wallet.Address != test.address {
				t.Fatalf("address = %s, want %s", wallet.Address, test.address)
			}
			if wallet.PrivateKey != test.privateKey {
				t.Fatalf("private key = %s, want %s", wallet.PrivateKey, test.privateKey)
			}
			if wallet.DerivationPath != test.path {
				t.Fatalf("path = %s, want %s", wallet.DerivationPath, test.path)
			}

			ok, recovered, message := walletClient.RecoverWalletFromPrivateKeyWithAddressType(wallet.PrivateKey, test.addressType)
			if !ok {
				t.Fatalf("RecoverWalletFromPrivateKeyWithAddressType failed: %s", message)
			}
			if recovered.Address != wallet.Address {
				t.Fatalf("recovered address = %s, want %s", recovered.Address, wallet.Address)
			}
		})
	}
}

func TestDefaultAddressTypeAndIndex(t *testing.T) {
	walletClient := NewBTCWallet("testnet", "")
	ok, first, message := walletClient.RecoverWalletFromMnemonic(testMnemonic, 0)
	if !ok {
		t.Fatalf("RecoverWalletFromMnemonic failed: %s", message)
	}
	if first.AddressType != AddressTypeSegWit || first.DerivationPath != "m/84'/1'/0'/0/0" {
		t.Fatalf("unexpected default wallet: %+v", first)
	}
	ok, second, message := walletClient.RecoverWalletFromMnemonic(testMnemonic, 1)
	if !ok {
		t.Fatalf("RecoverWalletFromMnemonic index 1 failed: %s", message)
	}
	if second.Address == first.Address {
		t.Fatal("different indexes returned the same address")
	}
}

func TestSignAndVerifyAllAddressTypes(t *testing.T) {
	walletClient := NewBTCWallet("bitcoin", "")
	for _, addressType := range []string{AddressTypeLegacy, AddressTypeP2SHSegWit, AddressTypeSegWit, AddressTypeTaproot} {
		t.Run(addressType, func(t *testing.T) {
			ok, wallet, message := walletClient.RecoverWalletFromMnemonicWithAddressType(testMnemonic, 0, addressType)
			if !ok {
				t.Fatalf("recover failed: %s", message)
			}
			ok, signed, message := walletClient.SignMessage(wallet.PrivateKey, "hello bitcoin")
			if !ok {
				t.Fatalf("sign failed: %s", message)
			}
			verified, message := walletClient.VerifySignature("hello bitcoin", signed.Signature, wallet.Address)
			if !verified {
				t.Fatalf("verify failed: %s", message)
			}
			verified, _ = walletClient.VerifySignature("changed", signed.Signature, wallet.Address)
			if verified {
				t.Fatal("signature unexpectedly verified for changed message")
			}
		})
	}
}

func TestGetBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/address/bc1qr6hkxdel6h7ww3y6f0lwt8rs8kvaw2m3hmzykx" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
            "chain_stats":{"funded_txo_sum":150000000,"spent_txo_sum":25000000},
            "mempool_stats":{"funded_txo_sum":1000,"spent_txo_sum":200}
        }`))
	}))
	defer server.Close()

	walletClient := NewBTCWallet("bitcoin", server.URL)
	ok, balance, message := walletClient.GetBalance("bc1qr6hkxdel6h7ww3y6f0lwt8rs8kvaw2m3hmzykx")
	if !ok {
		t.Fatalf("GetBalance failed: %s", message)
	}
	if balance != "1.25" {
		t.Fatalf("balance = %s, want 1.25", balance)
	}
	ok, info, message := walletClient.GetBalanceInfo("bc1qr6hkxdel6h7ww3y6f0lwt8rs8kvaw2m3hmzykx")
	if !ok {
		t.Fatalf("GetBalanceInfo failed: %s", message)
	}
	if info.ConfirmedSatoshi != 125000000 || info.UnconfirmedSatoshi != 800 || info.TotalSatoshi != 125000800 {
		t.Fatalf("unexpected balance info: %+v", info)
	}
}

func TestOfflineAndNetworkValidation(t *testing.T) {
	walletClient := NewBTCWallet("bitcoin", "")
	if walletClient.Connected() {
		t.Fatal("wallet with an empty API URL should be offline")
	}
	if ok, _ := walletClient.ValidateAddress("tb1qfmh0whrkqks92ax0p7x6r5clj4a0d9ncalqz93"); ok {
		t.Fatal("testnet address unexpectedly validated on mainnet")
	}
	if ok, _, message := walletClient.GetBalance("bc1qr6hkxdel6h7ww3y6f0lwt8rs8kvaw2m3hmzykx"); ok || message == "" {
		t.Fatal("offline GetBalance should fail with a message")
	}
}
