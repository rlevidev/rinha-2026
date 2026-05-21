package vectorizer

import "time"

// É um "tradutor" que transforma dados brutos (que variam muito em escala) em um formato uniforme e comparável.
type Normalizer struct {
	MaxAmount        float32			// Valor máximo de transação observado nos dados de treinamento. Usado para normalizar o valor da transação (v[0])
	MaxInstallments  float32			// Número máximo de parcelas observado. Usado para normalizar o número de parcelas (v[1])
	AmountVsAvgRatio float32			// Razão máxima entre valor da transação e média do cliente. Usado para normalizar a razão (v[2])
	MaxMinutes       float32			// Tempo máximo em minutos entre transações consecutivas. Usado para normalizar o tempo desde a última transação (v[5])
	MaxKm            float32			// Distância máxima em km observada. Usado para normalizar a distância do terminal (v[7])
	MaxTxCount24h    float32			// Número máximo de transações em 24h observado. Usado para normalizar o contador de transações recentes (v[8])
	MaxMerchantAvg   float32			// Média máxima de transações por estabelecimento. Usado para normalizar o risco do estabelecimento (v[13])
	MccRisk          map[string]float32	// Mapa de risco por MCC (código de categoria do estabelecimento). Usado para normalizar o risco do MCC (v[12])
}

// Mapeamento direto do JSON de entrada que a API recebe a cada requisição de score de fraude.
// Cada campo representa exatamente as informações necessárias para que o Vectorize transforme os dados em um vetor de 14 dimensões.
type Transaction struct {
	Amount        float32		// Valor total da transação. É usado na dimensão v[0] (normalizado pelo MaxAmount)
	Installments  int			// Quantidade de parcelas. É usado na dimensão v[1]
	RequestedAt   string		// Data e hora da transação. Essencial para extrair a hora do dia (v[3]) e o dia da semana (v[4])

	// Informações do cliente
	Customer struct {
		AvgAmount float32		// Média de valor das transações anteriores do cliente. Usado na dimensão v[2] (razão do valor atual vs média)
		TxCount24h int			// Quantas transações o cliente fez nas últimas 24 horas. Usado na dimensão v[8]
		KnowMerchants []string	// Lista de IDs de estabelecimentos que o cliente já fez transações. Usado na dimensão v[11] (se o estabelecimento atual é conhecido ou não)
	}
	
	// Onde ocorre a transação
	Terminal struct {
		KmFromHome float64		// Distância do local da transação até a casa do cliente. Usado na dimensão v[7] 
		IsOnline bool			// Identifica se é uma compra online ou física. Usado na dimensão v[9]
		CardPresent bool		// Identifica se o cartão físico estava presente. Usado na dimensão v[10]
	}
	
	// Histório recente
	LastTransaction *struct {
		Timestamp string		// Data da última transação. Comparado com RequestedAt para calcular a diferença em minutos (v[5])
		KmFromCurrent float64	// Distância da localização da transação anterior para a atual. Usado na dimensão v[6]
	}
	
	// Informações do estabelecimento
	Merchant struct {
		ID string				// Identificador único do estabelecimento. Comparado com a lista KnowMerchants do cliente.
		MCC string				// Merchant Category Code. Usado para buscar o nível de risco (v[12]) no mapa MccRisk
		AvgAmount float64		// Valor médio das transações do estabelecimento. Usado na dimensão v[13] (normalizado pelo MaxMerchantAvg)
	}
}

// Garante que os valores fiquem entre 0 e 1.
func clamp(v float32) float32 {
	if v < 0 {
		return 0
	} else if v > 1 {
		return 1
	}
	return v
}

