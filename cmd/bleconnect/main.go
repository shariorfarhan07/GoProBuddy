// Command bleconnect runs the GoPro Bluetooth connect steps one by one and prints each result.
// An optional argument picks the camera whose name or address contains it.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"goprobuddy/internal/gopro"
)

func main() {
	var cam *gopro.FoundCamera
	fmt.Println("scanning 15s…")
	err := gopro.Scan(15*time.Second, func(c gopro.FoundCamera) {
		fmt.Printf("found %s %s rssi=%d\n", c.Name, c.Address.String(), c.RSSI)
		if cam == nil && (len(os.Args) < 2 || strings.Contains(c.Name+" "+c.Address.String(), os.Args[1])) {
			cc := c
			cam = &cc
		}
	})
	if err != nil || cam == nil {
		fmt.Println("scan:", err, "found:", cam != nil)
		return
	}
	fmt.Println("connecting…")
	b, err := gopro.Connect(*cam)
	if err != nil {
		fmt.Println("CONNECT ERROR:", err)
		return
	}
	defer b.Close()
	fmt.Println("connected; enabling Wi-Fi…")
	fmt.Println("enable wifi:", b.EnableWiFi())
	ssid, pass, err := b.WiFiCredentials()
	fmt.Printf("ssid=%q pass=%q err=%v\n", ssid, pass, err)
}
