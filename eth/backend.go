// Copyright 2014 The go-ethereum Authors
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

// Package eth implements the Ethereum protocol.
package eth

import (
	"fmt"
	"github.com/pioplat/pioplat-core/common"
	"github.com/pioplat/pioplat-core/core"
	"github.com/pioplat/pioplat-core/core/types"
	"github.com/pioplat/pioplat-core/eth/ethconfig"
	"github.com/pioplat/pioplat-core/eth/protocols/eth"
	"github.com/pioplat/pioplat-core/event"
	"github.com/pioplat/pioplat-core/log"
	"github.com/pioplat/pioplat-core/node"
	"github.com/pioplat/pioplat-core/p2p"
	"github.com/pioplat/pioplat-core/p2p/dnsdisc"
	"github.com/pioplat/pioplat-core/p2p/enode"
	"github.com/pioplat/pioplat-core/params"
	"sync"
	"sync/atomic"
)

// Config contains the configuration options of the ETH protocol.
// Deprecated: use ethconfig.Config instead.
type Config = ethconfig.Config

// Ethereum implements the Ethereum full node service.
type Ethereum struct {
	config *ethconfig.Config

	// Handlers
	txPool              *core.TxPool
	blockchain          *core.Blockchain
	handler             *handler
	ethDialCandidates   enode.Iterator
	snapDialCandidates  enode.Iterator
	trustDialCandidates enode.Iterator

	eventMux *event.TypeMux

	networkID uint64

	p2pServer *p2p.Server

	lock sync.RWMutex // Protects the variadic fields (e.g. gas price and etherbase)

}

// New creates a new Ethereum object (including the
// initialisation of the common Ethereum object)
func New(stack *node.Node, config *ethconfig.Config) (*Ethereum, error) {
	// Ensure configuration values are compatible and sane
	if config.NoPruning && config.TrieDirtyCache > 0 {
		if config.SnapshotCache > 0 {
			config.TrieCleanCache += config.TrieDirtyCache * 3 / 5
			config.SnapshotCache += config.TrieDirtyCache * 2 / 5
		} else {
			config.TrieCleanCache += config.TrieDirtyCache
		}
		config.TrieDirtyCache = 0
	}
	log.Info("Allocated trie memory caches", "clean", common.StorageSize(config.TrieCleanCache)*1024*1024,
		"dirty", common.StorageSize(config.TrieDirtyCache)*1024*1024)

	eth := &Ethereum{
		config:     config,
		txPool:     core.NewTxPool(config.TxPool, config.EvaluationTxJsonFile), // TODO: implement transaction pool
		blockchain: core.NewBlockchain(params.BSCChainConfig, params.BSCGenesisHash, config.EvaluationBlockJsonFile),
		eventMux:   stack.EventMux(),
		networkID:  config.NetworkId,
		p2pServer:  stack.Server(),
	}

	var dbVer = "<nil>"
	log.Info("Initialising Ethereum protocol", "network", config.NetworkId, "dbversion", dbVer)

	var (
		err error
	)

	peers := newPeerSet()
	if err != nil {
		return nil, err
	}

	ethHandlerConfig := &handlerConfig{
		TxPool:                 eth.txPool,
		Chain:                  eth.blockchain,
		Network:                config.NetworkId,
		EventMux:               eth.eventMux,
		Whitelist:              config.Whitelist,
		DirectBroadcast:        config.DirectBroadcast,
		DiffSync:               config.DiffSync,
		DisablePeerTxBroadcast: config.DisablePeerTxBroadcast,
		PeerSet:                peers,

		// PERI_AND_LATENCY_RECORDER_CODE_PIECE
		PeriBroadcast: config.PeriBroadcast,
		PeriPeersIp:   make(map[string]interface{}),
	}

	for _, ip := range config.PeriPeersIp {
		ethHandlerConfig.PeriPeersIp[ip] = nil
	}

	// start Pioplat disguise client
	disguise = CreateDisguise(config)

	pioplatServer = CreatePioplatServer(eth.blockchain, eth.txPool, config.PioplatServerListenAddr, config.PioplatRpcAdminSecret)
	if eth.handler, err = newHandler(ethHandlerConfig); err != nil {
		return nil, err
	}
	pioplatServer.SetHandler(eth.handler)
	pioplatServer.SetEnqueueFn(eth.handler.blockFetcher.Enqueue, eth.handler.txFetcher.Enqueue)

	// Setup DNS discovery iterators.
	dnsclient := dnsdisc.NewClient(dnsdisc.Config{})
	eth.ethDialCandidates, err = dnsclient.NewIterator(eth.config.EthDiscoveryURLs...)
	if err != nil {
		return nil, err
	}
	eth.snapDialCandidates, err = dnsclient.NewIterator(eth.config.SnapDiscoveryURLs...)
	if err != nil {
		return nil, err
	}
	eth.trustDialCandidates, err = dnsclient.NewIterator(eth.config.TrustDiscoveryURLs...)
	if err != nil {
		return nil, err
	}

	// Register the backend on the node
	stack.RegisterProtocols(eth.Protocols())
	stack.RegisterLifecycle(eth)

	// PERI_AND_LATENCY_RECORDER_CODE_PIECE
	eth.config.PeriDataDirectory = stack.ResolvePath("")

	return eth, nil
}

