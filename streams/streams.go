// Package streams provides an object for managing associated input, output and
// error streams.  It's useful at the application level for managing STDIN,
// STDOUT and STDERR.
package streams

import (
	"io"
	"os"
)

// Streams is composed of input, output and error streams.
type Streams struct {
	In       io.ReadCloser  // Input stream.
	Out, Err io.WriteCloser // Output and error streams.
}

// New instantiates a Streams object, opening files referenced by the input,
// output and error arguments.  Existing output files will be overwritten. If
// any argument is an empty string then the appropriate STDIN, STDOUT or STDERR
// stream is used instead.  An error is returned if a file cannot be opened.
func New(input, output, error string) (s *Streams, err error) {
	r := new(Streams)

	// Set the input stream.
	if len(input) > 0 {
		r.In, err = os.Open(input)
		if err != nil {
			return
		}
	} else {
		r.In = os.Stdin // Use STDIN by default.
	}

	// Set the output stream.
	if len(output) > 0 {
		r.Out, err = os.Create(output)
		if err != nil {
			return
		}
	} else {
		r.Out = os.Stdout // Use STDOUT by default.
	}

	// Set the error stream.
	if len(error) > 0 {
		if error == output {
			r.Err = r.Out // Don't duplicate the stream.
		} else {
			r.Err, err = os.Create(error)
			if err != nil {
				return
			}
		}
	} else {
		r.Err = os.Stderr // Use STDERR by default.
	}

	s = r
	return
}

// Close implements io.Closer and closes any streams that were originally opened
// (i.e. anything that isn't STDIN, STDOUT or STDERR).
func (s *Streams) Close() (err error) {
	// close c, assigning any error to the return variable if not already set.
	close := func(c io.Closer) {
		e := c.Close()
		if err == nil {
			err = e
		}
	}

	// Close the input stream.
	if s.In != os.Stdin {
		defer close(s.In)
	}

	// Close the output stream.
	if s.Out != os.Stdout {
		defer close(s.Out)
	}

	// Close the error stream.
	if s.Err != os.Stderr && s.Err != s.Out {
		defer close(s.Err)
	}

	return
}
