//go:build linux

package term

func termiosGetRequest() uintptr { return 0x5401 }
