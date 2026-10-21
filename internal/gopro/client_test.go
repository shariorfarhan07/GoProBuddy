package gopro

import "testing"

func TestParseMediaList(t *testing.T) {
	body := []byte(`{"id":"1","media":[{"d":"100GOPRO","fs":[
		{"n":"GX010001.MP4","cre":"1696000000","mod":"1696000000","s":"123456789"},
		{"n":"G0010003.JPG","cre":"1696000100","mod":"1696000100","b":"3","l":"5","g":"1"}
	]}]}`)
	items, err := parseMediaList(body)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GX010001.MP4", "G0010003.JPG", "G0010004.JPG", "G0010005.JPG"}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(items), len(want), items)
	}
	for i, w := range want {
		if items[i].Name != w {
			t.Errorf("item %d = %s, want %s", i, items[i].Name, w)
		}
	}
	if items[0].Size != 123456789 || !items[0].IsVideo() || items[1].IsVideo() {
		t.Errorf("bad fields: %+v", items[0])
	}
	c := NewClient("")
	if got := c.FileURL(items[0]); got != "http://10.5.5.9:8080/videos/DCIM/100GOPRO/GX010001.MP4" {
		t.Errorf("FileURL = %s", got)
	}
}
