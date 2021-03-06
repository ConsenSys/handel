package monitor

import (
	"bytes"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestMonitorReadyNormal(t *testing.T) {
	m := make(map[string]string)
	m["servers"] = "1"
	stat := NewStats(m, nil)
	fresh := stat.String()
	// First set up monitor listening
	mon := NewDefaultMonitor(stat)
	defer mon.Stop()
	go mon.Listen()
	time.Sleep(100 * time.Millisecond)

	// Then measure
	err := ConnectSink("localhost:" + strconv.Itoa(DefaultSinkPort))
	if err != nil {
		t.Fatal(fmt.Sprintf("Error starting monitor: %s", err))
		return
	}

	meas := newSingleMeasure("round", 10)
	meas.Record()
	time.Sleep(200 * time.Millisecond)
	newSingleMeasure("round", 20)
	EndAndCleanup()
	time.Sleep(100 * time.Millisecond)
	updated := mon.stats.String()
	if updated == fresh {
		t.Fatal("Stats not updated ?")
	}
}

func TestStatsKeyOrder(t *testing.T) {
	m := make(map[string]string)
	m["servers"] = "1"
	m["hosts"] = "1"
	m["bf"] = "2"
	// create stats
	stat := NewStats(m, nil)
	m1 := newSingleMeasure("round", 10)
	m2 := newSingleMeasure("setup", 5)
	stat.Update(m1)
	stat.Update(m2)
	str := new(bytes.Buffer)
	stat.WriteHeader(str)
	stat.WriteValues(str)

	stat2 := NewStats(m, nil)
	stat2.Update(m1)
	stat2.Update(m2)

	str2 := new(bytes.Buffer)
	stat2.WriteHeader(str2)
	stat2.WriteValues(str2)
	if !bytes.Equal(str.Bytes(), str2.Bytes()) {
		t.Fatal("KeyOrder / output not the same for same stats")
	}
}

// setupMonitor launches a basic monitor with a created Stats object
// When finished with the monitor, just call `End()`
func setupMonitor(t *testing.T) (*Monitor, *Stats) {
	m := make(map[string]string)
	m["servers"] = "1"
	stat := NewStats(m, nil)
	// First set up monitor listening
	mon := NewDefaultMonitor(stat)
	go mon.Listen()
	time.Sleep(100 * time.Millisecond)

	// Then measure
	err := ConnectSink("localhost:" + strconv.Itoa(int(mon.sinkPort)))
	if err != nil {
		t.Fatal(fmt.Sprintf("Error starting monitor: %s", err))
	}
	return mon, stat
}
