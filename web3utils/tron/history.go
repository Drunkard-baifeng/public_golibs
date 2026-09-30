package tron

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fbsobreira/gotron-sdk/pkg/address"
)

type TransferRecord struct {
	TransactionID   string
	From            string
	To              string
	Amount          string
	AmountRaw       string
	Timestamp       int64
	Type            string
	Status          string
	Direction       string
	ContractAddress string
	TokenName       string
	TokenSymbol     string
	Decimals        int
}

func (w *TronWallet) GetTRXTransfers(accountAddress string, limit int, onlyConfirmed bool) (bool, []TransferRecord, string) {
	if _, err := address.Base58ToAddress(accountAddress); err != nil {
		return false, nil, "invalid address"
	}
	limit = normalizeHistoryLimit(limit)

	query := url.Values{}
	query.Set("only_confirmed", strconv.FormatBool(onlyConfirmed))
	query.Set("limit", strconv.Itoa(limit))
	query.Set("order_by", "block_timestamp,desc")
	query.Set("visible", "true")

	var response tronGridTRXResponse
	if err := w.doJSON(http.MethodGet, "/v1/accounts/"+url.PathEscape(accountAddress)+"/transactions", query, nil, &response); err != nil {
		return false, nil, err.Error()
	}

	transfers := make([]TransferRecord, 0, len(response.Data))
	for _, tx := range response.Data {
		if len(tx.RawData.Contract) == 0 || tx.RawData.Contract[0].Type != "TransferContract" {
			continue
		}
		value := tx.RawData.Contract[0].Parameter.Value
		fromAddress := normalizeAddress(value.OwnerAddress)
		toAddress := normalizeAddress(value.ToAddress)
		amount := big.NewInt(value.Amount)
		status := "UNKNOWN"
		if len(tx.Ret) > 0 && tx.Ret[0].ContractRet != "" {
			status = tx.Ret[0].ContractRet
		}
		transfers = append(transfers, TransferRecord{
			TransactionID: tx.TxID,
			From:          fromAddress,
			To:            toAddress,
			Amount:        formatUnits(amount, 6),
			AmountRaw:     amount.String(),
			Timestamp:     tx.BlockTimestamp,
			Type:          "TRX",
			Status:        status,
			Direction:     transferDirection(accountAddress, fromAddress, toAddress),
			TokenSymbol:   "TRX",
			Decimals:      6,
		})
	}
	return true, transfers, ""
}

func (w *TronWallet) GetTokenTransfers(accountAddress string, contractAddress string, limit int, onlyConfirmed bool) (bool, []TransferRecord, string) {
	if _, err := address.Base58ToAddress(accountAddress); err != nil {
		return false, nil, "invalid address"
	}
	if contractAddress != "" {
		if _, err := address.Base58ToAddress(contractAddress); err != nil {
			return false, nil, "invalid contract address"
		}
	}
	limit = normalizeHistoryLimit(limit)

	query := url.Values{}
	query.Set("only_confirmed", strconv.FormatBool(onlyConfirmed))
	query.Set("limit", strconv.Itoa(limit))
	query.Set("order_by", "block_timestamp,desc")
	if contractAddress != "" {
		query.Set("contract_address", contractAddress)
	}

	var response tronGridTRC20Response
	if err := w.doJSON(http.MethodGet, "/v1/accounts/"+url.PathEscape(accountAddress)+"/transactions/trc20", query, nil, &response); err != nil {
		return false, nil, err.Error()
	}

	transfers := make([]TransferRecord, 0, len(response.Data))
	for _, tx := range response.Data {
		amountRaw, ok := new(big.Int).SetString(tx.Value, 10)
		if !ok {
			continue
		}
		decimals, err := strconv.Atoi(tx.TokenInfo.Decimals)
		if err != nil || decimals < 0 {
			decimals = 0
		}
		transfers = append(transfers, TransferRecord{
			TransactionID:   tx.TransactionID,
			From:            tx.From,
			To:              tx.To,
			Amount:          formatUnits(amountRaw, decimals),
			AmountRaw:       amountRaw.String(),
			Timestamp:       tx.BlockTimestamp,
			Type:            "TRC20",
			Status:          "SUCCESS",
			Direction:       transferDirection(accountAddress, tx.From, tx.To),
			ContractAddress: tx.TokenInfo.Address,
			TokenName:       tx.TokenInfo.Name,
			TokenSymbol:     tx.TokenInfo.Symbol,
			Decimals:        decimals,
		})
	}
	return true, transfers, ""
}

func (w *TronWallet) getEnergyPriceSUN() int64 {
	var response struct {
		ChainParameter []struct {
			Key   string `json:"key"`
			Value int64  `json:"value"`
		} `json:"chainParameter"`
	}
	if err := w.doJSON(http.MethodPost, "/wallet/getchainparameters", nil, strings.NewReader("{}"), &response); err != nil {
		return defaultEnergyPriceSUN
	}
	for _, parameter := range response.ChainParameter {
		if parameter.Key == "getEnergyFee" && parameter.Value > 0 {
			return parameter.Value
		}
	}
	return defaultEnergyPriceSUN
}

func (w *TronWallet) doJSON(method, path string, query url.Values, body io.Reader, target any) error {
	if w == nil || w.httpClient == nil || w.httpURL == "" {
		return fmt.Errorf("tron HTTP client is not configured")
	}
	endpoint := w.httpURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if w.apiKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", w.apiKey)
	}

	response, err := w.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("TronGrid HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode TronGrid response: %w", err)
	}
	return nil
}

func normalizeHistoryLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func transferDirection(accountAddress, fromAddress, toAddress string) string {
	if toAddress == accountAddress {
		return "IN"
	}
	if fromAddress == accountAddress {
		return "OUT"
	}
	return ""
}

func normalizeAddress(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "T") {
		return value
	}
	value = strings.TrimPrefix(value, "0x")
	if len(value) == 42 && strings.HasPrefix(value, "41") {
		if parsed, err := address.HexToAddress(value); err == nil {
			return parsed.String()
		}
	}
	return value
}

type tronGridTRXResponse struct {
	Data []struct {
		TxID           string `json:"txID"`
		BlockTimestamp int64  `json:"block_timestamp"`
		Ret            []struct {
			ContractRet string `json:"contractRet"`
		} `json:"ret"`
		RawData struct {
			Contract []struct {
				Type      string `json:"type"`
				Parameter struct {
					Value struct {
						OwnerAddress string `json:"owner_address"`
						ToAddress    string `json:"to_address"`
						Amount       int64  `json:"amount"`
					} `json:"value"`
				} `json:"parameter"`
			} `json:"contract"`
		} `json:"raw_data"`
	} `json:"data"`
}

type tronGridTRC20Response struct {
	Data []struct {
		TransactionID  string `json:"transaction_id"`
		BlockTimestamp int64  `json:"block_timestamp"`
		From           string `json:"from"`
		To             string `json:"to"`
		Value          string `json:"value"`
		TokenInfo      struct {
			Symbol   string `json:"symbol"`
			Address  string `json:"address"`
			Decimals string `json:"decimals"`
			Name     string `json:"name"`
		} `json:"token_info"`
	} `json:"data"`
}
