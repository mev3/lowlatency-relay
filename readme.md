## Get Started

This project is a scalable and customizable framework for reducing blockchain latency that excels with light weight, efficiency, and low cost. Currently, it only supports Binance Smart Chain (`bsc`) is  supported, Ethereum (`eth`) and Polygon(`matic`) will be supported in the future. 

### BSC

The system consists of two roles, first a full node, which is a modified `bsc` used to support relay nodes, which can be any number, and the more deployed, the lower the latency.

### Build 

**build modified full node**

```bash
git clone https://github.com/mev3/lowlatency-geth.git
cd lowlatency-geth
make geth
```
After that you should refer to the BSC [documentation](https://docs.binance.org/smart-chain/developer/fullnode.html) to  get this full node up and running. It is recommended to use [snapshot](https://github.com/bnb-chain/bsc-snapshots),  which greatly reduces the time to synchronize to the latest state.


**build relay node**
```bash
git clone https://github.com/mev3/lowlatency-relay.git
cd lowlatency-relay/cmd/piobsc
go build
```
You should deploy these relay nodes on different continents so that you can minimize latency as low as possible.

### Run

Here are the parameters in the configuration file related to the pioplat function, and the corresponding descriptions. Please put these parameters in the configuration file, all under the `[Eth]` category.
```toml
PeriActive = true # whether enable peri neighbor selection
PeriPeriod = 600  # peri cyclic time
PeriReplaceRatio = 0.2 # neighbors ratio to drop each period
PeriBlockNodeRatio = 0.3 # ratio of neighbors deliver blocks fast to reserve each period
PeriTxNodeRatio = 0.5 # ratio of neighbors deliver transactions fast to reserve each period
PeriMinInbound  = 10
PeriMaxDelayPenalty = 30000 # used to compute scores for each neighbors
PeriAnnouncePenalty = 0 # unused now
PeriObservedTxRatio  = 4 # only broadcast 1/PeriObservedTxRatio transactions to neighbors
PeriTargeted  = false # parameter of peri neighbor selection, currently unused
PeriTargetAccountList  = [] # parameter of peri neighbor selection, currently unused
PeriNoPeerIPList   = [] # do not connect this ip
PeriNoDropList  = [] # do not drop peers of this ip
PeriLogFilePath = "./peri.log" # log file
DisguiseServerUrl = "<ip and port of pioplat modified full node>"
PioplatServerListenAddr = "0.0.0.0:9998" # to connect with others relay nodes
EvaluationBlockJsonFile = "./block_timestamp.json" # to record blocks receiving timestamp
EvaluationTxJsonFile = "./tx_timestamp.json" # to record transactions receiving timestamp
PioplatRpcAdminSecret = "xxxx" # rpc admin secret
```



