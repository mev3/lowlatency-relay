package core

import (
	"container/heap"
	"errors"
	"github.com/pioplat/pioplat-core/common"
	"github.com/pioplat/pioplat-core/common/mclock"
	"github.com/pioplat/pioplat-core/core/types"
	"github.com/pioplat/pioplat-core/event"
	"github.com/pioplat/pioplat-core/log"
	"sync"
	"time"
)

// expHeap tracks transactions and their expiry time.
type expHeap []expItem

// expItem is an entry in addrHistory.
type expItem struct {
	item *types.Transaction
	exp  mclock.AbsTime
}

// nextExpiry returns the next expiry time.
func (h *expHeap) nextExpiry() mclock.AbsTime {
	return (*h)[0].exp
}

// add adds an item and sets its expiry time.
func (h *expHeap) add(item *types.Transaction, exp mclock.AbsTime) {
	heap.Push(h, expItem{item, exp})
}

// contains checks whether a transaction is present.
func (h expHeap) contains(hash common.Hash) bool {
	for _, v := range h {
		if v.item.Hash() == hash {
			return true
		}
	}
	return false
}

// expire removes items with expiry time before 'now'.
func (h *expHeap) expire(now mclock.AbsTime, onExp func(transaction *types.Transaction)) {
	for h.Len() > 0 && h.nextExpiry() < now {
		item := heap.Pop(h)
		if onExp != nil {
			onExp(item.(expItem).item)
		}
	}
}

// heap.Interface boilerplate
func (h expHeap) Len() int            { return len(h) }
func (h expHeap) Less(i, j int) bool  { return h[i].exp < h[j].exp }
func (h expHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *expHeap) Push(x interface{}) { *h = append(*h, x.(expItem)) }
func (h *expHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

var (
	// ErrAlreadyKnown is returned if the transactions is already contained
	// within the pool.
	ErrAlreadyKnown = errors.New("already known")

	// ErrUnderpriced is returned if a transaction's gas price is below the minimum
	// configured for the transaction pool.
	ErrUnderpriced = errors.New("transaction underpriced")

	// ErrReplaceUnderpriced is returned if a transaction is attempted to be replaced
	// with a different one without the required price bump.
	ErrReplaceUnderpriced = errors.New("replacement transaction underpriced")
)

type broadcastTxsToPioplatFn func(txs []*types.Transaction)

type TxPool struct {
	mut                *sync.Mutex
	txs                expHeap
	txsByHash          map[common.Hash]*types.Transaction
	expirePeriod       mclock.AbsTime
	txFeed             event.Feed
	reannoTxFeed       event.Feed // Event feed for announcing transactions again
	scope              event.SubscriptionScope
	broadcastToPioplat broadcastTxsToPioplatFn
	log                log.Logger
}

func NewTxPool(config TxPoolConfig, evaluationTxJsonFile string) *TxPool {
	tp := &TxPool{
		mut:          new(sync.Mutex),
		txsByHash:    make(map[common.Hash]*types.Transaction),
		expirePeriod: config.ExpirePeriod,
		log:          log.New(),
	}
	fileHandler := log.Must.FileHandler(evaluationTxJsonFile, log.JSONFormat())
	tp.log.SetHandler(fileHandler)
	return tp
}

func (tp *TxPool) SetBroadcastFn(f broadcastTxsToPioplatFn) {
	tp.broadcastToPioplat = f
}

func (tp *TxPool) Has(hash common.Hash) bool {
	tp.mut.Lock()
	defer tp.mut.Unlock()
	if _, ok := tp.txsByHash[hash]; ok {
		return true
	}
	return false
}

func (tp *TxPool) Get(hash common.Hash) *types.Transaction {
	tp.mut.Lock()
	defer tp.mut.Unlock()
	if tx, ok := tp.txsByHash[hash]; ok {
		return tx
	}
	return nil
}

func (tp *TxPool) AddRemotes(txs []*types.Transaction) []error {
	var (
		res      []error
		addedTxs []*types.Transaction
	)

	// broadcast transactions to pioplat clients in p2p rlpx network
	tp.broadcastToPioplat(txs)

	for _, tx := range txs {
		if tp.Has(tx.Hash()) {
			res = append(res, ErrAlreadyKnown)
			continue
		}
		tp.mut.Lock()
		tp.txsByHash[tx.Hash()] = tx
		tp.txs.add(tx, tp.expirePeriod)
		tp.mut.Unlock()

		ctx := log.Ctx{
			"hash":              tx.Hash(),
			"receive_timestamp": tx.Time().UnixMilli(),
		}
		tp.log.Info("", ctx)

		addedTxs = append(addedTxs, tx)
		res = append(res, nil)
	}

	tp.mut.Lock()
	tp.txs.expire(mclock.Now(), func(tx *types.Transaction) {
		delete(tp.txsByHash, tx.Hash())
	})
	tp.mut.Unlock()

	tp.txFeed.Send(NewTxsEvent{addedTxs})
	return res
}

func (tp *TxPool) Pending(enforceTips bool) map[common.Address]types.Transactions {
	var res = make(map[common.Address]types.Transactions)
	return res
}

func (tp *TxPool) SubscribeNewTxsEvent(ch chan<- NewTxsEvent) event.Subscription {
	return tp.scope.Track(tp.txFeed.Subscribe(ch))
}

func (tp *TxPool) SubscribeReannoTxsEvent(ch chan<- ReannoTxsEvent) event.Subscription {
	// Default ReannounceTime is 10 years, won't announce by default.
	return tp.scope.Track(tp.reannoTxFeed.Subscribe(ch))
}

func (tp *TxPool) Stop() {
	tp.scope.Close()
}

// TxPoolConfig are the configuration parameters of the transaction pool.
type TxPoolConfig struct {
	ExpirePeriod mclock.AbsTime
}

// DefaultTxPoolConfig contains the default configurations for the transaction
// pool.
var DefaultTxPoolConfig = TxPoolConfig{
	ExpirePeriod: mclock.AbsTime(60 * time.Second),
}
