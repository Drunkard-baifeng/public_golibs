// Package solana provides wallet, signing, balance, and transfer utilities for
// the Solana blockchain.
package solana

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	solanago "github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/bip39"
	associatedtokenaccount "github.com/solana-foundation/solana-go/v2/programs/associated-token-account"
	"github.com/solana-foundation/solana-go/v2/programs/system"
	"github.com/solana-foundation/solana-go/v2/programs/token"
	"github.com/solana-foundation/solana-go/v2/rpc"
)

const (
	defaultConfirmationTimeout = 120
)

type Wallet struct {
	Address        string
	PrivateKey     string
	Mnemonic       string
	Index          int
	DerivationPath string
}

type SignMessage struct {
	Signature string
}

// GasEstimate keeps the cross-chain API name used by the EVM package. Solana
// charges a transaction fee in lamports rather than EVM-style gas.
type GasEstimate struct {
	FeeSOL      string
	FeeLamports uint64
}

type TransferCoinOptions struct {
	SkipPreflight        bool
	MaxRetries           uint
	WaitForConfirmation  bool
	ConfirmationTimeoutS int
}

type TransferTokenOptions struct {
	CreateAssociatedTokenAccount bool
	SkipPreflight                bool
	MaxRetries                   uint
	WaitForConfirmation          bool
	ConfirmationTimeoutS         int
}

type SolanaWallet struct {
	rpcURL      string
	client      *rpc.Client
	commitment  rpc.CommitmentType
	NativeToken string
	ChainName   string
}

func DefaultTransferCoinOptions() *TransferCoinOptions {
	return &TransferCoinOptions{ConfirmationTimeoutS: defaultConfirmationTimeout}
}

func DefaultTransferTokenOptions() *TransferTokenOptions {
	return &TransferTokenOptions{
		CreateAssociatedTokenAccount: true,
		ConfirmationTimeoutS:         defaultConfirmationTimeout,
	}
}

func NewSolanaWallet(rpcURL string) *SolanaWallet {
	w := &SolanaWallet{
		rpcURL:      strings.TrimSpace(rpcURL),
		commitment:  rpc.CommitmentConfirmed,
		NativeToken: "SOL",
		ChainName:   resolveChainName(rpcURL),
	}
	if w.rpcURL != "" {
		w.client = rpc.New(w.rpcURL)
	}
	return w
}

// Close releases the reference to the RPC client. The SDK client uses the
// shared HTTP transport and does not expose a separate Close operation.
func (w *SolanaWallet) Close() error {
	if w != nil {
		w.client = nil
	}
	return nil
}

func (w *SolanaWallet) Connected() bool {
	return w != nil && w.client != nil
}

func (w *SolanaWallet) GenerateWallet(index int) (bool, *Wallet, string) {
	if index < 0 {
		return false, nil, "index cannot be negative"
	}
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return false, nil, err.Error()
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return false, nil, err.Error()
	}
	return w.RecoverWalletFromMnemonic(mnemonic, index)
}

func (w *SolanaWallet) RecoverWalletFromMnemonic(mnemonic string, index int) (bool, *Wallet, string) {
	mnemonic = strings.TrimSpace(mnemonic)
	if mnemonic == "" {
		return false, nil, "mnemonic is empty"
	}
	if index < 0 {
		return false, nil, "index cannot be negative"
	}
	path := derivationPath(index)
	privateKey, err := solanago.PrivateKeyFromMnemonicAtPath(mnemonic, "", path)
	if err != nil {
		return false, nil, err.Error()
	}
	return true, walletFromPrivateKey(privateKey, mnemonic, index, path), ""
}

func (w *SolanaWallet) RecoverWalletFromPrivateKey(privateKey string) (bool, *Wallet, string) {
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return false, nil, err.Error()
	}
	return true, walletFromPrivateKey(key, "", 0, ""), ""
}

func (w *SolanaWallet) SignMessage(privateKey string, message string) (bool, *SignMessage, string) {
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return false, nil, err.Error()
	}
	signature, err := key.Sign([]byte(message))
	if err != nil {
		return false, nil, err.Error()
	}
	return true, &SignMessage{Signature: signature.String()}, ""
}

