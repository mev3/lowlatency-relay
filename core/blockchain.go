package core

import (
	"math/big"
	"sync"
	"time"

	"github.com/pioplat/pioplat-core/common"
	"github.com/pioplat/pioplat-core/core/types"
	"github.com/pioplat/pioplat-core/event"
	"github.com/pioplat/pioplat-core/log"
	"github.com/pioplat/pioplat-core/params"
	"github.com/pioplat/pioplat-core/rlp"
	"github.com/pioplat/pioplat-core/trie"
)

const BlockContainerSizeLimit = 128

type fixedSizeBlockQueue struct {
	blockNumbers  []uint64
	uniqueNumbers map[uint64][]*types.Block
	hash2Number   map[common.Hash]uint64
	size          int
	head          int
	count         int
	mut           *sync.Mutex
}

func newFixedSizeBlockQueue(capacity int) *fixedSizeBlockQueue {
	q := &fixedSizeBlockQueue{
		blockNumbers:  make([]uint64, capacity),
		uniqueNumbers: make(map[uint64][]*types.Block),
		hash2Number:   make(map[common.Hash]uint64),
		size:          capacity,
		head:          0,
		count:         0,
		mut:           new(sync.Mutex),
	}
	return q
}

func (q *fixedSizeBlockQueue) Push(block *types.Block) {
	q.mut.Lock()
	defer q.mut.Unlock()
	var blockNumber = block.NumberU64()
	if _, alreadyExists := q.uniqueNumbers[blockNumber]; alreadyExists {
		q.uniqueNumbers[blockNumber] = append(q.uniqueNumbers[blockNumber], block)
		q.hash2Number[block.Hash()] = blockNumber
		return
	}

	if q.count >= q.size {
		blockNumberToRemove := q.blockNumbers[(q.head+q.size-1)%q.size]
		for _, block := range q.uniqueNumbers[blockNumberToRemove] {
			delete(q.hash2Number, block.Hash())
		}
		delete(q.uniqueNumbers, blockNumberToRemove)
		q.count -= 1
	}

	q.blockNumbers[q.head] = blockNumber
	q.head = (q.head + 1) % q.size

	q.uniqueNumbers[blockNumber] = append(q.uniqueNumbers[blockNumber], block)
	q.hash2Number[block.Hash()] = blockNumber
	q.count += 1
}

func (q *fixedSizeBlockQueue) First() *types.Block {
	q.mut.Lock()
	defer q.mut.Unlock()

	if q.count > 0 {
		currentBlockNumber := q.blockNumbers[(q.head+q.size-1)%q.size]
		return q.uniqueNumbers[currentBlockNumber][0]
	}
	return nil
}

func (q *fixedSizeBlockQueue) Exist(hash common.Hash, number uint64) bool {
	q.mut.Lock()
	defer q.mut.Unlock()

	if _, alreadyExists := q.uniqueNumbers[number]; !alreadyExists {
		return false
	}
	for _, block := range q.uniqueNumbers[number] {
		if block.Hash() == hash {
			return true
		}
	}
	return false
}

func (q *fixedSizeBlockQueue) ExistByHash(hash common.Hash) bool {
	q.mut.Lock()
	defer q.mut.Unlock()
	if _, exist := q.hash2Number[hash]; exist {
		return true
	}
	return false
}

func (q *fixedSizeBlockQueue) Hash2Number(hash common.Hash) (uint64, bool) {
	q.mut.Lock()
	defer q.mut.Unlock()
	number, ok := q.hash2Number[hash]
	return number, ok
}

func (q *fixedSizeBlockQueue) UniqueNumber(number uint64) ([]*types.Block, bool) {
	q.mut.Lock()
	defer q.mut.Unlock()
	blocks, ok := q.uniqueNumbers[number]
	return blocks, ok
}

