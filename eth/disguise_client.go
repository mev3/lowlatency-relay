package eth

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/pioplat/pioplat-core/common"
	"github.com/pioplat/pioplat-core/core/forkid"
	"github.com/pioplat/pioplat-core/core/types"
	"github.com/pioplat/pioplat-core/eth/ethconfig"
	"github.com/pioplat/pioplat-core/log"
	"github.com/pioplat/pioplat-core/rlp"
	"io"
	"math/big"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	GetGenesisMsg             = "1100"
	GetBlockNumber            = "1200"
	GetForksMsg               = "1300"
	GetAllAbove               = "1400"
	GetHeaderByHash           = "2100"
	GetHeaderByNumber         = "2200"
	GetHeaderByHashAndNumber  = "2300"
	GetAncestor               = "3000"
	GetBlockBodyRlpByHash     = "4000"
	GetReceiptsByHash         = "5000"
	GetContractCodeWithPrefix = "6000"
	GetTrieNode               = "7000"
	GetTotalDifficulty        = "8000"
)

const (
	CmdSize         = 4
	LengthSize      = 4
	HashSize        = 32
	BlockNumberSize = 8
	ForkNumberSize  = 8
)

var InvalidCommandMsgErr = errors.New("invalid command of message")
var InvalidLengthMsgErr = errors.New("invalid length of message")

type DisguiseClient struct {
	conn              *tls.Conn
	forks             []uint64
	genesisHash       common.Hash
	curBlockNumber    uint64
	lastGetForkIdTime time.Time
	forkID            forkid.ID
	forkFilter        forkid.Filter
	tcpAddr           *net.TCPAddr
	tlsConfig         *tls.Config
	mut               *sync.Mutex
	connecting        uint32
}

func CreateDisguise(config *ethconfig.Config) *DisguiseClient {
	var (
		err       error
		pemCert   []byte
		certPool  *x509.CertPool
		recvCount = 0
		recvBuf   = make([]byte, 0x100)
		tcpAddr   *net.TCPAddr
	)

	clt := &DisguiseClient{
		mut:        new(sync.Mutex),
		connecting: 0,
	}
	// read cert file
	pemCert, err = os.ReadFile(config.DisguiseServerX509CertFile)
	if err != nil {
		log.Crit("read cert file failed", "reason", err, "cert", config.DisguiseServerX509CertFile)
	}
	certPool = x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(pemCert); !ok {
		log.Crit("parse cert pem failed")
	}
	clt.tlsConfig = &tls.Config{RootCAs: certPool}

	tcpAddr, err = net.ResolveTCPAddr("tcp", config.DisguiseServerUrl)
	if err != nil {
		log.Crit("invalid tcp address", "reason", err)
	}
	clt.tcpAddr = tcpAddr

	clt.conn, err = tls.Dial("tcp", tcpAddr.String(), clt.tlsConfig)
	if err != nil {
		log.Crit("connect disguise server failed", "reason", err)
	}

	_, err = clt.conn.Write([]byte(GetAllAbove))
	for err != nil {
		log.Crit("send GetAllAbove command message to disguise server failed", "reason", err)
	}

	recvCount, err = clt.conn.Read(recvBuf)
	if err != nil {
		log.Crit("recv from disguise server failed", "reason", err)
	}
	err = clt.decodeMsg(recvBuf[:recvCount])
	if err != nil {
		log.Crit("recv from disguise msg decode failed", "reason", err)
	}
	clt.lastGetForkIdTime = time.Now()
	atomic.StoreUint32(&clt.connecting, 0)

	clt.forkFilter = forkid.NewFilterInDisguise(clt.forks, clt.genesisHash, clt.curBlockNumber)
	clt.forkID = forkid.NewIdInDisguise(clt.forks, clt.genesisHash, clt.curBlockNumber)

	fmt.Println("info: disguise client connect to server successfully")
	fmt.Println("info: forks ", clt.forks)
	fmt.Println("info: genesis ", clt.genesisHash)
	fmt.Println("info: block number ", clt.curBlockNumber)

	return clt
}

