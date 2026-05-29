package main

import (
	"fmt"

	"github.com/Drunkard-baifeng/public_golibs/web3utils"
)

func Web3UtilsCall() {
	evm := web3utils.NewEVMWallet("", 0)
	defer evm.Close()

	// ok, wallet, msg := evm.GenerateWallet(0)
	// if !ok {
	// 	fmt.Println("GenerateWallet failed:", msg)
	// 	return
	// }
	// fmt.Println("address:", wallet.Address)
	// fmt.Println("private:", wallet.PrivateKey)
	// fmt.Println("mnemonic:", wallet.Mnemonic)

	// mnemonic := "tiger jelly liberty tree obvious prevent desk hint sentence onion lady display"
	// ok, walletByMnemonic, msg := evm.RecoverWalletFromMnemonic(mnemonic, 0)
	// if !ok {
	// 	fmt.Println("RecoverWalletFromMnemonic failed:", msg)
	// 	return
	// }
	// fmt.Println("recover by mnemonic:", walletByMnemonic.Address)

	// ok, walletByPrivate, msg := evm.RecoverWalletFromPrivateKey(walletByMnemonic.PrivateKey)
	// if !ok {
	// 	fmt.Println("RecoverWalletFromPrivateKey failed:", msg)
	// 	return
	// }
	// fmt.Println("recover by private:", walletByPrivate.Address)
	// fmt.Println("private:", walletByPrivate.PrivateKey)

	signText := "RichWorld中国"
	ok, signResult, msg := evm.SignMessage("0xa0cae50832449d0f7d38f7fed002066f1e04b467d782dc5cb908e2100c647997", signText)
	if !ok {
		fmt.Println("SignMessage failed:", msg)
		return
	}
	fmt.Println("signature:", signResult.Signature)
	fmt.Println("signature_r:", signResult.SignatureR)
	fmt.Println("signature_s:", signResult.SignatureS)
	fmt.Println("signature_v:", signResult.SignatureV)

	ok, msg = evm.VerifySignature(signText, signResult.Signature, "0xD63FA832915ec48c837BAab82dFaFf35699C02cb")
	fmt.Println("verify:", ok, msg)
}

func Web3UtilsOnlineCall() {
	rpcURL := "https://bsc-dataseed1.binance.org"
	address := "0xc2b44a674bd4e7d9a50783a85f40744d37049eb4"

	web3utils.InitDefaultEVM(rpcURL, 56)
	defer func() { _ = web3utils.CloseDefaultEVM() }()

	ok, balance, msg := web3utils.GetBalanceDefault(address)
	fmt.Println("connected:", web3utils.DefaultEVM().Connected(), "chain:", web3utils.DefaultEVM().ChainName, web3utils.DefaultEVM().NativeToken)
	fmt.Println("balance:", ok, balance, msg)
}

func Web3UtilsGetTokenBalanceCall() {
	rpcURL := "https://bsc-dataseed1.binance.org"
	userAddress := "0xc2b44a674bd4e7d9a50783a85f40744d37049eb4"
	tokenContract := "0x55d398326f99059fF775485246999027B3197955" // BSC-USDT

	web3utils.InitDefaultEVM(rpcURL, 56)
	defer func() { _ = web3utils.CloseDefaultEVM() }()

	ok, balance, msg := web3utils.GetTokenBalanceDefault(userAddress, tokenContract, "")
	fmt.Println("connected:", web3utils.DefaultEVM().Connected(), "chain:", web3utils.DefaultEVM().ChainName, web3utils.DefaultEVM().NativeToken)
	fmt.Println("token balance:", ok, balance, msg)
}

func Web3UtilsTransferCoinCall() {
	rpcURL := "https://bsc-dataseed1.binance.org"
	privateKey := "" // 填你的私钥（0x开头或不带都行）
	toAddress := "0xa5fbb44ce14e1d784e0305804cbe2f4368952931"
	amount := "0.0001"

	if privateKey == "" {
		fmt.Println("Web3UtilsTransferCoinCall skip: privateKey is empty")
		return
	}

	web3utils.InitDefaultEVM(rpcURL, 56)
	defer func() { _ = web3utils.CloseDefaultEVM() }()

	opts := web3utils.DefaultTransferCoinOptions()
	opts.WaitForConfirmation = true
	opts.GasPriceBuffer = 1.2
	opts.GasLimitBuffer = 1.2

	ok, txHash, msg := web3utils.TransferCoinDefault(privateKey, toAddress, amount, opts)
	fmt.Println("transfer coin:", ok, txHash, msg)
}

func Web3UtilsTransferTokenCall() {
	rpcURL := "https://bsc-dataseed1.binance.org"
	privateKey := "" // 填你的私钥（0x开头或不带都行）
	toAddress := "0xa5fbb44ce14e1d784e0305804cbe2f4368952931"
	tokenContract := "0x55d398326f99059fF775485246999027B3197955" // BSC-USDT
	amount := "1"

	if privateKey == "" {
		fmt.Println("Web3UtilsTransferTokenCall skip: privateKey is empty")
		return
	}

	web3utils.InitDefaultEVM(rpcURL, 56)
	defer func() { _ = web3utils.CloseDefaultEVM() }()

	opts := web3utils.DefaultTransferTokenOptions()
	opts.WaitForConfirmation = true
	opts.GasPriceBuffer = 1.2
	opts.GasLimitBuffer = 1.2

	ok, txHash, msg := web3utils.TransferTokenDefault(privateKey, toAddress, amount, tokenContract, opts)
	fmt.Println("transfer token:", ok, txHash, msg)
}
