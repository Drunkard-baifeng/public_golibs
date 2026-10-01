package main

import (
	"fmt"
	"os"

	"github.com/Drunkard-baifeng/public_golibs/web3utils/bitcoin"
)

// BitcoinWalletCall 演示 BTC 离线钱包派生、私钥恢复和消息签名。
func BitcoinWalletCall() {
	// 传入空 API URL，此示例全程不访问网络。
	walletClient := bitcoin.NewBTCWallet("bitcoin", "")
	defer walletClient.Close()

	// 公开测试助记词，不得用于存放真实资产。
	mnemonic := "swing kitchen wreck slim filter name tennis spell garden fog seek science"
	addressTypes := []string{
		bitcoin.AddressTypeLegacy,
		bitcoin.AddressTypeP2SHSegWit,
		bitcoin.AddressTypeSegWit,
		bitcoin.AddressTypeTaproot,
	}

	var segwitWallet *bitcoin.Wallet
	for _, addressType := range addressTypes {
		ok, wallet, msg := walletClient.RecoverWalletFromMnemonicWithAddressType(mnemonic, 0, addressType)
		if !ok {
			fmt.Printf("recover %s wallet failed: %s\n", addressType, msg)
			return
		}
		fmt.Printf("type=%s address=%s private=%s path=%s\n",
			wallet.AddressType,
			wallet.Address,
			wallet.PrivateKey,
			wallet.DerivationPath,
		)
		if addressType == bitcoin.AddressTypeSegWit {
			segwitWallet = wallet
		}
	}

	if segwitWallet == nil {
		fmt.Println("segwit wallet was not derived")
		return
	}

	ok, recovered, msg := walletClient.RecoverWalletFromPrivateKeyWithAddressType(
		segwitWallet.PrivateKey,
		bitcoin.AddressTypeSegWit,
	)
	if !ok {
		fmt.Println("RecoverWalletFromPrivateKeyWithAddressType failed:", msg)
		return
	}
	if recovered.Address != segwitWallet.Address {
		fmt.Println("private key recovery returned a different address")
		return
	}
	fmt.Println("recover by private:", recovered.Address)

	signText := "RichWorld中国"
	ok, signResult, msg := walletClient.SignMessage(segwitWallet.PrivateKey, signText)
	if !ok {
		fmt.Println("SignMessage failed:", msg)
		return
	}
	if signResult == nil {
		fmt.Println("SignMessage returned an empty result")
		return
	}
	fmt.Println("signature:", signResult.Signature)
	fmt.Println("signature_r:", signResult.SignatureR)
	fmt.Println("signature_s:", signResult.SignatureS)
	fmt.Println("signature_v:", signResult.SignatureV)

	ok, msg = walletClient.VerifySignature(signText, signResult.Signature, segwitWallet.Address)
	fmt.Println("verify:", ok, msg)
}

// BitcoinWalletGenerateCall 生成一个全新的 BIP84 Native SegWit 钱包。
func BitcoinWalletGenerateCall() {
	walletClient := bitcoin.NewBTCWallet("bitcoin", "")
	defer walletClient.Close()

	ok, wallet, msg := walletClient.GenerateWallet(0)
	if !ok {
		fmt.Println("GenerateWallet failed:", msg)
		return
	}
	if wallet == nil {
		fmt.Println("GenerateWallet returned an empty wallet")
		return
	}
	fmt.Println("address:", wallet.Address)
	fmt.Println("private:", wallet.PrivateKey)
	fmt.Println("mnemonic:", wallet.Mnemonic)
	fmt.Println("derivation path:", wallet.DerivationPath)
	fmt.Println("warning: this example prints secrets; do not use them for a funded wallet")
}