func (w *SolanaWallet) VerifySignature(message string, signature string, address string) (bool, string) {
	publicKey, err := solanago.PublicKeyFromBase58(strings.TrimSpace(address))
	if err != nil {
		return false, "invalid address: " + err.Error()
	}
	sig, err := solanago.SignatureFromBase58(strings.TrimSpace(signature))
	if err != nil {
		return false, "invalid signature: " + err.Error()
	}
	return sig.Verify(publicKey, []byte(message)), ""
}

func (w *SolanaWallet) EstimateGasFee(fromAddress string, toAddress string, amountSOL string, feeBuffer float64) (bool, *GasEstimate, string) {
	if err := w.requireClient(); err != nil {
		return false, nil, err.Error()
	}
	from, err := solanago.PublicKeyFromBase58(strings.TrimSpace(fromAddress))
	if err != nil {
		return false, nil, "invalid from address: " + err.Error()
	}
	to, err := solanago.PublicKeyFromBase58(strings.TrimSpace(toAddress))
	if err != nil {
		return false, nil, "invalid to address: " + err.Error()
	}
	lamports, err := parseUnitsUint64(amountSOL, 9)
	if err != nil {
		return false, nil, err.Error()
	}
	if lamports == 0 {
		return false, nil, "amount must be greater than zero"
	}

	instruction := system.NewTransferInstruction(lamports, from, to).Build()
	fee, _, err := w.estimateFeeForInstructions(context.Background(), from, []solanago.Instruction{instruction})
	if err != nil {
		return false, nil, err.Error()
	}
	if feeBuffer <= 0 {
		feeBuffer = 1
	}
	fee = multiplyUint64ByFloat(fee, feeBuffer)
	return true, &GasEstimate{
		FeeSOL:      formatUnits(new(big.Int).SetUint64(fee), 9),
		FeeLamports: fee,
	}, ""
}

func (w *SolanaWallet) GetBalance(address string) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "0", err.Error()
	}
	publicKey, err := solanago.PublicKeyFromBase58(strings.TrimSpace(address))
	if err != nil {
		return false, "0", "invalid address: " + err.Error()
	}
	result, err := w.client.GetBalance(context.Background(), publicKey, w.commitment)
	if err != nil {
		return false, "0", err.Error()
	}
	return true, formatUnits(new(big.Int).SetUint64(result.Value), 9), ""
}

func (w *SolanaWallet) TransferCoin(privateKey string, toAddress string, amountSOL string, options *TransferCoinOptions) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "", err.Error()
	}
	options = normalizeCoinOptions(options)
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return false, "", err.Error()
	}
	from := key.PublicKey()
	to, err := solanago.PublicKeyFromBase58(strings.TrimSpace(toAddress))
	if err != nil {
		return false, "", "invalid to address: " + err.Error()
	}
	lamports, err := parseUnitsUint64(amountSOL, 9)
	if err != nil {
		return false, "", err.Error()
	}
	if lamports == 0 {
		return false, "", "amount must be greater than zero"
	}

	ctx := context.Background()
	instructions := []solanago.Instruction{system.NewTransferInstruction(lamports, from, to).Build()}
	fee, blockhash, err := w.estimateFeeForInstructions(ctx, from, instructions)
	if err != nil {
		return false, "", err.Error()
	}
	balance, err := w.client.GetBalance(ctx, from, w.commitment)
	if err != nil {
		return false, "", err.Error()
	}
	required := new(big.Int).Add(new(big.Int).SetUint64(lamports), new(big.Int).SetUint64(fee))
	if new(big.Int).SetUint64(balance.Value).Cmp(required) < 0 {
		return false, "", fmt.Sprintf("insufficient SOL balance, current: %s", formatUnits(new(big.Int).SetUint64(balance.Value), 9))
	}

	tx, err := buildSignedTransaction(instructions, blockhash, key)
	if err != nil {
		return false, "", err.Error()
	}
	signature, err := w.client.SendTransactionWithOpts(ctx, tx, transactionOpts(options.SkipPreflight, options.MaxRetries, w.commitment))
	if err != nil {
		return false, "", err.Error()
	}
	txID := signature.String()
	if options.WaitForConfirmation {
		ok, receipt, message := w.WaitForTransactionReceipt(txID, options.ConfirmationTimeoutS)
		if !ok {
			return false, txID, message
		}
		return true, txID, fmt.Sprintf("transaction confirmed in slot %v", receipt["slot"])
	}
	return true, txID, "transfer sent"
}