type broadcastBlockToPioplatFn func(b *types.Block)
type reqHeaderByHashFn func(hash common.Hash) (*types.Header, error)
type reqHeaderByHashAndNumberFn func(hash common.Hash, number uint64) (*types.Header, error)
type reqHeaderByNumberFn func(number uint64) (*types.Header, error)
type reqBlockBodyRlpFn func(hash common.Hash) (rlp.RawValue, error)
type reqReceiptsByHashFn func(hash common.Hash) (types.Receipts, error)
type reqTrieNodeByHashFn func(hash common.Hash) ([]byte, error)
type reqContractCodeWithPrefixFn func(hash common.Hash) ([]byte, error)
type reqAncestorFn func(hash common.Hash, blockNumber uint64, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64)
type reqTotalDifficultyFn func(hash common.Hash, number uint64) (*big.Int, error)

type Blockchain struct {
	currentBlock              *types.Block
	blockContainer            *fixedSizeBlockQueue
	chainConfig               *params.ChainConfig
	genesisHash               common.Hash
	chainHeadFeed             event.Feed
	chainBlockFeed            event.Feed
	scope                     event.SubscriptionScope
	broadcastToPioplat        broadcastBlockToPioplatFn
	reqHeaderByHash           reqHeaderByHashFn
	reqHeaderByHashAndNumber  reqHeaderByHashAndNumberFn
	reqHeaderByNumber         reqHeaderByNumberFn
	reqBlockBodyRlp           reqBlockBodyRlpFn
	reqReceiptsByHash         reqReceiptsByHashFn
	reqTrieNodeByHash         reqTrieNodeByHashFn
	reqContractCodeWithPrefix reqContractCodeWithPrefixFn
	reqAncestor               reqAncestorFn
	reqTotalDifficulty        reqTotalDifficultyFn
	log                       log.Logger
}

func NewBlockchain(config *params.ChainConfig, genesisHash common.Hash, evaluationJsonFile string) *Blockchain {
	bc := &Blockchain{
		currentBlock: types.NewBlock(&types.Header{
			ParentHash: genesisHash,
			Number:     big.NewInt(1),
		}, []*types.Transaction{}, []*types.Header{}, []*types.Receipt{}, trie.NewStackTrie()),
		blockContainer: newFixedSizeBlockQueue(BlockContainerSizeLimit),
		chainConfig:    config,
		genesisHash:    genesisHash,
		log:            log.New(),
	}
	fileHandler := log.Must.FileHandler(evaluationJsonFile, log.JSONFormat())
	bc.log.SetHandler(fileHandler)
	return bc
}

func (bc *Blockchain) SetBroadcastFn(f broadcastBlockToPioplatFn) {
	bc.broadcastToPioplat = f
}

func (bc *Blockchain) SetPioplatFullNodeFn(reqHeaderByHash reqHeaderByHashFn,
	reqHeaderByHashAndNumber reqHeaderByHashAndNumberFn,
	reqHeaderByNumber reqHeaderByNumberFn,
	reqBlockBodyRlp reqBlockBodyRlpFn,
	reqReceiptsByHash reqReceiptsByHashFn,
	reqTrieNodeByHash reqTrieNodeByHashFn,
	reqContractCodeWithPrefix reqContractCodeWithPrefixFn,
	reqAncestor reqAncestorFn,
	reqTotalDifficulty reqTotalDifficultyFn) {

	bc.reqHeaderByHash = reqHeaderByHash
	bc.reqHeaderByHashAndNumber = reqHeaderByHashAndNumber
	bc.reqHeaderByNumber = reqHeaderByNumber
	bc.reqBlockBodyRlp = reqBlockBodyRlp
	bc.reqReceiptsByHash = reqReceiptsByHash
	bc.reqTrieNodeByHash = reqTrieNodeByHash
	bc.reqContractCodeWithPrefix = reqContractCodeWithPrefix
	bc.reqAncestor = reqAncestor
	bc.reqTotalDifficulty = reqTotalDifficulty
}