func (dc *DisguiseClient) decodeMsg(msg []byte) error {
	if bytes.HasPrefix(msg, []byte(GetAllAbove)) {
		// ============================================
		// 4 bytes: command of message
		// 4 bytes: length of forks (little endian)
		// 32 bytes: genesis hash
		// 8 bytes: block number (little endian)
		// (many) 8 bytes: fork
		if len(msg) < CmdSize+LengthSize+HashSize+BlockNumberSize {
			return InvalidLengthMsgErr
		}
		forksLength := binary.LittleEndian.Uint32(msg[CmdSize : CmdSize+LengthSize])
		msgTotalLength := CmdSize + LengthSize + HashSize + BlockNumberSize + ForkNumberSize*forksLength
		if len(msg) != int(msgTotalLength) {
			return InvalidLengthMsgErr
		}
		copy(dc.genesisHash[:], msg[CmdSize+LengthSize:CmdSize+LengthSize+HashSize])
		dc.curBlockNumber = binary.LittleEndian.Uint64(
			msg[CmdSize+LengthSize+HashSize : CmdSize+LengthSize+HashSize+BlockNumberSize])
		dc.forks = dc.forks[:0]
		offsite := CmdSize + LengthSize + HashSize + BlockNumberSize
		for i := 0; i < int(forksLength); i++ {
			dc.forks = append(dc.forks, binary.LittleEndian.Uint64(
				msg[offsite+i*ForkNumberSize:offsite+i*ForkNumberSize+ForkNumberSize]))
		}

	} else if bytes.HasPrefix(msg, []byte(GetForksMsg)) {
		// ============================================
		// 4 bytes: command of message
		// 4 bytes: length of forks (little endian)
		// (many) 8 bytes: fork
		if len(msg) < CmdSize+LengthSize {
			return InvalidLengthMsgErr
		}
		forksLength := binary.LittleEndian.Uint32(msg[CmdSize : CmdSize+LengthSize])
		msgTotalLength := CmdSize + LengthSize + ForkNumberSize*forksLength
		if len(msg) != int(msgTotalLength) {
			return InvalidLengthMsgErr
		}
		dc.forks = dc.forks[:0]
		for i := 1; i <= int(forksLength); i++ {
			dc.forks = append(dc.forks, binary.LittleEndian.Uint64(msg[CmdSize+LengthSize:CmdSize+LengthSize+i*ForkNumberSize]))
		}

	} else if bytes.HasPrefix(msg, []byte(GetGenesisMsg)) {
		// ============================================
		// 4 bytes: command of message
		// 32 bytes: genesis hash
		if len(msg) != CmdSize+HashSize {
			return InvalidCommandMsgErr
		}
		copy(dc.genesisHash[:], msg[CmdSize:CmdSize+HashSize])

	} else if bytes.HasPrefix(msg, []byte(GetBlockNumber)) {
		// ============================================
		// 4 bytes: command of message
		// 8 bytes: block number (little endian)
		if len(msg) != CmdSize+BlockNumberSize {
			return InvalidLengthMsgErr
		}
		dc.curBlockNumber = binary.LittleEndian.Uint64(msg[CmdSize : CmdSize+BlockNumberSize])

	} else {
		return InvalidCommandMsgErr
	}
	return nil
}

func (dc *DisguiseClient) TryingConnect() {
	if atomic.CompareAndSwapUint32(&dc.connecting, 0, 1) {
		_ = dc.conn.Close()
		var (
			err error
			con *tls.Conn
		)
		for {
			time.Sleep(5 * time.Second)
			con, err = tls.Dial("tcp", dc.tcpAddr.String(), dc.tlsConfig)
			if err == nil {
				dc.conn = con
				break
			}
		}
		atomic.StoreUint32(&dc.connecting, 0)
	}
}

