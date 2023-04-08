package main

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/naoina/toml"
	"github.com/pioplat/pioplat-core/eth/ethconfig"
	"github.com/pioplat/pioplat-core/log"
	"github.com/pioplat/pioplat-core/metrics"
	"github.com/pioplat/pioplat-core/node"
	"os"
	"reflect"
	"unicode"
)

// These settings ensure that TOML keys use the same names as Go struct fields.
var tomlSettings = toml.Config{
	NormFieldName: func(rt reflect.Type, key string) string {
		return key
	},
	FieldToKey: func(rt reflect.Type, field string) string {
		return field
	},
	MissingField: func(rt reflect.Type, field string) error {
		var link string
		if unicode.IsUpper(rune(rt.Name()[0])) && rt.PkgPath() != "main" {
			link = fmt.Sprintf(", see https://godoc.org/%s#%s for available fields", rt.PkgPath(), rt.Name())
		}
		return fmt.Errorf("field '%s' is not defined in %s%s", field, rt.String(), link)
	},
}

type pioplatConfig struct {
	Node    node.Config
	Eth     ethconfig.Config
	Metrics metrics.Config
}

func loadConfig(file string, cfg *pioplatConfig) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()

	err = tomlSettings.NewDecoder(bufio.NewReader(f)).Decode(cfg)
	// Add file name to errors that have a line number.
	if _, ok := err.(*toml.LineError); ok {
		err = errors.New(file + ", " + err.Error())
	}
	return err
}

func makeConfigNode(file string) (*node.Node, pioplatConfig) {
	cfg := pioplatConfig{
		Node:    node.DefaultConfig,
		Metrics: metrics.Config{},
	}

	if err := loadConfig(file, &cfg); err != nil {
		fmt.Println("load config from file failed", "reason", err)
		log.Crit("load config from file failed", "reason", err)
	}
	stack, err := node.New(&cfg.Node)
	if err != nil {
		fmt.Println("create node server failed", "reason", err)
		log.Crit("create node server failed", "reason", err)
	}

	return stack, cfg
}
