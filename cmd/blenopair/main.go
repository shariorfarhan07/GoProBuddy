// Command blenopair connects without pairing and lists GATT services, for debugging.
package main

import (
	"fmt"
	"time"

	"tinygo.org/x/bluetooth"
)

func main() {
	a := bluetooth.DefaultAdapter
	must(a.Enable())
	var addr *bluetooth.Address
	time.AfterFunc(15*time.Second, func() { a.StopScan() })
	a.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		for _, m := range r.ManufacturerData() {
			if m.CompanyID == 0x02F2 && addr == nil {
				ad := r.Address
				addr = &ad
				fmt.Printf("found %s rssi=%d mfr=% x\n", ad.String(), r.RSSI, m.Data)
				go a.StopScan()
			}
		}
	})
	if addr == nil {
		fmt.Println("not found")
		return
	}
	for try := 1; try <= 3; try++ {
		dev, err := a.Connect(*addr, bluetooth.ConnectionParams{ConnectionTimeout: bluetooth.NewDuration(15 * time.Second)})
		if err != nil {
			fmt.Println("connect:", err)
			continue
		}
		svcs, err := dev.DiscoverServices(nil)
		fmt.Printf("try %d: services=%d err=%v\n", try, len(svcs), err)
		for _, s := range svcs {
			chars, cerr := s.DiscoverCharacteristics(nil)
			fmt.Printf("  svc %s chars=%d err=%v\n", s.UUID().String(), len(chars), cerr)
			for _, c := range chars {
				buf := make([]byte, 64)
				n, rerr := c.Read(buf)
				fmt.Printf("    char %s read=%q err=%v\n", c.UUID().String(), buf[:n], rerr)
			}
		}
		dev.Disconnect()
		if err == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
