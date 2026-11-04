// Command zpaq-go compresses and decompresses ZPAQ streaming-format data.
//
//	zpaq-go c [-level N] [-name NAME] [-block MiB] [-j N] [-o OUT] [FILE]
//	zpaq-go d [-o OUT] [FILE]
//	zpaq-go l [FILE]
//	zpaq-go t [FILE]
//
// With no FILE (or "-") input is read from standard input. Compressed output
// is written to standard output unless -o is given. The archives are plain
// ZPAQ level-2 streaming blocks, so they can also be read by the reference
// zpaq program ("zpaq x archive.zpaq").
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	zpaq "github.com/morsuning/zpaq-go"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `usage: zpaq-go <command> [flags] [FILE]

commands:
  c   compress FILE (or stdin) into a ZPAQ streaming archive
  d   decompress a ZPAQ streaming archive and write the contents
  l   list the segments (name, size, comment) of an archive
  t   test an archive: decode everything and verify the checksums

flags (c): -level 0..5 (default 4)  -name NAME  -block MiB  -j N  -o OUT
flags (d): -o OUT  -skip-garbage
`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "c", "compress":
		err = cmdCompress(rest, stdin, stdout, stderr)
	case "d", "decompress":
		err = cmdDecompress(rest, stdin, stdout, stderr)
	case "l", "list":
		err = cmdList(rest, stdin, stdout, stderr)
	case "t", "test":
		err = cmdTest(rest, stdin, stdout, stderr)
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "zpaq-go: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(stderr, "zpaq-go: %v\n", err)
			return 2
		}
		fmt.Fprintf(stderr, "zpaq-go: %v\n", err)
		return 1
	}
	return 0
}

type usageError string

func (e usageError) Error() string { return string(e) }

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("zpaq-go "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parse handles flags that may appear before or after the single FILE
// argument and returns that argument ("" for stdin).
func parse(fs *flag.FlagSet, args []string) (string, error) {
	var file string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return "", err
			}
			return "", usageError(err.Error())
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		if file != "" {
			return "", usageError("too many arguments")
		}
		file, args = args[0], args[1:]
	}
	if file == "-" {
		file = ""
	}
	return file, nil
}

func openIn(file string, stdin io.Reader) (io.Reader, func(), error) {
	if file == "" {
		return stdin, func() {}, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

// openOut returns the destination writer. A named output is written to a
// temporary file in the same directory and renamed on commit, so a failed
// run never leaves a truncated result behind.
func openOut(path string, stdout io.Writer) (w io.Writer, commit func() error, abort func(), err error) {
	if path == "" || path == "-" {
		bw := bufio.NewWriterSize(stdout, 1<<16)
		return bw, bw.Flush, func() {}, nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".zpaq-go-*")
	if err != nil {
		return nil, nil, nil, err
	}
	bw := bufio.NewWriterSize(f, 1<<16)
	commit = func() error {
		if err := bw.Flush(); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Chmod(f.Name(), 0o644); err != nil {
			return err
		}
		return os.Rename(f.Name(), path)
	}
	abort = func() { f.Close(); os.Remove(f.Name()) }
	return bw, commit, abort, nil
}

func cmdCompress(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := newFlags("c", stderr)
	level := fs.Int("level", int(zpaq.Default), "compression level 0 (store) .. 5 (best)")
	name := fs.String("name", "", "segment name stored in the archive (default: input file name)")
	block := fs.Int("block", 0, "block size in MiB (default: library default)")
	jobs := fs.Int("j", 0, "number of blocks compressed in parallel (default: number of CPUs)")
	out := fs.String("o", "", "output file (default: stdout)")
	file, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *level < 0 || *level > 5 {
		return usageError("-level must be between 0 and 5")
	}
	if *block < 0 || *jobs < 0 {
		return usageError("-block and -j must not be negative")
	}
	if *name == "" && file != "" {
		*name = filepath.Base(file)
	}
	in, closeIn, err := openIn(file, stdin)
	if err != nil {
		return err
	}
	defer closeIn()
	w, commit, abort, err := openOut(*out, stdout)
	if err != nil {
		return err
	}
	opts := []zpaq.Option{zpaq.WithLevel(zpaq.Level(*level)), zpaq.WithName(*name)}
	if *block > 0 {
		opts = append(opts, zpaq.WithBlockSize(*block<<20))
	}
	if *jobs > 0 {
		opts = append(opts, zpaq.WithConcurrency(*jobs))
	}
	zw := zpaq.NewWriter(w, opts...)
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		abort()
		return err
	}
	if err := zw.Close(); err != nil {
		abort()
		return err
	}
	if err := commit(); err != nil {
		abort()
		return err
	}
	return nil
}

func cmdDecompress(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := newFlags("d", stderr)
	out := fs.String("o", "", "output file (default: stdout)")
	skip := fs.Bool("skip-garbage", false, "skip bytes between blocks instead of failing")
	file, err := parse(fs, args)
	if err != nil {
		return err
	}
	in, closeIn, err := openIn(file, stdin)
	if err != nil {
		return err
	}
	defer closeIn()
	w, commit, abort, err := openOut(*out, stdout)
	if err != nil {
		return err
	}
	zr := zpaq.NewReader(bufio.NewReaderSize(in, 1<<16), zpaq.WithSkipGarbage(*skip))
	defer zr.Close()
	if _, err := io.Copy(w, zr); err != nil {
		abort()
		return err
	}
	if err := commit(); err != nil {
		abort()
		return err
	}
	return nil
}

func cmdList(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := newFlags("l", stderr)
	file, err := parse(fs, args)
	if err != nil {
		return err
	}
	in, closeIn, err := openIn(file, stdin)
	if err != nil {
		return err
	}
	defer closeIn()
	var n int
	var total int64
	err = zpaq.Walk(bufio.NewReader(in), func(name, comment string, data []byte) error {
		n++
		total += int64(len(data))
		fmt.Fprintf(stdout, "%12d  %-24s  %s\n", len(data), quote(name), quote(comment))
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%d segment(s), %d bytes\n", n, total)
	return nil
}

func cmdTest(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := newFlags("t", stderr)
	file, err := parse(fs, args)
	if err != nil {
		return err
	}
	in, closeIn, err := openIn(file, stdin)
	if err != nil {
		return err
	}
	defer closeIn()
	var n int
	var total int64
	err = zpaq.Walk(bufio.NewReader(in), func(_, _ string, data []byte) error {
		n++
		total += int64(len(data))
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "OK: %d segment(s), %d bytes\n", n, total)
	return nil
}

func quote(s string) string {
	if s == "" {
		return "-"
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}
