package contract

import (
	"container/list"
	"context"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"

	"github.com/machinefi/sprout/smartcontracts/go/project"
	"github.com/machinefi/sprout/smartcontracts/go/prover"
)

var (
	allTopicHash = []common.Hash{
		attributeSetTopicHash,
		projectPausedTopicHash,
		projectResumedTopicHash,
		projectConfigUpdatedTopicHash,

		operatorSetTopicHash,
		nodeTypeUpdatedTopicHash,
		proverPausedTopicHash,
		proverResumedTopicHash,
	}
)

type Contract struct {
	epoch                      uint64
	proverContractAddress      common.Address
	projectContractAddress     common.Address
	blockNumberContractAddress common.Address
	multiCallContractAddress   common.Address
	chainHeadNotifications     []chan<- uint64
	projectNotifications       []chan<- *Project
	blockProjects              *blockProjects
	blockProvers               *blockProvers
	scanInterval               time.Duration
	client                     *ethclient.Client
	proverInstance             *prover.Prover
	projectInstance            *project.Project
}

func (c *Contract) Project(projectID, blockNumber uint64) *Project {
	return c.blockProjects.project(projectID, blockNumber)
}

func (c *Contract) LatestProject(projectID uint64) *Project {
	return c.blockProjects.project(projectID, 0)
}

func (c *Contract) LatestProjects() []*Project {
	bp := c.blockProjects.projects()
	ps := make([]*Project, 0, len(bp.Projects))
	for _, p := range bp.Projects {
		ps = append(ps, p)
	}
	return ps
}

func (c *Contract) Provers(blockNumber uint64) []*Prover {
	bp := c.blockProvers.provers(blockNumber)
	ps := make([]*Prover, 0, len(bp.Provers))
	for _, p := range bp.Provers {
		ps = append(ps, p)
	}
	return ps
}

func (c *Contract) LatestProvers() []*Prover {
	bp := c.blockProvers.provers(0)
	ps := make([]*Prover, 0, len(bp.Provers))
	for _, p := range bp.Provers {
		ps = append(ps, p)
	}
	return ps
}

func (c *Contract) Prover(operator common.Address) *Prover {
	return c.blockProvers.prover(operator)
}

func (c *Contract) notifyProject(bp *blockProject) {
	for _, p := range bp.Projects {
		for _, n := range c.projectNotifications {
			n <- p
		}
	}
}

func (c *Contract) notifyChainHead(chainHead uint64) {
	for _, n := range c.chainHeadNotifications {
		n <- chainHead
	}
}

func (c *Contract) addBlockProject(bp *blockProject) {
	c.blockProjects.add(bp)
	c.notifyProject(bp)
}

func (c *Contract) list() (uint64, error) {
	projects, projectMinBlockNumber, projectMaxBlockNumber, err := listProject(c.client, c.projectContractAddress, c.blockNumberContractAddress, c.multiCallContractAddress)
	if err != nil {
		return 0, err
	}
	provers, proverMinBlockNumber, proverMaxBlockNumber, err := listProver(c.client, c.proverContractAddress, c.blockNumberContractAddress, c.multiCallContractAddress)
	if err != nil {
		return 0, err
	}
	minBlockNumber := min(projectMinBlockNumber, proverMinBlockNumber)
	maxBlockNumber := max(projectMaxBlockNumber, proverMaxBlockNumber)
	minBlockNumber = minBlockNumber - c.epoch

	query := ethereum.FilterQuery{
		Addresses: []common.Address{c.proverContractAddress, c.projectContractAddress},
		Topics:    [][]common.Hash{allTopicHash},
		FromBlock: new(big.Int).SetUint64(minBlockNumber),
		ToBlock:   new(big.Int).SetUint64(maxBlockNumber),
	}
	logs, err := c.client.FilterLogs(context.Background(), query)
	if err != nil {
		return 0, errors.Wrap(err, "failed to filter contract logs")
	}

	if len(logs) == 0 {
		logs = []types.Log{{
			Topics:      []common.Hash{emptyHash},
			BlockNumber: maxBlockNumber,
		}}
	}

	blockProjects := list.New()
	blockProjectMap := map[uint64]*blockProject{}
	blockProvers := list.New()
	blockProverMap := map[uint64]*blockProver{}

	if err := processProjectLogs(func(p *blockProject) {
		blockProjects.PushBack(p)
		blockProjectMap[p.BlockNumber] = p
	}, logs, c.projectInstance); err != nil {
		return 0, errors.Wrap(err, "failed to process project logs")
	}
	if err := processProverLogs(func(p *blockProver) {
		blockProvers.PushBack(p)
		blockProverMap[p.BlockNumber] = p
	}, logs, c.proverInstance); err != nil {
		return 0, errors.Wrap(err, "failed to process prover logs")
	}

	minBlockProject := &blockProject{
		BlockNumber: minBlockNumber,
		Projects:    map[uint64]*Project{},
	}
	for _, p := range projects {
		for e := blockProjects.Back(); e != nil; e = e.Prev() {
			ebp := e.Value.(*blockProject)
			if ebp.BlockNumber >= p.BlockNumber {
				continue
			}
			ep, ok := ebp.Projects[p.ID]
			if ok {
				p.merge(ep)
			}
		}
		p.BlockNumber = minBlockNumber
		minBlockProject.Projects[p.ID] = p
	}

	minBlockProver := &blockProver{
		BlockNumber: minBlockNumber,
		Provers:     map[uint64]*Prover{},
	}
	for _, p := range provers {
		for e := blockProvers.Back(); e != nil; e = e.Prev() {
			ebp := e.Value.(*blockProver)
			if ebp.BlockNumber >= p.BlockNumber {
				continue
			}
			ep, ok := ebp.Provers[p.ID]
			if ok {
				p.merge(ep)
			}
		}
		p.BlockNumber = minBlockNumber
		minBlockProver.Provers[p.ID] = p
	}

	c.blockProjects.add(minBlockProject)
	c.blockProvers.add(minBlockProver)

	for n := minBlockNumber + 1; n <= maxBlockNumber; n++ {
		p, ok := blockProjectMap[n]
		if ok {
			c.blockProjects.add(p)
		} else {
			c.blockProjects.add(&blockProject{
				BlockNumber: n,
				Projects:    map[uint64]*Project{},
			})
		}
	}

	for n := minBlockNumber + 1; n <= maxBlockNumber; n++ {
		p, ok := blockProverMap[n]
		if ok {
			c.blockProvers.add(p)
		} else {
			c.blockProvers.add(&blockProver{
				BlockNumber: n,
				Provers:     map[uint64]*Prover{},
			})
		}
	}

	return maxBlockNumber, nil
}

