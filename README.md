# reflexionhealth/endless

Zero downtime restarts for golang HTTP and HTTPS servers.  
A fork of [fvbock/endless](https://github.com/fvbock/endless), frankenstein'd together with
improvements from ReflexionHealth, [goagain](https://github.com/goagain/goagain), and [caddy](https://github.com/mholt/caddy).

Link to the normal [documentation](https://godoc.org/github.com/reflexionhealth/endless)

## Default Timeouts & MaxHeaderBytes

There are three variables exported by the package that control the values set for `DefaultReadTimeOut`, `DefaultWriteTimeOut`, and `MaxHeaderBytes` on the inner [`http.Server`](https://golang.org/pkg/net/http/#Server):

    DefaultReadTimeOut    time.Duration
    DefaultWriteTimeOut   time.Duration
    DefaultMaxHeaderBytes int

The endless default behaviour is to use the same defaults defined in `net/http`.

These have impact on endless by potentially not letting the parent process die until all connections are handled/finished.


### Shutdown Timeout

To deal with hanging requests on the parent, the parent server will exit the main
loop 60 seconds after receiving the shutdown signal from the forked child process.
Still running requests are terminated on exit. This behaviour can be controlled by another exported variable:

    DefaultShutdownTimeout time.Duration

The default is 60 seconds. When set to `-1` requests will not be terminated, instead relying on the Read and Write timeouts.


## Signals

The endless server will listen for the following signals: `syscall.SIGHUP`, `syscall.SIGUSR1`, `syscall.SIGUSR2`, `syscall.SIGINT`, `syscall.SIGTERM`, and `syscall.SIGTSTP`:

`SIGHUP` currently triggers a fork/restart (TODO: eventually, this should be a reload)  
`SIGUSR2` will trigger a fork/restart  
`SIGINT` and `SIGTERM` will trigger a shutdown of the server (it will finish running requests)  
`SIGQUIT` triggers a much less graceful shutdown  


## Usage

    import "github.com/reflexionhealth/endless"

and then replacing `http.ListenAndServe` with `endless.ListenAndServe` or `http.ListenAndServeTLS` with `endless.ListenAndServeTLS`

    err := endless.ListenAndServe("localhost:4242", handler)

After starting your server you can make some changes, build, and send `SIGHUP` to the running process and it will finish
handling any outstanding requests and serve all new incoming ones with the new binary.

### Advanced Example
This example demonstrates how you can add custom logging or other behavior to the server

    func main() {
        pid := syscall.Getpid()
        service := endless.NewServer("localhost:4242", mux)
        service.ErrorLog = log.New(os.Stderr, fmt.Sprintf("[%d] ", pid), 0)
        service.BeforeStart = func() { log.Printf("[%d] Starting\n", pid) }
        service.BeforeRestart = func() { log.Printf("[%d] Restarting\n", pid) }
        service.BeforeShutdown = func() { log.Printf("[%d] Stopping\n", pid) }
        err := service.ListenAndServe()
        if err != nil {
          log.Printf("[%d] Error: %s\n", pid, err)
        } else {
          log.Printf("[%d] Exited\n", pid)
        }
    }
