package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/rlevidev/rinha-2026/internal/dataset"
	"github.com/rlevidev/rinha-2026/internal/search"
)

func main() {
	inputPath := flag.String("input", "", "Path to references.json.gz")
	outputPath := flag.String("output", "", "Path to save index.bin")
	m := flag.Int("m", 8, "HNSW M parameter (max neighbors per node)")
	efConstruct := flag.Int("ef-construct", 200, "HNSW EfConstruct parameter")
	ef := flag.Int("ef", 10, "HNSW Ef parameter (search width)")
	flag.Parse()

	if *inputPath == "" || *outputPath == "" {
		flag.Usage()
		os.Exit(1)
	}

	start := time.Now()
	log.Printf("Loading dataset from %s...", *inputPath)
	refs, err := dataset.Load(*inputPath)
	if err != nil {
		log.Fatalf("Failed to load dataset: %v", err)
	}
	log.Printf("Loaded %d references in %v", len(refs), time.Since(start))

	start = time.Now()
	log.Printf("Building HNSW index (M=%d, efConstruct=%d)...", *m, *efConstruct)

	// Ef padrão para 50, mas pode ser alterado posteriormente ao carregar o índice para busca.
	// A capacidade é definida como len(refs) para pré-alocar memória.
	hnsw := search.New(len(refs), *m, *efConstruct, *ef)

	for i, ref := range refs {
		hnsw.Insert(ref.Vector, ref.IsFraud)
		if (i+1)%100000 == 0 {
			log.Printf("Inserted %d/%d nodes", i+1, len(refs))
		}
	}
	log.Printf("Index built in %v. Total nodes: %d", time.Since(start), len(refs))

	start = time.Now()
	log.Printf("Saving index to %s...", *outputPath)
	if err := hnsw.SaveBinary(*outputPath); err != nil {
		log.Fatalf("Failed to save index: %v", err)
	}
	log.Printf("Index saved in %v", time.Since(start))
}
