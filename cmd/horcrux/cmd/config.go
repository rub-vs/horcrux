package cmd

import (
	"fmt"
	"github.com/mitchellh/go-homedir"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/jackzampolin/horcrux/internal/signer"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

func init() {
	// TODO: config nodes add/remove
	// TODO: config peers add/remove
	// TODO: config chain-id set
	configCmd.AddCommand(initCmd())
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Commands to configure the horcrux signer",
}

func addPeerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "init [chain-id] [chain-nodes]",
		Aliases: []string{"i"},
		Short:   "initalize configuration file and home directory if one doesn't already exist",
		Long: "initalize configuration file, use flags for cosigner configuration.\n\n" +
			"[chain-id] is the chain id of the chain to validate\n" +
			"[chain-nodes] is a comma seperated array of chain node addresses i.e.\n" +
			"tcp://chain-node-1:1234,tcp://chain-node-2:1234",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			return nil
		},
	}
	return cmd
}

func initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "init [chain-id] [chain-nodes]",
		Aliases: []string{"i"},
		Short:   "initalize configuration file and home directory if one doesn't already exist",
		Long: "initalize configuration file, use flags for cosigner configuration.\n\n" +
			"[chain-id] is the chain id of the chain to validate\n" +
			"[chain-nodes] is a comma seperated array of chain node addresses i.e.\n" +
			"tcp://chain-node-1:1234,tcp://chain-node-2:1234",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			cid := args[0]
			var cn []ChainNode
			if len(args) == 2 {
				cn, err = chainNodesFromArg(args[1])
				if err != nil {
					return err
				}
			}

			var homeDir string
			if cfgFile != "" {
				homeDir = cfgFile
			} else {
				home, _ := homedir.Dir()
				homeDir = path.Join(home, ".horcrux")
			}

			//TODO double check that omitting directory check does not introduce unexpected behavior
			//if _, err := os.Stat(homeDir); !os.IsNotExist(err) {
			//	return fmt.Errorf("%s is not empty, check for existing configuration and clear path before trying again", homeDir)
			//}
			var cfg *Config
			cs, _ := cmd.Flags().GetBool("cosigner")
			if cs {
				p, _ := cmd.Flags().GetString("peers")
				threshold, _ := cmd.Flags().GetInt("threshold")
				peers, err := peersFromFlag(p)
				if err != nil {
					return err
				}
				cfg = &Config{
					HomeDir: homeDir,
					ChainID: cid,
					CosignerConfig: &CosignerConfig{
						Threshold: threshold,
						P2PListen: "tcp://0.0.0.0:2222",
						Peers:     peers,
					},
					ChainNodes: cn,
				}
				if err = validateCosignerConfig(cfg); err != nil {
					return err
				}
			} else {
				if len(cn) == 0 {
					return fmt.Errorf("must input at least one node")
				}
				cfg = &Config{
					HomeDir:    homeDir,
					ChainID:    cid,
					ChainNodes: cn,
				}
				if err = validateSingleSignerConfig(cfg); err != nil {
					return err
				}
			}
			// create all directories up to the state directory
			if err = os.MkdirAll(path.Join(homeDir, "state"), 0755); err != nil {
				return err
			}
			// create the config file
			if err = writeConfigFile(path.Join(homeDir, "config.yaml"), cfg); err != nil {
				return err
			}

			// initialize state/{chainid}_priv_validator_state.json file
			if _, err = signer.LoadOrCreateSignState(path.Join(homeDir, "state", fmt.Sprintf("%s_priv_validator_state.json", cid))); err != nil {
				return err
			}

			// if node is a cosigner initialize state/{chainid}_priv_validator_state.json file
			if cs {
				if _, err = signer.LoadOrCreateSignState(path.Join(homeDir, "state", fmt.Sprintf("%s_share_sign_state.json", cid))); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolP("cosigner", "c", false, "set to initialize a cosigner node, requires --peers and --threshold")
	cmd.Flags().StringP("peers", "p", "", "cosigner peer addresses in format tcp://{addr}:{port}|{share-id} (i.e. \"tcp://node-1:2222|2,tcp://node-2:2222|3\")")
	cmd.Flags().IntP("threshold", "t", 0, "indicate number of signatures required for threshold signature")
	return cmd
}

func writeConfigFile(path string, cfg *Config) error {
	return ioutil.WriteFile(path, cfg.MustMarshalYaml(), 0644)
}

