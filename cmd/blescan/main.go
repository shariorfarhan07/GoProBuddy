// Command blescan lists every Bluetooth LE device the PC can see, to debug discovery.
package main

import (
	"fmt"
	"time"

	"tinygo.org/x/bluetooth"
)

func main() {
	a := bluetooth.DefaultAdapter
	if err := a.Enable(); err != nil {
		fmt.Println("enable:", err)
		return
	}
	seen := map[string]bool{}
	time.AfterFunc(15*time.Second, func() { a.StopScan() })
	err := a.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
		key := r.Address.String() + r.LocalName()
		if seen[key] {
			return
		}
		seen[key] = true
		var mfr []string
		for _, m := range r.ManufacturerData() {
			mfr = append(mfr, fmt.Sprintf("0x%04X:% x", m.CompanyID, m.Data))
		}
		fmt.Printf("%s rssi=%d name=%q uuids=%v mfr=%v\n", r.Address.String(), r.RSSI, r.LocalName(), r.ServiceUUIDs(), mfr)
	})
	fmt.Println("scan done:", err, "devices:", len(seen))
}
