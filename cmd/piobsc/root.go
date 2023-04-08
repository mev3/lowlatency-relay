package main

import (
	"fmt"
	"github.com/mitchellh/go-homedir"
	"github.com/pioplat/pioplat-core/eth"
	"github.com/pioplat/pioplat-core/log"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"io"
	"os"
	"path/filepath"
)

var rootCmd = &cobra.Command{
	Use:   "piobsc",
	Short: "piobsc is pioplat p2p accelerating tool for binance smart chain",
	Long: `A Fast and light client pretend to be fullnode which listen new block and new transaction in network.
Pioplat use peri strategy to select p2p neighbors and adaptive algorithm to approach miners/validators & nodes who 
broadcast transaction frequently`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		actualFlagsCnt := 0
		countActualFlags := func(t *pflag.Flag) {
			actualFlagsCnt += 1
		}
		cmd.Flags().Visit(countActualFlags)
		if actualFlagsCnt == 0 {
			cmd.Help()
			os.Exit(0)
		}

		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		var err error

		initConfig()

		nodeServer, config := makeConfigNode(cfgFile)

		eth.New(nodeServer, &config.Eth)
		err = nodeServer.Start()

		if err != nil {
			log.Crit("node server start failed", "reason", err)
		}

		ch := make(chan int)
		<-ch
	},
}

var (
	cfgFile string
	glogger *log.GlogHandler
)

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ${HOME}/.piobsc.toml)")

	//log.Root().SetHandler(log.LvlFilterHandler(log.LvlInfo, log.StreamHandler(os.Stderr, log.TerminalFormat(true))))

	glogger = log.NewGlogHandler(log.StreamHandler(os.Stderr, log.TerminalFormat(true)))
	ostream := log.StreamHandler(io.Writer(os.Stderr), log.TerminalFormat(true))
	glogger.SetHandler(ostream)
	glogger.Verbosity(log.LvlInfo)
	log.PrintOrigins(true)
	log.Root().SetHandler(glogger)

	fmt.Println("log.Root().GetHandler()", log.Root().GetHandler())
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := homedir.Dir()
		if err != nil {
			fmt.Println("open home dir failed", "reason", err)
			os.Exit(1)
		}
		viper.SetConfigFile(filepath.Join(home, ".piobsc.toml"))
	}

	if err := viper.ReadInConfig(); err != nil {
		fmt.Println("read config failed", "reason", err)
		os.Exit(1)
	}

	log.Info("read config from toml file", "file", viper.ConfigFileUsed())
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