func (s *Ethereum) TxPool() *core.TxPool     { return s.txPool }
func (s *Ethereum) EventMux() *event.TypeMux { return s.eventMux }
func (s *Ethereum) IsListening() bool        { return true } // Always listening
func (s *Ethereum) Synced() bool             { return atomic.LoadUint32(&s.handler.acceptTxs) == 1 }
func (s *Ethereum) SetSynced()               { atomic.StoreUint32(&s.handler.acceptTxs, 1) }
func (s *Ethereum) ArchiveMode() bool        { return s.config.NoPruning }

// Protocols returns all the currently configured
// network protocols to start.
func (s *Ethereum) Protocols() []p2p.Protocol {
	protos := eth.MakeProtocols((*ethHandler)(s.handler), s.networkID, s.ethDialCandidates)
	return protos
}

// Start implements node.Lifecycle, starting all internal goroutines needed by the
// Ethereum protocol implementation.
func (s *Ethereum) Start() error {
	eth.StartENRUpdater(s.blockchain, s.p2pServer.LocalNode())

	// Figure out a max peers count based on the server limits
	maxPeers := s.p2pServer.MaxPeers
	if s.config.LightServ > 0 {
		if s.config.LightPeers >= s.p2pServer.MaxPeers {
			return fmt.Errorf("invalid peer config: light peer count (%d) >= total peer count (%d)", s.config.LightPeers, s.p2pServer.MaxPeers)
		}
		maxPeers -= s.config.LightPeers
	}
	// Start the networking layer and the light server if requested
	s.handler.Start(maxPeers)

	if err := pioplatServer.Start(); err != nil {
		log.Crit("start pioplat server failed", "reason", err)
	}

	// PERI_AND_LATENCY_RECORDER_CODE_PIECE
	peri = CreatePeri(s.p2pServer, s.config, s.handler)
	// start running peri eviction
	peri.StartPeri()
	pioplatServer.setRatiosFn = peri.SetRatios

	// set peri to BlockChain and txPool objects
	s.txPool.SetBroadcastFn(func(txs []*types.Transaction) {
		peri.BroadcastTransactionsToPioplatPeer(txs)
	})
	s.blockchain.SetBroadcastFn(func(b *types.Block) {
		peri.BroadcastBlockToPioplatPeer(nil, b, b.Difficulty())
	})

	s.blockchain.SetPioplatFullNodeFn(disguise.RequestHeaderByHash,
		disguise.RequestHeaderByHashAndNumber,
		disguise.RequestHeaderByNumber,
		disguise.RequestBlockBodyRLP,
		disguise.RequestReceiptsByHash,
		disguise.RequestTrieNodeByHash,
		disguise.RequestContractCodeWithPrefix,
		disguise.RequestAncestor,
		disguise.RequestTotalDifficulty)
	return nil
}

// Stop implements node.Lifecycle, terminating all internal goroutines used by the
// Ethereum protocol.
func (s *Ethereum) Stop() error {
	// Stop all the peer-related stuff first.
	s.ethDialCandidates.Close()
	s.snapDialCandidates.Close()
	s.trustDialCandidates.Close()
	s.handler.Stop()

	// Then stop everything else.
	s.txPool.Stop()

	// Clean shutdown marker as the last thing before closing db
	s.eventMux.Stop()

	return nil
}
