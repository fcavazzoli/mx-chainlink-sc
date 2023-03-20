package aggregator

import (
	"strconv"
	"strings"
	"sync"

	"github.com/ElrondNetwork/elrond-adapter/config"
	"github.com/ElrondNetwork/elrond-adapter/data"
	logger "github.com/ElrondNetwork/elrond-go-logger"
)

// QuoteFiat and QuoteStable are constants that define the fiat and stable quote currencies used by the exchange aggregator.
const (
	QuoteFiat   = "USD"
	QuoteStable = "USDT"
)

// minValidResults is a constant that represents the minimum number of valid results required to compute a median price.
const (
	minValidResults = 3
)

// Exchange is an interface that defines two methods: FetchPrice and Name.
// FetchPrice takes two string arguments, base and quote, and returns a float64 value and an error.
// Name returns a string representing the name of the exchange.
type Exchange interface {
	FetchPrice(base, quote string) (float64, error)
	Name() string
}

// log is a logger instance from the elrond-go-logger package, used to log events in the aggregator.
var log = logger.GetOrCreate("aggregator")

// ExchangeAggregator is a struct that represents an aggregator for the prices of multiple cryptocurrency exchanges.
// It contains the following fields:
// exchanges: a slice of Exchange interface instances, representing the supported exchanges whose prices will be aggregated.
// prices: a map with string keys (currency symbols) and float64 values, representing the latest price for each currency pair.
// This map is initialized to 0 for each currency pair in the exchangeConfig passed to NewExchangeAggregator.
// config: an instance of config.ExchangeConfig, containing configuration parameters for the aggregator.
type ExchangeAggregator struct {
	exchanges []Exchange
	prices    map[string]float64
	config    config.ExchangeConfig
}

// supportedExchanges is a slice of Exchange interface pointers representing the exchanges currently supported by the aggregator.
var supportedExchanges = []Exchange{
	&Binance{},
	&Bitfinex{},
	&Cryptocom{},
	&Kraken{},
	&Gemini{},
	&Huobi{},
	&Hitbtc{},
	&Okex{},
}

// NewExchangeAggregator function creates and returns a new instance of ExchangeAggregator.
// It takes an argument exchangeConfig of type config.ExchangeConfig which contains the configuration for the aggregator.
// The function initializes the prices map for each base currency specified in the exchangeConfig.Pairs list.
// It returns a pointer to the created ExchangeAggregator instance.
func NewExchangeAggregator(exchangeConfig config.ExchangeConfig) *ExchangeAggregator {
	prices := make(map[string]float64)
	for _, pair := range exchangeConfig.Pairs {
		prices[pair.Base] = 0
	}
	return &ExchangeAggregator{
		exchanges: supportedExchanges,
		config:    exchangeConfig,
		prices:    prices,
	}
}

// GetPricesForPairs function returns a list of data.FeedPair.
// It takes no arguments but uses the exchange pairs specified in eh.config.Pairs.
// The function gets the price for each exchange pair and aggregates it into a data.FeedPair struct.
// If the CheckPercentageChange flag in eh.config is true, it checks if the percentage change is greater
// than or equal to the PercentageThreshold before appending the result to the results list.
// The function returns the results list containing all the aggregated prices.
func (eh *ExchangeAggregator) GetPricesForPairs() []data.FeedPair {
	var results []data.FeedPair
	for _, pair := range eh.config.Pairs {
		currPrice, err := eh.GetPrice(pair.Base, pair.Quote)
		if err != nil {
			log.Error("failed to aggregate price for pair",
				"base", pair.Base,
				"quote", pair.Quote,
				"err", err.Error(),
			)
			break
		}

		lastPrice := eh.prices[pair.Base]
		pairData := data.FeedPair{
			Base:  pair.Base,
			Quote: pair.Quote,
			Value: eh.MultiplyFloat64CastStr(currPrice),
		}

		log.Info("aggregated price for pair",
			"base", pair.Base,
			"quote", pair.Quote,
			"price raw", currPrice,
			"price multiplied", pairData.Value,
		)

		if lastPrice == 0 {
			results = append(results, pairData)
			eh.prices[pair.Base] = currPrice
			continue
		}

		if !eh.config.CheckPercentageChange {
			results = append(results, pairData)
		} else {
			percentageChange := PercentageChange(currPrice, lastPrice)
			if percentageChange >= eh.config.PercentageThreshold {
				results = append(results, pairData)
				eh.prices[pair.Base] = currPrice
			}
		}
	}
	return results
}

// GetPrice function fetches the median price of a base/quote currency pair from different exchanges.
// It takes two arguments base and quote of type string.
// The function fetches the prices from all the supported exchanges for the given base and quote currency pair, computes the median value and returns it.
// If the number of valid prices is less than minValidResults, an error of type NotEnoughDataToComputeErr is returned.
// The function returns the median price and an error if any.
func (eh *ExchangeAggregator) GetPrice(base, quote string) (float64, error) {
	var wg sync.WaitGroup
	var mut sync.Mutex
	var prices []float64

	baseUpper := strings.ToUpper(base)
	quoteUpper := strings.ToUpper(quote)
	for _, exchange := range eh.exchanges {
		wg.Add(1)
		go func(exchange Exchange) {
			defer wg.Done()
			price, err := exchange.FetchPrice(baseUpper, quoteUpper)
			mut.Lock()
			defer mut.Unlock()
			if err != nil {
				log.Debug("failed to fetch price",
					"exchange", exchange.Name(),
					"base", baseUpper,
					"quote", quoteUpper,
					"err", err.Error(),
				)
				return
			}
			prices = append(prices, price)
		}(exchange)
	}
	wg.Wait()

	if !(len(prices) >= minValidResults) {
		log.Error("failed to reach min valid results threshold",
			"err", NotEnoughDataToComputeErr.Error(),
		)
		return -1, NotEnoughDataToComputeErr
	}

	medianPrice := ComputeMedian(prices)
	return TruncateFloat64(medianPrice), nil
}

// MultiplyFloat64CastStr function multiplies the given float value with the eh.config.MultiplicationPrecision and returns the result as a string.
// It takes a single argument val of type float64.
// The function multiplies the given val with the eh.config.MultiplicationPrecision, casts the result as an uint64, and then returns it as a string.
func (eh *ExchangeAggregator) MultiplyFloat64CastStr(val float64) string {
	multiplied := uint64(val * float64(eh.config.MultiplicationPrecision))
	return strconv.FormatUint(multiplied, 10)
}
