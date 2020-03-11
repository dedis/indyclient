package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"

	"go.dedis.ch/indyclient"
)

var argLedger = flag.Int("ledger", int(indyclient.PoolLedger), "the ledger to download (default = 0, the pool ledger)")
var argLimit = flag.Int("limit", 10, "how many will be fetched")
var argAll = flag.Bool("all", false, "fetch all, no limit")

func main() {
	flag.Parse()

	pool, _ := indyclient.NewPool(indyclient.SovrinPool("BuilderNet"))

	fmt.Println("[")
	for i := 1; ; i++ {
		if !*argAll {
			if i > *argLimit {
				break
			}
		}

		fmt.Fprintln(os.Stderr, "getting", i)
		reply, err := pool.GetTransaction(indyclient.LedgerId(*argLedger), i)
		if err != nil {
			log.Fatal(err)
		}

		// This is what indy returns in reply.Result when there is no matching txn:
		// {"identifier":"Go1ndyC1ient1111111111","reqId":1,"type":"3","data":null}
		// TODO: Decode this correctly.
		if bytes.HasSuffix(reply.Result, []byte("\"data\":null}")) {
			fmt.Fprintln(os.Stderr, "last transaction found, done")
			break
		}

		if i != 1 {
			fmt.Println(",")
		}
		fmt.Println(string(reply.Result))
	}
	fmt.Println("]")
}
