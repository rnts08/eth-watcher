package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

type Config struct {
	RPC    string `json:"rpc"`
	APIKey string `json:"apiKey,omitempty"`
	Output string `json:"output"`
	Log    string `json:"log"`

	Events struct {
		Transfers bool `json:"transfers"`
		Liquidity bool `json:"liquidity"`
		Trades    bool `json:"trades"`
	} `json:"events"`

	Dexes []struct {
		Name             string `json:"name"`
		PairCreatedTopic string `json:"pairCreatedTopic"`
		SwapTopic        string `json:"swapTopic"`
	} `json:"dexes"`

	Contracts []struct {
		Address    string  `json:"address"`
		Name       string  `json:"name"`
		Type       string  `json:"type"`
		RiskWeight float64 `json:"risk_weight"`
	} `json:"contracts"`
}

type Finding struct {
	Contract     string   `json:"contract"`
	Deployer     string   `json:"deployer"`
	Block        uint64   `json:"block"`
	TokenType    string   `json:"tokenType"`
	MintDetected bool     `json:"mintDetected"`
	RiskScore    int      `json:"riskScore"`
	Flags        []string `json:"flags"`
	TxHash       string   `json:"txHash"`
}

type ContractState struct {
	Deployer         string
	TokenType        string
	Mints            int
	LiquidityCreated bool
	Traded           bool
}

type WatcherStats struct {
	NewContracts int
	Mints        int
	Liquidity    int
	Trades       int
}

type Watcher struct {
	cfg         Config
	tracked     map[string]*ContractState
	lock        sync.RWMutex
	transferSig common.Hash
	dexPairs    []common.Hash
	dexSwaps    []common.Hash
	startTime   time.Time
	stats       WatcherStats
}

func main() {
	w := &Watcher{
		tracked:   make(map[string]*ContractState),
		startTime: time.Now(),
	}

	configPath := flag.String("config", "config.json", "Path to configuration JSON")
	flag.Parse()

	w.loadConfig(*configPath)
	w.setupLogging()

	log.Println("eth-watch starting…")

	client, err := ethclient.Dial(w.cfg.RPC)
	if err != nil {
		log.Fatalf("RPC connection failed: %v", err)
	}

	outFile, err := os.OpenFile(w.cfg.Output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open output file: %v", err)
	}
	defer outFile.Close()

	// Keccak-256 hash of the standard ERC-20 and ERC-721 Transfer event signature.
	w.transferSig = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")

	for _, d := range w.cfg.Dexes {
		w.dexPairs = append(w.dexPairs, common.HexToHash(d.PairCreatedTopic))
		w.dexSwaps = append(w.dexSwaps, common.HexToHash(d.SwapTopic))
	}

	w.loadWatchedContracts()

	go w.subscribeDeployments(client, outFile)
	if w.cfg.Events.Transfers {
		go w.subscribeTransfers(client, outFile)
	}
	if w.cfg.Events.Liquidity || w.cfg.Events.Trades {
		w.subscribeLiquidityAndTrades(client, outFile)
	}
}

func (w *Watcher) loadConfig(path string) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("config open error: %v", err)
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&w.cfg); err != nil {
		log.Fatalf("config decode error: %v", err)
	}

	if w.cfg.RPC == "" {
		log.Fatal("rpc required in config")
	}
	if w.cfg.Output == "" {
		w.cfg.Output = "eth-watch-events.jsonl"
	}
	if w.cfg.Log == "" {
		w.cfg.Log = "eth-watch.log"
	}
}

func (w *Watcher) setupLogging() {
	logFile, err := os.OpenFile(w.cfg.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("log file open error: %v", err)
	}
	log.SetOutput(logFile)
}

func (w *Watcher) loadWatchedContracts() {
	for _, c := range w.cfg.Contracts {
		addr := strings.ToLower(c.Address)
		w.tracked[addr] = &ContractState{
			Deployer:  "unknown",
			TokenType: c.Type,
		}
	}
	log.Printf("Loaded %d watched contracts\n", len(w.tracked))
}

func (w *Watcher) subscribeDeployments(client *ethclient.Client, out *os.File) {
	headers := make(chan *types.Header)
	sub, err := client.SubscribeNewHead(context.Background(), headers)
	if err != nil {
		log.Fatalf("Header subscription failed: %v", err)
	}

	for {
		select {
		case err := <-sub.Err():
			log.Fatalf("Header subscription error: %v", err)

		case header := <-headers:
			block, err := client.BlockByHash(context.Background(), header.Hash())
			if err != nil {
				log.Printf("Block lookup error: %v", err)
				continue
			}

			for _, tx := range block.Transactions() {
				if tx.To() != nil {
					continue
				}

				receipt, err := client.TransactionReceipt(context.Background(), tx.Hash())
				if err != nil || receipt.ContractAddress == (common.Address{}) {
					continue
				}

				code, err := client.CodeAt(context.Background(), receipt.ContractAddress, nil)
				if err != nil || len(code) == 0 {
					continue
				}

				tokenType := detectTokenType(code)
				if tokenType == "" {
					continue
				}

				from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
				if err != nil {
					continue
				}

				addr := strings.ToLower(receipt.ContractAddress.Hex())

				w.lock.Lock()
				w.tracked[addr] = &ContractState{
					Deployer:  from.Hex(),
					TokenType: tokenType,
				}
				w.stats.NewContracts++
				w.lock.Unlock()

				log.Printf("New contract %s type=%s deployer=%s", addr, tokenType, from.Hex())

				writeEvent(out, Finding{
					Contract:  addr,
					Deployer:  from.Hex(),
					Block:     receipt.BlockNumber.Uint64(),
					TokenType: tokenType,
					RiskScore: 10,
					Flags:     []string{"NewContract"},
					TxHash:    tx.Hash().Hex(),
				})

				w.writeStats()
			}
		}
	}
}

