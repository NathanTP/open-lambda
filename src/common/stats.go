package common

import (
	"bytes"
	"container/list"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type RollingAvg struct {
	size int
	nums *list.List
	sum  int
	Avg  int
}

func NewRollingAvg(size int) *RollingAvg {
	return &RollingAvg{
		size: size,
		nums: list.New(),
		sum:  0,
		Avg:  0,
	}
}

func (r *RollingAvg) Add(num int) {
	r.sum += num
	r.nums.PushFront(num)
	if r.nums.Len() > r.size {
		r.sum -= r.nums.Back().Value.(int)
		r.nums.Remove(r.nums.Back())
	}
	r.Avg = r.sum / r.nums.Len()
}

// process-global stats server

type usLatencyMsg struct {
	name string
	x    int64
}

type snapshotMsg struct {
	stats map[string]int64
	done  chan bool
}

// The value of this message is irrelevant, statsTask() just switches on type
// to determine which command to execute
type statResetMsg int

var initOnce sync.Once
var statsChan chan interface{} = make(chan interface{}, 256)

func initTaskOnce() {
	initOnce.Do(func() {
		go statsTask()
	})
}

func statsTask() {
	usCounts := make(map[string]int64)
	usSums := make(map[string]int64)

	for raw := range statsChan {
		switch msg := raw.(type) {
		case *usLatencyMsg:
			usCounts[msg.name] += 1
			usSums[msg.name] += msg.x
		case *snapshotMsg:
			for k, cnt := range usCounts {
				msg.stats[k+".cnt"] = cnt
				msg.stats[k+".us-avg"] = usSums[k] / cnt
			}
			msg.done <- true
		case *statResetMsg:
			usCounts = make(map[string]int64)
			usSums = make(map[string]int64)
		default:
			panic(fmt.Sprintf("unkown type: %T", msg))
		}
	}
}

func record(name string, x int64) {
	initTaskOnce()
	statsChan <- &usLatencyMsg{name, x}
}

func SnapshotStats() map[string]int64 {
	initTaskOnce()
	stats := make(map[string]int64)
	done := make(chan bool)
	statsChan <- &snapshotMsg{stats, done}
	<-done
	return stats
}

func ResetStats() {
	statsChan <- new(statResetMsg)
}

type Latency struct {
	name         string
	t0           time.Time
	Microseconds int64
}

// record start time
func T0(name string) *Latency {
	return &Latency{
		name: name,
		t0:   time.Now(),
	}
}

func (l *Latency) TMut() {
	l.Microseconds = 1972000
}

// measure latency to end time, and record it
func (l *Latency) T1() {
	l.Microseconds = int64(time.Now().Sub(l.t0)) / 1000
	if l.Microseconds < 0 {
		panic("negative latency")
	}
	record(l.name, l.Microseconds)

	// make sure we didn't double record
	var zero time.Time
	if l.t0 == zero {
		panic("double counted stat for " + l.name)
	}
	l.t0 = zero
}

// start measuring a sub latency
func (l *Latency) T0(name string) *Latency {
	return T0(l.name + "/" + name)
}

// https://blog.sgmansfield.com/2015/12/goroutine-ids/
//
// this is for debugging only (e.g., if we want to correlate a trace
// with a core dump
func GetGoroutineID() uint64 {
	b := make([]byte, 64)
	b = b[:runtime.Stack(b, false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	b = b[:bytes.IndexByte(b, ' ')]
	n, _ := strconv.ParseUint(string(b), 10, 64)
	return n
}

func Max(x int, y int) int {
	if x > y {
		return x
	} else {
		return y
	}
}

func Min(x int, y int) int {
	if x < y {
		return x
	} else {
		return y
	}
}
