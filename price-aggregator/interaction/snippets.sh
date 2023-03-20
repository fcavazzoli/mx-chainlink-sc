#!/bin/bash

NETWORK=${1}
PEM=${2}

SC_ADDRESS=$(mxpy data load --key=address-${MARKET}-${NETWORK})
DEPLOY_TRANSACTION=$(mxpy data load --key=deployTransaction-${MARKET}-${NETWORK})

PROJECT="../../price-aggregator"

case ${NETWORK} in
testnet)
    PROXY="https://testnet-api.elrond.com"
    CHAIN_ID="T"
    ;;

devnet)
    PROXY="https://devnet-gateway.elrond.com"
    CHAIN_ID="D"
    ;;

mainnet)
    PROXY="https://gateway.elrond.com"
    CHAIN_ID="1"
    ;;

*)
    echo -n "error: unknown network"
    echo ""
    exit 1
    ;;
esac

# could it be estimated? https://docs.elrond.com/developers/gas-and-fees/user-defined-smart-contracts/#contract-calls
GAS_LIMIT=500000000

deploy() {

    local staking_token=str:${1//\"/}
    local staking_amount=${2}
    local slash_amount=${3}
    local slash_quorum=${4}
    local submission_count=${5}
    local decimals=${6}
    local oracles=${7}

    mxpy --verbose contract deploy --project=${PROJECT} --recall-nonce \
        --pem=${PEM} \
        --gas-limit=${GAS_LIMIT} \
        --outfile="price-aggregator-${NETWORK}.json" \
        --proxy=${PROXY} \
        --chain=${CHAIN_ID} \
        --arguments ${staking_token} ${staking_amount} ${slash_amount} ${slash_quorum} ${submission_count} ${decimals} ${oracles} \
        --wait-result \
        --send || return

    SC_ADDRESS=$(mxpy data parse --file=price-aggregator-${NETWORK}.json --expression="data['contractAddress']")
    DEPLOY_TRANSACTION=$(mxpy data parse --file=price-aggregator-${NETWORK}.json --expression="data['emittedTransactionHash']")

    mxpy data store --key=address-${NETWORK} --value=${SC_ADDRESS}
    mxpy data store --key=deployTransaction-${NETWORK} --value=${DEPLOY_TRANSACTION}

    echo ""
    echo "Smart contract address: ${SC_ADDRESS}"
}