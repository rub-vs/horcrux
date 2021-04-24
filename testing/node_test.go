package testing

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path"
	"strings"
	"time"

	"github.com/avast/retry-go"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	tmconfig "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/p2p"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	valKey   = "validator"
	genCoins = "1000000000000stake"
)

// ChainType represents the type of chain to instantiate
type ChainType struct {
	Repository string
	Version    string
	Bin        string
	Ports      []string
}

// ChainType instance for simd
var simdChain = &ChainType{
	Repository: "jackzampolin/simd",
	Version:    "v0.42.3",
	Bin:        "simd",
	Ports:      []string{"26656", "26657"},
}

// TestNode represents a node in the test network that is being created
type TestNode struct {
	Home     string
	Index    int
	ChainID  string
	Chain    *ChainType
	Provider *testcontainers.DockerProvider
}

// MakeTestNodes create the test node objects required for bootstrapping tests
func MakeTestNodes(count int, home, chainid string, chainType *ChainType, provider *testcontainers.DockerProvider) (out []*TestNode) {
	for i := 0; i < count; i++ {
		tn := &TestNode{Home: home, Index: i, Chain: chainType, ChainID: chainid, Provider: provider}
		tn.MkDir()
		out = append(out, tn)
	}
	return
}

// Name is the hostname of the test node container
func (tn *TestNode) Name() string {
	return fmt.Sprintf("node-%d", tn.Index)
}

// Dir is the directory where the test node files are stored
func (tn *TestNode) Dir() string {
	return fmt.Sprintf("%s/%s/", tn.Home, tn.Name())
}

// MkDir creates the directory for the testnode
func (tn *TestNode) MkDir() {
	if err := os.MkdirAll(tn.Dir(), 0755); err != nil {
		panic(err)
	}
}

// GentxPath returns the path to the gentx for a node
func (tn *TestNode) GentxPath() (string, error) {
	id, err := tn.NodeID()
	return path.Join(tn.Dir(), "config", "gentx", fmt.Sprintf("gentx-%s.json", id)), err
}

func (tn *TestNode) GenesisFilePath() string {
	return path.Join(tn.Dir(), "config", "genesis.json")
}

func (tn *TestNode) TMConfigPath() string {
	return path.Join(tn.Dir(), "config", "config.toml")
}

// Bind returns the home folder bind point for running the node
func (tn *TestNode) Bind() map[string]string {
	return map[string]string{tn.Dir(): fmt.Sprintf("/root/.%s", tn.Chain.Bin)}
}

func (tn *TestNode) NodeHome() string {
	return fmt.Sprintf("/root/.%s", tn.Chain.Bin)
}

// Keybase returns the keyring for a given node
func (tn *TestNode) Keybase() keyring.Keyring {
	kr, err := keyring.New("", keyring.BackendTest, tn.Dir(), os.Stdin)
	if err != nil {
		panic(err)
	}
	return kr
}

// SetValidatorConfigAndPeers modifies the config for a validator node to start a chain
func (tn *TestNode) SetValidatorConfigAndPeers(peers TestNodes) error {
	// Pull current config
	cfg := tmconfig.DefaultConfig()
	// turn down blocktimes to make the chain faster
	cfg.Consensus.TimeoutCommit = 1 * time.Second
	cfg.Consensus.TimeoutPropose = 1 * time.Second

	// Open up rpc address
	cfg.RPC.ListenAddress = "tcp://0.0.0.0:26657"

	// Allow for some p2p weirdness
	cfg.P2P.AllowDuplicateIP = true
	cfg.P2P.AddrBookStrict = false

	// Set log level to info
	cfg.BaseConfig.LogLevel = "info"

	// set persistent peer nodes
	ps, err := peers.PeerString()
	if err != nil {
		return err
	}
	cfg.P2P.PersistentPeers = ps

	// overwrite with the new config
	tmconfig.WriteConfigFile(tn.TMConfigPath(), cfg)
	return nil
}

func (tn *TestNode) NodeJob(ctx context.Context, cmd []string, waiting wait.Strategy) (testcontainers.Container, error) {
	// NOTE: on job containers generate random name
	container := RandLowerCaseLetterString(10)
	return tn.Provider.RunContainer(ctx, testcontainers.ContainerRequest{
		Image:        fmt.Sprintf("%s:%s", tn.Chain.Repository, tn.Chain.Version),
		ExposedPorts: tn.Chain.Ports,
		Cmd:          cmd,
		BindMounts:   tn.Bind(),
		WaitingFor:   waiting,
		Name:         container,
		Hostname:     container,
		AutoRemove:   true,
	})
}

// InitHomeFolder initializes a home folder for the given node
func (tn *TestNode) InitHomeFolder(ctx context.Context) (testcontainers.Container, error) {
	cmd := []string{tn.Chain.Bin, "init", tn.Name(),
		"--chain-id", tn.ChainID,
		"--home", tn.NodeHome(),
	}
	return tn.NodeJob(ctx, cmd, wait.ForLog("validator_accumulated_commissions"))
}

