package main

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/prometheus/client_golang/prometheus"
)

func TestDetectTokenType(t *testing.T) {
	tests := []struct {
		name     string
		bytecode string
		want     string
	}{
		{"ERC20", "608060405234801561001057600080fd5b50d4a9059cbb", "ERC20"},
		{"ERC721", "608060405234801561001057600080fd5b50d480ac58cd", "ERC721"},
		{"ERC1155", "608060405234801561001057600080fd5b50d4d9b67a26", "ERC1155"},
		{"Unknown", "6080604052", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _ := hex.DecodeString(tt.bytecode)
			if got := detectTokenType(code); got != tt.want {
				t.Errorf("detectTokenType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsHash(t *testing.T) {
	h1 := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	h2 := common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
	list := []common.Hash{h1}

	if !containsHash(list, h1) {
		t.Errorf("containsHash should return true for existing hash")
	}
	if containsHash(list, h2) {
		t.Errorf("containsHash should return false for non-existing hash")
	}
}

func TestHandleTransfer_Mint(t *testing.T) {
	// Setup Watcher with metrics initialized to avoid panic
	w := &Watcher{
		tracked: make(map[string]*ContractState),
		promMetrics: WatcherMetrics{
			MintsDetected: prometheus.NewCounter(prometheus.CounterOpts{Name: "test_mints"}),
		},
	}

	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	contractHex := strings.ToLower(contractAddr.Hex())

	w.tracked[contractHex] = &ContractState{
		Deployer:  "0xdeployer",
		TokenType: "ERC20",
	}

	// Create a temp file for output
	tmpfile, err := os.CreateTemp("", "test_output")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	// Construct a Mint log (Transfer from 0x0)
	// Topic 0: Transfer signature
	// Topic 1: From (0x000...000)
	// Topic 2: To (User)
	zeroAddr := common.Hash{} // 0x000...
	userAddr := common.HexToHash("0x0000000000000000000000001111111111111111111111111111111111111111")

	log := types.Log{
		Address: contractAddr,
		Topics:  []common.Hash{common.Hash{}, zeroAddr, userAddr},
		TxHash:  common.HexToHash("0xabc"),
	}

	w.handleTransfer(log, tmpfile)

	// Verify state update
	w.lock.RLock()
	state := w.tracked[contractHex]
	w.lock.RUnlock()

	if state.Mints != 1 {
		t.Errorf("Expected 1 mint, got %d", state.Mints)
	}
	if w.stats.Mints != 1 {
		t.Errorf("Expected global mints 1, got %d", w.stats.Mints)
	}
}

func TestHandleLiquidityOrTrade(t *testing.T) {
	w := &Watcher{
		tracked: make(map[string]*ContractState),
		promMetrics: WatcherMetrics{
			LiquidityEvents: prometheus.NewCounter(prometheus.CounterOpts{Name: "test_liquidity"}),
			TradesDetected:  prometheus.NewCounter(prometheus.CounterOpts{Name: "test_trades"}),
		},
	}

	pairCreatedTopic := common.HexToHash("0x111")
	swapTopic := common.HexToHash("0x222")
	w.dexPairs = []common.Hash{pairCreatedTopic}
	w.dexSwaps = []common.Hash{swapTopic}

	contractAddr := common.HexToAddress("0xabc")
	contractHex := strings.ToLower(contractAddr.Hex())
	w.tracked[contractHex] = &ContractState{
		Deployer: "0xdeployer",
	}

	tmpfile, err := os.CreateTemp("", "test_output")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	// Test Liquidity Event
	// Topic 1 must match the tracked contract address for the logic to pick it up
	topicContract := common.BytesToHash(contractAddr.Bytes())
	logLiq := types.Log{
		Address: contractAddr,
		Topics:  []common.Hash{pairCreatedTopic, topicContract, common.Hash{}},
		TxHash:  common.HexToHash("0xdef"),
	}

	w.handleLiquidityOrTrade(logLiq, tmpfile)

	w.lock.RLock()
	state := w.tracked[contractHex]
	w.lock.RUnlock()

	if !state.LiquidityCreated {
		t.Errorf("Expected LiquidityCreated to be true")
	}
}
