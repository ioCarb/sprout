package contract

import (
	"bytes"
	"context"
	"log/slog"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"

	"github.com/machinefi/sprout/smartcontracts/go/prover"
)

var (
	operatorSetTopicHash     = crypto.Keccak256Hash([]byte("OperatorSet(uint256,address)"))
	nodeTypeUpdatedTopicHash = crypto.Keccak256Hash([]byte("NodeTypeUpdated(uint256,uint256)"))
	proverPausedTopicHash    = crypto.Keccak256Hash([]byte("ProverPaused(uint256)"))
	proverResumedTopicHash   = crypto.Keccak256Hash([]byte("ProverResumed(uint256)"))

	emptyAddress = common.Address{}
)

type BlockProver struct {
	BlockNumber uint64
	Provers     map[uint64]*Prover
}

type Prover struct {
	ID              uint64
	OperatorAddress common.Address
	BlockNumber     uint64
	Paused          *bool
	NodeTypes       uint64
}

func (ps *BlockProver) Merge(diff *BlockProver) {
	ps.BlockNumber = diff.BlockNumber
	for id, p := range ps.Provers {
		diffP, ok := diff.Provers[id]
		if ok {
			p.Merge(diffP)
		}
	}
	for id, p := range diff.Provers {
		if _, ok := ps.Provers[id]; !ok {
			np := &Prover{}
			np.Merge(p)
			ps.Provers[id] = np
		}
	}
}

func (p *Prover) Merge(diff *Prover) {
	if diff.ID != 0 {
		p.ID = diff.ID
	}
	if !bytes.Equal(diff.OperatorAddress[:], emptyAddress[:]) {
		p.OperatorAddress = diff.OperatorAddress
	}
	if diff.BlockNumber != 0 {
		p.BlockNumber = diff.BlockNumber
	}
	if diff.Paused != nil {
		p.Paused = diff.Paused
	}
	if diff.NodeTypes != 0 {
		p.NodeTypes = diff.NodeTypes
	}
}

func ListAndWatchProver(chainEndpoint, contractAddress string, tracebackLength uint64) (<-chan *BlockProver, error) {
	ch := make(chan *BlockProver, 10)
	client, err := ethclient.Dial(chainEndpoint)
	if err != nil {
		return nil, errors.Wrap(err, "failed to dial chain endpoint")
	}

	instance, err := prover.NewProver(common.HexToAddress(contractAddress), client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to new prover contract instance")
	}

	latestBlockNumber, err := client.BlockNumber(context.Background())
	if err != nil {
		return nil, errors.Wrap(err, "failed to query the latest block number")
	}
	targetBlockNumber := latestBlockNumber - tracebackLength
	if err := listProver(ch, instance, targetBlockNumber); err != nil {
		return nil, err
	}

	topics := []common.Hash{operatorSetTopicHash, nodeTypeUpdatedTopicHash, proverResumedTopicHash}
	watchProver(ch, client, instance, 3*time.Second, contractAddress, topics, 1000, targetBlockNumber)

	return ch, nil
}

func listProver(ch chan<- *BlockProver, instance *prover.Prover, targetBlockNumber uint64) error {
	provers := map[uint64]*Prover{}
	for id := uint64(1); ; id++ {
		mp, err := instance.Operator(nil, new(big.Int).SetUint64(id))
		if err != nil {
			if strings.Contains(err.Error(), "execution reverted: ERC721: invalid token ID") {
				break
			}
			return errors.Wrapf(err, "failed to get operator from chain, prover_id %v", id)
		}

		isPaused, err := instance.IsPaused(nil, new(big.Int).SetUint64(id))
		if err != nil {
			return errors.Wrapf(err, "failed to get prover pause status from chain, prover_id %v", id)
		}
		nodeTypes, err := instance.NodeType(nil, new(big.Int).SetUint64(id))
		if err != nil {
			return errors.Wrapf(err, "failed to get prover nodeTypes from chain, prover_id %v", id)
		}

		provers[id] = &Prover{
			ID:              id,
			OperatorAddress: mp,
			BlockNumber:     targetBlockNumber,
			Paused:          &isPaused,
			NodeTypes:       nodeTypes.Uint64(),
		}
	}
	ch <- &BlockProver{
		BlockNumber: targetBlockNumber,
		Provers:     provers,
	}
	return nil
}