// CreateKey creates a key in the keyring backend test for the given node
func (tn *TestNode) CreateKey(ctx context.Context, name string) (testcontainers.Container, error) {
	cmd := []string{tn.Chain.Bin, "keys", "add", name,
		"--keyring-backend", "test",
		"--output", "json",
		"--home", tn.NodeHome(),
	}
	return tn.NodeJob(ctx, cmd, wait.ForLog("mnemonic"))
}

// AddGenesisAccount adds a genesis account for each key
func (tn *TestNode) AddGenesisAccount(ctx context.Context, address string) (testcontainers.Container, error) {
	cmd := []string{tn.Chain.Bin, "add-genesis-account", address, "1000000000000stake",
		"--home", tn.NodeHome(),
	}
	return tn.NodeJob(ctx, cmd, wait.ForLog(""))
}

// Gentx generates the gentx for a given node
func (tn *TestNode) Gentx(ctx context.Context, name string) (testcontainers.Container, error) {
	cmd := []string{tn.Chain.Bin, "gentx", valKey, "100000000000stake",
		"--keyring-backend", "test",
		"--home", tn.NodeHome(),
		"--chain-id", tn.ChainID,
	}
	return tn.NodeJob(ctx, cmd, wait.ForLog("Genesis transaction"))
}

func (tn *TestNode) CollectGentxs(ctx context.Context) (testcontainers.Container, error) {
	cmd := []string{tn.Chain.Bin, "collect-gentxs",
		"--home", tn.NodeHome(),
	}
	return tn.NodeJob(ctx, cmd, wait.ForLog("validator_accumulated_commissions"))
}

func (tn *TestNode) CreateNodeContainer(ctx context.Context) (testcontainers.Container, error) {
	return tn.Provider.CreateContainer(ctx, testcontainers.ContainerRequest{
		Image:        fmt.Sprintf("%s:%s", tn.Chain.Repository, tn.Chain.Version),
		ExposedPorts: tn.Chain.Ports,
		Cmd: []string{tn.Chain.Bin, "start",
			"--home", tn.NodeHome(),
		},
		BindMounts: tn.Bind(),
		WaitingFor: wait.ForLog("Starting RPC HTTP server"),
		Name:       tn.Name(),
		Hostname:   tn.Name(),
		AutoRemove: true,
	})
}

// InitNodeFilesAndGentx creates the node files and signs a genesis transaction
func (tn *TestNode) InitNodeFilesAndGentx(ctx context.Context) error {
	fmt.Println("init")
	if _, err := tn.InitHomeFolder(ctx); err != nil {
		return err
	}
	fmt.Println("create key")
	if _, err := tn.CreateKey(ctx, valKey); err != nil {
		return err
	}
	fmt.Println("get key")
	key, err := tn.GetKey(valKey)
	if err != nil {
		return err
	}
	fmt.Println("add genesis account")
	if _, err := tn.AddGenesisAccount(ctx, key.GetAddress().String()); err != nil {
		return err
	}
	fmt.Println("gentx")
	_, err = tn.Gentx(ctx, valKey)
	return err
}

// NodeID returns the node of a given node
func (tn *TestNode) NodeID() (string, error) {
	nodeKey, err := p2p.LoadNodeKey(path.Join(tn.Dir(), "config", "node_key.json"))
	if err != nil {
		return "", err
	}
	return string(nodeKey.ID()), nil
}

// GetKey gets a key, waiting until it is available
func (tn *TestNode) GetKey(name string) (info keyring.Info, err error) {
	return info, retry.Do(func() (err error) {
		info, err = tn.Keybase().Key(name)
		return err
	})
}

// RandLowerCaseLetterString returns a lowercase letter string of given length
func RandLowerCaseLetterString(length int) string {
	chars := []rune("abcdefghijklmnopqrstuvwxyz")
	var b strings.Builder
	for i := 0; i < length; i++ {
		i, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b.WriteRune(chars[i.Int64()])
	}
	return b.String()
}

// TestNodes is a collection of TestNode
type TestNodes []*TestNode

// PeerString returns the peer identifiers for a given set of nodes
// TODO: probably needs refactor
func (tn TestNodes) PeerString() (string, error) {
	out := []string{}
	for _, n := range tn {
		nid, err := n.NodeID()
		if err != nil {
			return "", err
		}
		out = append(out, fmt.Sprintf("%s@%s:%s", nid, n.Name(), "26656"))
	}
	return strings.Join(out, ","), nil
}

// Peers returns the peer nodes for a given node if it is included in a set of nodes
func (tn TestNodes) Peers(node *TestNode) (out TestNodes) {
	for _, n := range tn {
		if n.Index != node.Index {
			out = append(out, n)
		}
	}
	return
}
