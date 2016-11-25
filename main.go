// Package main implements the nicer command line tool.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/geo-data/nicer/sample"
	"github.com/geo-data/nicer/threshold"
)

// These get set on build.
var (
	version string = "No version information,"
	commit  string = "unknown"
)

func main() {
	var (
		tcpu       = flag.Float64("cpu-threshold", 90, "percentage of cpu usage above which commands will not be executed")
		tram       = flag.Float64("ram-threshold", 10, "percentage of free memory below which commands will not be executed")
		tload      = flag.Float64("load-threshold", (90.0/100.0)*float64(runtime.NumCPU()), "1 minute load average above which commands will not be executed")
		interval   = flag.Duration("interval", time.Second*1, "sampling interval for resource metrics")
		wait       = flag.Duration("wait", time.Second*1, "duration to wait between issuing commands. Used when there is no wait on resource thresholds")
		cin        = flag.String("input", "", "file location to read commands from. Defaults to STDIN.")
		cout       = flag.String("stdout", "", "file location to send command standard output to. Defaults to STDOUT.")
		cerr       = flag.String("stderr", "", "file location to send command standard error to. Defaults to STDERR.")
		v          = flag.Bool("v", false, "print version information and exit.")
		fout, ferr io.WriteCloser
		fin        io.Reader
	)

	flag.Parse()

	if *v {
		fmt.Printf("%s commit=%s\n", version, commit)
		os.Exit(0)
	}

	if len(*cin) > 0 {
		fh, err := os.Open(*cin)
		if err != nil {
			log.Fatal(err)
		}
		defer fh.Close()
		fin = fh
	} else {
		fin = os.Stdin
	}

	if len(*cout) > 0 {
		fh, err := os.Create(*cout)
		if err != nil {
			log.Fatal(err)
		}
		defer fh.Close()
		fout = fh
	} else {
		fout = os.Stdout
	}

	if len(*cerr) > 0 {
		if *cerr == *cout {
			ferr = fout
		} else {
			fh, err := os.Create(*cerr)
			if err != nil {
				log.Fatal(err)
			}
			defer fh.Close()
			ferr = fh
		}
	} else {
		ferr = os.Stderr
	}

	t := threshold.NewThresholdGroup(
		threshold.NewThreshold("cpu", sample.NewCPUSampler(), float32(*tcpu), true),
		threshold.NewThreshold("ram", sample.MemorySampler, float32(*tram), false),
		threshold.NewThreshold("load", sample.LoadAvg1MinSampler, float32(*tload), true),
	)

	ctx, cancel := context.WithCancel(context.Background())
	catchSignals(cancel)

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

	scanner := bufio.NewScanner(fin)
	commands := make(chan string, 5)
	go func() {
		defer close(commands)

		for scanner.Scan() {
			cmd := scanner.Text()
			if len(cmd) > 0 {
				commands <- cmd
			}
		}
		if err := scanner.Err(); err != nil {
			log.Println("reading standard input:", err)
		}
	}()

	runCommands(ctx, commands, t, *wait, fout, ferr)
}

func runCommands(
	ctx context.Context,
	commands <-chan string,
	t *threshold.ThresholdGroup,
	wait time.Duration,
	fout, ferr io.Writer,
) {
	var wg sync.WaitGroup

	i := 0
Process:
	for {
		select {
		case <-ctx.Done():
			break Process
		case cmdText, ok := <-commands:
			if !ok {
				break Process
			}

			i++
			if t.Exceeded() {
				log.Println("waiting")
				t.Wait()
			} else if i > 1 {
				time.Sleep(wait)
				if t.Exceeded() {
					log.Println("waiting")
					t.Wait()
				}
			}

			wg.Add(1)
			go func(index int) {
				defer wg.Done()

				cmd, stdout, stderr, err := startCommand(ctx, cmdText)
				if err != nil {
					log.Println("failed to start command", cmdText, err)
					return
				}

				pid := cmd.Process.Pid
				log.Printf("nicer %d executing pid %d: %s", index, pid, cmdText)

				scanOut := bufio.NewScanner(stdout)
				scanErr := bufio.NewScanner(stderr)

				var swg sync.WaitGroup
				swg.Add(2)
				go func() {
					defer swg.Done()
					scan(scanOut, fout, pid)
				}()
				go func() {
					defer swg.Done()
					scan(scanErr, ferr, pid)
				}()
				swg.Wait()
				if err := scanOut.Err(); err != nil {
					log.Println("scanning stdout for pid", pid, err)
				}
				if err := scanErr.Err(); err != nil {
					log.Println("scanning stderr for pid", pid, err)
				}

				if err := cmd.Wait(); err != nil {
					log.Printf("command pid %d failed: %s", pid, err)
				} else {
					log.Printf("command pid %d succeeded", pid)
				}

			}(i)
		}
	}

	wg.Wait()
}

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

func startCommand(ctx context.Context, cmdStr string) (cmd *exec.Cmd, stdout, stderr io.ReadCloser, err error) {
	cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	if stdout, err = cmd.StdoutPipe(); err != nil {
		return
	}
	if stderr, err = cmd.StderrPipe(); err != nil {
		return
	}
	if err = cmd.Start(); err != nil {
		return
	}
	return
}

func scan(s *bufio.Scanner, w io.Writer, pid int) {
	for s.Scan() {
		fmt.Fprintf(w, "nicer %d: %s\n", pid, s.Text())
	}
}
