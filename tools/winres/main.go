// Command winres turns an .ico file into rsrc_windows_<arch>.syso files, which the Go
// linker builds into the Windows .exe. The icon gets resource ID 1, which is what
// Explorer shows for the .exe and what Gio loads for the window and taskbar icon.
//
//	go run ./tools/winres assets/logo.ico
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"os"
)

type icoImage struct {
	w, h, colors uint8
	planes, bpp  uint16
	data         []byte
}

func readICO(path string) ([]icoImage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 6 || binary.LittleEndian.Uint16(b[2:]) != 1 {
		return nil, fmt.Errorf("%s is not an .ico file", path)
	}
	n := int(binary.LittleEndian.Uint16(b[4:]))
	var out []icoImage
	for i := 0; i < n; i++ {
		e := b[6+16*i:]
		size := binary.LittleEndian.Uint32(e[8:])
		off := binary.LittleEndian.Uint32(e[12:])
		if int(off+size) > len(b) {
			return nil, fmt.Errorf("image %d runs past the end of the file", i)
		}
		out = append(out, icoImage{
			w: e[0], h: e[1], colors: e[2],
			planes: binary.LittleEndian.Uint16(e[4:]), bpp: binary.LittleEndian.Uint16(e[6:]),
			data: b[off : off+size],
		})
	}
	return out, nil
}

const (
	rtIcon      = 3
	rtGroupIcon = 14
	langEnUS    = 0x409
)

// buildRsrc lays out the .rsrc section: directory tree, data entries, then data.
// It returns the section bytes and the offsets of the RVA fields that need relocating.
func buildRsrc(imgs []icoImage) ([]byte, []uint32) {
	// Group icon: GRPICONDIR followed by one 14-byte entry per image (IDs 1..n).
	var grp bytes.Buffer
	binary.Write(&grp, binary.LittleEndian, []uint16{0, 1, uint16(len(imgs))})
	for i, im := range imgs {
		grp.Write([]byte{im.w, im.h, im.colors, 0})
		binary.Write(&grp, binary.LittleEndian, im.planes)
		binary.Write(&grp, binary.LittleEndian, im.bpp)
		binary.Write(&grp, binary.LittleEndian, uint32(len(im.data)))
		binary.Write(&grp, binary.LittleEndian, uint16(i+1))
	}
	blobs := make([][]byte, 0, len(imgs)+1)
	for _, im := range imgs {
		blobs = append(blobs, im.data)
	}
	blobs = append(blobs, grp.Bytes()) // last blob is the group

	n := len(imgs)
	dirSize := func(entries int) int { return 16 + 8*entries }
	// Directory layout: root(2) | icon type dir(n) | group type dir(1) | n icon lang dirs(1) | group lang dir(1)
	rootOff := 0
	iconTypeOff := rootOff + dirSize(2)
	grpTypeOff := iconTypeOff + dirSize(n)
	iconLangOff := grpTypeOff + dirSize(1)
	grpLangOff := iconLangOff + n*dirSize(1)
	dataEntOff := grpLangOff + dirSize(1)
	blobOff := dataEntOff + 16*(n+1)

	var sec bytes.Buffer
	le := func(v any) { binary.Write(&sec, binary.LittleEndian, v) }
	dir := func(ids int) { le([]uint32{0, 0}); le([]uint16{0, 0, 0, uint16(ids)}) }
	sub := func(id uint32, off int) { le(id); le(uint32(off) | 0x80000000) }

	dir(2)
	sub(rtIcon, iconTypeOff)
	sub(rtGroupIcon, grpTypeOff)
	dir(n)
	for i := 0; i < n; i++ {
		sub(uint32(i+1), iconLangOff+i*dirSize(1))
	}
	dir(1)
	sub(1, grpLangOff) // group icon ID 1
	for i := 0; i < n; i++ {
		dir(1)
		le(uint32(langEnUS))
		le(uint32(dataEntOff + 16*i))
	}
	dir(1)
	le(uint32(langEnUS))
	le(uint32(dataEntOff + 16*n))

	var relocs []uint32
	off := blobOff
	for _, b := range blobs {
		off = (off + 7) &^ 7
		relocs = append(relocs, uint32(sec.Len()))
		le([]uint32{uint32(off), uint32(len(b)), 0, 0})
		off += len(b)
	}
	for _, b := range blobs {
		for sec.Len()%8 != 0 {
			sec.WriteByte(0)
		}
		sec.Write(b)
	}
	return sec.Bytes(), relocs
}

// coff wraps the section in a COFF object with one section symbol and the relocations.
func coff(machine, relocType uint16, sec []byte, relocs []uint32) []byte {
	const hdr, shdr = 20, 40
	rawOff := hdr + shdr
	relOff := rawOff + len(sec)
	symOff := relOff + 10*len(relocs)

	var b bytes.Buffer
	le := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	le(machine)
	le(uint16(1)) // sections
	le(uint32(0)) // timestamp
	le(uint32(symOff))
	le(uint32(1)) // symbols
	le(uint16(0)) // optional header
	le(uint16(0)) // characteristics

	b.WriteString(".rsrc\x00\x00\x00")
	le([]uint32{0, 0, uint32(len(sec)), uint32(rawOff), uint32(relOff), 0})
	le([]uint16{uint16(len(relocs)), 0})
	le(uint32(0x40000040)) // initialized data, readable

	b.Write(sec)
	for _, r := range relocs {
		le(r)
		le(uint32(0)) // symbol 0: the section
		le(relocType)
	}
	b.WriteString(".rsrc\x00\x00\x00")
	le(uint32(0))  // value
	le(uint16(1))  // section number
	le(uint16(0))  // type
	b.WriteByte(3) // static
	b.WriteByte(0) // aux symbols
	le(uint32(4))  // empty string table
	return b.Bytes()
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: winres <icon.ico>")
	}
	imgs, err := readICO(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	sec, relocs := buildRsrc(imgs)
	for _, t := range []struct {
		arch          string
		machine, kind uint16
	}{
		{"amd64", 0x8664, 3}, // IMAGE_REL_AMD64_ADDR32NB
		{"arm64", 0xAA64, 2}, // IMAGE_REL_ARM64_ADDR32NB
		{"386", 0x014C, 7},   // IMAGE_REL_I386_DIR32NB
	} {
		name := "rsrc_windows_" + t.arch + ".syso"
		if err := os.WriteFile(name, coff(t.machine, t.kind, sec, relocs), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %s (%d icon sizes)\n", name, len(imgs))
	}
}
