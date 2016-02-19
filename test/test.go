package main

import (
	"log"
	"math/rand"
	"os"
	"os/exec"
	"sync"
	"time"
)

var (
	keepRestarting bool
)

func compileAndStartTestServer(compileDone chan struct{}) {
	cmd := exec.Command("go", "build", "-a", "-o", "test/runendless", "test/server/server.go")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Start()
	if err != nil {
		log.Fatalln("COMPILE ERROR:", err)
	}
	err = cmd.Wait()
	if err != nil {
		log.Fatalln("COMPILE ERROR:", err)
	}

	cmd = exec.Command("./test/runendless")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	if err != nil {
		log.Fatalln("START ERROR:", err)
	}
	compileDone <- struct{}{}
	err = cmd.Wait()
	if err != nil {
		log.Fatalln("START ERROR:", err)
	}
	return
}

func runApacheBench() {
	time.Sleep(time.Second * 1)
	cmd := exec.Command("ab", "-c 1000", "-n 100000", "http://localhost:4242/foo")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	var err error
	err = cmd.Start()
	if err != nil {
		log.Fatalln("BENCH ERROR:", err)
	}
	err = cmd.Wait()
	if err != nil {
		log.Fatalln("BENCH ERROR:", err)
	}
}

func stopTestServer() (err error) {
	time.Sleep(time.Second * 5)
	cmd := exec.Command("./test/stop.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		log.Fatalln("SIGTERM ERROR:", err)
		return
	}

	err = cmd.Wait()
	if err != nil {
		log.Fatalln("SIGTERM ERROR:", err)
	}
	return
}

func killTestServer() (err error) {
	time.Sleep(time.Second * 5)
	cmd := exec.Command("./test/kill.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		log.Fatalln("SIGKILL ERROR:", err)
		return
	}

	err = cmd.Wait()
	if err != nil {
		log.Fatalln("SIGKILL ERROR:", err)
	}
	return
}

func keepRestartingServer() {
	time.Sleep(time.Second * 1)
	for keepRestarting {
		time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)
		cmd := exec.Command("./test/restart.sh")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Start()
		if err != nil {
			log.Println("SIGHUP ERROR:", err)
		}
		err = cmd.Wait()
		if err != nil {
			log.Println("SIGHUP ERROR:", err)
		}
	}
}

func main() {
	log.SetFlags(0)
	// check for ab - pretty hacky...
	out, _ := exec.Command("which", "ab").Output()
	if len(out) == 0 {
		log.Fatalln("DEPEDENCY ERROR: cant find ab (apache bench); not running test")
	}

	compileDone := make(chan struct{}, 1)
	testSteps := sync.WaitGroup{}
	testSteps.Add(2)
	go func() {
		compileAndStartTestServer(compileDone)
		testSteps.Done()
	}()

	time.Sleep(time.Second * 1)

	go func() {
		<-compileDone
		defer killTestServer()
		keepRestarting = true
		go keepRestartingServer()
		runApacheBench()

		keepRestarting = false
		stopTestServer()
		testSteps.Done()
	}()

	testSteps.Wait()
}