func (dc *DisguiseClient) GetForkInfo() (forkid.ID, forkid.Filter) {
	var (
		err       error
		recvCount int
		recvBuf   = make([]byte, 0x400)
		fetchSuc  = true
	)

	dc.mut.Lock()
	defer dc.mut.Unlock()

	if time.Since(dc.lastGetForkIdTime) > 2*time.Second && atomic.LoadUint32(&dc.connecting) == 0 {
		_, err = dc.conn.Write([]byte(GetBlockNumber))
		if err != nil {
			log.Warn("send GetBlockNumber command message to disguise server failed", "reason", err)
			go dc.TryingConnect()
		} else {
			recvCount, err = dc.conn.Read(recvBuf)
			if err != nil {
				log.Warn("recv from disguise server failed", "reason", err)
				fetchSuc = false
			}

			if fetchSuc {
				err = dc.decodeMsg(recvBuf[:recvCount])
				if err != nil {
					log.Warn("recv from disguise msg decode failed", "reason", err)
					fetchSuc = false
				}
			}

			if fetchSuc {
				dc.lastGetForkIdTime = time.Now()
			}
		}
	}

	if fetchSuc == false {
		dc.forkFilter = forkid.NewFilterInDisguise(dc.forks, dc.genesisHash, dc.curBlockNumber)
		dc.forkID = forkid.NewIdInDisguise(dc.forks, dc.genesisHash, dc.curBlockNumber)
		go dc.TryingConnect()
	}

	return dc.forkID, dc.forkFilter
}

func (dc *DisguiseClient) RequestHeaderByHash(hash common.Hash) (*types.Header, error) {
	var (
		err    error
		msgBuf = make([]byte, CmdSize+HashSize)
		data   []byte
		header = &types.Header{}
	)

	dc.mut.Lock()
	defer dc.mut.Unlock()

	// request
	// 4 bytes cmd
	// 32 bytes hash

	// response
	// 4 bytes - msg length
	// ... []byte header in rlp

	log.Info("request header by hash start", "hash", hash)
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetHeaderByHash)
		copy(msgBuf[CmdSize:], hash.Bytes())
		_, err = dc.conn.Write(msgBuf)
		log.Info("request header by hash sent message", "msg", msgBuf)
		if err != nil {
			log.Warn("send GetHeaderByHash command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			log.Info("request header by hash receive message", "msg", data)
			if err != nil {
				log.Warn("read header from server failed", "reason", err)
			} else if len(data) > 8 {
				err = rlp.DecodeBytes(data, header)
				if err != nil {
					log.Warn("rlp decode header from server failed", "reason", err)
				}
				//log.Info("request header by hash done", "response", header.Hash())
				return header, nil
			} else {
				//log.Info("request header by hash done", "response", "nil")
				return nil, nil
			}
		}
	}

	return nil, err
}

func (dc *DisguiseClient) RequestHeaderByHashAndNumber(hash common.Hash, number uint64) (*types.Header, error) {
	var (
		err    error
		msgBuf = make([]byte, CmdSize+HashSize+BlockNumberSize)
		data   []byte
		header = &types.Header{}
	)
	dc.mut.Lock()
	defer dc.mut.Unlock()

	// request
	// 4 bytes cmd
	// 32 bytes hash
	// 8 bytes number

	// response
	// 4 bytes - msg length
	// ... []byte header in rlp
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetHeaderByHashAndNumber)
		copy(msgBuf[CmdSize:], hash.Bytes())
		binary.LittleEndian.PutUint64(msgBuf[CmdSize+BlockNumberSize:], number)
		_, err = dc.conn.Write(msgBuf)
		if err != nil {
			log.Warn("send GetHeaderByHashAndNumber command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			if err != nil {
				log.Warn("read header from server failed", "reason", err)
			} else if len(data) > 8 {
				err = rlp.DecodeBytes(data, header)
				if err != nil {
					log.Warn("rlp decode header from server failed", "reason", err)
				}
				return header, nil
			} else {
				return nil, nil
			}
		}
	}

	return nil, err
}