func (w *SolanaWallet) TransferToken(privateKey string, toAddress string, amount string, mintAddress string, options *TransferTokenOptions) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "", err.Error()
	}
	options = normalizeTokenOptions(options)
	key, err := parsePrivateKey(privateKey)
	if err != nil {
		return false, "", err.Error()
	}
	owner := key.PublicKey()
	recipient, err := solanago.PublicKeyFromBase58(strings.TrimSpace(toAddress))
	if err != nil {
		return false, "", "invalid to address: " + err.Error()
	}
	mint, err := solanago.PublicKeyFromBase58(strings.TrimSpace(mintAddress))
	if err != nil {
		return false, "", "invalid mint address: " + err.Error()
	}

	ctx := context.Background()
	tokenProgram, decimals, err := w.tokenProgramAndDecimals(ctx, mint)
	if err != nil {
		return false, "", err.Error()
	}
	amountUnits, err := parseUnitsUint64(amount, int(decimals))
	if err != nil {
		return false, "", err.Error()
	}
	if amountUnits == 0 {
		return false, "", "amount must be greater than zero"
	}

	sourceATA, _, err := solanago.FindAssociatedTokenAddressWithProgram(owner, mint, tokenProgram)
	if err != nil {
		return false, "", err.Error()
	}
	destinationATA, _, err := solanago.FindAssociatedTokenAddressWithProgram(recipient, mint, tokenProgram)
	if err != nil {
		return false, "", err.Error()
	}
	balance, err := w.client.GetTokenAccountBalance(ctx, sourceATA, w.commitment)
	if err != nil {
		return false, "", err.Error()
	}
	if balance == nil || balance.Value == nil {
		return false, "", "source token account has no balance"
	}
	currentUnits, ok := new(big.Int).SetString(balance.Value.Amount, 10)
	if !ok {
		return false, "", "invalid token balance returned by RPC"
	}
	if currentUnits.Cmp(new(big.Int).SetUint64(amountUnits)) < 0 {
		return false, "", fmt.Sprintf("token balance is not enough, current: %s", balance.Value.UiAmountString)
	}

	instructions := make([]solanago.Instruction, 0, 2)
	if options.CreateAssociatedTokenAccount {
		instructions = append(instructions,
			associatedtokenaccount.NewCreateIdempotentInstructionWithTokenProgram(owner, recipient, mint, tokenProgram).Build(),
		)
	}
	transferInstruction := token.NewTransferCheckedInstruction(
		amountUnits,
		decimals,
		sourceATA,
		mint,
		destinationATA,
		owner,
		nil,
	).Build().SetProgramID(tokenProgram)
	instructions = append(instructions, transferInstruction)

	fee, blockhash, err := w.estimateFeeForInstructions(ctx, owner, instructions)
	if err != nil {
		return false, "", err.Error()
	}
	solBalance, err := w.client.GetBalance(ctx, owner, w.commitment)
	if err != nil {
		return false, "", err.Error()
	}
	if solBalance.Value < fee {
		return false, "", fmt.Sprintf("insufficient SOL balance for fee, current: %s", formatUnits(new(big.Int).SetUint64(solBalance.Value), 9))
	}

	tx, err := buildSignedTransaction(instructions, blockhash, key)
	if err != nil {
		return false, "", err.Error()
	}
	signature, err := w.client.SendTransactionWithOpts(ctx, tx, transactionOpts(options.SkipPreflight, options.MaxRetries, w.commitment))
	if err != nil {
		return false, "", err.Error()
	}
	txID := signature.String()
	if options.WaitForConfirmation {
		ok, receipt, message := w.WaitForTransactionReceipt(txID, options.ConfirmationTimeoutS)
		if !ok {
			return false, txID, message
		}
		return true, txID, fmt.Sprintf("token transfer confirmed in slot %v", receipt["slot"])
	}
	return true, txID, "token transfer sent"
}

