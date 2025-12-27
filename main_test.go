package main

import (
	"encoding/hex"
	"testing"
)

func TestDetectTokenType(t *testing.T) {
	tests := []struct {
		name     string
		bytecode string
		want     string
	}{
		{"ERC20", "608060405234801561001057600080fd5b50a9059cbb", "ERC20"},
		{"ERC721", "608060405280ac58cd", "ERC721"},
		{"ERC1155", "6080604052d9b67a26", "ERC1155"},
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

func TestAnalyzeCode(t *testing.T) {
	tests := []struct {
		name         string
		bytecode     string
		wantFlags    []string
		wantScoreMin int
	}{
		{
			name:         "SelfDestruct",
			bytecode:     "6080604052ff", // FF is SELFDESTRUCT
			wantFlags:    []string{"SelfDestruct", "Stateless"},
			wantScoreMin: 80,
		},
		{
			name:         "BadRandomness",
			bytecode:     "608060405240", // 40 is BLOCKHASH
			wantFlags:    []string{"BadRandomness", "Stateless"},
			wantScoreMin: 45,
		},
		{
			name:         "FakeToken",
			bytecode:     "6080604052a9059cbb", // Transfer sig (a9059cbb) + No SSTORE
			wantFlags:    []string{"Stateless", "FakeToken"},
			wantScoreMin: 80,
		},
		{
			name:         "ValidToken",
			bytecode:     "608060405255a9059cbb", // SSTORE (55) + Transfer sig
			wantFlags:    []string{},             // Should NOT have Stateless or FakeToken
			wantScoreMin: 0,
		},
		{
			name:         "MintableBurnable",
			bytecode:     "40c10f1942966c6855", // Mint sig + Burn sig + SSTORE
			wantFlags:    []string{"Mintable", "Burnable"},
			wantScoreMin: 10,
		},
		{
			name:         "LowLevelCallAndFactory",
			bytecode:     "f1f055", // CALL (F1) + CREATE (F0) + SSTORE
			wantFlags:    []string{"LowLevelCall", "ContractFactory"},
			wantScoreMin: 20,
		},
		{
			name:         "CalldataSize",
			bytecode:     "3655", // CALLDATASIZE (36) + SSTORE
			wantFlags:    []string{"CalldataSizeCheck"},
			wantScoreMin: 5,
		},
		{
			name:         "TaxToken",
			bytecode:     "a9059cbb0455", // Transfer sig + DIV (04) + SSTORE
			wantFlags:    []string{"TaxToken"},
			wantScoreMin: 20,
		},
		{
			name:         "StrictBalanceEquality",
			bytecode:     "311455", // BALANCE (31) + EQ (14) + SSTORE
			wantFlags:    []string{"StrictBalanceEquality"},
			wantScoreMin: 10,
		},
		{
			name:         "UncheckedCall",
			bytecode:     "f15055", // CALL (F1) + POP (50) + SSTORE
			wantFlags:    []string{"UncheckedCall"},
			wantScoreMin: 25, // LowLevelCall (10) + UncheckedCall (15)
		},
		{
			name:         "LockedEther",
			bytecode:     "600055", // PUSH1 00 + SSTORE (No CALL/CREATE/SELFDESTRUCT)
			wantFlags:    []string{"LockedEther"},
			wantScoreMin: 5,
		},
		{
			name:         "DivideBeforeMultiply",
			bytecode:     "0460020255", // DIV (04) + PUSH1 02 + MUL (02) + SSTORE
			wantFlags:    []string{"DivideBeforeMultiply"},
			wantScoreMin: 10,
		},
		{
			name:         "ShadowingState",
			bytecode:     "545055", // SLOAD (54) + POP (50) + SSTORE
			wantFlags:    []string{"ShadowingState"},
			wantScoreMin: 5,
		},
		{
			name:         "GasUsage",
			bytecode:     "5a55", // GAS (5A) + SSTORE
			wantFlags:    []string{"GasUsage"},
			wantScoreMin: 5,
		},
		{
			name:         "IncorrectConstructor",
			bytecode:     "673448dd55", // constructor() sig + SSTORE
			wantFlags:    []string{"IncorrectConstructor"},
			wantScoreMin: 5,
		},
		{
			name:         "HiddenMint",
			bytecode:     "a9059cbb55", // Transfer sig + SSTORE + NO Transfer Event Topic
			wantFlags:    []string{"HiddenMint"},
			wantScoreMin: 40,
		},
		{
			name:         "WriteToSlotZero",
			bytecode:     "600055", // PUSH1 00 + SSTORE
			wantFlags:    []string{"WriteToSlotZero"},
			wantScoreMin: 20,
		},
		{
			name:         "SuspiciousCodeSize",
			bytecode:     "3855", // CODESIZE + SSTORE
			wantFlags:    []string{"SuspiciousCodeSize"},
			wantScoreMin: 5,
		},
		{
			name:         "HardcodedGasLimit",
			bytecode:     "60fff155", // PUSH1 ff + CALL + SSTORE
			wantFlags:    []string{"HardcodedGasLimit", "LowLevelCall"},
			wantScoreMin: 15,
		},
		{
			name:         "ReturnBomb",
			bytecode:     "fd", // REVERT (No RETURN, No STOP)
			wantFlags:    []string{"ReturnBomb"},
			wantScoreMin: 50,
		},
		{
			name:         "ERC777Reentrancy",
			bytecode:     "6000551820a4b7618bde71dce8cdc73aab6c95905fad24", // SSTORE + ERC1820 Address
			wantFlags:    []string{"ERC777Reentrancy"},
			wantScoreMin: 20,
		},
		{
			name:         "UncheckedReturnData",
			bytecode:     "f155", // CALL (F1) + SSTORE (No RETURNDATASIZE 3D)
			wantFlags:    []string{"UncheckedReturnData", "LowLevelCall"},
			wantScoreMin: 20,
		},
		{
			name:         "CheckedReturnData",
			bytecode:     "f13d55",                 // CALL (F1) + RETURNDATASIZE (3D) + SSTORE
			wantFlags:    []string{"LowLevelCall"}, // Should NOT have UncheckedReturnData
			wantScoreMin: 10,
		},
		{
			name:         "WithdrawalAndRenounce",
			bytecode:     "3ccfd60b715018a655", // withdraw() + renounceOwnership() + SSTORE
			wantFlags:    []string{"Withdrawal", "RenounceOwnership"},
			wantScoreMin: 0,
		},
		{
			name:         "LoopDetected",
			bytecode:     "5b600057", // JUMPDEST (0) + PUSH1 0 + JUMPI (Loop to 0)
			wantFlags:    []string{"LoopDetected"},
			wantScoreMin: 5,
		},
		{
			name:         "InfiniteLoop",
			bytecode:     "5b600056", // JUMPDEST (0) + PUSH1 0 + JUMP (Unconditional loop)
			wantFlags:    []string{"LoopDetected", "InfiniteLoop"},
			wantScoreMin: 25,
		},
		{
			name:         "CallInLoop",
			bytecode:     "5bf1600057", // JUMPDEST + CALL + PUSH1 0 + JUMPI
			wantFlags:    []string{"LoopDetected", "CallInLoop"},
			wantScoreMin: 15,
		},
		{
			name:         "GasDependentLoop",
			bytecode:     "5b5a600057", // JUMPDEST + GAS + PUSH1 0 + JUMPI
			wantFlags:    []string{"LoopDetected", "GasDependentLoop"},
			wantScoreMin: 15,
		},
		{
			name:         "SuspiciousStateChange",
			bytecode:     "600155", // PUSH 1 + SSTORE (No SLOAD)
			wantFlags:    []string{"SuspiciousStateChange"},
			wantScoreMin: 10,
		},
		{
			name:         "NormalStateChange",
			bytecode:     "5455",     // SLOAD + SSTORE
			wantFlags:    []string{}, // Should NOT have SuspiciousStateChange
			wantScoreMin: 0,
		},
		{
			name:         "DelegateCallToZero",
			bytecode:     "6000f4", // PUSH1 0 + DELEGATECALL
			wantFlags:    []string{"DelegateCall", "DelegateCallToZero"},
			wantScoreMin: 50,
		},
		{
			name:         "CostlyLoop",
			bytecode:     "5b55600057", // JUMPDEST + SSTORE + PUSH1 0 + JUMPI
			wantFlags:    []string{"LoopDetected", "CostlyLoop"},
			wantScoreMin: 15,
		},
		{
			name:         "ProxyDestruction",
			bytecode:     "f4ff", // DELEGATECALL + SELFDESTRUCT
			wantFlags:    []string{"DelegateCall", "SelfDestruct", "ProxyDestruction"},
			wantScoreMin: 90,
		},
		{
			name:         "MetamorphicExploit",
			bytecode:     "f5ff", // CREATE2 + SELFDESTRUCT
			wantFlags:    []string{"Metamorphic", "SelfDestruct", "MetamorphicExploit"},
			wantScoreMin: 100,
		},
		{
			name:         "HardcodedSelfDestruct",
			bytecode:     "730000000000000000000000000000000000000001ff", // PUSH20 (addr) + SELFDESTRUCT
			wantFlags:    []string{"SelfDestruct", "HardcodedSelfDestruct"},
			wantScoreMin: 100,
		},
		{
			name:         "UncheckedMath",
			bytecode:     "0155", // ADD (01) + SSTORE (No Panic signature)
			wantFlags:    []string{"UncheckedMath"},
			wantScoreMin: 10,
		},
		{
			name:         "UnsafeDelegateCall",
			bytecode:     "35f4", // CALLDATALOAD (35) + DELEGATECALL (F4)
			wantFlags:    []string{"DelegateCall", "UnsafeDelegateCall"},
			wantScoreMin: 40,
		},
		{
			name:         "ReentrancyGuard",
			bytecode:     "6000555265656e7472616e63794775617264", // SSTORE + "ReentrancyGuard" string
			wantFlags:    []string{"ReentrancyGuard"},
			wantScoreMin: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _ := hex.DecodeString(tt.bytecode)
			flags, score := analyzeCode(code)

			for _, want := range tt.wantFlags {
				found := false
				for _, got := range flags {
					if got == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("analyzeCode() missing flag %v. Got: %v", want, flags)
				}
			}

			if score < tt.wantScoreMin {
				t.Errorf("analyzeCode() score = %v, want >= %v", score, tt.wantScoreMin)
			}
		})
	}
}

func TestBuildRPCURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		key  string
		want string
	}{
		{
			name: "NoKey",
			base: "https://rpc.example.com",
			key:  "",
			want: "https://rpc.example.com",
		},
		{
			name: "SimpleAppend",
			base: "https://rpc.example.com",
			key:  "12345",
			want: "https://rpc.example.com/12345",
		},
		{
			name: "BaseWithSlash",
			base: "https://rpc.example.com/",
			key:  "12345",
			want: "https://rpc.example.com/12345",
		},
		{
			name: "QueryParamKey",
			base: "https://rpc.example.com",
			key:  "?key=12345",
			want: "https://rpc.example.com?key=12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildRPCURL(tt.base, tt.key); got != tt.want {
				t.Errorf("buildRPCURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "ValidConfig",
			cfg: Config{
				RPC: []RPCConfig{{URL: "wss://example.com"}},
			},
			wantErr: false,
		},
		{
			name: "NoRPCs",
			cfg: Config{
				RPC: []RPCConfig{},
			},
			wantErr: true,
		},
		{
			name: "EmptyRPCURL",
			cfg: Config{
				RPC: []RPCConfig{{URL: ""}},
			},
			wantErr: true,
		},
		{
			name: "ValidWhaleThreshold",
			cfg: Config{
				RPC:            []RPCConfig{{URL: "wss://example.com"}},
				WhaleThreshold: "1000000000000000000",
			},
			wantErr: false,
		},
		{
			name: "InvalidWhaleThreshold",
			cfg: Config{
				RPC:            []RPCConfig{{URL: "wss://example.com"}},
				WhaleThreshold: "not-a-number",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateConfig(&tt.cfg); (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
