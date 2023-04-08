// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Package ethconfig contains the configuration of the ETH and LES protocols.
package ethconfig

import (
	"math/big"
	"os"
	"os/user"
	"time"

	"github.com/pioplat/pioplat-core/common"
	"github.com/pioplat/pioplat-core/core"
	"github.com/pioplat/pioplat-core/params"
)

// Defaults contains default settings for use on the Ethereum main net.
var Defaults = Config{
	NetworkId:               1,
	TxLookupLimit:           2350000,
	LightPeers:              100,
	UltraLightFraction:      75,
	DatabaseCache:           512,
	TrieCleanCache:          154,
	TrieCleanCacheJournal:   "triecache",
	TrieCleanCacheRejournal: 60 * time.Minute,
	TrieDirtyCache:          256,
	TrieTimeout:             60 * time.Minute,
	TriesInMemory:           128,
	SnapshotCache:           102,
	DiffBlock:               uint64(86400),
	TxPool:                  core.DefaultTxPoolConfig,
	RPCGasCap:               50000000,
	RPCEVMTimeout:           5 * time.Second,
	RPCTxFeeCap:             1, // 1 ether

	// PERI_PEER_EVICTION_POLICY_CODE_PIECE
	PeriActive: false,
}

func init() {
	home := os.Getenv("HOME")
	if home == "" {
		if user, err := user.Current(); err == nil {
			home = user.HomeDir
		}
	}
}

//go:generate gencodec -type Config -formats toml -out gen_config.go

