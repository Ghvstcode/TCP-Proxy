package server

import (
	"fmt"
	"github.com/Ghvstcode/TCP-Proxy/pkg/loadBalancer"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// SigChannel is a wrapper around an os.Signal channel
// It contains the sync.Once field
// to ensure we aren't attempting to closing channel C multiple times
type SigChannel struct {
	C    chan os.Signal
	once sync.Once
}

// NewSigChannel returns a new initialised SigChannel struct
func NewSigChannel() *SigChannel {
	return &SigChannel{C: make(chan os.Signal)}
}

// App represents the configuration provided by the user
type App struct {
	// Name is the name of the application as specified in the provided config file
	Name string
	// Ports is a list of all the ports the proxy listens on for this specific App
	Ports []int
	// Targets is a list of all available backends for a specific App
	Targets []string
}

// SafeClose safely closes the channel 'C' once.
func (sc *SigChannel) SafeClose() {
	sc.once.Do(func() {
		close(sc.C)
	})
}

// Server defines parameters for running the TCP proxy server
type Server struct {
	// This is used to received signals to know when to shutdown the server
	Quit *SigChannel
	// This is a map of all the listeners and the app names
	// This allows us accept connections on a port
	// and associate it with a backend for the sake of forwarding connections
	listener map[*net.TCPListener]string
	// listenerGroup exists for the purpose of synchronisation
	listenerGroup sync.WaitGroup
	mu            sync.Mutex
	// This is a map of all the apps and their associated backends.
	// This is managed by the loadbalancer which periodically -
	// updates the status of a backend to indicate if it is healthy or noy
	serverPool map[string]*loadBalancer.ServerPool
}

// NewServer initialises a new server
// It takes in an array of app configurations and uses that to initialise the server
func NewServer(apps []App) *Server {
	s := &Server{
		Quit: NewSigChannel(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.listener = make(map[*net.TCPListener]string)
	s.serverPool = make(map[string]*loadBalancer.ServerPool)

	// Iterate over the various application configs
	for _, app := range apps {
		// Iterate over an apps ports in order to create a listener for it
		for _, port := range app.Ports {
			addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+strconv.Itoa(port))
			if err != nil {
				log.Printf("Unable to resolve IP for port %v err:%v", port, err)
			}

			l, err := net.ListenTCP("tcp", addr)
			if err != nil {
				log.Printf("Unable to listen %v", err)
			}

			// Map the listener to the app name
			s.listener[l] = app.Name
		}

		// Create a new loadbalancer(LB) server pool from the various app targets
		sp := loadBalancer.NewServerPool(app.Targets)
		s.serverPool[app.Name] = sp

		// Start the healthcheck for the newly created LB server pool
		go sp.HealthCheck()
	}

	s.listenerGroup.Add(1)
	// Create a goroutine to serve connections based on the configs initialised above
	// This is non-blocking, Server runs in a background goroutine
	go s.Serve()
	return s
}

// Serve accepts incoming connections on the listeners
func (s *Server) Serve() {
	defer s.listenerGroup.Done()

	// We iterate over all the listeners and create goroutines for each to accept connections
	for ln, val := range s.listener {
		s.listenerGroup.Add(1)

		val := val
		go func(listener *net.TCPListener) {
			s.acceptConnections(listener, val)
			s.listenerGroup.Done()
		}(ln)
	}

	log.Println("Started Listening For New Connections")

}

// acceptConnections exists to accept incoming connections and pass them down to the handler
func (s *Server) acceptConnections(listener *net.TCPListener, app string) {
	for {
		conn, err := (*listener).AcceptTCP()
		if err != nil {
			select {
			case <-s.Quit.C:
				return
			default:
				log.Printf("an error occured while accepting TCP connections %v", err)
			}
		} else {
			s.listenerGroup.Add(1)
			go func() {
				s.handleConnection(conn, app)
				s.listenerGroup.Done()
			}()
		}
	}
}

// handleConnection passes connections over to the proxy
func (s *Server) handleConnection(conn net.Conn, appName string) {
	for {
		s.listenerGroup.Add(1)
		go func() {
			s.proxyConnection(conn, appName)
			s.listenerGroup.Done()
		}()

		s.listenerGroup.Wait()
	}
}

func (s *Server) proxyConnection(conn net.Conn, appName string) {
	var remoteAddr string

	// The appName is the key for the map containing all the TargetBackends, At this point we retrieve it.
	backend := s.serverPool[appName]

	//Retrieve the remote address which connections should be forwarded to!
	nextTarget := backend.GetNextPeer()
	if nextTarget != nil {
		remoteAddr = nextTarget.GetAddress()
	}

	// Close the connection once we are done
	defer conn.Close()

	conn2, err := DialTCP("tcp", remoteAddr)
	if err != nil {
		log.Println("Error dialing remote address", err)
		return
	}

	// remember to close this connection when we are finished.
	defer conn2.Close()

	go io.Copy(conn2, conn)
	io.Copy(conn, conn2)

	return
}

// Stop attempts to close all open listeners
// First it closes the signal channel and ranges over all listeners
// closing them one at a time
func (s *Server) Stop() {
	s.Quit.SafeClose()
	for ln := range s.listener {
		if err := (*ln).Close(); err != nil {
			log.Fatal("Error occurred while closing listener", err)
		}
	}
	fmt.Println("Stoped gracefully")
}

// Conn  is an implementation od the net.Conn interface
// It allows us set an idleTimeout for reads and writes
// The official docs "An idle timeout can be implemented by repeatedly extending the deadline -
// after successful Read or Write calls".
type Conn struct {
	net.Conn
	// idleTimeout value
	idleTimeout time.Duration
}

// Read implements the Conn interface and sets the Read Deadline
func (c *Conn) Read(b []byte) (int, error) {
	err := c.Conn.SetReadDeadline(time.Now().Add(c.idleTimeout))
	if err != nil {
		return 0, err
	}
	return c.Conn.Read(b)
}

// Write implements the Conn interface and sets the Write Deadline
func (c *Conn) Write(b []byte) (int, error) {
	err := c.Conn.SetWriteDeadline(time.Now().Add(c.idleTimeout))
	if err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}

// DialTCP is used to connect to the remote address for proxying
func DialTCP(network, addr string) (*Conn, error) {
	taddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	saddr, err := tcpAddrToSocketAddr(taddr)
	if err != nil {
		return nil, err
	}

	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if err != nil {
		return nil, err
	}

	// Set Socket option to reuse address
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		syscall.Close(fd)
		return nil, err
	}

	// Syscall to make the connection
	if err = syscall.Connect(fd, saddr); err != nil && err != syscall.EINPROGRESS {
		syscall.Close(fd)
		return nil, err
	}

	// Generate a name that will be used for the to-be generated file
	name := fmt.Sprintf("tcp.%d.%s.%s", os.Getpid(), network, addr)
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()

	// Get a copy of the network
	c, err := net.FileConn(f)
	if err != nil {
		syscall.Close(fd)
		return nil, err
	}

	// set the timeout period and return the connection
	cn := &Conn{
		Conn:        c,
		idleTimeout: 20 * time.Second,
	}
	return cn, nil
}

// convert the TCP address to a Socket Address which can be used for connecting
func tcpAddrToSocketAddr(addr *net.TCPAddr) (syscall.Sockaddr, error) {
	switch {
	case addr.IP.To4() != nil:
		ip := [4]byte{}
		copy(ip[:], addr.IP.To4())

		return &syscall.SockaddrInet4{Addr: ip, Port: addr.Port}, nil

	default:
		ip := [16]byte{}
		copy(ip[:], addr.IP.To16())

		zoneID, err := strconv.ParseUint(addr.Zone, 10, 32)
		if err != nil {
			return nil, err
		}

		return &syscall.SockaddrInet6{Addr: ip, Port: addr.Port, ZoneId: uint32(zoneID)}, nil
	}

}