// Coração da detecção.
// Transforma uma estrutura de dados complexa em um vetor de 14 dimensões.
func (n *Normalizer) Vectorize(tx *Transaction) [14]float32 {
	var v [14]float32

	// v[0] -> Valor da transação normalizado pelo valor máximo observado
	// v[1] -> Número de parcelas normalizado pelo máximo observado
	// v[2] -> Razão entre o valor atual e a média do cliente
	// v[3] -> Hora do dia normalizada (0-23 → 0-1)
	// v[4] -> Dia da semana normalizado (0-6 → 0-1)
	// v[5] -> Minutos desde a última transação (normalizado pelo máximo observado)
	// v[6] -> Distância da última transação em km (normalizada pelo máximo observado)
	// v[7] -> Distância do terminal até a casa do cliente (normalizada pelo máximo observado)
	// v[8] -> Número de transações do cliente nas últimas 24h (normalizado pelo máximo observado)
	// v[9] -> 1 se for transação online, 0 se for física
	// v[10] -> 1 se o cartão estava presente, 0 se não
	// v[11] -> 1 se o estabelecimento é conhecido pelo cliente, 0 se não
	// v[12] -> Nível de risco do MCC (normalizado pelo máximo observado)
	// v[13] -> Valor médio do estabelecimento (normalizado pelo máximo observado)

	requestedAt, _ := time.Parse(time.RFC3339, tx.RequestedAt)	// Converte a string de data para um objeto TIme do Go.
	hour := float32(requestedAt.UTC().Hour())	// Extrai a hora do dia (0-23) para normalização posterior.

	// Ajuste: (Go_weekday + 6) % 7 para seguir spec da rinha (seg=0)
	dow := float32((int(requestedAt.UTC().Weekday()) + 6) % 7)	// Calcula o dia da semana (0-6)

	v[0] = clamp(float32(tx.Amount) / n.MaxAmount)	// Valor bruto normalizado pelo valor máximo observado
	v[1] = clamp(float32(tx.Installments) / n.MaxInstallments)	// Número de parcelas normalizadas pelo máximo observado

	// Proteção contra divisão por zero
	if tx.Customer.AvgAmount > 0 {
		// Ratio: quão fora do padrão é este gasto?
		v[2] = clamp(float32(tx.Amount/tx.Customer.AvgAmount) / n.AmountVsAvgRatio)
	}

	v[3] = hour / 23.0	// Normaliza a hora (0-23) para (0-1)
	v[4] = dow / 6.0	// Normaliza o dia da semana (0-6) para (0-1)

	// Verifica se há transação anterior
	if tx.LastTransaction == nil {	// Não tem transação anterior
		v[5] = -1	// Negatico -> indicam dados ausentes, não zero.
		v[6] = -1
	} else {
		lastAt, _ := time.Parse(time.RFC3339, tx.LastTransaction.Timestamp)	// Parse do tempo anterior
		minutes := float32(requestedAt.Sub(lastAt).Minutes())				// Calcula tempo decorrido
		v[5] = clamp(minutes / n.MaxMinutes)								// Normaliza tempo
		v[6] = clamp(float32(tx.LastTransaction.KmFromCurrent) / n.MaxKm)	// Normaliza distância
	}

	v[7] = clamp(float32(tx.Terminal.KmFromHome) / n.MaxKm)			// Distância da casa
	v[8] = clamp(float32(tx.Customer.TxCount24h) / n.MaxTxCount24h)	// Volume de compras recente
	if tx.Terminal.IsOnline {
		v[9] = 1	// Binário 1 se online
	}
	if tx.Terminal.CardPresent {
		v[10] = 1	// Binário 1 se presencial
	}

	// Verifica se o cliente já comprou no estabelecimento
	v[11] = 1	// Assume "desconhecido" por padrão
	for _, k := range tx.Customer.KnowMerchants {	// Procura o estabelecimento na lista de conhecidos do cliente.
		if k == tx.Merchant.ID {
			v[11] = 0;	// Se encontrado, marca 0 (conhecido)
			break
		}
	}

	// Busca risco no mapa do Normalizer
	if risk, ok := n.MccRisk[tx.Merchant.MCC]; ok {
		v[12] = risk
	} else {
		v[12] = 0.5	// Valor neutro para categorias desconhecidas
	}

	// Risco baseado na média do estabelecimento
	v[13] = clamp(float32(tx.Merchant.AvgAmount) / n.MaxMerchantAvg)

	return v	// Retorna o vetor pronto para busca
}