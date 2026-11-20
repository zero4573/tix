package cvewatch

import "testing"

func TestDistroVersionOrder(t *testing.T) {
	for _, c := range []struct {
		eco, a, b string
		want      int
	}{
		{"Debian:12", "3.0.11-1~deb12u1", "3.0.11-1~deb12u2", -1},
		{"Debian:12", "3.0.11-1~deb12u2", "3.0.11-1", -1},
		{"Debian:12", "1.0~rc1", "1.0", -1},
		{"Debian:12", "1:1.0", "2.0", 1},
		{"Debian:12", "1.2.3-1", "1.2.3-1", 0},
		{"Debian:12", "1.10", "1.9", 1},
		{"Debian:12", "1.0a", "1.0+b1", -1},
		{"Ubuntu:22.04:LTS", "3.0.2-0ubuntu1.10", "3.0.2-0ubuntu1.9", 1},
		{"Red Hat:enterprise_linux:9::baseos", "1:3.0.7-16.el9_2", "1:3.0.7-27.el9", -1},
		{"Red Hat:enterprise_linux:9::baseos", "3.0.7-27.el9", "1:3.0.1-1.el9", -1},
		{"Red Hat:enterprise_linux:9::baseos", "2.0~rc1-1", "2.0-1", -1},
		{"Red Hat:enterprise_linux:9::baseos", "2.0^post1-1", "2.0-1", 1},
		{"Rocky Linux:9", "1.10-1", "1.9-1", 1},
		{"Rocky Linux:9", "1.0a-1", "1.0-1", 1},
		{"Alpine:v3.20", "3.1.4-r0", "3.1.4-r1", -1},
		{"Alpine:v3.20", "1.2.3_rc1-r0", "1.2.3-r0", -1},
		{"Alpine:v3.20", "1.2.3_p1-r0", "1.2.3-r5", 1},
		{"Alpine:v3.20", "1.2.3a-r0", "1.2.3-r0", 1},
		{"Alpine:v3.20", "1.10.0-r0", "1.9.9-r9", 1},
		{"Alpine:v3.20", "1.2-r0", "1.2.1-r0", -1},
		{"npm", "1.0.0-rc.1", "1.0.0", -1},
	} {
		if got := compareIn(c.eco, c.a, c.b); got != c.want {
			t.Errorf("%s: %s vs %s = %d, want %d", c.eco, c.a, c.b, got, c.want)
		}
		if got := compareIn(c.eco, c.b, c.a); got != -c.want {
			t.Errorf("%s: %s vs %s = %d, want %d", c.eco, c.b, c.a, got, -c.want)
		}
	}
}
