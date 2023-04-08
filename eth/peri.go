package eth

import (
	"fmt"
	"github.com/pioplat/pioplat-core/common"
	"github.com/pioplat/pioplat-core/core/forkid"
	"github.com/pioplat/pioplat-core/core/types"
	"github.com/pioplat/pioplat-core/eth/ethconfig"
	"github.com/pioplat/pioplat-core/eth/protocols/eth"
	"github.com/pioplat/pioplat-core/log"
	"github.com/pioplat/pioplat-core/p2p"
	"github.com/pioplat/pioplat-core/p2p/enode"
	"math"
	"math/big"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// PERI_AND_LATENCY_RECORDER_CODE_PIECE

const (
	milli2Nano                = 1000000
	transactionArrivalReplace = 30000
	enodeSplitIndex           = 137
	perDbPath                 = "peri_nodes"
	periNodeCountKey          = "nodes_count"
	periBlocklistCountKey     = "blocklist_count"
	periBlockIpKeyPrefix      = "b_ip_"
	periBlockTimeKeyPrefix    = "b_time_"
	periBlockExpireKeyPrefix  = "b_unix_"
	periNodeKeyPrefix         = "n_"
	maxBlockDist              = 32 // Maximum allowed distance from the chain head to block received
)

// item in expired blocklist
type blockItem struct {
	ip          string    // IP of the peer in blocklist.
	count       int64     // Indicates the number of times this item has been added to the blocklist
	expiredTime time.Time // Expiration time in absolute format
}

type Peri struct {
	config           *ethconfig.Config // ethconfig used globally during program execution
	handler          *handler          // implement handler to blocks and transactions arriving, access all peers right now
	replaceCount     int               // count of replacement during every round
	blockPeersCount  int               // count of slots for peers delivering blocks early during every round
	txsPeerCount     int               // count of slots for peers delivering transactions early during every round
	maxDelayDuration int64             // max delay duration in nano time

	locker           *sync.Mutex                      // locker to protect the below map fields.
	txArrivals       map[common.Hash]int64            // whether transactions are received, announcement and body are treated equally
	txArrivalPerPeer map[common.Hash]map[string]int64 // transactions timestamp by peers
	txOldArrivals    map[common.Hash]int64            // record all stale transactions, avoid the situation where the message from the straggler is recorded

	blockArrivals       map[blockAnnounce]int64            // whether transactions are received, announcement and body are treated equally
	blockArrivalPerPeer map[blockAnnounce]map[string]int64 // blocks timestamp by peers

	peersSnapShot map[string]string    // record peers' id, used by noDropPeer function
	blocklist     map[string]blockItem // block list of ip address

	fileLogger log.Logger // eviction log
	nodesDb    *enode.DB
}

type idScore struct {
	id    string
	score float64
}

type blockAnnounce struct {
	hash   common.Hash
	number uint64
}

func blockAnnouncesFromHashesAndNumbers(hashes []common.Hash, numbers []uint64) []blockAnnounce {
	var length int
	if len(hashes) <= len(numbers) {
		length = len(hashes)
	} else {
		length = len(numbers)
	}

	var result = make([]blockAnnounce, 0, length)
	for i := 0; i < length; i++ {
		result = append(result, blockAnnounce{
			hash:   hashes[i],
			number: numbers[i],
		})
	}

	return result
}

func CreatePeri(p2pServe *p2p.Server, config *ethconfig.Config, h *handler) *Peri {
	var (
		err   error
		f     *os.File
		node  *enode.Node
		nodes []*enode.Node
	)
	peri := &Peri{
		config:           config,
		handler:          h,
		locker:           new(sync.Mutex),
		replaceCount:     int(math.Round(float64(h.maxPeers) * config.PeriReplaceRatio)),
		blockPeersCount:  int(math.Round(float64(h.maxPeers) * config.PeriBlockNodeRatio)),
		maxDelayDuration: int64(config.PeriMaxDelayPenalty * milli2Nano),
		txArrivals:       make(map[common.Hash]int64),
		txArrivalPerPeer: make(map[common.Hash]map[string]int64),
		txOldArrivals:    make(map[common.Hash]int64),

		blockArrivals:       make(map[blockAnnounce]int64),
		blockArrivalPerPeer: make(map[blockAnnounce]map[string]int64),

		peersSnapShot: make(map[string]string),
		blocklist:     make(map[string]blockItem),
		fileLogger:    log.New(),
	}
	peri.txsPeerCount = h.maxPeers - peri.replaceCount - peri.blockPeersCount

	databasePath := filepath.Join(config.PeriDataDirectory, perDbPath)
	peri.nodesDb, err = enode.OpenDB(databasePath)
	if err != nil {
		log.Crit("open peri database failed", "err", err)
	}

	// load nodes from peri's database
	periNodesCountStr := peri.nodesDb.FetchString([]byte(periNodeCountKey))
	periNodesCount, err := strconv.ParseInt(periNodesCountStr, 10, 32)
	if err == nil && periNodesCount > 0 {
		for i := 0; i < int(periNodesCount); i++ {
			enodeUrl := peri.nodesDb.FetchString([]byte(periNodeKeyPrefix + strconv.Itoa(i)))
			if enodeUrl != "" {
				node, err = enode.Parse(enode.ValidSchemes, enodeUrl)
				if err != nil {
					log.Warn("parse enode failed when create peri", "err", err, "url", enodeUrl)
					continue
				}
				nodes = append(nodes, node)
			}
		}
	}
	if len(nodes) > h.maxPeers {
		p2pServe.AddPeriInitialNodes(nodes[:h.maxPeers])
	} else {
		p2pServe.AddPeriInitialNodes(nodes)
	}

	// load blocklist from peri's data
	periBlocklistCountStr := peri.nodesDb.FetchString([]byte(periBlocklistCountKey))
	periBlocklistCount, err := strconv.ParseInt(periBlocklistCountStr, 10, 32)
	if err == nil && periBlocklistCount > 0 {
		for i := 0; i < int(periBlocklistCount); i++ {
			blockIp := peri.nodesDb.FetchString([]byte(periBlockIpKeyPrefix + strconv.Itoa(i)))
			blockCountStr := peri.nodesDb.FetchString([]byte(periBlockTimeKeyPrefix + strconv.Itoa(i)))
			blockExpireStr := peri.nodesDb.FetchString([]byte(periBlockExpireKeyPrefix + strconv.Itoa(i)))
			blockCount, _ := strconv.ParseInt(blockCountStr, 10, 64)
			blockExpire, _ := strconv.ParseInt(blockExpireStr, 10, 64)
			if blockIp != "" {
				peri.blocklist[blockIp] = blockItem{
					ip:          blockIp,
					count:       blockCount,
					expiredTime: time.Unix(blockExpire, 0),
				}
			}
		}
	}

	if config.PeriLogFilePath != "" {
		f, err = os.OpenFile(config.PeriLogFilePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			log.Crit("open peri log file failed", "err", err)
		}
		logHandler := log.StreamHandler(f, log.LogfmtFormat())
		peri.fileLogger.SetHandler(logHandler)
	}

	return peri
}

// StartPeri Start Peri (at the initialization of geth)
func (p *Peri) StartPeri() {
	go func() {
		var (
			interrupt       = make(chan os.Signal, 1)
			killed          = false
			saveNodesOnExit = false
			err             error
		)
		signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(interrupt)
		defer p.nodesDb.Close()

		ticker := time.NewTicker(time.Second * time.Duration(p.config.PeriPeriod))
		for killed == false {
			select {
			case <-ticker.C:
				log.Warn("new peri period start disconnect by score")
				p.disconnectByScore()
				saveNodesOnExit = true
			case <-interrupt:
				log.Warn("peri eviction policy interrupted")
				killed = true
			}
		}

		if saveNodesOnExit {
			p.lock()
			defer p.unlock()
			blockScores, txScores, _ := p.getScores()
			var peersReserve = make(map[string]interface{})

			for i := len(blockScores) - 1; i >= 0 && i >= len(blockScores)-p.blockPeersCount; i-- {
				if _, ok := peersReserve[txScores[i].id]; !ok {
					peersReserve[txScores[i].id] = struct{}{}
				}
			}
			for i := len(txScores) - 1; i >= 0 && i >= len(txScores)-p.txsPeerCount; i-- {
				if _, ok := peersReserve[blockScores[i].id]; !ok {
					peersReserve[blockScores[i].id] = struct{}{}
				}
			}
			numDrop := len(txScores) - p.blockPeersCount - p.txsPeerCount
			if numDrop < 0 {
				numDrop = 0
			}

			// store nodes to peri's database
			err = p.nodesDb.StoreString([]byte(periNodeCountKey), fmt.Sprint(len(peersReserve)))
			if err != nil {
				log.Warn("peri store node count failed when exit", "err", err)
			}

			i := 0
			for id := range peersReserve {
				enode := p.peersSnapShot[id]
				err = p.nodesDb.StoreString([]byte(periNodeKeyPrefix+strconv.Itoa(i)), enode)
				i++
				if err != nil {
					log.Warn("peri store enode failed when exit", "err", err)
				}
			}

			// store blocklist items to peri's database
			err = p.nodesDb.StoreString([]byte(periBlocklistCountKey), fmt.Sprint(len(p.blocklist)))
			if err != nil {
				log.Warn("peri store block item failed when exit", "err", err)
			}

			i = 0
			for _, blockedItem := range p.blocklist {
				err = p.nodesDb.StoreString([]byte(periBlockIpKeyPrefix+strconv.Itoa(i)), blockedItem.ip)
				if err != nil {
					log.Warn("peri store block item's ip failed when exit", "err", err)
				}
				err = p.nodesDb.StoreString([]byte(periBlockTimeKeyPrefix+strconv.Itoa(i)), fmt.Sprint(blockedItem.count))
				if err != nil {
					log.Warn("peri store block item's count failed when exit", "err", err)
				}
				err = p.nodesDb.StoreString([]byte(periBlockExpireKeyPrefix+strconv.Itoa(i)), fmt.Sprint(blockedItem.expiredTime.Unix()))
				if err != nil {
					log.Warn("peri store block item's expired time failed when exit", "err", err)
				}
				i++
			}
		}
	}()
}

func (p *Peri) lock() {
	p.locker.Lock()
}

func (p *Peri) unlock() {
	p.locker.Unlock()
}

func (p *Peri) recordBlockAnnounces(peer *eth.Peer, hashes []common.Hash, numbers []uint64, isAnnouncement bool) {
	var (
		timestamp             = time.Now().UnixNano()
		peerId                = peer.ID()
		enodeUrl              = peer.Peer.Node().URLv4()
		newBlockAnnouncements = blockAnnouncesFromHashesAndNumbers(hashes, numbers)
	)

	p.lock()
	defer p.unlock()

	for _, blockAnnouncement := range newBlockAnnouncements {
		if dist := int64(blockAnnouncement.number) - int64(p.handler.chain.CurrentBlock().NumberU64()); dist < -maxBlockDist {
			log.Warn("peri already seen this block so skip this block announcement",
				"block", blockAnnouncement.number, "peer", peer.Node().IP())
			continue
		}

		arrivalTimestamp, arrived := p.blockArrivals[blockAnnouncement]
		if arrived {
			// already seen this block then check which one is earlier
			if timestamp < arrivalTimestamp {
				p.blockArrivals[blockAnnouncement] = timestamp
			}
			p.blockArrivalPerPeer[blockAnnouncement][peerId] = timestamp
		} else {
			// first received then update information
			p.blockArrivals[blockAnnouncement] = timestamp
			p.blockArrivalPerPeer[blockAnnouncement] = map[string]int64{peerId: timestamp}
		}

		if p.config.PeriShowTxDelivery {
			if isAnnouncement {
				log.Info("receive block announcement", "peer", enodeUrl[enodeSplitIndex:], "blocknumber", blockAnnouncement.number)
			} else {
				log.Info("receive full block body", "peer", enodeUrl[enodeSplitIndex:], "blocknumber", blockAnnouncement.number)
			}
		}
	}
}

func (p *Peri) recordBlockBody(peer *eth.Peer, block *types.Block) {
	p.recordBlockAnnounces(peer, []common.Hash{block.Hash()}, []uint64{block.Number().Uint64()}, false)
}

func (p *Peri) recordTransactionAnnounces(peer *eth.Peer, hashes []common.Hash, isAnnouncement bool) {
	var (
		timestamp = time.Now().UnixNano()
		peerId    = peer.ID()
		enodeUrl  = peer.Peer.Node().URLv4()
	)

	p.lock()
	defer p.unlock()

	for _, txHash := range hashes {
		if _, stale := p.txOldArrivals[txHash]; stale {
			// already seen this transaction so skip this new transaction announcement
			if p.config.PeriShowTxDelivery {
				log.Warn("peri already seen this transaction so skip this new transaction announcement",
					"tx", txHash, "peer", peer.Node().IP())
			}
			continue
		}

		arrivalTimestamp, arrived := p.txArrivals[txHash]
		if arrived {
			// already seen this transaction then check which one is earlier
			if timestamp < arrivalTimestamp {
				p.txArrivals[txHash] = timestamp
			}
			p.txArrivalPerPeer[txHash][peerId] = timestamp
		} else {
			// first received then update information
			p.txArrivals[txHash] = timestamp
			p.txArrivalPerPeer[txHash] = map[string]int64{peerId: timestamp}
		}

		if p.config.PeriShowTxDelivery {
			log.Info("receive transaction announcement", "peer", enodeUrl[enodeSplitIndex:], "tx", fmt.Sprint(txHash))
		}
	}
}

func (p *Peri) recordTransactionBody(peer *eth.Peer, transactions []*types.Transaction) {
	var hashs = make([]common.Hash, 0, len(transactions))
	for _, tx := range transactions {
		hashs = append(hashs, tx.Hash())
	}
	p.recordTransactionAnnounces(peer, hashs, false)
}

// getScores compute score by blocks receiving timestamp and by txs receiving timestamp
// it returns two idScore array, the first is blocks score, the second is txs score.
// it also returns excused peer list which contains peers that connect too late.
// this function assures that length of block scores and transaction cores are equal.
func (p *Peri) getScores() ([]idScore, []idScore, map[string]bool) {
	var (
		blockScores       []idScore
		transactionScores []idScore
		excused           = make(map[string]bool)

		latestBlockArrivalTimestamp int64
		latestTxArrivalTimestamp    int64
		peerBirthTimestamp          int64
		peerDelayDuration           int64
		totalDelayDuration          int64
		peerForwardCount            int
		peerAverageDelay            float64
	)

	// here is computing block scores
	for _, arrivalTimestamp := range p.blockArrivals {
		if arrivalTimestamp > latestBlockArrivalTimestamp {
			latestBlockArrivalTimestamp = arrivalTimestamp
		}
	}

	// below is computing transaction scores
	for _, arrivalTimestamp := range p.txArrivals {
		if arrivalTimestamp > latestTxArrivalTimestamp {
			latestTxArrivalTimestamp = arrivalTimestamp
		}
	}

	p.handler.peers.lock.RLock()
	for id, peer := range p.handler.peers.peers {
		p.peersSnapShot[id] = peer.Node().URLv4()
		peerBirthTimestamp = peer.Peer.ConnectedTimestamp

		// computing block scores
		peerForwardCount, totalDelayDuration, peerAverageDelay = 0, 0, 0.0
		for blockAnnouncement, arrivalTimestamp := range p.blockArrivals {
			if arrivalTimestamp < peerBirthTimestamp {
				continue
			}

			arrivalTimestampThisPeer, forwardThisPeer := p.blockArrivalPerPeer[blockAnnouncement][id]
			peerDelayDuration = arrivalTimestampThisPeer - arrivalTimestamp
			if forwardThisPeer == false || peerDelayDuration > p.maxDelayDuration {
				peerDelayDuration = p.maxDelayDuration
			}

			peerForwardCount += 1
			totalDelayDuration += peerDelayDuration
		}
		if peerForwardCount == 0 {
			// the peer maybe connect too late, if so, excuse it from computing scores temporarily
			if peerBirthTimestamp > latestBlockArrivalTimestamp-p.config.PeriMaxDeliveryTolerance*milli2Nano {
				excused[id] = true
			}
			peerAverageDelay = float64(p.maxDelayDuration)
		} else {
			peerAverageDelay = float64(totalDelayDuration) / float64(peerForwardCount)
		}

		blockScores = append(blockScores, idScore{
			id:    id,
			score: peerAverageDelay,
		})

		// computing txs scores
		peerForwardCount, totalDelayDuration, peerAverageDelay = 0, 0, 0.0
		for tx, arrivalTimestamp := range p.txArrivals {
			if arrivalTimestamp < peerBirthTimestamp {
				continue
			}

			arrivalTimestampThisPeer, forwardThisPeer := p.txArrivalPerPeer[tx][id]
			peerDelayDuration = arrivalTimestampThisPeer - arrivalTimestamp
			if !forwardThisPeer || peerDelayDuration > p.maxDelayDuration {
				peerDelayDuration = p.maxDelayDuration
			}
			peerForwardCount += 1
			totalDelayDuration += peerDelayDuration
		}
		if peerForwardCount == 0 {
			// the peer maybe connect too late, if so, excuse it from computing scores temporarily
			if peerBirthTimestamp > latestTxArrivalTimestamp-p.config.PeriMaxDeliveryTolerance*milli2Nano {
				excused[id] = true
			}
			peerAverageDelay = float64(p.maxDelayDuration)
		} else {
			peerAverageDelay = float64(totalDelayDuration) / float64(peerForwardCount)
		}

		transactionScores = append(transactionScores, idScore{
			id:    id,
			score: peerAverageDelay,
		})
	}
	p.handler.peers.lock.RUnlock()

	// scores are sorted by descending order
	sort.Slice(blockScores, func(i, j int) bool {
		ndi, ndj := p.isNoDropPeer(blockScores[i].id), p.isNoDropPeer(blockScores[j].id)
		if ndi && !ndj {
			return false // give i lower priority when i cannot be dropped
		} else if ndj && !ndi {
			return true
		} else {
			return blockScores[i].score > blockScores[j].score
		}
	})
	sort.Slice(transactionScores, func(i, j int) bool {
		ndi, ndj := p.isNoDropPeer(transactionScores[i].id), p.isNoDropPeer(transactionScores[j].id)
		if ndi && !ndj {
			return false // give i lower priority when i cannot be dropped
		} else if ndj && !ndi {
			return true
		} else {
			return transactionScores[i].score > transactionScores[j].score
		}
	})

	return blockScores, transactionScores, excused
}

// check if a node is always undroppable (for instance, a predefined no drop ip list)
func (p *Peri) isNoDropPeer(id string) bool {
	var enode = p.peersSnapShot[id]
	var ipAddress = extractIPFromEnode(enode)

	for _, ip := range p.config.PeriNoDropList {
		if ip == ipAddress {
			return true
		}
	}
	return false
}

func (p *Peri) resetRecords() {
	// lock is assume to be held
	for tx, arrival := range p.txArrivals {
		p.txOldArrivals[tx] = arrival
	}

	// clear old arrival states which are assumed not to be forwarded anymore
	if len(p.txOldArrivals) > p.config.PeriMaxTransactionAmount {
		listArrivals := make([]struct {
			txHash           common.Hash
			arrivalTimestamp int64
		}, 0, len(p.txOldArrivals))

		for tx, arrival := range p.txOldArrivals {
			listArrivals = append(listArrivals, struct {
				txHash           common.Hash
				arrivalTimestamp int64
			}{tx, arrival})
		}

		// Sort arrival time by ascending order
		sort.Slice(listArrivals, func(i, j int) bool {
			return listArrivals[i].arrivalTimestamp < listArrivals[j].arrivalTimestamp
		})

		// Delete the earliest arrivals
		var n int
		if len(p.txOldArrivals) < transactionArrivalReplace {
			n = len(p.txOldArrivals)
		} else {
			n = transactionArrivalReplace
		}
		for i := 0; i < n; i++ {
			delete(p.txOldArrivals, listArrivals[i].txHash)
		}
	}

	// reset arrival states
	p.txArrivals = make(map[common.Hash]int64)
	p.txArrivalPerPeer = make(map[common.Hash]map[string]int64)
	p.blockArrivals = make(map[blockAnnounce]int64)
	p.blockArrivalPerPeer = make(map[blockAnnounce]map[string]int64)
	p.peersSnapShot = make(map[string]string)
}

func (p *Peri) SetRatios(replaceRatio, blockRatio, txRatio float64) {
	// assume replace ratio + block ratio + tx ratio = 1
	p.locker.Lock()
	defer p.locker.Unlock()
	p.replaceCount = int(float64(p.handler.maxPeers) * replaceRatio)
	p.blockPeersCount = int(float64(p.handler.maxPeers) * blockRatio)
	p.txsPeerCount = int(float64(p.handler.maxPeers) * txRatio)
}

func (p *Peri) disconnectByScore() {
	p.locker.Lock()
	defer p.locker.Unlock()

	var peersReserve = make(map[string]interface{})
	if len(p.blockArrivals) == 0 || len(p.txArrivals) == 0 {
		log.Warn("no block or transactions recorded, peri policy skipped.", "peer count", p.handler.peers.len())
		return
	}

	blockScores, txScores, excused := p.getScores()
	// getScores returns blockScores, txScores. They have same length.

	if len(blockScores) > 0 {
		idx := math.Max(float64(len(blockScores)-p.blockPeersCount), 0)
		for i := len(blockScores) - 1; i >= int(idx); i-- {
			if _, ok := peersReserve[blockScores[i].id]; !ok {
				peersReserve[blockScores[i].id] = struct{}{}
			}
		}
	}
	if len(txScores) > 0 {
		idx := math.Max(float64(len(txScores)-p.txsPeerCount), 0)
		for i := len(txScores) - 1; i >= int(idx); i-- {
			if _, ok := peersReserve[txScores[i].id]; !ok {
				peersReserve[txScores[i].id] = struct{}{}
			}
		}
	}

	// assume length of block scores and transaction scores are identical
	// if current count of peers larger than block peers count plus tx peers count
	// then drop count of peers to block count + tx count

	// case1: size of reserve equal current neighbors but still less than threshold, no action
	// case2: size of reserve smaller than current neighbors and still less than threshold, drop the rest.
	neighbors := int(math.Min(
		float64(len(txScores)),
		float64(len(blockScores))))

	var peersRemove []string
	for i := 0; i < neighbors; i++ {
		if _, ok := peersReserve[txScores[i].id]; !ok {
			peersRemove = append(peersRemove, txScores[i].id)
		}
	}

	var toDropIdsSlice []string
	if neighbors > p.blockPeersCount+p.txsPeerCount {
		// randomly drop neighbors
		toDropNum := neighbors - (p.blockPeersCount + p.txsPeerCount)
		if len(peersReserve) <= p.blockPeersCount+p.txsPeerCount {
			rand.Shuffle(len(peersRemove), func(i, j int) {
				peersRemove[i], peersRemove[j] = peersRemove[j], peersRemove[i]
			})
			toDropIdsSlice = peersRemove[:toDropNum]
		} else {
			toDropNum = toDropNum - len(peersRemove)
			toDropIds := make(map[string]struct{})
			idx := math.Max(float64(len(blockScores)-p.blockPeersCount), 0)
			right := math.Min(idx+float64(toDropNum), float64(len(blockScores)))
			for i := int(idx); i < int(right); i++ {
				toDropIds[blockScores[i].id] = struct{}{}
			}

			idx = math.Max(float64(len(txScores)-p.txsPeerCount), 0)
			right = math.Min(idx+float64(toDropNum), float64(len(txScores)))
			for i := int(idx); i < int(right); i++ {
				toDropIds[txScores[i].id] = struct{}{}
			}

			for id := range toDropIds {
				toDropIdsSlice = append(toDropIdsSlice, id)
			}
			rand.Shuffle(len(toDropIdsSlice), func(i, j int) {
				toDropIdsSlice[i], toDropIdsSlice[j] = toDropIdsSlice[j], toDropIdsSlice[i]
			})
			toDropIdsSlice = append(toDropIdsSlice[:toDropNum], peersRemove...)
		}
	}

	// case3: size of reserve larger than threshold, random select and drop.

	// number of peers to drop
	numDrop := len(toDropIdsSlice)

	// show logs on console and persistent some information
	p.summaryStats(blockScores, txScores, excused, numDrop)

	log.Info("before dropping during disconnect by score", "count", p.handler.peers.len())

	if p.config.PeriActive {
		for i := 0; i < numDrop; i++ {
			id := toDropIdsSlice[i]
			if _, ok := excused[id]; ok {
				continue
			}
			// drop nodes, and add them to the blocklist
			if blockedItem, ok := p.blocklist[extractIPFromEnode(p.peersSnapShot[id])]; ok {
				blockedItem.count += 1
				blockedItem.expiredTime = time.Now().Add(time.Duration(math.Pow(2, float64(blockedItem.count))) * 20 * time.Minute)
				p.blocklist[extractIPFromEnode(p.peersSnapShot[id])] = blockedItem
			} else {
				p.blocklist[extractIPFromEnode(p.peersSnapShot[id])] = blockItem{
					ip:          extractIPFromEnode(p.peersSnapShot[id]),
					count:       1,
					expiredTime: time.Now().Add(time.Duration(math.Pow(2, float64(blockedItem.count))) * 20 * time.Minute),
				}
			}
			p.handler.removePeer(id)
			p.handler.unregisterPeer(id)
		}
	}

	log.Info("after dropping during disconnect by score", "count", p.handler.peers.len())
	p.resetRecords()
}

func extractIPFromEnode(enode string) string {
	parts := strings.Split(enode[enodeSplitIndex:], ":")
	return parts[0]
}

func (p *Peri) summaryStats(blockScores []idScore, txScores []idScore, excused map[string]bool, numDrop int) {
	timestamp := time.Now()
	log.Warn("peri policy is triggered", "timestamp", timestamp)
	p.fileLogger.Warn("peri policy is triggered", "timestamp", timestamp)
	blockCount, transactionCount := len(p.blockArrivals), len(p.txArrivals)

	log.Warn("Peri policy summary", "count of blocks", blockCount,
		"count of block score", len(blockScores), "count of drop", numDrop)
	p.fileLogger.Warn("Peri policy summary", "count of blocks", blockCount,
		"count of block score", len(blockScores), "count of drop", numDrop)

	log.Warn("Peri policy summary", "count of transactions", transactionCount,
		"count of tx score", len(txScores), "count of drop", numDrop)
	p.fileLogger.Warn("Peri policy summary", "count of transactions", transactionCount,
		"count of tx score", len(txScores), "count of drop", numDrop)

	for _, element := range blockScores {
		log.Warn("Peri computation score of peers", "enode", p.peersSnapShot[element.id], "block-score", element.score)
		p.fileLogger.Warn("Peri computation score of peers", "enode", p.peersSnapShot[element.id], "block-score", element.score)
	}
	for _, element := range txScores {
		log.Warn("Peri computation score of peers", "enode", p.peersSnapShot[element.id], "tx-score", element.score)
		p.fileLogger.Warn("Peri computation score of peers", "enode", p.peersSnapShot[element.id], "tx-score", element.score)
	}
}

func (p *Peri) isBlocked(enode string) bool {
	p.lock()
	defer p.unlock()
	blockedItem, ok := p.blocklist[extractIPFromEnode(enode)]
	if ok && time.Now().After(blockedItem.expiredTime) == false {
		return true
	}
	return false
}

func (p *Peri) BroadcastBlockToPioplatPeer(peer *eth.Peer, block *types.Block, td *big.Int) {
	if dist := int64(block.NumberU64()) - int64(p.handler.chain.CurrentBlock().NumberU64()); dist < -maxBlockDist || dist > maxBlockDist {
		return
	}

	if p.handler.periBroadcast {
		// use map p.handler.periPeersIp to decide whether broadcast this block
		pioplatCount := 0
		p.handler.peers.lock.RLock()
		for _, ethPeerElement := range p.handler.peers.peers {
			peerIp := ethPeerElement.Node().IP().String()
			if _, found := p.handler.periPeersIp[peerIp]; found {
				if ethPeerElement.KnownBlock(block.Hash()) == false {
					ethPeerElement.AsyncSendNewBlock(block, td)
					if peer != nil {
						log.Info("deliver block to pioplat peer", "block", block.NumberU64(), "from", peer.Node().IP().String(), "to", peerIp)
					} else {
						log.Info("deliver block to pioplat peer", "block", block.NumberU64(), "to", peerIp)
					}
				}

				// all Pioplat nodes have been searched, ending early.
				pioplatCount += 1
				if pioplatCount >= len(p.handler.periPeersIp) {
					break
				}
			}
		}
		p.handler.peers.lock.RUnlock()
	}
}

func (p *Peri) BroadcastTransactionsToPioplatPeer(txs []*types.Transaction) {
	if p.handler.periBroadcast {
		for _, tx := range txs {
			// use map p.handler.periPeersIp to decide whether broadcast this block
			pioplatCount := 0
			p.handler.peers.lock.RLock()
			for _, aEthPeer := range p.handler.peers.peers {
				peerIp := aEthPeer.Node().IP().String()
				if _, found := p.handler.periPeersIp[peerIp]; found {
					if aEthPeer.KnownTransaction(tx.Hash()) == false {
						aEthPeer.AsyncSendTransactions([]common.Hash{tx.Hash()})
						if p.config.PeriShowTxDelivery {
							log.Info("deliver transaction to pioplat peer", "tx", tx.Hash(), "ip", peerIp)
						}
					}

					// all Pioplat nodes have been searched, ending early.
					pioplatCount += 1
					if pioplatCount >= len(p.handler.periPeersIp) {
						break
					}
				}
			}
			p.handler.peers.lock.RUnlock()
		}
	}
}

func (p *Peri) getForkIdFromFullNode() forkid.ID {
	//res := forkid.NewID(p.handler.chain.Config(), p.handler.chain.GenesisHash())
	return forkid.ID{}
}