func (dc *DisguiseClient) RequestHeaderByNumber(number uint64) (*types.Header, error) {
	var (
		err    error
		msgBuf = make([]byte, CmdSize+BlockNumberSize)
		data   []byte
		header = &types.Header{}
	)
	dc.mut.Lock()
	defer dc.mut.Unlock()

	// request
	// 4 bytes cmd
	// 8 bytes number

	// response
	// 4 bytes - msg length
	// ... []byte header in rlp
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetHeaderByNumber)
		binary.LittleEndian.PutUint64(msgBuf[CmdSize:], number)
		_, err = dc.conn.Write(msgBuf)
		if err != nil {
			log.Warn("send GetHeaderByNumber command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			if err != nil {
				log.Warn("read header from server failed", "reason", err)
			} else if len(data) > 8 {
				err = rlp.DecodeBytes(data, header)
				if err != nil {
					log.Warn("rlp decode header from server failed", "reason", err)
				}
				return header, nil
			} else {
				return nil, nil
			}
		}
	}

	return nil, err
}

func (dc *DisguiseClient) RequestBlockBodyRLP(hash common.Hash) (rlp.RawValue, error) {
	var (
		err    error
		msgBuf = make([]byte, CmdSize+HashSize)
		data   []byte
	)
	dc.mut.Lock()
	defer dc.mut.Unlock()

	// request
	// 4 bytes cmd
	// 32 bytes hash

	// response
	// 4 bytes - msg length
	// ... []byte header in rlp
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetBlockBodyRlpByHash)
		copy(msgBuf[CmdSize:], hash.Bytes())
		_, err = dc.conn.Write(msgBuf)
		if err != nil {
			log.Warn("send GetBlockBodyRlpByHash command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			if err != nil {
				log.Warn("read block body rlp from server failed", "reason", err)
			} else if len(data) > 8 {
				return data, nil
			} else {
				return nil, nil
			}
		}
	}
	return []byte{}, err
}

func (dc *DisguiseClient) RequestReceiptsByHash(hash common.Hash) (types.Receipts, error) {
	var (
		err      error
		msgBuf   = make([]byte, CmdSize+BlockNumberSize)
		data     []byte
		receipts = types.Receipts{}
	)
	dc.mut.Lock()
	defer dc.mut.Unlock()

	// request
	// 4 bytes cmd
	// 32 bytes hash

	// response
	// 4 bytes - msg length
	// ... []byte receipts in rlp
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetReceiptsByHash)
		copy(msgBuf[CmdSize:], hash.Bytes())
		_, err = dc.conn.Write(msgBuf)
		if err != nil {
			log.Warn("send GetReceiptsByHash command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			if err != nil {
				log.Warn("read receipts from server failed", "reason", err)
			} else if len(data) > 8 {
				err = rlp.DecodeBytes(data, receipts)
				if err != nil {
					log.Warn("rlp decode receipts from server failed", "reason", err)
				}
				return receipts, nil
			} else {
				return nil, nil
			}
		}
	}
	return receipts, err
}

func (dc *DisguiseClient) RequestTrieNodeByHash(hash common.Hash) ([]byte, error) {
	var (
		err    error
		msgBuf = make([]byte, CmdSize+HashSize)
		data   []byte
	)
	dc.mut.Lock()
	defer dc.mut.Unlock()

	// request
	// 4 bytes cmd
	// 32 bytes hash

	// response
	// 4 bytes - msg length
	// ... []byte header in rlp
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetTrieNode)
		copy(msgBuf[CmdSize:], hash.Bytes())
		_, err = dc.conn.Write(msgBuf)
		if err != nil {
			log.Warn("send GetTrieNode command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			if err != nil {
				log.Warn("read tri node from server failed", "reason", err)
			} else if len(data) > 8 {
				return data, nil
			} else {
				return nil, nil
			}
		}
	}
	return []byte{}, err
}

func (dc *DisguiseClient) RequestContractCodeWithPrefix(hash common.Hash) ([]byte, error) {
	var (
		err    error
		msgBuf = make([]byte, CmdSize+HashSize)
		data   []byte
	)
	dc.mut.Lock()
	defer dc.mut.Unlock()

	// request
	// 4 bytes cmd
	// 32 bytes hash

	// response
	// 4 bytes - msg length
	// ... []byte header in rlp
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetContractCodeWithPrefix)
		copy(msgBuf[CmdSize:], hash.Bytes())
		_, err = dc.conn.Write(msgBuf)
		if err != nil {
			log.Warn("send GetContractCodeWithPrefix command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			if err != nil {
				log.Warn("read contract code from server failed", "reason", err)
			} else if len(data) > 8 {
				return data, nil
			} else {
				return nil, nil
			}
		}
	}
	return []byte{}, err
}