// Config contains configuration options for of the ETH and LES protocols.
type Config struct {
	// The genesis block, which is inserted if the database is empty.
	// If nil, the Ethereum main net block is used.
	Genesis *core.Genesis `toml:",omitempty"`

	// Protocol options
	NetworkId              uint64 // Network ID to use for selecting peers to connect to
	DisablePeerTxBroadcast bool

	// This can be set to list of enrtree:// URLs which will be queried for
	// for nodes to connect to.
	EthDiscoveryURLs   []string
	SnapDiscoveryURLs  []string
	TrustDiscoveryURLs []string

	NoPruning           bool // Whether to disable pruning and flush everything to disk
	DirectBroadcast     bool
	DisableSnapProtocol bool //Whether disable snap protocol
	DisableDiffProtocol bool //Whether disable diff protocol
	EnableTrustProtocol bool //Whether enable trust protocol
	DiffSync            bool // Whether support diff sync
	PipeCommit          bool
	RangeLimit          bool

	TxLookupLimit uint64 `toml:",omitempty"` // The maximum number of blocks from head whose tx indices are reserved.

	// Whitelist of required block number -> hash values to accept
	Whitelist map[uint64]common.Hash `toml:"-"`

	// Light client options
	LightServ          int  `toml:",omitempty"` // Maximum percentage of time allowed for serving LES requests
	LightIngress       int  `toml:",omitempty"` // Incoming bandwidth limit for light servers
	LightEgress        int  `toml:",omitempty"` // Outgoing bandwidth limit for light servers
	LightPeers         int  `toml:",omitempty"` // Maximum number of LES client peers
	LightNoPrune       bool `toml:",omitempty"` // Whether to disable light chain pruning
	LightNoSyncServe   bool `toml:",omitempty"` // Whether to serve light clients before syncing
	SyncFromCheckpoint bool `toml:",omitempty"` // Whether to sync the header chain from the configured checkpoint

	// Ultra Light client options
	UltraLightServers      []string `toml:",omitempty"` // List of trusted ultra light servers
	UltraLightFraction     int      `toml:",omitempty"` // Percentage of trusted servers to accept an announcement
	UltraLightOnlyAnnounce bool     `toml:",omitempty"` // Whether to only announce headers, or also serve them

	// Database options
	SkipBcVersionCheck bool `toml:"-"`
	DatabaseHandles    int  `toml:"-"`
	DatabaseCache      int
	DatabaseFreezer    string
	DatabaseDiff       string
	PersistDiff        bool
	DiffBlock          uint64
	PruneAncientData   bool

	TrieCleanCache          int
	TrieCleanCacheJournal   string        `toml:",omitempty"` // Disk journal directory for trie cache to survive node restarts
	TrieCleanCacheRejournal time.Duration `toml:",omitempty"` // Time interval to regenerate the journal for clean cache
	TrieDirtyCache          int
	TrieTimeout             time.Duration
	SnapshotCache           int
	TriesInMemory           uint64
	Preimages               bool

	// Transaction pool options
	TxPool core.TxPoolConfig

	// Enables tracking of SHA3 preimages in the VM
	EnablePreimageRecording bool

	// Miscellaneous options
	DocRoot string `toml:"-"`

	// RPCGasCap is the global gas cap for eth-call variants.
	RPCGasCap uint64

	// RPCEVMTimeout is the global timeout for eth-call.
	RPCEVMTimeout time.Duration

	// RPCTxFeeCap is the global transaction fee(price * gaslimit) cap for
	// send-transction variants. The unit is ether.
	RPCTxFeeCap float64

	// Checkpoint is a hardcoded checkpoint which can be nil.
	Checkpoint *params.TrustedCheckpoint `toml:",omitempty"`

	// CheckpointOracle is the configuration for checkpoint oracle.
	CheckpointOracle *params.CheckpointOracleConfig `toml:",omitempty"`

	// Berlin block override (TODO: remove after the fork)
	OverrideBerlin *big.Int `toml:",omitempty"`

	// Arrow Glacier block override (TODO: remove after the fork)
	OverrideArrowGlacier *big.Int `toml:",omitempty"`

	// OverrideTerminalTotalDifficulty (TODO: remove after the fork)
	OverrideTerminalTotalDifficulty *big.Int `toml:",omitempty"`

	// PERI_PEER_EVICTION_POLICY_CODE_PIECE
	PeriActive               bool    // global switch of Peri peer eviction policy
	PeriPeriod               uint64  // period of peer reselection in seconds
	PeriReplaceRatio         float64 // 0~1, ratio of replaced peers in each epoch
	PeriBlockNodeRatio       float64 // 0~1, ratio of reserve peers that approach to nodes who broadcast blocks
	PeriTxNodeRatio          float64 // 0~1, ratio of reserve peers that approach to nodes who broadcast transactions
	PeriMinInbound           int     //
	PeriMaxDelayPenalty      uint64  // Maximum delay of a tx recorded at a neighbor in ms
	PeriAnnouncePenalty      uint64  // The penalty for announce, to encourage receive block body, in ms
	PeriMaxDeliveryTolerance int64
	PeriObservedTxRatio      int      // (1 / sampling rate) of global latency; must be int
	PeriTargeted             bool     // global latency if `false`, targeted latency if `true` (target accounts in `TargetAccountList`)
	PeriShowTxDelivery       bool     // Controls whether the console prints all txs
	PeriTargetAccountList    []string // list of target accounts
	PeriNoPeerIPList         []string // node with IP addresses in this list are permanently blocked
	PeriNoDropList           []string
	PeriMaxTransactionAmount int      // max count of old transactions in old arrival record
	PeriMaxBlockAmount       int      // max count of old blocks in old arrival record
	PeriLogFilePath          string   // peri log file path
	PeriDataDirectory        string   // database to store evicted peers
	PeriBroadcast            bool     // Flag whether broadcast block to peri peers
	PeriPeersIp              []string // Peri peers' ip list

	// Pioplat Disguise Mechanism
	DisguiseServerUrl          string // tcp address of full node that instrumented to provider forkId or something else information
	DisguiseServerX509CertFile string // server tls cert file

	PioplatServerListenAddr string // tcp & udp address of pioplat server
	EvaluationBlockJsonFile string
	EvaluationTxJsonFile    string

	PioplatRpcAdminSecret string // secret of rcp, used to add pioplat relay nodes, to send transaction in mainnet
}
