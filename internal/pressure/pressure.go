// Package pressure decides whether the machine is too busy to start a loop.
//
// There was already a guard for this, /usr/local/bin/fleet-guard, and it works
// — but it lives only on the box it guards (untracked, one disk failure from
// gone) and only 37 of 99 loops remember to call it. A gate every caller has to
// opt into protects the loops that did not need protecting; the ones written in
// a hurry are exactly the ones that skip it. So the check belongs in the runner,
// where no loop can forget it.
//
// Prefer PSI over load average. Load average on Linux counts uninterruptible
// sleep, so a box stalled on disk reads a high load while its CPUs are idle —
// rbm21 showed load 8.15 on 6 cores with 16 GB free and zero memory pressure,
// and the real signal was io.full at 8.5%. PSI says what is actually starved.
package pressure

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Paths into /proc, replaced in tests. Reading real kernel files is the whole
// job, so the parsing has to be exercised against fixtures rather than against
// whatever the build machine happens to be doing.
var (
	psiIOPath   = "/proc/pressure/io"
	psiCPUPath  = "/proc/pressure/cpu"
	memInfoPath = "/proc/meminfo"
	loadAvgPath = "/proc/loadavg"

	// cgroup v2 exposes PSI per cgroup. Inside a container that is the only
	// honest answer to "is MY work being starved".
	cgroupIOPath  = "/sys/fs/cgroup/io.pressure"
	cgroupCPUPath = "/sys/fs/cgroup/cpu.pressure"
)

// psiPaths returns the io and cpu pressure files to read, preferring this
// cgroup's own over the host's.
//
// This matters more than it sounds. rbm21 is an LXC container, and lxcfs
// virtualises /proc/cpuinfo, /proc/diskstats and /proc/loadavg for the
// container -- but NOT /proc/pressure. So /proc/pressure/io reports the
// PHYSICAL HOST: it read io.full=42.8% while this container's own cgroup read
// 0.00%. The fleets were being skipped because a different tenant on the same
// machine was busy, while they were neither causing the stall nor suffering it.
//
// A gate on the wrong number is worse than no gate: it stops real work for a
// reason that has nothing to do with the work.
func psiPaths() (ioPath, cpuPath string) {
	ioPath, cpuPath = psiIOPath, psiCPUPath
	if _, err := os.Stat(cgroupIOPath); err == nil {
		ioPath = cgroupIOPath
	}
	if _, err := os.Stat(cgroupCPUPath); err == nil {
		cpuPath = cgroupCPUPath
	}
	return ioPath, cpuPath
}

// Limits are the thresholds a loop must be under to start.
type Limits struct {
	// LoadPerCore is the 1-minute load average divided by core count. Scaling by
	// cores is the point: a flat "load 10" is idle on 32 cores and drowning on 2.
	LoadPerCore float64
	// MemUsedPct is used memory as a percentage of total, from MemAvailable —
	// which accounts for reclaimable cache, unlike free.
	MemUsedPct float64
	// CPUSomeAvg10 / IOFullAvg10 are PSI percentages over the last 10s.
	// io.full means every task was stalled, which is the one that actually
	// means "do not add work".
	CPUSomeAvg10 float64
	IOFullAvg10  float64
	// HostIOFullAvg10 gates on the whole machine rather than this container.
	// 0 disables it, which is the default: see Check.
	HostIOFullAvg10 float64
}

// DefaultLimits are deliberately generous. This gate exists to refuse work on a
// machine that is already struggling, not to keep it idle: a skipped loop runs
// on the next tick, so the cost of skipping is small and the cost of piling on
// is a wedged box.
func DefaultLimits() Limits {
	return Limits{
		LoadPerCore:  2.0,
		MemUsedPct:   90,
		CPUSomeAvg10:    60,
		IOFullAvg10:     40,
		HostIOFullAvg10: 0,
	}
}

