package dataset

import (
	"compress/gzip"
	"encoding/json"
	"os"
)

type Reference struct {
	// 3 milhões de registros ocupam centenas de MB. Qualquer byte economizado aqui é crucial para não estourar os limites.
	// Por isso, usamos float32 em vez de float64.
	Vector  [14]float32
	IsFraud bool
}

// Load carrega o dataset em streaming (não traz 284MB de JSON para memória)
func Load(path string) ([]Reference, error) {

	// Em vez de ler o arquivo .json.gz inteiro para a memória e depois descomprimir, usamos gzip.NewReader em conjunto com json.NewDecoder.
	// Isso permite ler o arquivo "pedaço por pedaço" (streaming) conforme o processador solicita, sem carregar o arquivo bruto de 284MB (estimado) de uma vez só.

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	dec := json.NewDecoder(gz)
	dec.Token() // consome o '[' inicial para não ler o array como um único token

	// Otimização de memória
	// A função make com capacity=3_000_000 reserva uma bloco contíguo de memória para 3 milhões de elementos logo no início.
	// Isso evita alocações dinâmicas e fragmentação de memória.
	refs := make([]Reference, 0, 3_000_000)

	// Loop de processamento
	for dec.More() { // Verifica se ainda existem itens no array JSON
		// stuct temporária para fazer o decode do JSON
		var raw struct {
			Vector [14]float32 `json:"vector"`
			Label  string      `json:"label"`	// "fraud" ou "Legit"
		}
		
		// Decodifica o JSON para a struct temporária
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}

		// Passa os dados para Reference
		ref := Reference{
			Vector: raw.Vector,
			IsFraud: raw.Label == "fraud", // Converte a string "fraud" (que ocupa vários bytes) para um simples bool (1 byte)
		}
		
		refs = append(refs, ref)
	}

	return refs, nil
}