func (w *SolanaWallet) GetTokenBalance(address string, mintAddress string) (bool, string, string) {
	if err := w.requireClient(); err != nil {
		return false, "0", err.Error()
	}
	owner, err := solanago.PublicKeyFromBase58(strings.TrimSpace(address))
	if err != nil {
		return false, "0", "invalid address: " + err.Error()
	}
	mint, err := solanago.PublicKeyFromBase58(strings.TrimSpace(mintAddress))
	if err != nil {
		return false, "0", "invalid mint address: " + err.Error()
	}

	ctx := context.Background()
	tokenProgram, _, err := w.tokenProgramAndDecimals(ctx, mint)
	if err != nil {
		return false, "0", err.Error()
	}
	ata, _, err := solanago.FindAssociatedTokenAddressWithProgram(owner, mint, tokenProgram)
	if err != nil {
		return false, "0", err.Error()
	}
	result, err := w.client.GetTokenAccountBalance(ctx, ata, w.commitment)
	if err != nil {
		if errors.Is(err, rpc.ErrNotFound) {
			return true, "0", ""
		}
		return false, "0", err.Error()
	}
	if result == nil || result.Value == nil || result.Value.UiAmountString == "" {
		return true, "0", ""
	}
	return true, result.Value.UiAmountString, ""
}

func (w *SolanaWallet) WaitForTransactionReceipt(signature string, timeoutSeconds int) (bool, map[string]any, string) {
	if err := w.requireClient(); err != nil {
		return false, map[string]any{}, err.Error()
	}
	sig, err := solanago.SignatureFromBase58(strings.TrimSpace(signature))
	if err != nil {
		return false, map[string]any{}, "invalid transaction signature: " + err.Error()
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultConfirmationTimeout
	}

	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	for {
		statuses, statusErr := w.client.GetSignatureStatuses(context.Background(), true, sig)
		if statusErr != nil && !errors.Is(statusErr, rpc.ErrNotFound) {
			return false, map[string]any{"signature": signature, "confirmed": false}, statusErr.Error()
		}
		if statusErr == nil && statuses != nil && len(statuses.Value) > 0 && statuses.Value[0] != nil {
			status := statuses.Value[0]
			result := signatureStatusMap(signature, status)
			if status.Err != nil {
				return false, result, fmt.Sprintf("transaction failed: %v", status.Err)
			}
			if signatureStatusConfirmed(status) {
				return true, result, ""
			}
		}
		if time.Now().After(deadline) {
			return false, map[string]any{
				"signature": signature,
				"confirmed": false,
			}, "transaction confirmation timeout"
		}
		time.Sleep(2 * time.Second)
	}
}

func (w *SolanaWallet) estimateFeeForInstructions(ctx context.Context, payer solanago.PublicKey, instructions []solanago.Instruction) (uint64, solanago.Hash, error) {
	latest, err := w.client.GetLatestBlockhash(ctx, w.commitment)
	if err != nil {
		return 0, solanago.Hash{}, err
	}
	if latest == nil || latest.Value == nil {
		return 0, solanago.Hash{}, errors.New("RPC returned no recent blockhash")
	}
	tx, err := solanago.NewTransaction(instructions, latest.Value.Blockhash, solanago.TransactionPayer(payer))
	if err != nil {
		return 0, solanago.Hash{}, err
	}
	feeResult, err := w.client.GetFeeForMessage(ctx, tx.Message.ToBase64(), w.commitment)
	if err != nil {
		return 0, solanago.Hash{}, err
	}
	if feeResult == nil || feeResult.Value == nil {
		return 0, solanago.Hash{}, errors.New("RPC returned no fee for transaction message")
	}
	return *feeResult.Value, latest.Value.Blockhash, nil
}

