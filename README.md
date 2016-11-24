# Nicer

Nicer is a command line tool designed to concurrently execute shell commands on
Linux systems once various system resource metrics fall within acceptable
tolerances.  As such it acts as a rate limiter for commands, delaying their
execution until resources become available and thereby reducing the chances of
the entire system becoming overloaded.  This makes it particularly suited to
managing queued tasks.

Metrics currently assessed are: CPU usage; memory usage and the 1 minute system
load metrics.

## Example

The following shell command simulates a set of 100 concurrent CPU bound tasks
taking between 5 and 50 seconds for each task:

```
for i in {1..100}
do
    echo "timeout $(shuf -i 5-50 -n 1 -z)s nice -20 sha1sum /dev/zero; echo finished" | sh &
done
```

The problem with this is that it risks hogging the CPU at the expense of other
concurrent tasks that may also be running.  Refactoring this command a little
and running it through `nicer` ensures that the CPU resources should not be
monopolised and the system load should remain tolerable whilst still running as
many commands in parallel as possible:

```
for i in {1..100}
do
    echo "timeout $(shuf -i 5-50 -n 1 -z)s nice -20 sha1sum /dev/zero; echo finished"
done | nicer
```

## Usage

```
$ nicer -help
Usage of nicer:
  -cpu-threshold float
        percentage of cpu usage above which commands will not be executed (default 90)
  -input string
        file location to read commands from. Defaults to STDIN.
  -interval duration
        sampling interval for resource metrics (default 1s)
  -load-threshold float
        1 minute load average above which commands will not be executed (default 7.2)
  -ram-threshold float
        percentage of free memory below which commands will not be executed (default 10)
  -stderr string
        file location to send command standard error to. Defaults to STDERR.
  -stdout string
        file location to send command standard output to. Defaults to STDOUT.
  -wait duration
        duration to wait between issuing commands. Used when there is no wait on resource thresholds (default 1s)

```

Note that the `-load-threshold` default will vary based on the host system
capabilities.

## Limitations

`nicer` performs no management of the commands it runs, other than passing on
any interrupt signals it receives.  As an example of the implications, `nicer`
may run multiple commands in parallel, having ensured that system resources were
within specified limits **at the time** the commands were executed.  However, if
these commands use resources irregularly, for example making heavy use of the
CPU some time after execution, there is a chance that resources will become
overloaded.  The risk of this happening is application dependent but the `-wait`
flag may be of use in certain circumstances in delaying execution between one
command and the next, giving the previous command time to hit its stride.

## Installation

### From source

Install [Go](https://golang.org/) and simply:

```
go get github.com/geo-data/nicer
```

This should install the `nicer` binary under `$GOPATH/bin`.

## Developing

Typing `make dev` from the project root builds a development Docker image and
runs a container, placing you at a command prompt within this container.  This
uses [Docker Compose](https://docs.docker.com/compose/), so ensure you have it
installed.

The project root is bind mounted to the current working directory in the
container allowing you to edit files on the host and run `make` commands within
the container. The main command you'll probably use is `make rebuild` which
builds and runs the the project using
[Realize](https://tockins.github.io/realize/).  This provides live building and
reloading of the `nicer` binary whenever source files change.

# License

MIT - See the file `LICENSE` for details.
