package zpaq_test

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"strings"

	zpaq "github.com/morsuning/zpaq-go"
)

func Example() {
	input := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog\n", 200))

	archive, err := zpaq.Compress(nil, input, zpaq.WithLevel(zpaq.Default), zpaq.WithName("fox.txt"))
	if err != nil {
		log.Fatal(err)
	}
	restored, err := zpaq.Decompress(nil, archive)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(bytes.Equal(input, restored), len(archive) < len(input)/20)
	// Output: true true
}

func ExampleNewWriter() {
	var archive bytes.Buffer
	w := zpaq.NewWriter(&archive, zpaq.WithLevel(zpaq.Fast), zpaq.WithName("log.txt"))
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}

	r := zpaq.NewReader(&archive)
	defer r.Close()
	text, err := io.ReadAll(r)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(text), strings.HasSuffix(string(text), "line 999\n"))
	// Output: 8890 true
}

func ExampleWalk() {
	var archive []byte
	for _, name := range []string{"a.txt", "b.txt"} {
		var err error
		archive, err = zpaq.Compress(archive, []byte("contents of "+name), zpaq.WithName(name), zpaq.WithLevel(zpaq.Store))
		if err != nil {
			log.Fatal(err)
		}
	}
	err := zpaq.Walk(bytes.NewReader(archive), func(name, comment string, data []byte) error {
		fmt.Printf("%s: %q\n", name, data)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	// Output:
	// a.txt: "contents of a.txt"
	// b.txt: "contents of b.txt"
}

func ExampleWithBlockSize() {
	data := bytes.Repeat([]byte("0123456789"), 5000)
	archive, err := zpaq.Compress(nil, data, zpaq.WithBlockSize(10_000), zpaq.WithLevel(zpaq.Fast))
	if err != nil {
		log.Fatal(err)
	}
	blocks := 0
	zpaq.Walk(bytes.NewReader(archive), func(string, string, []byte) error { blocks++; return nil })
	fmt.Println(blocks)
	// Output: 5
}
