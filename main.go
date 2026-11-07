// powerlog samples APU power and temperatures from sysfs (hwmon) and appends
// one averaged CSV row per period. Read-only, needs no privileges.
//
// Each row covers the interval [time, time+period) and holds the average of the
// samples taken in it. Energy of a row in joules = power_w × samples × sample
// interval (normally 2 × 0.5 s = 1 s; only rows at startup/shutdown are partial).
//
// The output file may be renamed by logrotate at any time: before each write
// the path is checked and the file is reopened (with a fresh header) if it was
// moved away.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const header = "time,power_w,power_max_w,gpu_temp_c,cpu_temp_c,nvme_temp_c,gpu_busy_pct,samples\n"

// Sensor paths, resolved by hwmon name at startup (hwmonN numbering can change between boots).
type sensors struct {
	power, gpuTemp, cpuTemp, nvmeTemp, gpuBusy string
}

type sample struct {
	power, gpuTemp, cpuTemp, nvmeTemp, gpuBusy float64 // NaN if unreadable
}

func findHwmon(name string) string {
	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, d := range dirs {
		if b, err := os.ReadFile(d + "/name"); err == nil && strings.TrimSpace(string(b)) == name {
			return d
		}
	}
	return ""
}

func discover() (sensors, error) {
	var s sensors
	gpu := findHwmon("amdgpu")
	if gpu == "" {
		return s, fmt.Errorf("amdgpu hwmon not found")
	}
	s.power = gpu + "/power1_input" // PPT: whole-APU package power, microwatts
	s.gpuTemp = gpu + "/temp1_input"
	s.gpuBusy = gpu + "/device/gpu_busy_percent"
	if d := findHwmon("k10temp"); d != "" {
		s.cpuTemp = d + "/temp1_input" // Tctl
	}
	if d := findHwmon("nvme"); d != "" {
		s.nvmeTemp = d + "/temp1_input"
	}
	return s, nil
}

// readVal reads an integer sysfs value and divides it by scale; NaN on any error.
func readVal(path string, scale float64) float64 {
	if path == "" {
		return math.NaN()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return math.NaN()
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return math.NaN()
	}
	return float64(v) / scale
}

func (s sensors) read() sample {
	return sample{
		power:    readVal(s.power, 1e6),
		gpuTemp:  readVal(s.gpuTemp, 1e3),
		cpuTemp:  readVal(s.cpuTemp, 1e3),
		nvmeTemp: readVal(s.nvmeTemp, 1e3),
		gpuBusy:  readVal(s.gpuBusy, 1),
	}
}

// mean accumulates an average, skipping NaN samples.
type mean struct {
	sum float64
	n   int
}

func (m *mean) add(v float64) {
	if !math.IsNaN(v) {
		m.sum += v
		m.n++
	}
}

func (m mean) fmt(prec int) string {
	if m.n == 0 {
		return ""
	}
	return strconv.FormatFloat(m.sum/float64(m.n), 'f', prec, 64)
}

// bucket aggregates the samples belonging to one output row.
type bucket struct {
	start                                   time.Time
	power, gpuTemp, cpuTemp, nvmeTemp, busy mean
	maxPower                                float64
	samples                                 int
}

func (b *bucket) add(s sample) {
	b.power.add(s.power)
	b.gpuTemp.add(s.gpuTemp)
	b.cpuTemp.add(s.cpuTemp)
	b.nvmeTemp.add(s.nvmeTemp)
	b.busy.add(s.gpuBusy)
	if !math.IsNaN(s.power) {
		b.maxPower = math.Max(b.maxPower, s.power)
	}
	b.samples++
}

func (b *bucket) row() string {
	maxP := ""
	if b.power.n > 0 {
		maxP = strconv.FormatFloat(b.maxPower, 'f', 2, 64)
	}
	return fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%d\n",
		b.start.UTC().Format(time.RFC3339),
		b.power.fmt(2), maxP,
		b.gpuTemp.fmt(1), b.cpuTemp.fmt(1), b.nvmeTemp.fmt(1), b.busy.fmt(0),
		b.samples)
}

// csvFile appends rows to path, reopening it if logrotate moved it away.
type csvFile struct {
	path string
	f    *os.File
}

func (c *csvFile) ensureOpen() error {
	if c.path == "-" {
		if c.f == nil {
			c.f = os.Stdout
			c.f.WriteString(header)
		}
		return nil
	}
	if c.f != nil {
		cur, err1 := os.Stat(c.path)
		open, err2 := c.f.Stat()
		if err1 == nil && err2 == nil && os.SameFile(cur, open) {
			return nil
		}
		c.f.Close() // rotated or deleted: start a new file
		c.f = nil
	}
	f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if st, err := f.Stat(); err == nil && st.Size() == 0 {
		if _, err := f.WriteString(header); err != nil {
			f.Close()
			return err
		}
	}
	c.f = f
	return nil
}

func (c *csvFile) write(line string) {
	if err := c.ensureOpen(); err != nil {
		log.Printf("open %s: %v", c.path, err)
		return
	}
	if _, err := c.f.WriteString(line); err != nil {
		log.Printf("write %s: %v", c.path, err)
		c.f.Close()
		c.f = nil
	}
}

func main() {
	out := flag.String("o", "-", "output CSV file, - for stdout")
	sampleEvery := flag.Duration("sample", 500*time.Millisecond, "sampling interval")
	period := flag.Duration("period", time.Second, "interval covered by each CSV row")
	flag.Parse()
	log.SetFlags(0)

	if *sampleEvery <= 0 || *period < *sampleEvery || *period%*sampleEvery != 0 {
		log.Fatal("-period must be a positive multiple of -sample")
	}
	sens, err := discover()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("power=%s gpu_temp=%s cpu_temp=%s nvme_temp=%s gpu_busy=%s -> %s",
		sens.power, sens.gpuTemp, sens.cpuTemp, sens.nvmeTemp, sens.gpuBusy, *out)

	csv := &csvFile{path: *out}
	if err := csv.ensureOpen(); err != nil {
		log.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	// Sample half an interval off the period boundary (e.g. at .25 and .75 s),
	// so scheduling jitter never pushes a sample into the neighbouring row.
	now := time.Now()
	first := now.Truncate(*period).Add(*sampleEvery / 2)
	for !first.After(now) {
		first = first.Add(*sampleEvery)
	}
	time.Sleep(time.Until(first))
	ticker := time.NewTicker(*sampleEvery)
	defer ticker.Stop()

	var cur *bucket
	handle := func(t time.Time) {
		start := t.Truncate(*period)
		if cur != nil && !cur.start.Equal(start) {
			csv.write(cur.row())
			cur = nil
		}
		if cur == nil {
			cur = &bucket{start: start}
		}
		cur.add(sens.read())
	}

	handle(time.Now())
	for {
		select {
		case t := <-ticker.C:
			handle(t)
		case <-sig:
			if cur != nil {
				csv.write(cur.row()) // partial row; its samples column shows how many
			}
			return
		}
	}
}
