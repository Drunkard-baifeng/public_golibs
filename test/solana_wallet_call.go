package main

import (
	"fmt"

	"github.com/Drunkard-baifeng/public_golibs/web3utils/solana"
)

func SolanaWalletCall() {
	walletClient := solana.NewSolanaWallet("")
	defer walletClient.Close()

	ok, wallet, msg := walletClient.GenerateWallet(0)
	if !ok {
		fmt.Println("GenerateWallet failed:", msg)
		return
	}
	fmt.Println("address:", wallet.Address)
	fmt.Println("private:", wallet.PrivateKey)
	fmt.Println("mnemonic:", wallet.Mnemonic)
	fmt.Println("derivation path:", wallet.DerivationPath)

	// Public BIP39 test vector; never use it for a funded account.
	// mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	// ok, walletByMnemonic, msg := walletClient.RecoverWalletFromMnemonic(mnemonic, 0)
	// if !ok {
	// 	fmt.Println("RecoverWalletFromMnemonic failed:", msg)
	// 	return
	// }
	// fmt.Println("address:", walletByMnemonic.Address)
	// fmt.Println("private:", walletByMnemonic.PrivateKey)
	// fmt.Println("derivation path:", walletByMnemonic.DerivationPath)

	// ok, walletByPrivate, msg := walletClient.RecoverWalletFromPrivateKey(walletByMnemonic.PrivateKey)
	// if !ok {
	// 	fmt.Println("RecoverWalletFromPrivateKey failed:", msg)
	// 	return
	// }
	// fmt.Println("recover by private:", walletByPrivate.Address)

	// signText := "RichWorld中国"
	// ok, signResult, msg := walletClient.SignMessage(walletByMnemonic.PrivateKey, signText)
	// if !ok {
	// 	fmt.Println("SignMessage failed:", msg)
	// 	return
	// }
	// fmt.Println("signature:", signResult.Signature)

	// ok, msg = walletClient.VerifySignature(signText, signResult.Signature, walletByMnemonic.Address)
	// fmt.Println("verify:", ok, msg)
}

func SolanaWalletOnlineCall() {
	rpcURL := "https://api.mainnet-beta.solana.com"
	address := "85CJss3SGPH3G37tdYdomKkhyky3Hog9y9MWcmbwSbZb"

	solana.InitDefaultSolana(rpcURL)
	defer func() { _ = solana.CloseDefaultSolana() }()

	ok, balance, msg := solana.GetBalanceDefault(address)
	fmt.Println("connected:", solana.DefaultSolana().Connected(), "chain:", solana.DefaultSolana().ChainName, solana.DefaultSolana().NativeToken)
	fmt.Println("balance:", ok, balance, msg)
}

func SolanaWalletEstimateFeeCall() {
	rpcURL := "https://api.mainnet-beta.solana.com"
	fromAddress := "85CJss3SGPH3G37tdYdomKkhyky3Hog9y9MWcmbwSbZb" // 填付款钱包地址
	toAddress := "7PbXw3C8FGQjWigaXZxs82EbapLrurANcZFBNvjUvN9T"   // 填接收钱包地址
	amount := "0.0001"

	if fromAddress == "" || toAddress == "" {
		fmt.Println("SolanaWalletEstimateFeeCall skip: address is empty")
		return
	}

	solana.InitDefaultSolana(rpcURL)
	defer func() { _ = solana.CloseDefaultSolana() }()

	ok, fee, msg := solana.EstimateGasFeeDefault(fromAddress, toAddress, amount, 1.2)
	fmt.Println("estimate fee:", ok, fee, msg)
}

func SolanaWalletGetTokenBalanceCall() {
	rpcURL := "https://api.mainnet-beta.solana.com"
	userAddress := "85CJss3SGPH3G37tdYdomKkhyky3Hog9y9MWcmbwSbZb" // 填钱包 owner 地址，不是 ATA 地址
	mintAddress := "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB" // Mainnet USDT

	if userAddress == "" {
		fmt.Println("SolanaWalletGetTokenBalanceCall skip: userAddress is empty")
		return
	}

	solana.InitDefaultSolana(rpcURL)
	defer func() { _ = solana.CloseDefaultSolana() }()

	ok, balance, msg := solana.GetTokenBalanceDefault(userAddress, mintAddress)
	fmt.Println("connected:", solana.DefaultSolana().Connected(), "chain:", solana.DefaultSolana().ChainName, solana.DefaultSolana().NativeToken)
	fmt.Println("token balance:", ok, balance, msg)
}

func SolanaWalletTransferCoinCall() {
	rpcURL := "https://api.mainnet-beta.solana.com"
	privateKey := "2YLr2hKXfGULYq7kF5j6shU6owoVCY9vMvkJ751SceNimKMAPPLGFcoH4mJQaWLz8MQPD9p484Bm7B3Z9hALnMaH" // 填 Solana Base58 私钥
	toAddress := "7PbXw3C8FGQjWigaXZxs82EbapLrurANcZFBNvjUvN9T"                                              // 填接收钱包地址
	amount := "0.00088"

	if privateKey == "" || toAddress == "" {
		fmt.Println("SolanaWalletTransferCoinCall skip: privateKey or toAddress is empty")
		return
	}

	solana.InitDefaultSolana(rpcURL)
	defer func() { _ = solana.CloseDefaultSolana() }()

	opts := solana.DefaultTransferCoinOptions()
	opts.WaitForConfirmation = true
	opts.ConfirmationTimeoutS = 120

	ok, signature, msg := solana.TransferCoinDefault(privateKey, toAddress, amount, opts)
	fmt.Println("transfer SOL:", ok, signature, msg)
}

func SolanaWalletTransferTokenCall() {
	rpcURL := "https://api.mainnet-beta.solana.com"
	privateKey := "2YLr2hKXfGULYq7kF5j6shU6owoVCY9vMvkJ751SceNimKMAPPLGFcoH4mJQaWLz8MQPD9p484Bm7B3Z9hALnMaH" // 填 Solana Base58 私钥
	toAddress := "7PbXw3C8FGQjWigaXZxs82EbapLrurANcZFBNvjUvN9T"                                              // 填接收钱包 owner 地址，不是 ATA 地址
	mintAddress := "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"                                            // 填 Mainnet SPL Token 或 Token-2022 mint 地址 // Mainnet USDT
	amount := "1"

	if privateKey == "" || toAddress == "" || mintAddress == "" {
		fmt.Println("SolanaWalletTransferTokenCall skip: privateKey, toAddress or mintAddress is empty")
		return
	}

	solana.InitDefaultSolana(rpcURL)
	defer func() { _ = solana.CloseDefaultSolana() }()

	opts := solana.DefaultTransferTokenOptions()
	opts.CreateAssociatedTokenAccount = true
	opts.WaitForConfirmation = true
	opts.ConfirmationTimeoutS = 120

	ok, signature, msg := solana.TransferTokenDefault(privateKey, toAddress, amount, mintAddress, opts)
	fmt.Println("transfer SPL token:", ok, signature, msg)
}