func watchProver(ch chan<- *BlockProver, client *ethclient.Client, instance *prover.Prover, interval time.Duration,
	contractAddress string, topics []common.Hash, step, startBlockNumber uint64) {
	queriedBlockNumber := startBlockNumber
	query := ethereum.FilterQuery{
		Addresses: []common.Address{common.HexToAddress(contractAddress)},
		Topics: [][]common.Hash{{
			(topics[0]),
			(topics[1]),
			(topics[2]),
		}},
	}
	ticker := time.NewTicker(interval)

	go func() {
		for range ticker.C {
			from := queriedBlockNumber + 1
			to := from + step

			latestBlockNumber, err := client.BlockNumber(context.Background())
			if err != nil {
				slog.Error("failed to query the latest block number", "error", err)
				continue
			}
			if to > latestBlockNumber {
				to = latestBlockNumber
			}
			if from > to {
				continue
			}
			query.FromBlock = new(big.Int).SetUint64(from)
			query.ToBlock = new(big.Int).SetUint64(to)
			logs, err := client.FilterLogs(context.Background(), query)
			if err != nil {
				slog.Error("failed to filter contract logs", "error", err)
				continue
			}
			if processProverLogs(ch, logs, instance) {
				queriedBlockNumber = to
			}
		}
	}()
}

func processProverLogs(ch chan<- *BlockProver, logs []types.Log, instance *prover.Prover) bool {
	if len(logs) == 0 {
		return true
	}
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].BlockNumber != logs[j].BlockNumber {
			return logs[i].BlockNumber < logs[j].BlockNumber
		}
		return logs[i].TxIndex < logs[j].TxIndex
	})
	psMap := map[uint64]*BlockProver{}

	for _, l := range logs {
		ps, ok := psMap[l.BlockNumber]
		if !ok {
			ps = &BlockProver{
				BlockNumber: l.BlockNumber,
				Provers:     map[uint64]*Prover{},
			}
		}
		switch l.Topics[0] {
		case operatorSetTopicHash:
			e, err := instance.ParseOperatorSet(l)
			if err != nil {
				slog.Error("failed to parse project attribute set event", "error", err)
				return false
			}

			p, ok := ps.Provers[e.Id.Uint64()]
			if !ok {
				p = &Prover{ID: e.Id.Uint64()}
			}
			p.OperatorAddress = e.Operator
			ps.Provers[e.Id.Uint64()] = p

		case nodeTypeUpdatedTopicHash:
			e, err := instance.ParseNodeTypeUpdated(l)
			if err != nil {
				slog.Error("failed to parse project paused event", "error", err)
				return false
			}

			p, ok := ps.Provers[e.Id.Uint64()]
			if !ok {
				p = &Prover{ID: e.Id.Uint64()}
			}
			p.NodeTypes = e.Typ.Uint64()
			ps.Provers[e.Id.Uint64()] = p

		case proverPausedTopicHash:
			e, err := instance.ParseProverPaused(l)
			if err != nil {
				slog.Error("failed to parse project resumed event", "error", err)
				return false
			}

			p, ok := ps.Provers[e.Id.Uint64()]
			if !ok {
				p = &Prover{ID: e.Id.Uint64()}
			}
			paused := true
			p.Paused = &paused
			ps.Provers[e.Id.Uint64()] = p

		case proverResumedTopicHash:
			e, err := instance.ParseProverResumed(l)
			if err != nil {
				slog.Error("failed to parse project config updated event", "error", err)
				return false
			}

			p, ok := ps.Provers[e.Id.Uint64()]
			if !ok {
				p = &Prover{ID: e.Id.Uint64()}
			}
			paused := false
			p.Paused = &paused
			ps.Provers[e.Id.Uint64()] = p
		}
		psMap[l.BlockNumber] = ps
	}

	psSlice := []*BlockProver{}
	for _, p := range psMap {
		psSlice = append(psSlice, p)
	}
	sort.Slice(psSlice, func(i, j int) bool {
		return psSlice[i].BlockNumber < psSlice[j].BlockNumber
	})

	for _, p := range psSlice {
		ch <- p
	}
	return true
}
