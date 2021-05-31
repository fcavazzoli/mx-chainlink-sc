package gasStation

import (
	"github.com/ElrondNetwork/elrond-adapter/aggregator"
	"github.com/ElrondNetwork/elrond-adapter/config"
	"math"
	"math/big"
)

const (
	gasNowUrl   = "https://www.gasnow.org/api/v3/gas/price"
	ethTicker   = "ETH"
	baseGwei    = "GWEI"
	quote       = "USD"
	ethDecimals = 18
)

var wei = math.Pow(10, -ethDecimals)

type Response struct {
	Code uint16  `json:"code"`
	Data GasData `json:"data"`
}

type GasData struct {
	Fast     uint64 `json:"fast"`
	Standard uint64 `json:"standard"`
	Slow     uint64 `json:"slow"`
}

type GasPair struct {
	Base         string
	Quote        string
	Denomination string
	Address      string
	Endpoint     string
}

type EthGasDenominator struct {
	exchangeAggregator *aggregator.ExchangeAggregator
	gasConfig          config.GasConfig
}

func NewEthGasDenominator(
	exchangeAggregator *aggregator.ExchangeAggregator,
	gasConfig config.GasConfig,
) *EthGasDenominator {
	return &EthGasDenominator{
		exchangeAggregator: exchangeAggregator,
		gasConfig:          gasConfig,
	}
}

func (egd *EthGasDenominator) GasPriceDenominated() (GasPair, error) {
	target := egd.gasConfig.TargetAsset
	gasLimit := egd.gasConfig.GasLimit
	targetDecimals := egd.gasConfig.TargetAssetDecimals

	gasData, err := egd.gasPriceGwei()
	if err != nil {
		return GasPair{}, err
	}
	ethPrice, err := egd.exchangeAggregator.GetPrice(ethTicker, quote)
	if err != nil {
		return GasPair{}, err
	}
	targetPrice, err := egd.exchangeAggregator.GetPrice(target, quote)
	if err != nil {
		return GasPair{}, err
	}

	gweiFast := gasData.Fast * gasLimit
	gweiAsEth := float64(gweiFast) * wei
	nominalValue := ethPrice * gweiAsEth
	nominalAmount := nominalValue / targetPrice

	targetUnit := math.Pow(10, float64(targetDecimals))
	denominatedAmount := int64(nominalAmount * targetUnit)
	return GasPair{
		Base:         baseGwei,
		Quote:        target,
		Denomination: big.NewInt(denominatedAmount).String(),
		Address:      egd.gasConfig.Address,
		Endpoint:     egd.gasConfig.Endpoint,
	}, nil
}

func (egd *EthGasDenominator) gasPriceGwei() (GasData, error) {
	var gnr Response
	err := aggregator.HttpGet(gasNowUrl, &gnr)
	if err != nil {
		return GasData{}, err
	}
	return gnr.Data, nil
}
