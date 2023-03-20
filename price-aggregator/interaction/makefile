# change default shell from /bin/sh to /bin/bash
SHELL=/bin/bash

# by default
NETWORK ?= devnet

PEM ?= ../../elrond-adapter/config/wallets/$(NETWORK)/user2.pem

ADMIN ?= $(shell mxpy wallet pem-address $(PEM))

build:
	cd .. && mxpy contract build

deploy:
	source ./snippets.sh $(NETWORK) $(PEM) && deploy HTM-123456 1 1 1 1 1 $(ADMIN)