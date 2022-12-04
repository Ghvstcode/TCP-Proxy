package loadBalancer

import (
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type ServerPool struct {
	backends []*Backend
	current  uint64
}

func NewServerPool(targets []string) *ServerPool {
	backends := make([]*Backend, 0)
	for _, val := range targets {
		backends = append(backends, NewBackend(val))
	}

	return &ServerPool{
		backends: backends,
		current:  0,
	}
}

type Backend struct {
	target string
	Alive  bool
	mu     sync.RWMutex
}

func NewBackend(target string) *Backend {
	return &Backend{
		target: target,
		Alive:  true,
		mu:     sync.RWMutex{},
	}
}

func isBackendAlive(addr string) bool {
	timeout := 2 * time.Second
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		log.Println("Site unreachable, error: ", err)
		return false
	}
	defer conn.Close()
	return true
}

func (s *ServerPool) healthCheckItr() {
	for _, b := range s.backends {
		status := "up"
		alive := isBackendAlive(b.target)
		b.SetAlive(alive)
		if !alive {
			status = "down"
		}

		log.Printf("%s [%s]\n", b.target, status)
	}
}

func (s *ServerPool) HealthCheck() {
	t := time.NewTicker(time.Second * 20)
	for {
		select {
		case <-t.C:
			log.Println("Starting health check...")
			s.healthCheckItr()
			log.Println("Health check completed")
		}
	}
}

func (s *ServerPool) NextIndex() int {
	bel := len(s.backends)
	return int(atomic.AddUint64(&s.current, uint64(1)) % uint64(bel))
}

func (s *ServerPool) GetNextPeer() *Backend {
	// loop entire backends to find out an Alive backend
	next := s.NextIndex()
	l := len(s.backends) + next // start from next and move a full cycle
	for i := next; i < l; i++ {
		idx := i % len(s.backends) // take an index by modding with length
		// if we have an alive backend, use it and store if its not the original one
		if s.backends[idx].IsAlive() {
			if i != next {
				atomic.StoreUint64(&s.current, uint64(idx)) // mark the current one
			}
			return s.backends[idx]
		}
	}
	return nil
}

// SetAlive for this backend
func (b *Backend) SetAlive(alive bool) {
	b.mu.Lock()
	b.Alive = alive
	b.mu.Unlock()
}

// IsAlive returns true when backend is alive
func (b *Backend) IsAlive() (alive bool) {
	b.mu.RLock()
	alive = b.Alive
	b.mu.RUnlock()
	return
}

func (b *Backend) GetAddress() string {
	return b.target
}
