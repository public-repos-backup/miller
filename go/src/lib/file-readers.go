// ================================================================
// Wrapper for os.Open which maps string filename to *os.File, which in turn
// implements io.ReadCloser, and optional in turn wrapping that in a
// gzip/zlib/bunzip2 reader. Shared across record-readers for all the various
// input-file formats (CSV, JSON, XTAB, DKVP, NIDX, PPRINT) which Miller
// supports.
//
// There are two ways of handling compressed data in the Miller Go port:
//
// * A user-specified 'prepipe' command such as 'gunzip', where we popen a
//   process, hand it the filename via '< filename', and read from that pipe;
//
// * An indication to use an in-process encoding reader (gzip or bzip2, etc).
//
// If a prepipe is specified, it is used; else if an encoding is specified, it
// is used; otherwise the file suffix (.bz2, .gz, .z) is consulted; otherwise
// the file is treated as text.
// ================================================================

package lib

import (
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"compress/zlib"
	"io"
	"os"
	"strings"
)

type TFileInputEncoding int

const (
	FileInputEncodingDefault TFileInputEncoding = iota
	FileInputEncodingBzip2
	FileInputEncodingGzip
	FileInputEncodingZlib
)

// OpenFileForRead: If prepipe is non-empty, popens "{prepipe} < {filename}"
// and returns a handle to that where prepipe is nominally things like
// "gunzip", "cat", etc.  Otherwise, delegates to an in-process reader which
// can natively handle gzip/bzip2/zlib depending on the specified encoding.  If
// the encoding isn't a compression encoding, this ends up being simply
// os.Open.
func OpenFileForRead(
	filename string,
	prepipe string,
	encoding TFileInputEncoding, // ignored if prepipe is non-empty
) (io.ReadCloser, error) {
	if prepipe != "" {
		return openPrepipedHandleForRead(filename, prepipe)
	} else {
		handle, err := os.Open(filename)
		if err != nil {
			return nil, err
		}
		return openEncodedHandleForRead(handle, encoding, filename)
	}
}

// OpenStdin: if prepipe is non-empty, popens "{prepipe}" and returns a handle
// to that where prepipe is nominally things like "gunzip", "cat", etc.
// Otherwise, delegates to an in-process reader which can natively handle
// gzip/bzip2/zlib depending on the specified encoding.  If the encoding isn't
// a compression encoding, this ends up being simply os.Stdin.
func OpenStdin(
	prepipe string,
	encoding TFileInputEncoding, // ignored if prepipe is non-empty
) (io.ReadCloser, error) {
	if prepipe != "" {
		return openPrepipedHandleForRead("", prepipe)
	} else {
		return openEncodedHandleForRead(os.Stdin, encoding, "")
	}
}

func openPrepipedHandleForRead(
	filename string,
	prepipe string,
) (io.ReadCloser, error) {
	escapedFilename := escapeFileNameForPopen(filename)

	var command string
	if filename == "" { // stdin
		command = prepipe
	} else {
		command = prepipe + " < " + escapedFilename
	}

	return OpenInboundHalfPipe(command)
}

// Avoids shell-injection cases by replacing single-quote with backslash
// single-quote and double-quote with backslack double-quote, then wrapping the
// entire result in initial and final single-quote.
//
// TODO: test on Windows. Maybe needs move to src/platform.
func escapeFileNameForPopen(filename string) string {
	var buffer bytes.Buffer
	foundQuote := false
	for _, c := range filename {
		if c == '\'' || c == '"' {
			buffer.WriteRune('\'')
			buffer.WriteRune(c)
			buffer.WriteRune('\'')
		} else {
			buffer.WriteRune(c)
		}
	}
	if foundQuote {
		return "'" + buffer.String() + "'"
	} else {
		return buffer.String()
	}
}

// TODO: comment
func openEncodedHandleForRead(
	handle *os.File,
	encoding TFileInputEncoding,
	filename string,
) (io.ReadCloser, error) {
	switch encoding {
	case FileInputEncodingBzip2:
		return NewBZip2ReadCloser(handle), nil
		break
	case FileInputEncodingGzip:
		return gzip.NewReader(handle)
		break
	case FileInputEncodingZlib:
		return zlib.NewReader(handle)
		break
	}

	InternalCodingErrorIf(encoding != FileInputEncodingDefault)

	if strings.HasSuffix(filename, ".bz2") {
		return NewBZip2ReadCloser(handle), nil
	}
	if strings.HasSuffix(filename, ".gz") {
		return gzip.NewReader(handle)
	}
	if strings.HasSuffix(filename, ".z") {
		return zlib.NewReader(handle)
	}

	// Pass along os.Stdin or os.Open(filename)
	return handle, nil
}

// ----------------------------------------------------------------
// BZip2ReadCloser remedies the fact that bzip2.NewReader does not implement io.ReadCloser.
type BZip2ReadCloser struct {
	originalHandle *os.File
	bzip2Handle    io.Reader
}

func NewBZip2ReadCloser(handle *os.File) *BZip2ReadCloser {
	return &BZip2ReadCloser{
		originalHandle: handle,
		bzip2Handle:    bzip2.NewReader(handle),
	}
}

func (rc *BZip2ReadCloser) Read(p []byte) (n int, err error) {
	return rc.bzip2Handle.Read(p)
}

func (rc *BZip2ReadCloser) Close() error {
	return rc.originalHandle.Close()
}

// ----------------------------------------------------------------
// Reading past end of files opened with os.Open returns the error which is
// io.EOF. Reading past close of pipes opened with popen (e.g. Miller's
// prepipe, where the file isn't 'foo.dat' but rather the process 'gunzip <
// foo.dat |') returns not io.EOF but an error with 'file already closed'
// within it. See also
// https://stackoverflow.com/questions/47486128/why-does-io-pipe-continue-to-block-even-when-eof-is-reached
func IsEOF(err error) bool {
	if err == nil {
		return false
	} else if err == io.EOF {
		return true
	} else if strings.Contains(err.Error(), "file already closed") {
		return true
	} else {
		return false
	}
}