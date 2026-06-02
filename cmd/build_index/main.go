package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/rlevidev/rinha-2026/internal/index"
)

const (
	maxRefs     = 3_500_000
	kmeansIters = 20
)

func readInput(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	}
	return raw, nil
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: build_index <input.json[.gz]> <output.bin> <tag 0..15>")
		os.Exit(1)
	}
	inPath, outPath := os.Args[1], os.Args[2]
	tag, err := strconv.Atoi(os.Args[3])
	if err != nil || tag < 0 || tag >= index.NPartitions {
		fmt.Fprintln(os.Stderr, "build_index: tag must be 0..15")
		os.Exit(1)
	}

	log := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "build_index[p%d]: "+format+"\n", append([]any{tag}, a...)...)
	}
	t0 := time.Now()

	buf, err := readInput(inPath)
	if err != nil {
		log("failed to read input: %v", err)
		os.Exit(1)
	}
	log("read %d MB of corpus", len(buf)>>20)

	refs, err := index.ParseRefs(buf, maxRefs)
	if err != nil {
		log("parse error: %v", err)
		os.Exit(1)
	}
	log("parsed %d refs (%.1fs)", len(refs), time.Since(t0).Seconds())

	refs = index.FilterByTag(refs, tag)
	log("%d refs match tag %d", len(refs), tag)
	if len(refs) == 0 {
		log("no refs for this partition — aborting")
		os.Exit(1)
	}

	k := len(refs) / 300
	if k < 64 {
		k = 64
	}
	if k > index.NClusters {
		k = index.NClusters
	}
	log("K = %d clusters (%d vec/cluster)", k, len(refs)/k)

	tk := time.Now()
	cent, assignments := index.KMeans(refs, k, kmeansIters)
	log("k-means done (%.1fs)", time.Since(tk).Seconds())

	offsets, order := index.CountingSortByCluster(assignments, k)
	index.SortWithinClusters(refs, cent, assignments, offsets, order)
	bboxMin, bboxMax, pairArr, labels := index.BBoxPack(refs, order, offsets, k)

	if err := index.WriteIndexBin(outPath, len(refs), offsets, bboxMin, bboxMax, pairArr, labels); err != nil {
		log("write error: %v", err)
		os.Exit(1)
	}
	log("wrote %s (total %.1fs)", outPath, time.Since(t0).Seconds())
}
