package adapter

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/ElrondNetwork/elrond-adapter/aggregator"
	"github.com/ElrondNetwork/elrond-adapter/config"
	models "github.com/ElrondNetwork/elrond-adapter/data"
	"github.com/ElrondNetwork/elrond-adapter/gasStation"
	"github.com/ElrondNetwork/elrond-adapter/interaction"
	logger "github.com/ElrondNetwork/elrond-go-logger"
)

var log = logger.GetOrCreate("adapter")

type adapter struct {
	chainInteractor    *interaction.BlockchainInteractor
	exchangeAggregator *aggregator.ExchangeAggregator
	ethGasDenominator  *gasStation.EthGasDenominator
	config             config.GeneralConfig
}

func NewAdapter(config config.GeneralConfig) (*adapter, error) {
	interactor, err := interaction.NewBlockchainInteractor(config.Blockchain)
	if err != nil {
		log.Error("failed initialising blockchain interactor", "err", err.Error())
		return nil, err
	}
	exchangeAggregator := aggregator.NewExchangeAggregator(config.Exchange)
	ethGasDenominator := gasStation.NewEthGasDenominator(exchangeAggregator, config.GasStation)
	return &adapter{
		chainInteractor:    interactor,
		exchangeAggregator: exchangeAggregator,
		ethGasDenominator:  ethGasDenominator,
		config:             config,
	}, nil
}

func (a *adapter) HandlePriceFeed(data models.RequestData) (string, error) {
	price, err := a.exchangeAggregator.GetPrice(data.Value, data.Result)
	if err != nil {
		return "", err
	}
	return a.exchangeAggregator.MultiplyFloat64CastStr(price), nil
}

func (a *adapter) HandleBatchPriceFeeds() ([]string, error) {
	var txHashes []string

	pairs := a.exchangeAggregator.GetPricesForPairs()
	inputData, err := a.prepareInputDataForPairsBatches(pairs)
	if err != nil {
		log.Error("price job: failed to parse arg hex", "err", err.Error())
		return nil, err
	}

	for _, data := range inputData {
		tx, innerErr := a.chainInteractor.CreateSignedTx(
			[]byte(data),
			a.config.PriceFeedBatch.Address,
			a.config.GasConfig.BatchTxGasLimit,
		)
		if innerErr != nil {
			log.Error("price job: failed to sign transaction", "err", innerErr.Error())
			return nil, innerErr
		}

		txHash, innerErr := a.chainInteractor.SendTx(tx)
		if innerErr != nil {
			log.Error("price job: failed to send transaction", "err", innerErr.Error())
			return nil, innerErr
		}

		ok, innerErr := a.chainInteractor.WaitTxExecutionCheckStatus(txHash)
		if innerErr != nil {
			return nil, innerErr
		}
		if !ok {
			return nil, errors.New(fmt.Sprintf("price job: transaction failed. Hash: %s", txHash))
		}
		txHashes = append(txHashes, txHash)
	}

	return txHashes, nil
}

func (a *adapter) HandleWriteFeed(data models.RequestData) (string, error) {
	scEndpoint := data.Function
	scAddress := data.ScAddress
	if scEndpoint == "" {
		scEndpoint = a.config.Contract.Endpoint
	}
	if scAddress == "" {
		scAddress = a.config.Contract.Address
	}

	argsHex, err := prepareWriteRequestArgsHex(data.Value, data.RoundID)
	if err != nil {
		log.Error("write job: failed to prepare args hex", "err", err.Error())
		return "", err
	}
	inputData := scEndpoint + "@" + argsHex
	tx, err := a.chainInteractor.CreateSignedTx([]byte(inputData), scAddress, a.config.GasConfig.FeedTxGasLimit)
	if err != nil {
		log.Error("write job: failed to sign transaction", "err", err.Error())
		return "", err
	}

	txHash, err := a.chainInteractor.SendTx(tx)
	if err != nil {
		log.Error("write job: failed to send transaction", "err", err.Error())
		return "", err
	}

	return txHash, nil
}

func (a *adapter) HandleEthGasDenomination() ([]string, error) {
	var txHashes []string

	gasPairs := a.ethGasDenominator.GasPricesDenominated()
	inputData := a.config.GasStation.Endpoint
	for _, gasPair := range gasPairs {
		argsHex, err := prepareJobResultArgsHex(gasPair.Base, gasPair.Quote, gasPair.Denomination)
		if err != nil {
			log.Error("gas denomination: failed to parse arg hex", "err", err.Error())
			return nil, err
		}

		inputData += "@" + argsHex
	}

	tx, err := a.chainInteractor.CreateSignedTx(
		[]byte(inputData),
		a.config.GasStation.Address,
		a.config.GasConfig.BatchTxGasLimit,
	)
	if err != nil {
		log.Error("gas denomination: failed to sign transaction", "err", err.Error())
		return nil, err
	}

	txHash, err := a.chainInteractor.SendTx(tx)
	if err != nil {
		log.Error("gas denomination: failed to send transaction", "err", err.Error())
		return nil, err
	}

	txHashes = append(txHashes, txHash)
	return txHashes, nil
}

func (a *adapter) prepareInputDataForPairsBatches(pairs []aggregator.PairData) ([]string, error) {
	var inputData []string

	batchSize := a.config.PriceFeedBatch.BatchSize
	for i := 0; i < len(pairs); i += batchSize {
		batchEnd := i + batchSize
		if batchEnd > len(pairs) {
			batchEnd = len(pairs)
		}

		currentBatch := pairs[i:batchEnd]
		batchInputData := a.config.PriceFeedBatch.Endpoint
		for _, pair := range currentBatch {
			argsHex, err := prepareJobResultArgsHex(pair.Base, pair.Quote, pair.PriceMultiplied)
			if err != nil {
				return nil, err
			}
			batchInputData += "@" + argsHex
		}
		inputData = append(inputData, batchInputData)
	}

	return inputData, nil
}

func prepareJobResultArgsHex(base, quote, price string) (string, error) {
	parsedPrice, ok := big.NewInt(0).SetString(price, 10)
	if !ok {
		return "", errors.New("failure parsing price")
	}

	args := hex.EncodeToString([]byte(base)) +
		"@" + hex.EncodeToString([]byte(quote)) +
		"@" + hex.EncodeToString(parsedPrice.Bytes())

	return args, nil
}

func prepareWriteRequestArgsHex(value, roundID string) (string, error) {
	parsedValue, ok := big.NewInt(0).SetString(value, 10)
	if !ok {
		return "", errors.New("failure parsing request value")
	}
	parsedRoundID, ok := big.NewInt(0).SetString(roundID, 10)
	if !ok {
		return "", errors.New("failure parsing roundID")
	}

	args := hex.EncodeToString(parsedValue.Bytes()) +
		"@" + hex.EncodeToString(parsedRoundID.Bytes())

	return args, nil
}