// FromEnv applies overrides. Setting any limit to 0 disables that check.
func FromEnv() Limits {
	l := DefaultLimits()
	envFloat("FLEET_MAX_LOAD_PER_CORE", &l.LoadPerCore)
	envFloat("FLEET_MAX_MEM_PCT", &l.MemUsedPct)
	envFloat("FLEET_MAX_CPU_PSI", &l.CPUSomeAvg10)
	envFloat("FLEET_MAX_IO_PSI", &l.IOFullAvg10)
	envFloat("FLEET_MAX_HOST_IO_PSI", &l.HostIOFullAvg10)
	return l
}

// Check returns a human-readable reason when the machine is too loaded to take
// on a loop, or "" when it is fine. It never returns an error: a gate that
// cannot read /proc must let work through, because refusing every loop on a
// machine whose metrics are unreadable is worse than the pressure it guards.
func Check(l Limits) string {
	if os.Getenv("FLEET_IGNORE_PRESSURE") == "1" {
		return ""
	}

	ioPath, cpuPath := psiPaths()
	if l.IOFullAvg10 > 0 {
		if v, ok := psi(ioPath, "full", "avg10"); ok && v > l.IOFullAvg10 {
			return fmt.Sprintf("io pressure %.1f%% of the last 10s with everything stalled (limit %.0f%%)", v, l.IOFullAvg10)
		}
	}
	if l.CPUSomeAvg10 > 0 {
		if v, ok := psi(cpuPath, "some", "avg10"); ok && v > l.CPUSomeAvg10 {
			return fmt.Sprintf("cpu pressure %.1f%% over the last 10s (limit %.0f%%)", v, l.CPUSomeAvg10)
		}
	}
	// Optional courtesy to the rest of the machine: back off when the HOST is
	// struggling even though we are not. Off by default -- a neighbour being
	// busy is not a reason to stop, and on a shared box it would mean the
	// quietest tenant yields to the loudest forever.
	if l.HostIOFullAvg10 > 0 && ioPath != psiIOPath {
		if v, ok := psi(psiIOPath, "full", "avg10"); ok && v > l.HostIOFullAvg10 {
			return fmt.Sprintf("host io pressure %.1f%% (limit %.0f%%) -- this container is fine, the machine is not",
				v, l.HostIOFullAvg10)
		}
	}
	if l.MemUsedPct > 0 {
		if used, ok := memUsedPct(); ok && used > l.MemUsedPct {
			return fmt.Sprintf("memory %.1f%% used (limit %.0f%%)", used, l.MemUsedPct)
		}
	}
	if l.LoadPerCore > 0 {
		if load, ok := loadAvg1(); ok {
			cores := runtime.NumCPU()
			if cores < 1 {
				cores = 1
			}
			per := load / float64(cores)
			if per > l.LoadPerCore {
				return fmt.Sprintf("load %.2f over %d cores = %.2f per core (limit %.2f)", load, cores, per, l.LoadPerCore)
			}
		}
	}
	return ""
}

// psi reads a field such as ("some", "avg10") from a /proc/pressure file.
func psi(path, line, field string) (float64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, row := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(row, line+" ") {
			continue
		}
		for _, kv := range strings.Fields(row)[1:] {
			k, v, found := strings.Cut(kv, "=")
			if !found || k != field {
				continue
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, false
			}
			return f, true
		}
	}
	return 0, false
}

// memUsedPct derives usage from MemAvailable, which counts reclaimable cache as
// available. MemFree would report a healthy box with a warm page cache as full.
func memUsedPct() (float64, bool) {
	data, err := os.ReadFile(memInfoPath)
	if err != nil {
		return 0, false
	}
	var total, available float64
	for _, row := range strings.Split(string(data), "\n") {
		f := strings.Fields(row)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			available = v
		}
	}
	if total <= 0 || available <= 0 {
		return 0, false
	}
	return (total - available) / total * 100, true
}

func loadAvg1() (float64, bool) {
	data, err := os.ReadFile(loadAvgPath)
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(data))
	if len(f) < 1 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func envFloat(name string, dst *float64) {
	if v := os.Getenv(name); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			*dst = f
		}
	}
}
