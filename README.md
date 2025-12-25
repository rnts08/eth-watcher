# ETH Watcher

![Go CI](https://github.com/rnts08/eth-watcher/actions/workflows/ci.yml/badge.svg)

ETH Watcher is a real-time Ethereum event monitoring tool written in Go. It connects to an Ethereum RPC node via WebSocket to detect and analyze various on-chain activities, including contract deployments, token mints, liquidity creation, and DEX trades.

## Features

- **Contract Discovery**: Detects new smart contract deployments and identifies token standards (ERC20, ERC721, ERC1155).
- **Mint Detection**: Monitors `Transfer` events to detect token minting activities (transfers from the zero address).
- **DEX Monitoring**: Watches for liquidity pool creation and token swaps on configured DEXes (e.g., Uniswap V2).
- **Metrics**: Exposes Prometheus metrics for monitoring the watcher's health and detected events.
- **Resilience**: Includes a watchdog to detect stalled RPC connections and automatically reconnect.
- **Graceful Shutdown**: Handles OS signals (`SIGINT`, `SIGTERM`) for clean termination.

## Prerequisites

- Go 1.24 or later
- An Ethereum RPC WebSocket endpoint (e.g., Infura, Alchemy, or a public node).

## Configuration

The application is configured via a JSON file (default: `config.json`).

Key configuration sections:

- `rpc`: WebSocket URL for the Ethereum node.
- `events`: Toggles for specific event types (`transfers`, `liquidity`, `trades`).
- `dexes`: List of DEX event topics to watch.
- `contracts`: List of specific contracts to monitor with associated metadata.

## Building

To build the application executable:

```bash
go build -o eth-watch
```

## Running

Start the watcher by providing the path to your configuration file:

```bash
./eth-watch -config config.json
```

### Command Line Flags

- `-config`: Path to the configuration JSON file (default: `config.json`).
- `-metrics`: Address to serve Prometheus metrics (default: `:2112`).

## Testing

The project includes unit tests for event handling logic, token type detection, and concurrency safety.

To run the tests:

```bash
go test -v .
```

To run tests with the Go race detector enabled (recommended to verify thread safety):

```bash
go test -race -v .
```

## Output

### Events

Detected events are written to the configured output file (e.g., `eth-watch-events.jsonl`) in JSON Lines format.

Example:

```json
{"contract":"0x...","deployer":"0x...","block":123456,"tokenType":"ERC20","mintDetected":true,"riskScore":55,"flags":["MintDetected"],"txHash":"0x..."}
```

### Metrics

Prometheus metrics are exposed at `http://localhost:2112/metrics` (or the configured address).


### Tips and appreciations

***ETH/ERC20:*** 0x9b4FfDADD87022C8B7266e28ad851496115ffB48

***SOL:*** 68L4XzSbRUaNE4UnxEd8DweSWEoiMQi6uygzERZLbXDw

***BTC:*** bc1qkmzc6d49fl0edyeynezwlrfqv486nmk6p5pmta
