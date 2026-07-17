package config

import (
	"strings"
	"testing"
)

func FuzzEnvironmentValueParsing(f *testing.F) {
	f.Add("1780")
	f.Add("true")
	f.Add("https://game.example.com,http://localhost:1780")
	f.Add("")

	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 4096 || strings.IndexByte(value, 0) >= 0 {
			t.Skip()
		}

		t.Setenv("FUZZ_CONFIG_VALUE", value)
		integer := 0
		_ = getEnvInt("FUZZ_CONFIG_VALUE", &integer)
		boolean := false
		_ = getEnvBool("FUZZ_CONFIG_VALUE", &boolean)
		var values []string
		_ = getEnvStrSlice("FUZZ_CONFIG_VALUE", &values, true)
		text := ""
		_ = getEnvStr("FUZZ_CONFIG_VALUE", &text, true)
	})
}
