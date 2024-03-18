package project

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pkg/errors"

	"github.com/machinefi/sprout/output"
	"github.com/machinefi/sprout/types"
	"github.com/machinefi/sprout/utils/ipfs"
)

type Config struct {
	Code         string            `json:"code"`
	CodeExpParam string            `json:"codeExpParam,omitempty"`
	VMType       types.VM          `json:"vmType"`
	Output       OutputConfig      `json:"output"`
	Aggregation  AggregationConfig `json:"aggregation"`
	Version      string            `json:"version"`
}

type AggregationConfig struct {
	Amount uint `json:"amount,omitempty"`
}

type OutputConfig struct {
	Type     types.Output   `json:"type"`
	Ethereum EthereumConfig `json:"ethereum,omitempty"`
	Solana   SolanaConfig   `json:"solana,omitempty"`
	Textile  TextileConfig  `json:"textile,omitempty"`
}

type EthereumConfig struct {
	ChainEndpoint   string `json:"chainEndpoint"`
	ContractAddress string `json:"contractAddress"`
	ReceiverAddress string `json:"receiverAddress,omitempty"`
	ContractMethod  string `json:"contractMethod"`
	ContractAbiJSON string `json:"contractAbiJSON"`
}

type SolanaConfig struct {
	ChainEndpoint  string `json:"chainEndpoint"`
	ProgramID      string `json:"programID"`
	StateAccountPK string `json:"stateAccountPK"`
}

type TextileConfig struct {
	VaultID string `json:"vaultID"`
}

func (c *Config) GetOutput(privateKeyECDSA, privateKeyED25519 string) (output.Output, error) {
	outConf := c.Output

	switch outConf.Type {
	case types.OutputEthereumContract:
		ethConf := outConf.Ethereum
		return output.NewEthereum(ethConf.ChainEndpoint, privateKeyECDSA, ethConf.ContractAddress, ethConf.ReceiverAddress, ethConf.ContractAbiJSON, ethConf.ContractMethod)
	case types.OutputSolanaProgram:
		solConf := outConf.Solana
		return output.NewSolanaProgram(solConf.ChainEndpoint, solConf.ProgramID, privateKeyED25519, solConf.StateAccountPK)
	case types.OutputTextile:
		textileConf := outConf.Textile
		return output.NewTextileDBAdapter(textileConf.VaultID, privateKeyECDSA)
	default:
		return output.NewStdout(), nil
	}
}

type ProjectMeta struct {
	ProjectID uint64
	Uri       string
	Hash      [32]byte
	Paused    bool
}

func (m *ProjectMeta) GetConfigData(ipfsEndpoint string) ([]byte, error) {
	u, err := url.Parse(m.Uri)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse project uri %s", m.Uri)
	}

	var data []byte
	switch u.Scheme {
	case "http", "https":
		resp, _err := http.Get(m.Uri)
		if _err != nil {
			return nil, errors.Wrapf(_err, "failed to fetch project config, project_id %d, uri %s", m.ProjectID, m.Uri)
		}
		defer resp.Body.Close()
		// TODO network error should try again
		data, err = io.ReadAll(resp.Body)

	case "ipfs":
		// ipfs url: ipfs://${endpoint}/${cid}
		sh := ipfs.NewIPFS(u.Host)
		cid := strings.Split(strings.Trim(u.Path, "/"), "/")
		data, err = sh.Cat(cid[0])

	default:
		// fetch content by ipfs cid with default endpoint
		sh := ipfs.NewIPFS(ipfsEndpoint)
		cid := strings.Split(strings.Trim(u.Path, "/"), "/")
		data, err = sh.Cat(cid[0])
	}

	if err != nil {
		return nil, errors.Wrapf(err, "failed to read project config, project_id %d, uri %s", m.ProjectID, m.Uri)
	}

	h := sha256.New()
	if _, err := h.Write(data); err != nil {
		return nil, errors.Wrap(err, "failed to generate project config hash")
	}
	if !bytes.Equal(h.Sum(nil), m.Hash[:]) {
		return nil, errors.New("failed to validate project config hash")
	}

	return data, nil
}

func convertConfigs(data []byte) ([]*Config, error) {
	cs := []*Config{}
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal project config")
	}
	if len(cs) == 0 {
		return nil, errors.Errorf("empty project config")
	}
	for _, c := range cs {
		if c.Code == "" || c.VMType == "" || c.Version == "" {
			return nil, errors.Errorf("invalid project config")
		}
	}
	return cs, nil
}
