package endless

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	DefaultReadTimeout    time.Duration
	DefaultWriteTimeout   time.Duration
	DefaultMaxHeaderBytes int

	// Set DefaultShutdownTimeout to a negative value to never force requests to end
	DefaultShutdownTimeout time.Duration = 60 * time.Second
)

type EndlessServer struct {
	http.Server
	ShutdownTimeout time.Duration

	EndlessListener net.Listener
	tlsListener     *EndlessListener

	ErrorLog       *log.Logger
	BeforeStart    func()
	BeforeRestart  func()
	BeforeShutdown func()
	BeforeAbort    func()
	AfterShutdown  func(cleanExit bool)

	signalChan chan os.Signal
	waitHttp   sync.WaitGroup
	stopped    bool
}

// NewServer returns an intialized EndlessServer
func NewServer(addr string, handler http.Handler) *EndlessServer {
	srv := &EndlessServer{
		ShutdownTimeout: DefaultShutdownTimeout,

		signalChan: make(chan os.Signal, 2),
		waitHttp:   sync.WaitGroup{},
	}

	srv.Server.Addr = addr
	srv.Server.ReadTimeout = DefaultReadTimeout
	srv.Server.WriteTimeout = DefaultWriteTimeout
	srv.Server.MaxHeaderBytes = DefaultMaxHeaderBytes
	srv.Server.Handler = handler
	return srv
}

// ListenAndServe wraps http.ListenAndServe with graceful restart/shutdown
func ListenAndServe(addr string, handler http.Handler) error {
	server := NewServer(addr, handler)
	server.ErrorLog = log.New(os.Stderr, "", log.Flags())
	return server.ListenAndServe()
}

// ListenAndServeTLS wraps http.ListenAndServeTLS with graceful restart/shutdown
func ListenAndServeTLS(addr string, certFile string, keyFile string, handler http.Handler) error {
	server := NewServer(addr, handler)
	server.ErrorLog = log.New(os.Stderr, "", log.Flags())
	return server.ListenAndServeTLS(certFile, keyFile)
}

// ListenAndServe listens on the TCP network address then calls Serve
func (srv *EndlessServer) ListenAndServe() error {
	addr := srv.Addr
	if addr == "" {
		addr = ":http"
	}

	go srv.awaitSignals()

	listener, err := srv.listenOrResume(addr)
	if err != nil {
		return err
	}

	srv.EndlessListener = NewEndlessListener(listener, &srv.waitHttp)
	return srv.serve()
}

// ListenAndServeTLS listens on the TCP network address and then calls Serve
func (srv *EndlessServer) ListenAndServeTLS(certFile, keyFile string) error {
	addr := srv.Addr
	if addr == "" {
		addr = ":https"
	}

	config := &tls.Config{}
	if srv.TLSConfig != nil {
		*config = *srv.TLSConfig
	}
	if config.NextProtos == nil {
		config.NextProtos = []string{"http/1.1"}
	}

	var err error
	config.Certificates = make([]tls.Certificate, 1)
	config.Certificates[0], err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}

	go srv.awaitSignals()

	listener, err := srv.listenOrResume(addr)
	if err != nil {
		return err
	}

	srv.tlsListener = NewEndlessListener(listener, &srv.waitHttp)
	srv.EndlessListener = tls.NewListener(srv.tlsListener, config)
	return srv.serve()
}

func (srv *EndlessServer) listenOrResume(addr string) (net.Listener, error) {
	listenerPath := os.Getenv("ENDLESS_SOCKET_FD")
	if len(listenerPath) == 0 {
		// listen on a new socket
		return net.Listen("tcp", addr)
	}

	// resume listening on an existing socket
	var socket uintptr
	_, err := fmt.Sscan(listenerPath, &socket)
	if err != nil {
		return nil, err
	}

	listener, _ := net.FileListener(os.NewFile(socket, os.Getenv("ENDLESS_SOCKET_NAME")))
	switch listener.(type) {
	case *net.TCPListener, *net.UnixListener:
		break
	default:
		return nil, errors.New("file descriptor is not a TCP or Unix socket")
	}

	// deal with Go's insistence on dup(2)ing file descriptors
	err = syscall.Close(int(socket))
	if err != nil {
		return nil, err
	}

	// shutdown the parent gracefully
	syscall.Kill(syscall.Getppid(), syscall.SIGTERM)

	return listener, nil
}

func (srv *EndlessServer) serve() error {
	if srv.BeforeStart != nil {
		srv.BeforeStart()
	}

	err := srv.Server.Serve(srv.EndlessListener)
	finished := waitTimeout(&srv.waitHttp, srv.ShutdownTimeout)
	if srv.AfterShutdown != nil {
		srv.AfterShutdown(finished)
	}

	if srv.stopped {
		return nil
	} else {
		return err
	}
}

