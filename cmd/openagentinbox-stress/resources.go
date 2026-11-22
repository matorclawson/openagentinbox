package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type sample struct {
	Seconds                                 float64
	RSSKiB, HWMKiB, DatabaseBytes, WALBytes int64
	CPUTicks                                int64
}

func fileSize(p string) int64 {
	s, e := os.Stat(p)
	if e == nil {
		return s.Size()
	}
	return 0
}
func (b *bench) sample(start time.Time) (s sample, e error) {
	base := fmt.Sprintf("/proc/%d", b.proc.Process.Pid)
	raw, e := os.ReadFile(filepath.Join(base, "status"))
	if e != nil {
		return s, e
	}
	for _, line := range strings.Split(string(raw), "\n") {
		v := strings.Fields(line)
		if len(v) < 2 {
			continue
		}
		switch v[0] {
		case "VmRSS:":
			s.RSSKiB, e = strconv.ParseInt(v[1], 10, 64)
		case "VmHWM:":
			s.HWMKiB, e = strconv.ParseInt(v[1], 10, 64)
		}
		if e != nil {
			return s, e
		}
	}
	if s.RSSKiB <= 0 || s.HWMKiB <= 0 {
		return s, fmt.Errorf("process memory unavailable")
	}
	raw, e = os.ReadFile(filepath.Join(base, "stat"))
	if e != nil {
		return s, e
	}
	v := strings.Fields(string(raw)[strings.LastIndexByte(string(raw), ')')+1:])
	if len(v) < 13 {
		return s, fmt.Errorf("invalid proc stat")
	}
	u, e := strconv.ParseInt(v[11], 10, 64)
	if e != nil {
		return s, e
	}
	sys, e := strconv.ParseInt(v[12], 10, 64)
	if e != nil {
		return s, e
	}
	s.CPUTicks = u + sys
	s.Seconds = time.Since(start).Seconds()
	s.DatabaseBytes = fileSize(b.path)
	s.WALBytes = fileSize(b.path + "-wal")
	return s, nil
}
func clockTicks() (float64, error) {
	out, e := exec.Command("getconf", "CLK_TCK").Output()
	if e != nil {
		return 0, e
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}