func (dc *DisguiseClient) RequestAncestor(hash common.Hash, blockNumber uint64, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	var (
		err       error
		msgBuf    = make([]byte, CmdSize+HashSize+BlockNumberSize*3)
		data      []byte
		resHash   common.Hash
		resUint64 uint64
	)
	dc.mut.Lock()
	defer dc.mut.Unlock()

	// request
	// 4 bytes cmd
	// 32 bytes hash
	// 8 bytes number
	// 8 bytes number
	// 8 bytes number

	// response
	// 4 bytes - msg length
	// 32 bytes - hash
	// 8 bytes - uint64
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetAncestor)
		copy(msgBuf[CmdSize:], hash.Bytes())
		binary.LittleEndian.PutUint64(msgBuf[CmdSize+HashSize:], blockNumber)
		binary.LittleEndian.PutUint64(msgBuf[CmdSize+HashSize+BlockNumberSize*1:], ancestor)
		binary.LittleEndian.PutUint64(msgBuf[CmdSize+HashSize+BlockNumberSize*2:], *maxNonCanonical)
		_, err = dc.conn.Write(msgBuf)
		if err != nil {
			log.Warn("send GetAncestor command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			if err != nil {
				log.Warn("read ancestor hash from server failed", "reason", err)
			} else if len(data) >= HashSize+BlockNumberSize {
				resHash = common.BytesToHash(data[:HashSize])
				resUint64 = binary.LittleEndian.Uint64(data[HashSize : HashSize+BlockNumberSize])
				return resHash, resUint64
			}
		}
	}
	return common.Hash{}, 0
}

func (dc *DisguiseClient) RequestTotalDifficulty(hash common.Hash, number uint64) (*big.Int, error) {
	var (
		err           error
		msgBuf        = make([]byte, CmdSize+HashSize+BlockNumberSize)
		data          []byte
		resDifficulty = new(big.Int)
	)
	dc.mut.Lock()
	defer dc.mut.Unlock()
	// request
	// 4 bytes cmd
	// 32 bytes hash
	// 8 bytes number

	// response
	// 4 bytes - msg length
	// 8 bytes - uint64
	if atomic.LoadUint32(&dc.connecting) == 0 {
		copy(msgBuf, GetTotalDifficulty)
		copy(msgBuf[CmdSize:], hash.Bytes())
		binary.LittleEndian.PutUint64(msgBuf[CmdSize+HashSize:], number)

		_, err = dc.conn.Write(msgBuf)
		if err != nil {
			log.Warn("send GetTotalDifficulty command to disguise message failed", "reason", err)
			go dc.TryingConnect()
		} else {
			data, err = dc.readLengthBytes()
			if err != nil {
				log.Warn("read total difficulty from server failed", "reason", err)
			} else if len(data) >= BlockNumberSize {
				resDifficulty.SetBytes(data[:BlockNumberSize])
				return resDifficulty, nil
			}
		}
	}
	return resDifficulty, err
}

func (dc *DisguiseClient) readLengthBytes() ([]byte, error) {
	var (
		err      error
		recvBuf  = make([]byte, 0x4000)
		lenLimit = 100 << 10
		bytesLen uint32
		rdr      = bufio.NewReader(dc.conn)
	)
	_, err = io.ReadFull(rdr, recvBuf[:LengthSize])
	if err != nil {
		go dc.TryingConnect()
		return nil, err
	}
	bytesLen = binary.LittleEndian.Uint32(recvBuf[:LengthSize])
	if int(bytesLen) > lenLimit {
		return nil, errors.New(fmt.Sprint("invalid length: ", bytesLen))
	}
	if int(bytesLen) > len(recvBuf) {
		recvBuf = make([]byte, bytesLen)
	}
	if bytesLen == 0 {
		return []byte{}, nil
	}
	_, err = io.ReadFull(rdr, recvBuf[:bytesLen])
	if err != nil {
		return nil, err
	}
	return recvBuf[:bytesLen], err
}
