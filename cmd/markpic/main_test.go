package main

import (
	"os"
	"testing"
)

func TestApplyEnvFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		dev  string
		dir  string
	}{
		{"无参数不动环境", nil, "", ""},
		{"-dev 用默认地址", []string{"-dev"}, defaultDevURL, ""},
		{"-dev 带地址", []string{"-dev", "http://127.0.0.1:5180"}, "http://127.0.0.1:5180", ""},
		{"--dev=地址", []string{"--dev=http://127.0.0.1:5181"}, "http://127.0.0.1:5181", ""},
		{"-data-dir 带值", []string{"-data-dir", ".tmp-data"}, "", ".tmp-data"},
		{"--data-dir=值", []string{"--data-dir=.tmp-data2"}, "", ".tmp-data2"},
		{"两个一起给", []string{"-dev", "-data-dir", ".tmp-data3"}, defaultDevURL, ".tmp-data3"},
		{"--restart 不受影响", []string{"--restart"}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MARKPIC_DEV_URL", "")
			t.Setenv("MARKPIC_DATA_DIR", "")
			applyEnvFlags(c.args)
			if got := os.Getenv("MARKPIC_DEV_URL"); got != c.dev {
				t.Fatalf("MARKPIC_DEV_URL = %q, want %q", got, c.dev)
			}
			if got := os.Getenv("MARKPIC_DATA_DIR"); got != c.dir {
				t.Fatalf("MARKPIC_DATA_DIR = %q, want %q", got, c.dir)
			}
		})
	}
}