func (bc *Blockchain) EnqueueNewBlock(block *types.Block) {
	if !bc.HasBlock(block.Hash(), block.NumberU64()) {
		ctx := log.Ctx{
			"number":            block.NumberU64(),
			"hash":              block.Hash(),
			"block_timestamp":   block.Time(),
			"receive_timestamp": time.Now().UnixMilli(),
		}
		bc.log.Info("", ctx)

		bc.blockContainer.Push(block)
		bc.chainHeadFeed.Send(ChainHeadEvent{Block: block})
		bc.chainBlockFeed.Send(ChainHeadEvent{block})

		// broadcast to pioplat clients in p2p rlpx network
		bc.broadcastToPioplat(block)
	}
	if block.NumberU64() > bc.currentBlock.NumberU64() && block.Hash() != bc.currentBlock.Hash() {
		bc.currentBlock = block
	}
}

func (bc *Blockchain) InsertChain(blocks types.Blocks) (int, error) {
	for _, block := range blocks {
		bc.EnqueueNewBlock(block)
	}
	return len(blocks), nil
}

func (bc *Blockchain) CurrentBlock() *types.Block {
	return bc.currentBlock
}

func (bc *Blockchain) CurrentHeader() *types.Header {
	return bc.currentBlock.Header()
}

func (bc *Blockchain) Config() *params.ChainConfig {
	return bc.chainConfig
}

func (bc *Blockchain) GenesisHash() common.Hash {
	return bc.genesisHash
}

func (bc *Blockchain) SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return bc.scope.Track(bc.chainHeadFeed.Subscribe(ch))
}

// SubscribeChainBlockEvent registers a subscription of ChainBlockEvent.
func (bc *Blockchain) SubscribeChainBlockEvent(ch chan<- ChainHeadEvent) event.Subscription {
	return bc.scope.Track(bc.chainBlockFeed.Subscribe(ch))
}

// HasBlock checks whether the local cache has the block with hash and number.
// If it not exists, we do not have to request to full node.
func (bc *Blockchain) HasBlock(hash common.Hash, number uint64) bool {
	return bc.blockContainer.Exist(hash, number)
}

// HasBlockByHash checks whether the local cache has the block with hash and number.
// If it not exists, we do not have to request to full node.
func (bc *Blockchain) HasBlockByHash(hash common.Hash) bool {
	return bc.blockContainer.ExistByHash(hash)
}

// GetBlockByHash is only used in block fetcher for checking if the local cache has the block corresponds
// to the given hash. So, if it not exists, we do not have to request to full node
func (bc *Blockchain) GetBlockByHash(hash common.Hash) *types.Block {
	var response *types.Block = nil
	if blockNumber, exists := bc.blockContainer.Hash2Number(hash); exists {
		blocks, _ := bc.blockContainer.UniqueNumber(blockNumber)
		for _, block := range blocks {
			if block.Hash() == hash {
				response = block
				break
			}
		}
	}
	return response
}

// GetHeaderByHash serves requests from neighbors follow eth66 protocol
// if the local cache do not contain the header, should request to the Pioplat full node
func (bc *Blockchain) GetHeaderByHash(hash common.Hash) *types.Header {
	var (
		err      error
		response *types.Header = nil
	)
	if blockNumber, exists := bc.blockContainer.Hash2Number(hash); exists {
		blocks, _ := bc.blockContainer.UniqueNumber(blockNumber)
		for _, block := range blocks {
			if block.Hash() == hash {
				response = block.Header()
				break
			}
		}
	} else if bc.reqHeaderByHash != nil {
		response, err = bc.reqHeaderByHash(hash)
		if err != nil {
			bc.log.Warn("blockchain request to pioplat full node header by hash failed", "reason", err)
		}
	}
	return response
}

func (bc *Blockchain) GetCanonicalHash(blockNumber uint64) common.Hash {
	return bc.GetHeaderByNumber(blockNumber).Hash()
}

func (bc *Blockchain) GetHeadersFrom(from uint64, count uint64) []rlp.RawValue {
	var (
		header *types.Header
		res    []rlp.RawValue
		data   rlp.RawValue
		err    error
	)
	for i := from; i < from+count; i++ {
		header = bc.GetHeaderByNumber(i)
		data, err = rlp.EncodeToBytes(header)
		if err != nil {
			res = append(res, rlp.RawValue{})
		} else {
			res = append(res, data)
		}
	}
	return res
}

