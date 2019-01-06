package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gcli "github.com/urfave/cli"
	"github.com/watercompany/coinjoin/pkg/client"
	"github.com/watercompany/coinjoin/pkg/coinjoin"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/util/droplet"
)

type coinjoinOutJSON struct {
	Addr  string `json:"addr"`
	Coins string `json:"coins"`
	Hours uint64 `json:"hours"`
}

func sendCoinJoinTxCmd() gcli.Command {
	name := "sendCoinJoinTx"
	return gcli.Command{
		Name:      name,
		Usage:     "Sends a coinjoin tranction to the coinjoin server",
		ArgsUsage: "[to address] [coin amount] [hours amount]",
		Description: `
		Note: the [amount] argument is the coins you will spend, 1 coins = 1e6 droplets.`,
		Flags: []gcli.Flag{
			gcli.StringFlag{
				Name:  "f",
				Usage: "[wallet file or path] From wallet. If no path is specified your default wallet path will be used.",
			},
			gcli.StringFlag{
				Name:  "address, a",
				Usage: "sender address",
			},
			gcli.StringFlag{
				Name:  "unspents, u",
				Usage: "comma separated list of unspents to use",
			},
			gcli.StringFlag{
				Name: "m",
				Usage: `[send to many] use JSON string to set multiple receive addresses, coins and hours,
				example: -m '[{"addr":"$addr1", "coins": "10.2", "hours": "1"}, {"addr":"$addr2", "coins": "20", "hours": "2"}]'`,
			},
			gcli.StringFlag{
				Name:   "nodeURL, n",
				Usage:  "coinjoin node url",
				EnvVar: "COINJOIN_API",
				Value:  "http://localhost:8081",
			},
		},
		OnUsageError: onCommandUsageError(name),
		Action: func(c *gcli.Context) error {
			nodeURL := c.String("nodeURL")
			if nodeURL == "" {
				return errors.New("missing node url")
			}

			coinjoinClient := client.NewCoinJoinClient(nodeURL)

			coinjoinTxn, err := createCoinjoinTxnCmdHandler(c)
			if err != nil {
				return err
			}

			res, err := coinjoinClient.AcceptTX(coinjoinTxn)
			if err != nil {
				return err
			}

			fmt.Printf("txid:%s\n", res.TransactionID)

			return nil
		},
	}
}

func getOuts(c *gcli.Context) ([]coinjoin.Out, error) {
	csv := c.String("csv")
	m := c.String("m")

	if csv != "" && m != "" {
		return nil, errors.New("-csv and -m cannot be combined")
	}

	if m != "" {
		return parseSendAmountsCoinjoinFromJSON(m)
	} else if csv != "" {
		fields, err := openCSV(csv)
		if err != nil {
			return nil, err
		}
		return parseSendAmountsCoinjoinFromCSV(fields)
	}

	if c.NArg() < 2 {
		return nil, errors.New("invalid argument")
	}

	toAddr := c.Args().First()

	if _, err := cipher.DecodeBase58Address(toAddr); err != nil {
		return nil, err
	}

	amt, hours, err := getAmountCoinjoin(c)
	if err != nil {
		return nil, err
	}

	return []coinjoin.Out{{
		Address: toAddr,
		Coins:   amt,
		Hours:   hours,
	}}, nil
}

func parseSendAmountsCoinjoinFromJSON(m string) ([]coinjoin.Out, error) {
	sas := []coinjoinOutJSON{}

	if err := json.NewDecoder(strings.NewReader(m)).Decode(&sas); err != nil {
		return nil, fmt.Errorf("invalid -m flag string, err: %v", err)
	}

	sendAmts := make([]coinjoin.Out, 0, len(sas))

	for _, sa := range sas {
		amt, err := droplet.FromString(sa.Coins)
		if err != nil {
			return nil, fmt.Errorf("invalid coins value in -m flag string: %v", err)
		}

		sendAmts = append(sendAmts, coinjoin.Out{
			Address: sa.Addr,
			Coins:   amt,
			Hours:   sa.Hours,
		})
	}

	return sendAmts, nil
}

func parseSendAmountsCoinjoinFromCSV(fields [][]string) ([]coinjoin.Out, error) {
	var sends []coinjoin.Out
	var errs []error
	for i, f := range fields {
		addr := f[0]

		addr = strings.TrimSpace(addr)

		if _, err := cipher.DecodeBase58Address(addr); err != nil {
			err = fmt.Errorf("[row %d] Invalid address %s: %v", i, addr, err)
			errs = append(errs, err)
			continue
		}

		coins, err := droplet.FromString(f[1])
		if err != nil {
			err = fmt.Errorf("[row %d] Invalid amount %s: %v", i, f[1], err)
			errs = append(errs, err)
			continue
		}

		hours, err := strconv.ParseUint(f[2], 64, 10)
		if err != nil {
			err := fmt.Errorf("[row %d] Invalid hours %s: %v", i, f[2], err)
			errs = append(errs, err)
		}

		sends = append(sends, coinjoin.Out{
			Address: addr,
			Coins:   coins,
			Hours:   hours,
		})
	}

	if len(errs) > 0 {
		errMsgs := make([]string, len(errs))
		for i, err := range errs {
			errMsgs[i] = err.Error()
		}

		errMsg := strings.Join(errMsgs, "\n")

		return nil, errors.New(errMsg)
	}

	return sends, nil
}

func getAmountCoinjoin(c *gcli.Context) (uint64, uint64, error) {
	if c.NArg() < 3 {
		return 0, 0, errors.New("not enough args")
	}

	amt, err := droplet.FromString(c.Args().Get(1))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid amount: %v", err)
	}

	hours, err := strconv.ParseUint(c.Args().Get(2), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hours: %v", err)
	}

	return amt, hours, nil
}

func createCoinjoinTxnCmdHandler(c *gcli.Context) (*coinjoin.Transaction, error) {
	fromAddress := c.String("address")
	if fromAddress == "" {
		return nil, errors.New("missing sender address")
	}

	if _, err := cipher.DecodeBase58Address(fromAddress); err != nil {
		return nil, fmt.Errorf("address %s is invalid: %v", fromAddress, err)
	}

	unspents := c.String("unspents")
	uxOuts := strings.Split(unspents, ",")
	outs, err := getOuts(c)
	if err != nil {
		return nil, fmt.Errorf("failed to get coinjoin outputs: %v", err)
	}


	return &coinjoin.Transaction{
		FromAddress: fromAddress,
		UxOuts:      uxOuts,
		Outs:        outs,
	}, nil
}

func parseSendCoinjoinTxArgs(c *gcli.Context) (*createRawTxArgs, error) {
	wltAddr, err := fromWalletOrAddress(c)
	if err != nil {
		return nil, err
	}

	chgAddr, err := getChangeAddress(wltAddr, c.String("c"))
	if err != nil {
		return nil, err
	}

	toAddrs, err := getToAddresses(c)
	if err != nil {
		return nil, err
	}

	if err := validateSendAmounts(toAddrs); err != nil {
		return nil, err
	}

	pr := NewPasswordReader([]byte(c.String("p")))

	return &createRawTxArgs{
		WalletID:      wltAddr.Wallet,
		Address:       wltAddr.Address,
		ChangeAddress: chgAddr,
		SendAmounts:   toAddrs,
		Password:      pr,
	}, nil
}

func getCoinJoinTxStatusCmd() gcli.Command {
	name := "getCoinJoinTxStatus"
	return gcli.Command{
		Name: name,
		Usage: "Get status of a coinjoin tx",
	}
}