func (c *Contract) watch(listedBlockNumber uint64) {
	queriedBlockNumber := listedBlockNumber
	query := ethereum.FilterQuery{
		Addresses: []common.Address{c.proverContractAddress, c.projectContractAddress},
		Topics:    [][]common.Hash{allTopicHash},
	}
	ticker := time.NewTicker(c.scanInterval)

	go func() {
		for range ticker.C {
			target := queriedBlockNumber + 1

			query.FromBlock = new(big.Int).SetUint64(target)
			query.ToBlock = new(big.Int).SetUint64(target)
			logs, err := c.client.FilterLogs(context.Background(), query)
			if err != nil {
				if !strings.Contains(err.Error(), "start block > tip height") {
					slog.Error("failed to filter contract logs", "error", err)
				}
				continue
			}

			if len(logs) == 0 {
				logs = []types.Log{{
					Topics:      []common.Hash{emptyHash},
					BlockNumber: target,
				}}
			}

			if err := processProjectLogs(c.addBlockProject, logs, c.projectInstance); err != nil {
				slog.Error("failed to process project logs", "error", err)
				continue
			}
			if err := processProverLogs(c.blockProvers.add, logs, c.proverInstance); err != nil {
				slog.Error("failed to process prover logs", "error", err)
				continue
			}

			c.notifyChainHead(target)

			queriedBlockNumber = target
		}
	}()
}

func New(epoch uint64, chainEndpoint string, proverContractAddress, projectContractAddress, blockNumberContractAddress, multiCallContractAddress common.Address, chainHeadNotifications []chan<- uint64, projectNotifications []chan<- *Project) (*Contract, error) {
	blockProjects := &blockProjects{
		capacity: epoch,
		blocks:   list.New(),
	}
	blockProvers := &blockProvers{
		capacity: epoch,
		blocks:   list.New(),
	}

	client, err := ethclient.Dial(chainEndpoint)
	if err != nil {
		return nil, errors.Wrap(err, "failed to dial chain endpoint")
	}
	projectInstance, err := project.NewProject(projectContractAddress, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to new project contract instance")
	}
	proverInstance, err := prover.NewProver(proverContractAddress, client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to new prover contract instance")
	}

	c := &Contract{
		epoch:                      epoch,
		proverContractAddress:      proverContractAddress,
		projectContractAddress:     projectContractAddress,
		blockNumberContractAddress: blockNumberContractAddress,
		multiCallContractAddress:   multiCallContractAddress,
		chainHeadNotifications:     chainHeadNotifications,
		projectNotifications:       projectNotifications,
		blockProjects:              blockProjects,
		blockProvers:               blockProvers,
		scanInterval:               1 * time.Second,
		client:                     client,
		proverInstance:             proverInstance,
		projectInstance:            projectInstance,
	}

	listedBlockNumber, err := c.list()
	if err != nil {
		return nil, err
	}
	go c.watch(listedBlockNumber)

	return c, nil
}