func (w *SolanaWallet) tokenProgramAndDecimals(ctx context.Context, mint solanago.PublicKey) (solanago.PublicKey, uint8, error) {
	account, err := w.client.GetAccountInfoWithOpts(ctx, mint, &rpc.GetAccountInfoOpts{Commitment: w.commitment})
	if err != nil {
		return solanago.PublicKey{}, 0, err
	}
	if account == nil || account.Value == nil {
		return solanago.PublicKey{}, 0, errors.New("mint account not found")
	}
	tokenProgram := account.Value.Owner
	if !tokenProgram.Equals(solanago.TokenProgramID) && !tokenProgram.Equals(solanago.Token2022ProgramID) {
		return solanago.PublicKey{}, 0, fmt.Errorf("account is not owned by the SPL Token or Token-2022 program: %s", tokenProgram)
	}
	supply, err := w.client.GetTokenSupply(ctx, mint, w.commitment)
	if err != nil {
		return solanago.PublicKey{}, 0, err
	}
	if supply == nil || supply.Value == nil {
		return solanago.PublicKey{}, 0, errors.New("RPC returned no token mint information")
	}
	return tokenProgram, supply.Value.Decimals, nil
}

func (w *SolanaWallet) requireClient() error {
	if w == nil {
		return errors.New("solana wallet is nil")
	}
	if w.client == nil {
		return errors.New("solana client is not connected")
	}
	return nil
}

func walletFromPrivateKey(privateKey solanago.PrivateKey, mnemonic string, index int, path string) *Wallet {
	return &Wallet{
		Address:        privateKey.PublicKey().String(),
		PrivateKey:     privateKey.String(),
		Mnemonic:       mnemonic,
		Index:          index,
		DerivationPath: path,
	}
}