func validateSingleSignerConfig(cfg *Config) error {
	if cfg.ChainID == "" {
		return fmt.Errorf("chain-id cannot be empty")
	}
	if len(cfg.ChainNodes) == 0 {
		return fmt.Errorf("need to have a node configured to sign for")
	}
	if err := validateChainNodes(cfg.ChainNodes); err != nil {
		return err
	}
	return nil
}

func validateCosignerConfig(cfg *Config) error {
	if cfg.ChainID == "" {
		return fmt.Errorf("chain-id cannot be empty")
	}
	if cfg.CosignerConfig == nil {
		return fmt.Errorf("cosigner config can't be empty")
	}
	if len(cfg.CosignerConfig.Peers)+1 < cfg.CosignerConfig.Threshold {
		return fmt.Errorf("number of peers + 1 (%d) must be greater than threshold (%d)", len(cfg.CosignerConfig.Peers)+1, cfg.CosignerConfig.Threshold)
	}
	if cfg.CosignerConfig.Threshold < 2 {
		return fmt.Errorf("threshold must be 2 or greater")
	}
	if _, err := url.Parse(cfg.CosignerConfig.P2PListen); err != nil {
		return fmt.Errorf("failed to parse p2p listen address")
	}
	if err := validateCosignerPeers(cfg.CosignerConfig.Peers); err != nil {
		return err
	}
	if err := validateChainNodes(cfg.ChainNodes); err != nil {
		return err
	}
	return nil
}

type Config struct {
	HomeDir        string          `json:"home-dir" yaml:"home-dir"`
	ChainID        string          `json:"chain-id" yaml:"chain-id"`
	CosignerConfig *CosignerConfig `json:"cosigner,omitempty" yaml:"cosigner,omitempty"`
	ChainNodes     []ChainNode     `json:"chain-nodes,omitempty" yaml:"chain-nodes,omitempty"`
}

func (c *Config) Nodes() (out []signer.NodeConfig) {
	for _, n := range c.ChainNodes {
		out = append(out, signer.NodeConfig{Address: n.PrivValAddr})
	}
	return
}

func (c *Config) MustMarshalYaml() []byte {
	out, err := yaml.Marshal(c)
	if err != nil {
		panic(err)
	}
	return out
}

type CosignerConfig struct {
	Threshold int            `json:"threshold" yaml:"threshold"`
	P2PListen string         `json:"p2p-listen" yaml:"p2p-listen"`
	Peers     []CosignerPeer `json:"peers" yaml:"peers"`
}

func (c *Config) CosignerPeers() (out []signer.CosignerConfig) {
	for _, p := range c.CosignerConfig.Peers {
		out = append(out, signer.CosignerConfig{ID: p.ShareID, Address: p.P2PAddr})
	}
	return
}

type CosignerPeer struct {
	ShareID int    `json:"share-id" yaml:"share-id"`
	P2PAddr string `json:"p2p-addr" yaml:"p2p-addr"`
}

func validateCosignerPeers(peers []CosignerPeer) error {
	// TODO: check IDs to ensure none are duplicated and
	// that the key share which is configured for the local node
	// is the last remaining share
	return nil
}

func peersFromFlag(peers string) (out []CosignerPeer, err error) {
	for _, p := range strings.Split(peers, ",") {
		ps := strings.Split(p, "|")
		if len(ps) != 2 {
			fmt.Println(ps)
			return nil, fmt.Errorf("invalid peer string %s", p)
		}
		shareid, err := strconv.ParseInt(ps[1], 10, 64)
		if err != nil {
			return nil, err
		}
		_, err = url.Parse(ps[0])
		if err != nil {
			return nil, err
		}
		out = append(out, CosignerPeer{ShareID: int(shareid), P2PAddr: ps[0]})
	}
	return
}

type ChainNode struct {
	PrivValAddr string `json:"priv-val-addr" yaml:"priv-val-addr"`
}

func chainNodesFromArg(arg string) ([]ChainNode, error) {
	cn := parseChainNodes(arg)
	return cn, validateChainNodes(cn)
}

func parseChainNodes(nodes string) (out []ChainNode) {
	for _, n := range strings.Split(nodes, ",") {
		out = append(out, ChainNode{PrivValAddr: n})
	}
	return
}

func validateChainNodes(nodes []ChainNode) (err error) {
	for _, n := range nodes {
		_, err = url.Parse(n.PrivValAddr)
	}
	return
}
