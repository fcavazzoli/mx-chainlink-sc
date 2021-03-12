#![no_std]
#![feature(assoc_char_funcs)]

elrond_wasm::imports!();
elrond_wasm::derive_imports!();

extern crate aggregator;
use crate::aggregator::aggregator_interface::{
    AggregatorInterface, AggregatorInterfaceProxy, Round,
};

#[macro_use]
extern crate alloc;

pub fn format_biguint<BigUint: BigUintApi>(number: &BigUint) -> Vec<u8> {
    let mut nr = number.clone();
    let radix = BigUint::from(10u32);
    let mut result = Vec::new();

    loop {
        let last_digit = nr.clone() % radix.clone();
        nr = nr / radix.clone();

        let digit = *last_digit.to_bytes_be().get(0).unwrap_or(&0) as u8;
        result.push('0' as u8 + digit);
        if nr == 0 {
            break;
        }
    }
    result.into_iter().rev().collect()
}

pub fn format_fixed_precision<BigUint: BigUintApi>(number: &BigUint, decimals: usize) -> Vec<u8> {
    let formatted_number = format_biguint(number);
    let padding_length = (decimals + 1)
        .checked_sub(formatted_number.len())
        .unwrap_or_default();
    let padding: Vec<u8> = vec!['0' as u8; padding_length];
    let padded_number = BoxedBytes::from_concat(&[&padding, &formatted_number]);
    let digits_before_dot = padded_number.len() - decimals;

    let left = padded_number.as_slice().iter().take(digits_before_dot);
    let dot = core::iter::once(&('.' as u8));
    let right = padded_number.as_slice().iter().skip(digits_before_dot);
    left.chain(dot).chain(right).cloned().collect()
}

#[elrond_wasm_derive::contract(EgldEsdtExchangeImpl)]
pub trait EgldEsdtExchange {
    #[init]
    fn init(&self, aggregator: Address) {
        self.aggregator().set(&aggregator);
    }

    #[payable("*")]
    #[endpoint]
    fn deposit(
        &self,
        #[payment] payment: BigUint,
        #[payment_token] payment_token: TokenIdentifier,
    ) -> SCResult<()> {
        only_owner!(self, "Only the owner can deposit tokens");
        self.increase_balance(&payment_token, &payment);
        Ok(())
    }

    #[payable("*")]
    #[endpoint]
    fn exchange(
        &self,
        #[payment] payment: BigUint,
        #[payment_token] source_token: TokenIdentifier,
        target_token: TokenIdentifier,
    ) -> SCResult<AsyncCall<BigUint>> {
        require!(payment > 0, "Payment must be more than 0");
        require!(
            self.balance().contains_key(&source_token),
            "ESDT provided as payment not supported by the exchange"
        );
        require!(
            self.balance().contains_key(&target_token),
            "Target ESDT token not supported by the exchange"
        );
        self.increase_balance(&source_token, &payment);

        Ok(
            contract_call!(self, self.aggregator().get(), AggregatorInterfaceProxy)
                .latest_round_data()
                .async_call()
                .with_callback(self.callbacks().finalize_exchange(
                    self.get_caller(),
                    payment,
                    source_token,
                    target_token,
                )),
        )
    }

    fn check_aggregator_tokens(
        &self,
        description: BoxedBytes,
        source_token: &TokenIdentifier,
        target_token: &TokenIdentifier,
    ) -> Result<bool, BoxedBytes> {
        let delimiter_position = description
            .as_slice()
            .iter()
            .position(|item| *item == '/' as u8)
            .ok_or(BoxedBytes::from(
                "Invalid aggregator description format (expected 2 tokens)".as_bytes(),
            ))?;
        let (first, second) = description.split(delimiter_position);
        let first_token = &TokenIdentifier::from(first);
        let second_token = &TokenIdentifier::from(second);
        if first_token == source_token && second_token == target_token {
            return Result::Ok(false);
        }
        if first_token == target_token && second_token == source_token {
            return Result::Ok(true);
        }
        Result::Err(
            "Exchange between chosen token types not supported."
                .as_bytes()
                .into(),
        )
    }

    fn convert(
        &self,
        amount: &BigUint,
        source_token: &TokenIdentifier,
        target_token: &TokenIdentifier,
        multiplier: &BigUint,
        divisor: &BigUint,
        precision_factor: &BigUint,
        decimals: usize,
    ) -> Result<(BigUint, BoxedBytes), BoxedBytes> {
        if divisor == &BigUint::zero() {
            return Result::Err("Convert - dividing by 0".as_bytes().into());
        }
        let converted_amount = amount * multiplier / divisor.clone();
        let rate = multiplier * precision_factor / divisor.clone();
        let message = self.conversion_message(
            amount,
            source_token,
            &rate,
            decimals,
            &converted_amount,
            target_token,
        )?;
        Result::Ok((converted_amount, message))
    }

