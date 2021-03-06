package prifimobile

// Configuration files read & write and related structs

import (
	"bytes"
	"github.com/BurntSushi/toml"
	prifi_protocol "github.com/dedis/prifi/sda/protocols"
	"go.dedis.ch/onet/v3/app"
	"go.dedis.ch/onet/v3/log"
	"golang.org/x/mobile/asset"
	"sync"
)

const prifiConfigFilename = "prifi.toml"
const cothorityConfigFilename = "identity.toml"
const cothorityGroupConfigFilename = "group.toml"

var prifiConfigSingleton *prifi_protocol.PrifiTomlConfig
var cothorityConfigSingleton *app.CothorityConfig
var groupConfigSingleton *app.Group

var onceClient, onceCothority, onceGroup sync.Once
var globalErr error

func getPrifiConfig() (*prifi_protocol.PrifiTomlConfig, error) {
	onceClient.Do(func() {
		prifiConfigSingleton, globalErr = initPrifiConfig()
	})
	return prifiConfigSingleton, globalErr
}

func getCothorityConfig() (*app.CothorityConfig, error) {
	onceCothority.Do(func() {
		cothorityConfigSingleton, globalErr = initCothorityConfig()
	})
	return cothorityConfigSingleton, globalErr
}

func getGroupConfig() (*app.Group, error) {
	onceGroup.Do(func() {
		groupConfigSingleton, globalErr = initCothorityGroupConfig()
	})
	return groupConfigSingleton, globalErr
}

// TODO: Reduce Code Duplication of both inits
func initPrifiConfig() (*prifi_protocol.PrifiTomlConfig, error) {
	tomlRawDataString, err := readTomlFromAssets(prifiConfigFilename)

	if err != nil {
		return nil, err
	}

	config := &prifi_protocol.PrifiTomlConfig{}
	_, err = toml.Decode(tomlRawDataString, config)
	if err != nil {
		log.Error("Could not parse toml file ", prifiConfigFilename)
		return nil, err
	}

	return config, nil
}

func initCothorityConfig() (*app.CothorityConfig, error) {
	tomlRawDataString, err := readTomlFromAssets(cothorityConfigFilename)

	if err != nil {
		return nil, err
	}

	config := &app.CothorityConfig{}
	_, err = toml.Decode(tomlRawDataString, config)
	if err != nil {
		log.Error("Could not parse toml file ", cothorityConfigFilename)
		return nil, err
	}

	return config, nil
}

// TODO: less code duplication?
func initCothorityGroupConfig() (*app.Group, error) {
	file, err := asset.Open(cothorityGroupConfigFilename)
	defer file.Close()

	group, err := app.ReadGroupDescToml(file)

	if err != nil {
		log.Error("Could not parse toml file ", cothorityGroupConfigFilename)
		return nil, err
	}

	if group == nil || group.Roster == nil || len(group.Roster.List) == 0 {
		log.Error("No servers found in roster from ", cothorityGroupConfigFilename)
		return nil, err
	}

	return group, nil
}

// TODO: Read from any given paths
func readTomlFromAssets(filename string) (string, error) {
	file, err := asset.Open(filename)
	defer file.Close()

	if err != nil {
		log.Error("Could not open file ", filename)
		return "", err
	}

	tomlRawDataBuffer := new(bytes.Buffer)
	_, err = tomlRawDataBuffer.ReadFrom(file)

	if err != nil {
		log.Error("Could not read file ", prifiConfigFilename)
		return "", err
	}

	return tomlRawDataBuffer.String(), err
}