// BitcoinWalletOnlineCall 通过默认 Blockstream Esplora API 查询 BTC 余额。
func BitcoinWalletOnlineCall() {
	address := "bc1psce69jzm8x9za2d68fyr3y9cesewxuqq0ue2m70ywlt4vg0ev86spw53l3"

	bitcoin.InitDefaultBTC("bitcoin")
	defer func() { _ = bitcoin.CloseDefaultBTC() }()

	ok, balance, msg := bitcoin.GetBalanceDefault(address)
	fmt.Println("connected:", bitcoin.DefaultBTC().Connected(), "network:", bitcoin.DefaultBTC().Network())
	fmt.Println("confirmed balance:", ok, balance, msg)

	ok, info, msg := bitcoin.GetBalanceInfoDefault(address)
	if !ok {
		fmt.Println("balance info failed:", msg)
		return
	}
	fmt.Printf("confirmed=%s BTC unconfirmed=%s BTC total=%s BTC\n",
		info.ConfirmedBTC,
		info.UnconfirmedBTC,
		info.TotalBTC,
	)
}

// BitcoinWalletTransferCoinCall 演示 BTC 主网转账。
func BitcoinWalletTransferCoinCall() {
	privateKey, toAddress, ok := bitcoinTransferEnvironment()
	if !ok {
		return
	}
	amount := "0.0001"

	bitcoin.InitDefaultBTC("bitcoin")
	defer func() { _ = bitcoin.CloseDefaultBTC() }()

	opts := bitcoin.DefaultTransferCoinOptions()
	opts.AddressType = bitcoin.AddressTypeTaproot
	opts.FeeTargetBlocks = 3
	opts.WaitForConfirmation = true
	opts.ConfirmationTimeoutS = 1800

	fromAddress := recoverBTCAddress(privateKey, opts.AddressType)
	ok, fee, msg := bitcoin.EstimateFeeDefault(fromAddress, toAddress, amount, opts)
	if !ok {
		fmt.Println("estimate BTC fee failed:", msg)
		return
	}
	fmt.Printf("fee=%s BTC rate=%.8f sat/vB inputs=%d change=%s BTC\n",
		fee.FeeBTC,
		fee.FeeRateSatPerVByte,
		fee.InputCount,
		fee.ChangeBTC,
	)

	ok, txID, msg := bitcoin.TransferCoinDefault(privateKey, toAddress, amount, opts)
	fmt.Println("transfer BTC:", ok, txID, msg)
}

// BitcoinWalletTransferAllCall 查询余额并将全部可花余额扣除手续费后转出。
func BitcoinWalletTransferAllCall() {
	privateKey, toAddress, ok := bitcoinTransferEnvironment()
	if !ok {
		return
	}

	bitcoin.InitDefaultBTC("bitcoin")
	defer func() { _ = bitcoin.CloseDefaultBTC() }()

	opts := bitcoin.DefaultTransferCoinOptions()
	opts.AddressType = bitcoin.AddressTypeTaproot
	opts.WaitForConfirmation = false

	fromAddress := recoverBTCAddress(privateKey, opts.AddressType)
	ok, info, msg := bitcoin.GetBalanceInfoDefault(fromAddress)
	if !ok {
		fmt.Println("get BTC balance failed:", msg)
		return
	}
	fmt.Printf("from=%s confirmed=%s BTC unconfirmed=%s BTC total=%s BTC\n",
		fromAddress,
		info.ConfirmedBTC,
		info.UnconfirmedBTC,
		info.TotalBTC,
	)

	ok, txID, msg := bitcoin.TransferAllDefault(privateKey, toAddress, opts)
	fmt.Println("transfer all BTC:", ok, txID, msg)
}

func bitcoinTransferEnvironment() (string, string, bool) {
	privateKey := os.Getenv("BTC_PRIVATE_KEY")
	toAddress := os.Getenv("BTC_TO_ADDRESS")
	if privateKey == "" || toAddress == "" {
		fmt.Println("BTC transfer skipped: set BTC_PRIVATE_KEY and BTC_TO_ADDRESS")
		return "", "", false
	}
	return privateKey, toAddress, true
}

func recoverBTCAddress(privateKey string, addressType string) string {
	walletClient := bitcoin.NewBTCWallet("bitcoin", "")
	defer walletClient.Close()
	ok, wallet, msg := walletClient.RecoverWalletFromPrivateKeyWithAddressType(privateKey, addressType)
	if !ok || wallet == nil {
		panic("recover BTC wallet failed: " + msg)
	}
	return wallet.Address
}