func (srv *EndlessServer) awaitSignals() {
	signal.Notify(srv.signalChan,
		syscall.SIGHUP,
		syscall.SIGUSR2,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	forked := false
	exiting := false
	aborted := false
	for {
		sig := <-srv.signalChan
		switch sig {
		case syscall.SIGHUP, syscall.SIGUSR2:
			if !forked {
				forked = true
				if srv.BeforeRestart != nil {
					srv.BeforeRestart()
				}
				err := srv.forkExec()
				if err != nil && srv.ErrorLog != nil {
					srv.ErrorLog.Println("error: couldn't fork new server:", err)
				}
			}
		case syscall.SIGINT, syscall.SIGTERM:
			if !exiting {
				exiting = true
				srv.stopped = true
				if srv.BeforeShutdown != nil {
					srv.BeforeShutdown()
				}
				srv.shutdownEventually()
			} else if sig == syscall.SIGINT {
				if !aborted {
					aborted = true
					if srv.BeforeAbort != nil {
						srv.BeforeAbort()
					}
					srv.shutdownNow()
				} else {
					os.Exit(1) // in case SIGINT is being spammed
				}
			}
		case syscall.SIGQUIT:
			if !aborted {
				aborted = true
				if srv.BeforeAbort != nil {
					srv.BeforeAbort()
				}
				srv.shutdownNow()
			} else {
				os.Exit(1) // in case SIGQUIT is being spammed
			}
		}
	}
}

// shutdownEventually closes the listener so that no new connections are accepted.
func (srv *EndlessServer) shutdownEventually() {
	// disable keep-alives on existing connections
	srv.SetKeepAlivesEnabled(false)
	err := srv.EndlessListener.Close()
	if err != nil && srv.ErrorLog != nil {
		srv.ErrorLog.Printf("error: %s\n", err)
	}
}

// shutdownNow exits quickly
func (srv *EndlessServer) shutdownNow() {
	srv.SetKeepAlivesEnabled(false)
	srv.EndlessListener.Close()
	os.Exit(1)
}

func (srv *EndlessServer) forkExec() error {
	var listener net.Listener
	var file *os.File
	var err error
	switch srv.EndlessListener.(type) {
	case *EndlessListener:
		listener = srv.EndlessListener.(*EndlessListener).Listener
	default: // tls listener
		listener = srv.tlsListener.Listener
	}
	file, err = listener.(*net.TCPListener).File()
	if err != nil {
		return err
	}

	fd := file.Fd()
	err = os.Setenv("ENDLESS_SOCKET_FD", fmt.Sprint(fd))
	if err != nil {
		return err
	}
	addr := listener.Addr()
	addrname := fmt.Sprintf("%s:%s->", addr.Network(), addr.String())
	err = os.Setenv("ENDLESS_SOCKET_NAME", addrname)
	if err != nil {
		return err
	}

	files := make([]*os.File, fd-3+1) // skips stdin, stdout, stderr
	files[fd-3] = os.NewFile(fd, addrname)
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = files
	return cmd.Start()
}

// EndlessListener is a net.Listener which can count the number of connections on it.
// Its methods mainly wrap net.Listener to be graceful.
type EndlessListener struct {
	net.Listener
	waitHttp   *sync.WaitGroup // pointer to the host's wg used for counting connections
	stop       chan error
	stopped    bool
	sync.Mutex // protects the stopped flag
}

// NewEndlessListener returns a EndlessListener that wraps a listener and uses wg to count connections.
func NewEndlessListener(l net.Listener, wg *sync.WaitGroup) *EndlessListener {
	el := &EndlessListener{Listener: l, stop: make(chan error), waitHttp: wg}
	go func() {
		<-el.stop
		el.Lock()
		el.stopped = true
		el.Unlock()
		el.stop <- el.Listener.Close()
	}()
	return el
}

// Accept accepts a connection.
func (el *EndlessListener) Accept() (net.Conn, error) {
	conn, err := el.Listener.Accept()
	if err != nil {
		return nil, err
	}

	el.waitHttp.Add(1)
	return EndlessConn{Conn: conn, waitHttp: el.waitHttp}, nil
}

// Close immediately closes the listener.
func (el *EndlessListener) Close() error {
	el.Lock()
	if el.stopped {
		el.Unlock()
		return syscall.EINVAL
	}
	el.Unlock()
	el.stop <- nil
	err := <-el.stop
	return err
}

func (el *EndlessListener) File() *os.File {
	// returns a dup(2) - FD_CLOEXEC flag *not* set
	tcp := el.Listener.(*net.TCPListener)
	file, _ := tcp.File()
	return file
}

// EndlessConn represents a connection on an EndlessListener so that we can keep track
// of the number of connections, thus facilitating a graceful shutdown.
type EndlessConn struct {
	net.Conn
	waitHttp *sync.WaitGroup // pointer to the host server's connection waitgroup
}

// Close closes c's underlying connection while updating the wg count.
func (c EndlessConn) Close() error {
	err := c.Conn.Close()
	if err != nil {
		return err
	}
	// close can fail on http2 connections (as of Oct. 2015, before http2 in std lib)
	// so don't decrement count unless close succeeds
	c.waitHttp.Done()
	return nil
}

// waitTimeout waits for the waitgroup for the specified max timeout.
// Returns true if waiting finishes before the timeout.
func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()

	select {
	case <-c:
		return true // completed
	case <-time.After(timeout):
		return false // timed out
	}
}
