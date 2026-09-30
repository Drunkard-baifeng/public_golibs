package main

import (
	"fmt"

	"github.com/Drunkard-baifeng/public_golibs/web3utils/tron"
)

func TronWalletCall() {
	walletClient := tron.NewTronWallet("mainnet", "")
	defer walletClient.Close()

	// ok, wallet, msg := walletClient.GenerateWallet(0)
	// if !ok {
	// 	fmt.Println("GenerateWallet failed:", msg)
	// 	return
	// }
	// fmt.Println("address:", wallet.Address)
	// fmt.Println("private:", wallet.PrivateKey)
	// fmt.Println("mnemonic:", wallet.Mnemonic)

	// Public BIP39 test vector; do not replace this with a production mnemonic.
	// mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	// ok, walletByMnemonic, msg := walletClient.RecoverWalletFromMnemonic(mnemonic, 0)
	// if !ok {
	// 	fmt.Println("RecoverWalletFromMnemonic failed:", msg)
	// 	return
	// }
	// fmt.Println("recover by mnemonic:", walletByMnemonic.Address)

	// ok, walletByPrivate, msg := walletClient.RecoverWalletFromPrivateKey(walletByMnemonic.PrivateKey)
	// if !ok {
	// 	fmt.Println("RecoverWalletFromPrivateKey failed:", msg)
	// 	return
	// }
	// fmt.Println("recover by private:", walletByPrivate.Address)
	// fmt.Println("private:", walletByPrivate.PrivateKey)

	// Public test key; never use it for a funded account.
	privateKey := "0x8185ae198672f631cf1d2c9447404353ff4597eff0603134d73918594f7424b4"
	ok, recoveredWallet, msg := walletClient.RecoverWalletFromPrivateKey(privateKey)
	if !ok {
		fmt.Println("RecoverWalletFromPrivateKey failed:", msg)
		return
	}

	signText := "RichWorld中国"
	ok, signResult, msg := walletClient.SignMessage(privateKey, signText)
	if !ok {
		fmt.Println("SignMessage failed:", msg)
		return
	}
	fmt.Println("address:", recoveredWallet.Address)
	fmt.Println("signature:", signResult.Signature)
	fmt.Println("signature_r:", signResult.SignatureR)
	fmt.Println("signature_s:", signResult.SignatureS)
	fmt.Println("signature_v:", signResult.SignatureV)

	ok, msg = walletClient.VerifySignature(signText, signResult.Signature, recoveredWallet.Address)
	fmt.Println("verify:", ok, msg)
}

func TronWalletOnlineCall() {
	apiKey := "" // 可选：填写 TronGrid TRON-PRO-API-KEY
	accountAddress := "TUFxc4XMFkXHsxZ42nDLd353dhJ4HQPmGS"

	tron.InitDefaultTron("mainnet", apiKey)
	defer func() { _ = tron.CloseDefaultTron() }()

	ok, balance, msg := tron.GetBalanceDefault(accountAddress)
	fmt.Println("connected:", tron.DefaultTron().Connected(), "chain:", tron.DefaultTron().ChainName, tron.DefaultTron().NativeToken)
	fmt.Println("balance:", ok, balance, msg)

	ok, account, msg := tron.GetAccountInfoDefault(accountAddress)
	fmt.Println("account info:", ok, account, msg)
}

func TronWalletGetTokenBalanceCall() {
	apiKey := "" // 可选：填写 TronGrid TRON-PRO-API-KEY
	accountAddress := "TUFxc4XMFkXHsxZ42nDLd353dhJ4HQPmGS"
	tokenContract := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t" // TRON-USDT

	tron.InitDefaultTron("mainnet", apiKey)
	defer func() { _ = tron.CloseDefaultTron() }()

	ok, tokenInfo, msg := tron.GetTokenInfoDefault(tokenContract)
	fmt.Println("token info:", ok, tokenInfo, msg)

	ok, balance, msg := tron.GetTokenBalanceDefault(accountAddress, tokenContract)
	fmt.Println("connected:", tron.DefaultTron().Connected(), "chain:", tron.DefaultTron().ChainName, tron.DefaultTron().NativeToken)
	fmt.Println("token balance:", ok, balance, msg)
}

func TronWalletHistoryCall() {
	apiKey := "" // TronGrid 历史接口建议填写 API Key
	accountAddress := "TUFxc4XMFkXHsxZ42nDLd353dhJ4HQPmGS"
	tokenContract := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t" // TRON-USDT

	tron.InitDefaultTron("mainnet", apiKey)
	defer func() { _ = tron.CloseDefaultTron() }()

	ok, trxTransfers, msg := tron.GetTRXTransfersDefault(accountAddress, 20, true)
	fmt.Println("TRX transfers:", ok, msg)
	for _, transfer := range trxTransfers {
		fmt.Printf("tx=%s from=%s to=%s amount=%s direction=%s status=%s\n",
			transfer.TransactionID,
			transfer.From,
			transfer.To,
			transfer.Amount,
			transfer.Direction,
			transfer.Status,
		)
	}

	ok, tokenTransfers, msg := tron.GetTokenTransfersDefault(accountAddress, tokenContract, 20, true)
	fmt.Println("TRC20 transfers:", ok, msg)
	for _, transfer := range tokenTransfers {
		fmt.Printf("tx=%s token=%s from=%s to=%s amount=%s direction=%s\n",
			transfer.TransactionID,
			transfer.TokenSymbol,
			transfer.From,
			transfer.To,
			transfer.Amount,
			transfer.Direction,
		)
	}
}

func TronWalletTransferCoinCall() {
	apiKey := ""     // 可选：填写 TronGrid TRON-PRO-API-KEY
	privateKey := "" // 填你的私钥（0x开头或不带都行）
	toAddress := "TUFxc4XMFkXHsxZ42nDLd353dhJ4HQPmGS"
	amount := "144.657396"

	if privateKey == "" {
		fmt.Println("TronWalletTransferCoinCall skip: privateKey is empty")
		return
	}

	tron.InitDefaultTron("mainnet", apiKey)
	defer func() { _ = tron.CloseDefaultTron() }()

	opts := tron.DefaultTransferCoinOptions()
	opts.WaitForConfirmation = true
	opts.ConfirmationTimeoutS = 120

	ok, txID, msg := tron.TransferCoinDefault(privateKey, toAddress, amount, opts)
	fmt.Println("transfer TRX:", ok, txID, msg)
}

func TronWalletTransferTokenCall() {
	apiKey := ""     // 可选：填写 TronGrid TRON-PRO-API-KEY
	privateKey := "" // 填你的私钥（0x开头或不带都行）
	toAddress := "TMuA6YqfCeX8EhbfYEg5y7S4DqzSJireY9"
	tokenContract := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t" // TRON-USDT
	amount := "1"

	if privateKey == "" {
		fmt.Println("TronWalletTransferTokenCall skip: privateKey is empty")
		return
	}

	tron.InitDefaultTron("mainnet", apiKey)
	defer func() { _ = tron.CloseDefaultTron() }()

	opts := tron.DefaultTransferTokenOptions()
	opts.AutoFeeLimit = true
	opts.FeeLimitBuffer = 1.5
	opts.WaitForConfirmation = true
	opts.ConfirmationTimeoutS = 120

	ok, txID, msg := tron.TransferTokenDefault(privateKey, toAddress, amount, tokenContract, opts)
	fmt.Println("transfer TRC20:", ok, txID, msg)
}
