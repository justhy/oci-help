package updater

import (
	"fmt"
	"testing"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest  string
		current string
		want    bool
	}{
		{"v1.0.2", "v1.0.1", true},
		{"v1.0.1", "v1.0.1", false},
		{"v1.0.9", "v1.0.10", false}, // 字符串字典序会误判为更新，数字比较正确
		{"v1.0.10", "v1.0.9", true},
		{"1.0.0", "v1.0.0", false}, // 前导 v 不影响
		{"v2.0.0", "v1.9.9", true},
		{"v1.1", "v1.0.9", true},
		{"v1.0.0-rc1", "v1.0.0", false}, // 忽略预发布后缀
		{"v1.0.0", "dev", true},         // 非语义化本地版本视为需要更新
		{"", "v1.0.0", false},
	}
	for _, c := range cases {
		if got := isNewerVersion(c.latest, c.current); got != c.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"v1.2.3", "[1 2 3]"},
		{"1.2.3", "[1 2 3]"},
		{"v1.2.3-beta.1", "[1 2 3]"},
		{"v1.2.3+build5", "[1 2 3]"},
		{"dev", "[]"},
		{"", "[]"},
	}
	for _, c := range cases {
		if got := fmt.Sprint(parseVersion(c.in)); got != c.want {
			t.Errorf("parseVersion(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