func parsePrivateKey(privateKey string) (solanago.PrivateKey, error) {
	privateKey = strings.TrimSpace(privateKey)
	if privateKey == "" {
		return nil, errors.New("private key is empty")
	}
	key, err := solanago.PrivateKeyFromBase58(privateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	return key, nil
}

func derivationPath(index int) string {
	return fmt.Sprintf("m/44'/501'/%d'/0'", index)
}

func buildSignedTransaction(instructions []solanago.Instruction, blockhash solanago.Hash, privateKey solanago.PrivateKey) (*solanago.Transaction, error) {
	payer := privateKey.PublicKey()
	tx, err := solanago.NewTransaction(instructions, blockhash, solanago.TransactionPayer(payer))
	if err != nil {
		return nil, err
	}
	_, err = tx.Sign(func(publicKey solanago.PublicKey) *solanago.PrivateKey {
		if publicKey.Equals(payer) {
			return &privateKey
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func transactionOpts(skipPreflight bool, maxRetries uint, commitment rpc.CommitmentType) rpc.TransactionOpts {
	opts := rpc.TransactionOpts{
		SkipPreflight:       skipPreflight,
		PreflightCommitment: commitment,
	}
	if maxRetries > 0 {
		opts.MaxRetries = &maxRetries
	}
	return opts
}

func normalizeCoinOptions(options *TransferCoinOptions) *TransferCoinOptions {
	if options == nil {
		return DefaultTransferCoinOptions()
	}
	copyOptions := *options
	if copyOptions.ConfirmationTimeoutS <= 0 {
		copyOptions.ConfirmationTimeoutS = defaultConfirmationTimeout
	}
	return &copyOptions
}

func normalizeTokenOptions(options *TransferTokenOptions) *TransferTokenOptions {
	if options == nil {
		return DefaultTransferTokenOptions()
	}
	copyOptions := *options
	if copyOptions.ConfirmationTimeoutS <= 0 {
		copyOptions.ConfirmationTimeoutS = defaultConfirmationTimeout
	}
	return &copyOptions
}

func signatureStatusConfirmed(status *rpc.SignatureStatusesResult) bool {
	if status == nil {
		return false
	}
	if status.ConfirmationStatus == rpc.ConfirmationStatusConfirmed || status.ConfirmationStatus == rpc.ConfirmationStatusFinalized {
		return true
	}
	// Older RPC nodes may omit confirmationStatus. A nil confirmation count
	// means the transaction has been rooted/finalized.
	return status.ConfirmationStatus == "" && status.Confirmations == nil
}

func signatureStatusMap(signature string, status *rpc.SignatureStatusesResult) map[string]any {
	result := map[string]any{
		"signature":           signature,
		"confirmed":           signatureStatusConfirmed(status),
		"slot":                status.Slot,
		"confirmation_status": string(status.ConfirmationStatus),
		"error":               status.Err,
	}
	if status.Confirmations != nil {
		result["confirmations"] = *status.Confirmations
	} else {
		result["confirmations"] = nil
	}
	return result
}

func resolveChainName(rpcURL string) string {
	value := strings.ToLower(rpcURL)
	switch {
	case strings.Contains(value, "devnet"):
		return "Solana Devnet"
	case strings.Contains(value, "testnet"):
		return "Solana Testnet"
	case strings.Contains(value, "mainnet"):
		return "Solana Mainnet"
	default:
		return "Solana"
	}
}

func parseUnitsUint64(amount string, decimals int) (uint64, error) {
	value, err := parseUnits(amount, decimals)
	if err != nil {
		return 0, err
	}
	if !value.IsUint64() {
		return 0, errors.New("amount is out of range")
	}
	return value.Uint64(), nil
}

func parseUnits(amount string, decimals int) (*big.Int, error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return nil, errors.New("amount is empty")
	}
	if decimals < 0 || decimals > 255 {
		return nil, errors.New("invalid decimals")
	}
	if strings.HasPrefix(amount, "+") {
		amount = amount[1:]
	}
	if strings.HasPrefix(amount, "-") {
		return nil, errors.New("amount cannot be negative")
	}

	parts := strings.Split(amount, ".")
	if len(parts) > 2 {
		return nil, errors.New("invalid amount")
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	if !isDigits(whole) {
		return nil, errors.New("invalid amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction != "" && !isDigits(fraction) {
			return nil, errors.New("invalid amount")
		}
	}
	if len(fraction) > decimals {
		return nil, fmt.Errorf("amount has more than %d decimal places", decimals)
	}
	fraction += strings.Repeat("0", decimals-len(fraction))

	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	wholeValue, ok := new(big.Int).SetString(whole, 10)
	if !ok {
		return nil, errors.New("invalid amount")
	}
	wholeValue.Mul(wholeValue, base)
	if fraction == "" {
		return wholeValue, nil
	}
	fractionValue, ok := new(big.Int).SetString(fraction, 10)
	if !ok {
		return nil, errors.New("invalid amount")
	}
	return wholeValue.Add(wholeValue, fractionValue), nil
}

func formatUnits(value *big.Int, decimals int) string {
	if value == nil {
		return "0"
	}
	negative := value.Sign() < 0
	abs := new(big.Int).Abs(new(big.Int).Set(value))
	digits := abs.Text(10)
	if decimals == 0 {
		if negative {
			return "-" + digits
		}
		return digits
	}
	if len(digits) <= decimals {
		digits = strings.Repeat("0", decimals-len(digits)+1) + digits
	}
	whole := digits[:len(digits)-decimals]
	fraction := strings.TrimRight(digits[len(digits)-decimals:], "0")
	result := whole
	if fraction != "" {
		result += "." + fraction
	}
	if negative {
		result = "-" + result
	}
	return result
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func multiplyUint64ByFloat(value uint64, factor float64) uint64 {
	if factor <= 0 {
		factor = 1
	}
	r := new(big.Rat).SetInt(new(big.Int).SetUint64(value))
	f := new(big.Rat)
	if _, ok := f.SetString(fmt.Sprintf("%.8f", factor)); !ok {
		return value
	}
	r.Mul(r, f)
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if new(big.Int).Mod(r.Num(), r.Denom()).Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsUint64() {
		return value
	}
	return q.Uint64()
}
