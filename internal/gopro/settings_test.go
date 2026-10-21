package gopro

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const gpControlDef = `{
 "modes":[
  {"value":0,"display_name":"Video","settings":[
   {"id":2,"display_name":"Resolution","options":[{"display_name":"4K","value":1},{"display_name":"1080","value":9}]},
   {"id":3,"display_name":"Frames Per Second","options":[{"display_name":"60","value":5},{"display_name":"30","value":8}]}]},
  {"value":1,"display_name":"Photo","settings":[
   {"id":2,"display_name":"Resolution","options":[{"display_name":"dup","value":1}]}]}
 ],
 "settings":[{"display_name":"Setup","settings":[
   {"id":59,"display_name":"Auto Power Down","options":[{"display_name":"Never","value":0},{"display_name":"5 Min","value":4}]}]}]
}`

func TestSettings(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gp/gpControl", gpControlDef)
	got, _, err := c.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d settings, want 3 (duplicates dropped, hints ignored): %+v", len(got), got)
	}
	if got[0].ID != 2 || got[0].Group != "Video" || len(got[0].Options) != 2 || got[0].Options[1].Name != "1080" {
		t.Errorf("first setting = %+v", got[0])
	}
	if got[2].ID != 59 || got[2].Group != "Setup" {
		t.Errorf("setup setting = %+v", got[2])
	}
}

func TestSettingValues(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gopro/camera/state", `{"status":{"70":80},"settings":{"2":1,"3":8,"x":4}}`)
	vals, err := c.SettingValues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if vals[2] != 1 || vals[3] != 8 || len(vals) != 2 {
		t.Errorf("vals = %v", vals)
	}
}

func TestSetSettingFallsBack(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gp/gpControl/setting/2/9", `{}`)
	if err := c.SetSetting(context.Background(), 2, 9); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSetting(context.Background(), 2, 99); err == nil {
		t.Error("expected an error for an option the camera rejects")
	}
}

func TestWebcamUnavailableOverWiFi(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.routes["/gopro/webcam/start"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"status":0,"error":7}`))
	}
	err := c.RunWebcam(context.Background(), 12, 0, nil, nil)
	var werr *WebcamError
	if !errors.As(err, &werr) || werr.Code != 7 || werr.USB {
		t.Fatalf("err = %v, want WebcamError 7 over Wi-Fi", err)
	}
	if !strings.Contains(err.Error(), "USB-C") {
		t.Errorf("message should point at USB-C: %v", err)
	}
	for _, h := range fc.hits {
		if h == "/gopro/webcam/start?res=12&fov=0" {
			t.Errorf("retried with the legacy request after a definite refusal: %s", h)
		}
	}
}

func TestIsUSBAddress(t *testing.T) {
	for ip, want := range map[string]bool{"172.24.106.51": true, "172.29.199.51": true, "10.5.5.9": false, "172.16.100.1": false, "nope": false} {
		if IsUSBAddress(ip) != want {
			t.Errorf("IsUSBAddress(%q) = %v", ip, !want)
		}
	}
}

// HERO9 firmware: settings at the top level, options numbered by "id", grouped by display_hints.
const gpControlHERO9 = `{
 "modes":[{"id":12,"display_name":"Video"}],
 "settings":[
  {"display_name":"Resolution","id":2,"options":[{"id":24,"display_name":"5K"},{"id":1,"display_name":"4K"}]},
  {"display_name":"Internal","id":999,"options":[{"id":0,"display_name":"x"}]},
  {"display_name":"GPS","id":83,"options":[{"id":0,"display_name":"Off"},{"id":1,"display_name":"On"}]}],
 "filters":[{"activated_by":[{"status_id":89,"values":[17,18]}],"blacklist":[{"setting_id":2,"opt_ids":[24,1]},{"setting_id":83,"opt_ids":[1]}]}],
 "display_hints":[
  {"key":"GPCAMERA_GROUP_MODE","display_name":"Mode Settings","settings":[{"setting_id":2}]},
  {"key":"GPCAMERA_GROUP_SETUP","display_name":"Setup","settings":[{"setting_id":83},{"setting_id":12345}]}]
}`

func TestSettingsHERO9(t *testing.T) {
	fc, c := newFakeCamera(t)
	fc.handle("/gp/gpControl", gpControlHERO9)
	got, filters, err := c.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(filters) != 1 {
		t.Fatalf("filters = %+v", filters)
	}
	// In mode 17 (status 89) resolution is fully blocked and GPS "On" is blocked.
	b := Blocked(filters, map[int]int{89: 17}, map[int]int{})
	if !b[2][24] || !b[2][1] || !b[83][1] || b[83][0] {
		t.Errorf("blocked in mode 17 = %v", b)
	}
	if b := Blocked(filters, map[int]int{89: 12}, nil); len(b) != 0 {
		t.Errorf("blocked in mode 12 = %v", b)
	}
	if len(got) != 2 {
		t.Fatalf("got %d settings, want 2 (internal one hidden): %+v", len(got), got)
	}
	if got[0].ID != 2 || got[0].Group != "Mode Settings" || got[0].Options[0] != (SettingOption{Value: 24, Name: "5K"}) {
		t.Errorf("resolution = %+v", got[0])
	}
	if got[1].ID != 83 || got[1].Group != "Setup" {
		t.Errorf("gps = %+v", got[1])
	}
}