    fn get_converted_sum(
        &self,
        payment: &BigUint,
        source_token: &TokenIdentifier,
        target_token: &TokenIdentifier,
        exchange_rate: &BigUint,
        decimals: usize,
        reverse_exchange: bool,
    ) -> Result<(BigUint, BoxedBytes), BoxedBytes> {
        let precision_factor = BigUint::from(10u64.pow(decimals as u32));
        if !reverse_exchange {
            self.convert(
                payment,
                source_token,
                target_token,
                exchange_rate,
                &precision_factor,
                &precision_factor,
                decimals,
            )
        } else {
            self.convert(
                payment,
                source_token,
                target_token,
                &precision_factor,
                exchange_rate,
                &precision_factor,
                decimals,
            )
        }
    }

    fn try_convert(
        &self,
        result: AsyncCallResult<Round<BigUint>>,
        payment: &BigUint,
        source_token: &TokenIdentifier,
        target_token: &TokenIdentifier,
    ) -> Result<(BigUint, BoxedBytes), BoxedBytes> {
        match result {
            AsyncCallResult::Ok(round) => {
                let reverse_exchange =
                    self.check_aggregator_tokens(round.description, source_token, target_token)?;
                let (converted_amount, conversion_message) = self.get_converted_sum(
                    payment,
                    source_token,
                    target_token,
                    &round.answer,
                    round.decimals as usize,
                    reverse_exchange,
                )?;
                match self.checked_decrease_balance(target_token, &converted_amount) {
                    Result::Err(error) => Result::Err(BoxedBytes::from_concat(&[
                        error.as_slice(),
                        b" (",
                        conversion_message.as_slice(),
                        b")",
                    ])),
                    Result::Ok(()) => Result::Ok((converted_amount, conversion_message)),
                }
            }
            AsyncCallResult::Err(error) => {
                self.checked_decrease_balance(source_token, &payment)?;
                Result::Err(BoxedBytes::from_concat(&[
                    b"Error when getting the price feed from the aggregator: ",
                    error.err_msg.as_ref(),
                ]))
            }
        }
    }

    #[callback]
    fn finalize_exchange(
        &self,
        #[call_result] result: AsyncCallResult<Round<BigUint>>,
        caller: Address,
        payment: BigUint,
        source_token: TokenIdentifier,
        target_token: TokenIdentifier,
    ) {
        match self.try_convert(result, &payment, &source_token, &target_token) {
            Result::Ok((converted_payment, conversion_message)) => {
                let message = BoxedBytes::from_concat(&[
                    b"exchange succesful ",
                    b"(",
                    conversion_message.as_slice(),
                    b")",
                ]);
                self.send().direct(
                    &caller,
                    &target_token,
                    &converted_payment,
                    message.as_slice(),
                );
            }
            Result::Err(error) => {
                let message = BoxedBytes::from_concat(&[b"refund (", error.as_slice(), b")"]);
                self.send()
                    .direct(&caller, &source_token, &payment, message.as_slice());
            }
        }
    }

    fn increase_balance(&self, token_identifier: &TokenIdentifier, amount: &BigUint) {
        let mut balance = self
            .balance()
            .get(&token_identifier)
            .unwrap_or_else(|| 0u32.into());
        balance += amount;
        self.balance().insert(token_identifier.clone(), balance);
    }

    fn checked_decrease_balance(
        &self,
        token_identifier: &TokenIdentifier,
        amount: &BigUint,
    ) -> Result<(), BoxedBytes> {
        match self.balance().get(&token_identifier) {
            Some(balance) => {
                if &balance < amount {
                    Result::Err(BoxedBytes::from_concat(&[
                        b"Insufficient balance: only ",
                        &format_biguint(&balance),
                        b" of ",
                        token_identifier.as_name(),
                        b" available",
                    ]))
                } else {
                    self.decrease_balance(token_identifier, amount);
                    Result::Ok(())
                }
            }
            None => Result::Err(BoxedBytes::from_concat(&[
                b"No ",
                token_identifier.as_name(),
                b" tokens are available",
            ])),
        }
    }

    fn decrease_balance(&self, token_identifier: &TokenIdentifier, amount: &BigUint) {
        let mut balance = self
            .balance()
            .get(&token_identifier)
            .unwrap_or_else(|| 0u32.into());
        balance -= amount;
        self.balance().insert(token_identifier.clone(), balance);
    }

    fn conversion_message(
        &self,
        payment: &BigUint,
        source_token: &TokenIdentifier,
        rate: &BigUint,
        rate_precision: usize,
        converted_token: &BigUint,
        target_token: &TokenIdentifier,
    ) -> Result<BoxedBytes, BoxedBytes> {
        Result::Ok(BoxedBytes::from_concat(&[
            b"conversion from ",
            &format_biguint(payment),
            b" of ",
            source_token.as_name(),
            b", using exchange rate ",
            &format_fixed_precision(rate, rate_precision),
            b", results in ",
            &format_biguint(converted_token),
            b" of ",
            target_token.as_name(),
        ]))
    }

    #[storage_mapper("aggregator")]
    fn aggregator(&self) -> SingleValueMapper<Self::Storage, Address>;

    #[storage_mapper("balance")]
    fn balance(&self) -> MapMapper<Self::Storage, TokenIdentifier, BigUint>;
}