func (bc *Blockchain) GetHeader(hash common.Hash, blockNumber uint64) *types.Header {
	var (
		err      error
		response *types.Header = nil
	)
	if blocks, exists := bc.blockContainer.UniqueNumber(blockNumber); exists {
		for _, block := range blocks {
			if block.Hash() == hash {
				response = block.Header()
				break
			}
		}
	} else {
		response, err = bc.reqHeaderByHashAndNumber(hash, blockNumber)
		if err != nil {
			bc.log.Warn("blockchain request to pioplat full node header by hash and number failed", "reason", err)
		}
	}
	return response
}

// GetHeaderByNumber get the block header by number, if no exists in local cache.
// request to the Pioplat full node
func (bc *Blockchain) GetHeaderByNumber(blockNumber uint64) *types.Header {
	var (
		err      error
		response *types.Header = nil
	)
	if blocks, exists := bc.blockContainer.UniqueNumber(blockNumber); exists {
		if len(blocks) == 1 {
			return blocks[0].Header()
		}
	}
	response, err = bc.reqHeaderByNumber(blockNumber)
	if err != nil {
		bc.log.Warn("blockchain request to pioplat full node header by number failed", "reason", err)
	}
	return response
}

func (bc *Blockchain) GetAncestor(hash common.Hash, blockNumber uint64, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	resHash, resNumber := bc.reqAncestor(hash, blockNumber, ancestor, maxNonCanonical)
	return resHash, resNumber
}

func (bc *Blockchain) GetBodyRLP(hash common.Hash) rlp.RawValue {
	var (
		err  error
		data rlp.RawValue
	)
	if blockNumber, exists := bc.blockContainer.Hash2Number(hash); exists {
		blocks, _ := bc.blockContainer.UniqueNumber(blockNumber)
		for _, block := range blocks {
			if block.Hash() == hash {
				data, err = rlp.EncodeToBytes(block.Body())
				if err != nil {
					return rlp.RawValue{}
				}
				return data
			}
		}
	} else {
		data, err = bc.reqBlockBodyRlp(hash)
		if err != nil {
			bc.log.Warn("blockchain request to pioplat full node block body by hash failed", "reason", err)
		}
	}
	return data
}

func (bc *Blockchain) GetTd(hash common.Hash, number uint64) *big.Int {
	var (
		err       error
		resNumber *big.Int
	)
	resNumber, err = bc.reqTotalDifficulty(hash, number)
	if err != nil {
		bc.log.Warn("blockchain request to pioplat full node total difficulty failed", "reason", err)
	}
	return resNumber
}

func (bc *Blockchain) GetReceiptsByHash(hash common.Hash) types.Receipts {
	var (
		err      error
		response types.Receipts
	)
	response, err = bc.reqReceiptsByHash(hash)
	if err != nil {
		bc.log.Warn("blockchain request to pioplat full node receipts by hash failed", "reason", err)
	}
	return response
}

// ContractCodeWithPrefix retrieves a blob of data associated with a contract
// hash request from the Pioplat full node
//
func (bc *Blockchain) ContractCodeWithPrefix(hash common.Hash) ([]byte, error) {
	var (
		err      error
		response []byte
	)
	response, err = bc.reqContractCodeWithPrefix(hash)
	if err != nil {
		bc.log.Warn("blockchain request to pioplat full node contract code by hash failed", "reason", err)
	}
	return response, err
}

// TrieNode retrieves a blob of data associated with a trie node
// request from the Pioplat full node.
func (bc *Blockchain) TrieNode(hash common.Hash) ([]byte, error) {
	var (
		err      error
		response []byte
	)
	response, err = bc.reqTrieNodeByHash(hash)
	if err != nil {
		bc.log.Warn("blockchain request to pioplat full node trie node by hash failed", "reason", err)
	}
	return response, err
}
