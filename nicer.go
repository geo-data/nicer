// Package main implements the nicer command line tool.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/geo-data/nicer/batch"
	"github.com/geo-data/nicer/sample"
	"github.com/geo-data/nicer/streams"
	"github.com/geo-data/nicer/threshold"
)

// These get set on build.
var (
	version string = "No version information,"
	commit  string = "unknown"
)

func main() {
	var (
		// Define the CLI options.
		tcpu     = flag.Float64("cpu-threshold", 90, "percentage of cpu usage above which commands will not be executed")
		tram     = flag.Float64("ram-threshold", 10, "percentage of free memory below which commands will not be executed")
		tload    = flag.Float64("load-threshold", (90.0/100.0)*float64(runtime.NumCPU()), "1 minute load average above which commands will not be executed")
		interval = flag.Duration("interval", time.Second*1, "sampling interval for resource metrics")
		wait     = flag.Duration("wait", time.Second*1, "duration to wait between issuing commands. Used when there is no wait on resource thresholds")
		cin      = flag.String("input", "", "file location to read commands from. Defaults to STDIN.")
		cout     = flag.String("stdout", "", "file location to send command standard output to. Defaults to STDOUT.")
		cerr     = flag.String("stderr", "", "file location to send command standard error to. Defaults to STDERR.")
		v        = flag.Bool("version", false, "print version information and exit.")
	)

	flag.Parse()

	// Print version information if required.
	if *v {
		fmt.Printf("%s commit=%s\n", version, commit)
		os.Exit(0)
	}

	// Open the streams for reading commands from and writing command output to.
	s, err := streams.New(*cin, *cout, *cerr)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			log.Println(err)
		}
	}()

	// Handle interrupt signals. These will be passed on to child commands via
	// the context.
	ctx, cancel := context.WithCancel(context.Background())
	catchSignals(cancel)

	// Define the system metric tolerances that should be maintained.
	t := threshold.NewGroup(
		threshold.New("cpu", sample.NewCPU(), float32(*tcpu), true),
		threshold.New("ram", sample.Memory, float32(*tram), false),
		threshold.New("load", sample.LoadAvg1Min, float32(*tload), true),
	)

	// Start measuring system metrics against the thresholds.
	t.Poll(ctx, *interval,
		func(name string, value float32, exceeded bool) {
			if exceeded {
				log.Printf("%s threshold exceeded: %v", name, value)
			} else {
				log.Printf("%s threshold ok: %v", name, value)
			}
		},
		func(name string, err error) {
			log.Println(name, err)
		},
	)

	// Start reading commands from the input, one command per line.
	scanner := bufio.NewScanner(s.In)
	cmds := make(chan string, 5)
	go func() {
		defer close(cmds)

		for scanner.Scan() {
			cmdStr := scanner.Text()
			if len(cmdStr) > 0 {
				cmds <- cmdStr
			}
		}
		if err := scanner.Err(); err != nil {
			log.Fatal(err)
		}
	}()

	// Handle the various events during command execution.
	b := batch.New(t, *wait, &batch.Events{
		Waiting: func() {
			log.Println("waiting...")
		},
		CmdStarted: func(count, pid int, cmd string) {
			log.Printf("job %d pid %d started: %s", count, pid, cmd)
		},
		CmdFinished: func(count, pid int, cmd string, err error) {
			if err != nil {
				log.Printf("job %d pid %d failed: %s", count, pid, err)
			} else {
				log.Printf("job %d pid %d succeeded", count, pid)
			}
		},
		CmdStdout: func(count, pid int, cmd string, line []byte) {
			fmt.Fprintf(s.Out, "job %d pid %d stdout: %s\n", count, pid, string(line))
		},
		CmdStderr: func(count, pid int, cmd string, line []byte) {
			fmt.Fprintf(s.Err, "job %d pid %d stderr: %s\n", count, pid, string(line))
		},
		CmdFailed: func(count int, cmd string, err error) {
			log.Printf("job %d failed to start command %s\n", cmd, err)
		},
		StdoutErr: func(count, pid int, cmd string, err error) {
			log.Printf("job %d pid %d error scanning stdout: %s\n", count, pid, err)
		},
		StderrErr: func(count, pid int, cmd string, err error) {
			log.Printf("job %d pid %d error scanning stderr: %s\n", count, pid, err)
		},
	})

	// Run the input commands.
	b.Run(ctx, cmds)
}

// catchSignals cancels the context whenever an interrupt is received.
func catchSignals(cancel context.CancelFunc) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		for sig := range c {
			log.Printf("received %s signal", sig.String())
			cancel()
		}
	}()
}
