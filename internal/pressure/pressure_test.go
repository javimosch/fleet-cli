package pressure

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtures writes /proc lookalikes and points the package at them.
func fixtures(t *testing.T, io, cpu, mem, load string) {
	t.Helper()
	d := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	psiIOPath, psiCPUPath = write("io", io), write("cpu", cpu)
	memInfoPath, loadAvgPath = write("mem", mem), write("load", load)
}

// The numbers rbm21 actually reported: load 8.15 on 6 cores with 16 GB free and
// zero memory pressure. Load average alone says "drowning"; PSI says the memory
// is fine and the stall is disk. A gate that only read load would skip every
// loop on a box with plenty of RAM.
const (
	rbm21IO   = "some avg10=0.89 avg60=7.59 avg300=8.60 total=1\nfull avg10=0.89 avg60=7.52 avg300=8.48 total=1\n"
	rbm21CPU  = "some avg10=5.20 avg60=6.08 avg300=7.33 total=1\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
	rbm21Mem  = "MemTotal:       20971520 kB\nMemFree:        10930176 kB\nMemAvailable:   16708608 kB\n"
	rbm21Load = "8.15 8.65 8.61 2/1443 12345\n"
)

func TestHealthyBoxIsNotBlocked(t *testing.T) {
	fixtures(t, rbm21IO, rbm21CPU, rbm21Mem, rbm21Load)
	l := DefaultLimits()
	l.LoadPerCore = 0 // core count varies by build machine; covered separately
	if reason := Check(l); reason != "" {
		t.Fatalf("blocked a box with 16GB free and low PSI: %s", reason)
	}
}

func TestMemoryIsMeasuredFromAvailableNotFree(t *testing.T) {
	// MemFree is 8% of total but MemAvailable is 80%: a warm page cache, not a
	// full machine. Reading MemFree would refuse to run anything here.
	mem := "MemTotal:       20971520 kB\nMemFree:         1677721 kB\nMemAvailable:   16777216 kB\n"
	fixtures(t, rbm21IO, rbm21CPU, mem, rbm21Load)
	used, ok := memUsedPct()
	if !ok {
		t.Fatal("could not read memory")
	}
	if used > 25 {
		t.Fatalf("used %.1f%% -- MemFree was used instead of MemAvailable", used)
	}
}

func TestEachSignalBlocksOnItsOwn(t *testing.T) {
	cases := []struct{ name, io, cpu, mem, load, want string }{
		{"io stall", "some avg10=99 total=1\nfull avg10=95.0 total=1\n", rbm21CPU, rbm21Mem, rbm21Load, "io pressure"},
		{"cpu stall", rbm21IO, "some avg10=88.0 total=1\n", rbm21Mem, rbm21Load, "cpu pressure"},
		{"memory", rbm21IO, rbm21CPU, "MemTotal: 1000 kB\nMemAvailable: 20 kB\n", rbm21Load, "memory"},
	}
	for _, c := range cases {
		fixtures(t, c.io, c.cpu, c.mem, c.load)
		got := Check(DefaultLimits())
		if got == "" {
			t.Errorf("%s: expected a block, got none", c.name)
			continue
		}
		if !contains(got, c.want) {
			t.Errorf("%s: reason %q does not mention %q", c.name, got, c.want)
		}
	}
}

func TestUnreadableProcLetsWorkThrough(t *testing.T) {
	// Refusing every loop on a machine whose metrics cannot be read would be a
	// worse outage than the pressure this guards against.
	psiIOPath, psiCPUPath = "/nonexistent/io", "/nonexistent/cpu"
	memInfoPath, loadAvgPath = "/nonexistent/mem", "/nonexistent/load"
	if reason := Check(DefaultLimits()); reason != "" {
		t.Fatalf("blocked when /proc was unreadable: %s", reason)
	}
}

func TestOverrideAndOptOut(t *testing.T) {
	fixtures(t, rbm21IO, rbm21CPU, rbm21Mem, rbm21Load)
	l := DefaultLimits()
	l.CPUSomeAvg10 = 1 // rbm21's cpu some avg10 is 5.20
	if Check(l) == "" {
		t.Fatal("a tightened limit did not block")
	}
	t.Setenv("FLEET_IGNORE_PRESSURE", "1")
	if reason := Check(l); reason != "" {
		t.Fatalf("FLEET_IGNORE_PRESSURE did not disable the gate: %s", reason)
	}
	t.Setenv("FLEET_IGNORE_PRESSURE", "")
	l.CPUSomeAvg10 = 0 // 0 disables just this check
	if reason := Check(l); reason != "" {
		t.Fatalf("a zero limit still blocked: %s", reason)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