func (w *Watcher) subscribeTransfers(client *ethclient.Client, out *os.File) {
	query := ethereum.FilterQuery{
		Topics: [][]common.Hash{{w.transferSig}},
	}

	logsChan := make(chan types.Log)
	sub, err := client.SubscribeFilterLogs(context.Background(), query, logsChan)
	if err != nil {
		log.Fatalf("Transfer subscription failed: %v", err)
	}

	for {
		select {
		case err := <-sub.Err():
			log.Fatalf("Transfer subscription error: %v", err)

		case vLog := <-logsChan:
			w.handleTransfer(vLog, out)
		}
	}
}

func (w *Watcher) subscribeLiquidityAndTrades(client *ethclient.Client, out *os.File) {
	query := ethereum.FilterQuery{
		Topics: [][]common.Hash{append(w.dexPairs, w.dexSwaps...)},
	}

	logsChan := make(chan types.Log)
	sub, err := client.SubscribeFilterLogs(context.Background(), query, logsChan)
	if err != nil {
		log.Fatalf("Liquidity subscription failed: %v", err)
	}

	for {
		select {
		case err := <-sub.Err():
			log.Fatalf("Liquidity subscription error: %v", err)

		case vLog := <-logsChan:
			w.handleLiquidityOrTrade(vLog, out)
		}
	}
}

func (w *Watcher) handleTransfer(vLog types.Log, out *os.File) {
	if len(vLog.Topics) < 3 {
		return
	}

	from := common.HexToAddress(vLog.Topics[1].Hex())
	if from != (common.Address{}) {
		return
	}

	contract := strings.ToLower(vLog.Address.Hex())

	w.lock.Lock()
	state, ok := w.tracked[contract]
	if !ok {
		w.lock.Unlock()
		return
	}
	state.Mints++
	w.stats.Mints++
	w.lock.Unlock()

	log.Printf("Mint detected contract=%s totalMints=%d", contract, state.Mints)

	flags := []string{"MintDetected"}
	score := 40 + state.Mints*15

	to := common.HexToAddress(vLog.Topics[2].Hex())
	if to.Hex() == state.Deployer {
		flags = append(flags, "MintToDeployer")
		score += 15
	}

	if state.Mints > 1 {
		flags = append(flags, "MultipleMints")
	}

	if score > 100 {
		score = 100
	}

	writeEvent(out, Finding{
		Contract:     contract,
		Deployer:     state.Deployer,
		Block:        uint64(vLog.BlockNumber),
		TokenType:    state.TokenType,
		MintDetected: true,
		RiskScore:    score,
		Flags:        flags,
		TxHash:       vLog.TxHash.Hex(),
	})

	w.writeStats()
}

func (w *Watcher) handleLiquidityOrTrade(vLog types.Log, out *os.File) {
	w.lock.Lock()
	defer w.lock.Unlock()

	for addr, state := range w.tracked {
		if containsHash(w.dexPairs, vLog.Topics[0]) && !state.LiquidityCreated {
			state.LiquidityCreated = true
			w.stats.Liquidity++

			log.Printf("Liquidity detected for %s", addr)

			writeEvent(out, Finding{
				Contract:  addr,
				Deployer:  state.Deployer,
				Block:     uint64(vLog.BlockNumber),
				TokenType: state.TokenType,
				RiskScore: 25,
				Flags:     []string{"LiquidityCreated"},
				TxHash:    vLog.TxHash.Hex(),
			})

			w.writeStats()
		}

		if containsHash(w.dexSwaps, vLog.Topics[0]) && !state.Traded {
			state.Traded = true
			w.stats.Trades++

			log.Printf("Trade detected for %s", addr)

			writeEvent(out, Finding{
				Contract:  addr,
				Deployer:  state.Deployer,
				Block:     uint64(vLog.BlockNumber),
				TokenType: state.TokenType,
				RiskScore: 20,
				Flags:     []string{"TradingDetected"},
				TxHash:    vLog.TxHash.Hex(),
			})

			w.writeStats()
		}
	}
}

func detectTokenType(code []byte) string {
	s := strings.ToLower(common.Bytes2Hex(code))
	switch {
	case strings.Contains(s, "a9059cbb"):
		return "ERC20"
	case strings.Contains(s, "80ac58cd"):
		return "ERC721"
	case strings.Contains(s, "d9b67a26"):
		return "ERC1155"
	default:
		return ""
	}
}

func containsHash(list []common.Hash, h common.Hash) bool {
	for _, v := range list {
		if v == h {
			return true
		}
	}
	return false
}

func writeEvent(out *os.File, f Finding) {
	w := bufio.NewWriter(out)
	b, err := json.Marshal(f)
	if err != nil {
		log.Printf("json marshal error: %v", err)
		return
	}
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
	_ = w.Flush()
}

func (w *Watcher) writeStats() {
	uptime := time.Since(w.startTime).Round(time.Second)
	log.Printf(
		"stats uptime=%s contracts=%d mints=%d liquidity=%d trades=%d",
		uptime,
		w.stats.NewContracts,
		w.stats.Mints,
		w.stats.Liquidity,
		w.stats.Trades,
	)
}
