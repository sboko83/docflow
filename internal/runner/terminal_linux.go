//go:build linux

package runner

func termiosGetRequest() uintptr { return 0x5401 }
func termiosSetRequest() uintptr { return 0x5402 }
