// Package main holds the logic of a single Handel node for the simulation
package main

import (
	"flag"
	"fmt"
	"time"

	h "github.com/ConsenSys/handel"
	"github.com/ConsenSys/handel/simul/lib"
)

var beaconBytes = []byte{0x01, 0x02, 0x03}

// BeaconTimeout represents how much time do we wait to receive the beacon
const BeaconTimeout = 2 * time.Minute

var configFile = flag.String("config", "", "config file created for the exp.")
var registryFile = flag.String("registry", "", "registry file based - array registry")
var id = flag.Int("id", -1, "peer id")
var run = flag.Int("run", -1, "which RunConfig should we run")
var master = flag.String("master", "", "master address to synchronize")
var syncAddr = flag.String("sync", "", "address to listen for master START")

// XXX maybe try with a database-backed registry if loading file in memory is
// too much when overloading

func main() {
	flag.Parse()
	//
	// SETUP PHASE
	//
	// 1. load all needed structures
	config := lib.LoadConfig(*configFile)
	runConf := config.Runs[*run]

	cons := config.NewConstructor()
	parser := lib.NewCSVParser()
	registry, node, err := lib.ReadAll(*registryFile, *id, parser, cons)
	network := config.NewNetwork(node.Identity)

	// 2. make the signature
	signature, err := node.Sign(lib.Message, nil)
	if err != nil {
		panic(err)
	}
	// 3. Setup report handel
	handel := h.NewHandel(network, registry, node.Identity, cons.Handel(), lib.Message, signature)
	reporter := h.NewReportHandel(handel)

	// 4. Sync with master - wait for the START signal
	syncer := lib.NewSyncSlave(*syncAddr, *master, *id)
	select {
	case <-syncer.WaitMaster():
		now := time.Now()
		formatted := fmt.Sprintf("%02d:%02d:%02d:%03d", now.Hour(),
			now.Minute(),
			now.Second(),
			now.Nanosecond())

		fmt.Printf("\n%s [+] %s synced - starting\n", formatted, node.Identity.Address())
	case <-time.After(BeaconTimeout):
		panic("Haven't received beacon in time!")
	}

	// 5. Start handel and run a timeout on the whole thing
	go reporter.Start()
	out := make(chan bool, 1)
	go func() {
		<-time.After(config.GetMaxTimeout())
		out <- true
	}()

	// 6. Wait for final signatures !
	enough := false
	for !enough {
		select {
		case sig := <-reporter.FinalSignatures():
			if sig.BitSet.Cardinality() >= runConf.Threshold {
				enough = true
				break
			}
		case <-out:
			panic("max timeout")
		}
	}
	fmt.Println("finished -> sending state to sync master")
	// 7. Sync with master - wait to close our node
	syncer.Reset()
	select {
	case <-syncer.WaitMaster():
		now := time.Now()
		formatted := fmt.Sprintf("%02d:%02d:%02d:%03d", now.Hour(),
			now.Minute(),
			now.Second(),
			now.Nanosecond())

		fmt.Printf("\n%s [+] %s synced - closing shop\n", formatted, node.Identity.Address())
	case <-time.After(BeaconTimeout):
		panic("Haven't received beacon in time!")
	}
}
