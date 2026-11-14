package main

import (
	"fmt"
	"sync"
	"time"
)

var idCounter struct {
	sync.Mutex
	n int64
}

func newID(prefix string) string {
	idCounter.Lock()
	idCounter.n++
	n := idCounter.n
	idCounter.Unlock()
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), n)
}
