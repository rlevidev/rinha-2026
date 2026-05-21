package dataset

import (
	"compress/gzip"
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	// Define o conteudo do JSON que o loader espera.
	content := `[
		{
			"vector": [0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0, 0.1, 0.2, 0.3, 0.4],
			"label": "fraud"
		},
		{
			"vector": [0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0],
			"label": "legit"
		}
	]`

	// Cria um arquivo temporario.
	tmpFile, err := os.CreateTemp("", "test-dataset-*.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// Escreve o conteudo no arquivo temporario.
	gzWriter := gzip.NewWriter(tmpFile)
	gzWriter.Write([]byte(content))
	gzWriter.Close()
	tmpFile.Close()

	// Executa o Load
	refs, err := Load(tmpFile.Name())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Garente que o loader leu tudo o que deveria.
	if len(refs) != 2 {
		t.Errorf("Expected 2 references, got %d", len(refs))
	}

	// Esperava true para o primeiro vetor (fraud)
	if !refs[0].IsFraud {	// "Se não for fraude..."
		t.Errorf("Expected first ref to be fraud")
	}

	// Esperava false para o segundo vetor (legit)
	if refs[1].IsFraud {	// "Se for fraude..."
		t.Errorf("Expected second ref to be legit")
	}
}
