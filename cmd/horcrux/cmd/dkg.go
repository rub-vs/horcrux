package cmd

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/cobra"
	"github.com/strangelove-ventures/horcrux/signer"
)

const flagID = "id"

func dkgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dkg",
		Short: "Commands for DKG trustless key generation",
	}

	cmd.AddCommand(dkgInitCmd())
	cmd.AddCommand(dkgRunCmd())

	return cmd
}

func dkgInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Args:  cobra.NoArgs,
		Short: `Command to initialize peer private key for libp2p communication during DKG`,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			flags := cmd.Flags()

			id, _ := flags.GetUint8(flagID)

			// This ed25519 key is used for libp2p communication during DKG.
			// i.e. this key is not used for consensus.
			privKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
			if err != nil {
				return err
			}

			rawKey, err := privKey.Raw()
			if err != nil {
				return err
			}

			filename := filepath.Join(config.HomeDir, "libp2p.key")

			if err := os.WriteFile(filename, rawKey, 0600); err != nil {
				return err
			}

			p2pID, err := peer.IDFromPrivateKey(privKey)
			if err != nil {
				return err
			}

			for i, cosigner := range config.Config.ThresholdModeConfig.Cosigners {
				if cosigner.ShardID == id {
					config.Config.ThresholdModeConfig.Cosigners[i].DKGID = p2pID.String()
				} else if cosigner.DKGID == "" {
					config.Config.ThresholdModeConfig.Cosigners[i].DKGID = "REPLACE ME"
				}
			}

			if err := config.WriteConfigFile(); err != nil {
				return err
			}

			fmt.Fprintf(
				cmd.OutOrStdout(),
				"libp2p key generated for DKG. `dkgID` to share with other members:\n%s\n",
				p2pID.String(),
			)

			return nil
		},
	}

	f := cmd.Flags()
	f.Uint8(flagID, 0, "cosigner shard ID as participant in DKG ceremony")
	_ = cmd.MarkFlagRequired(flagID)

	return cmd
}

// dkgCmd is a cobra command for performing
// a DKG key ceremony as a participating cosigner.
func dkgRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Args:  cobra.NoArgs,
		Short: `Perform DKG key sharding ceremony (no trusted "dealer")`,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			flags := cmd.Flags()

			id, _ := flags.GetUint8(flagID)
			chainID, _ := flags.GetString(flagChainID)
			threshold := config.Config.ThresholdModeConfig.Threshold
			shards := uint8(len(config.Config.ThresholdModeConfig.Cosigners))

			var errs []error

			if id == 0 {
				errs = append(errs, fmt.Errorf("id must not be zero"))
			}

			if id > shards {
				errs = append(errs, fmt.Errorf("id must not be greater than total shards"))
			}

			if chainID == "" {
				errs = append(errs, fmt.Errorf("chain-id flag must not be empty"))
			}

			if threshold == 0 {
				errs = append(errs, fmt.Errorf("threshold flag must be > 0, <= --shards, and > --shards/2"))
			}

			if shards == 0 {
				errs = append(errs, fmt.Errorf("shards flag must be greater than zero"))
			}

			if threshold > shards {
				errs = append(errs, fmt.Errorf(
					"threshold cannot be greater than total shards, got [threshold](%d) > [shards](%d)",
					threshold, shards,
				))
			}

			if threshold <= shards/2 {
				errs = append(errs, fmt.Errorf("threshold must be greater than total shards "+
					"divided by 2, got [threshold](%d) <= [shards](%d) / 2", threshold, shards))
			}

			if len(errs) > 0 {
				return errors.Join(errs...)
			}

			out, _ := cmd.Flags().GetString(flagOutputDir)
			if out != "" {
				if err := os.MkdirAll(out, 0700); err != nil {
					return err
				}
			}

			libp2pKeyFile := filepath.Join(config.HomeDir, "libp2p.key")
			p2pKeyBz, err := os.ReadFile(libp2pKeyFile)
			if err != nil {
				return fmt.Errorf("failed to read libp2p.key. Did you run horcrux dkg init first?: %w", err)
			}

			p2pKey, err := crypto.UnmarshalEd25519PrivateKey(p2pKeyBz)
			if err != nil {
				return err
			}

			// silence usage after all input has been validated
			cmd.SilenceUsage = true

			shard, err := signer.NetworkDKG(cmd.Context(), config.Config.ThresholdModeConfig.Cosigners, id, p2pKey, threshold)
			if err != nil {
				return err
			}

			shardBz, err := json.Marshal(shard)
			if err != nil {
				return err
			}

			filename := filepath.Join(out, fmt.Sprintf("%s_shard.json", chainID))

			if err := os.WriteFile(filename, shardBz, 0600); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Created Ed25519 Shard %s\n", filename)

			return nil
		},
	}

	addOutputDirFlag(cmd)

	f := cmd.Flags()
	f.Uint8(flagID, 0, "cosigner shard ID as participant in DKG ceremony")
	_ = cmd.MarkFlagRequired(flagID)
	f.String(flagChainID, "", "key shards will sign for this chain ID")
	_ = cmd.MarkFlagRequired(flagChainID)

	return cmd
}
