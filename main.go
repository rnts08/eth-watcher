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

var (
	cfg     Config
	tracked = make(map[string]*ContractState)
	lock    sync.RWMutex

	transferSig common.Hash
	dexPairs    []common.Hash
	dexSwaps    []common.Hash

	startTime time.Time
	stats     = struct {
		NewContracts int
		Mints        int
		Liquidity    int
		Trades       int
	}{}
)

func main() {
	startTime = time.Now()

	configPath := flag.String("config", "config.json", "Path to configuration JSON")
	flag.Parse()

	loadConfig(*configPath)
	setupLogging()

	log.Println("eth-watch starting…")

	client, err := ethclient.Dial(cfg.RPC)
	if err != nil {
		log.Fatalf("RPC connection failed: %v", err)
	}

	outFile, err := os.OpenFile(cfg.Output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open output file: %v", err)
	}
	defer outFile.Close()

	transferSig = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")

	for _, d := range cfg.Dexes {
		dexPairs = append(dexPairs, common.HexToHash(d.PairCreatedTopic))
		dexSwaps = append(dexSwaps, common.HexToHash(d.SwapTopic))
	}

	loadWatchedContracts()

	go subscribeDeployments(client, outFile)
	if cfg.Events.Transfers {
		go subscribeTransfers(client, outFile)
	}
	if cfg.Events.Liquidity || cfg.Events.Trades {
		subscribeLiquidityAndTrades(client, outFile)
	}
}

func loadConfig(path string) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("config open error: %v", err)
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		log.Fatalf("config decode error: %v", err)
	}

	if cfg.RPC == "" {
		log.Fatal("rpc required in config")
	}
	if cfg.Output == "" {
		cfg.Output = "eth-watch-events.jsonl"
	}
	if cfg.Log == "" {
		cfg.Log = "eth-watch.log"
	}
}

func setupLogging() {
	logFile, err := os.OpenFile(cfg.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("log file open error: %v", err)
	}
	log.SetOutput(logFile)
}

func loadWatchedContracts() {
	for _, c := range cfg.Contracts {
		addr := strings.ToLower(c.Address)
		tracked[addr] = &ContractState{
			Deployer:  "unknown",
			TokenType: c.Type,
		}
	}
	log.Printf("Loaded %d watched contracts\n", len(tracked))
}

func subscribeDeployments(client *ethclient.Client, out *os.File) {
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

				lock.Lock()
				tracked[addr] = &ContractState{
					Deployer:  from.Hex(),
					TokenType: tokenType,
				}
				stats.NewContracts++
				lock.Unlock()

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

				writeStats()
			}
		}
	}
}

func subscribeTransfers(client *ethclient.Client, out *os.File) {
	query := ethereum.FilterQuery{
		Topics: [][]common.Hash{{transferSig}},
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
			handleTransfer(vLog, out)
		}
	}
}

func subscribeLiquidityAndTrades(client *ethclient.Client, out *os.File) {
	query := ethereum.FilterQuery{
		Topics: [][]common.Hash{append(dexPairs, dexSwaps...)},
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
			handleLiquidityOrTrade(vLog, out)
		}
	}
}

func handleTransfer(vLog types.Log, out *os.File) {
	if len(vLog.Topics) < 3 {
		return
	}

	from := common.HexToAddress(vLog.Topics[1].Hex())
	if from != (common.Address{}) {
		return
	}

	contract := strings.ToLower(vLog.Address.Hex())

	lock.Lock()
	state, ok := tracked[contract]
	if !ok {
		lock.Unlock()
		return
	}
	state.Mints++
	stats.Mints++
	lock.Unlock()

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

	writeStats()
}

func handleLiquidityOrTrade(vLog types.Log, out *os.File) {
	lock.Lock()
	defer lock.Unlock()

	for addr, state := range tracked {
		if containsHash(dexPairs, vLog.Topics[0]) && !state.LiquidityCreated {
			state.LiquidityCreated = true
			stats.Liquidity++

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

			writeStats()
		}

		if containsHash(dexSwaps, vLog.Topics[0]) && !state.Traded {
			state.Traded = true
			stats.Trades++

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

			writeStats()
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

func writeStats() {
	uptime := time.Since(startTime).Round(time.Second)
	log.Printf(
		"stats uptime=%s contracts=%d mints=%d liquidity=%d trades=%d",
		uptime,
		stats.NewContracts,
		stats.Mints,
		stats.Liquidity,
		stats.Trades,
	)
}

