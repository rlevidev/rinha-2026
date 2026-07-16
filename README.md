# rinha-2026
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)
![GOEXPERIMENT](https://img.shields.io/badge/GOEXPERIMENT-simd-blueviolet)
![Ranking](https://img.shields.io/badge/Rinha%202026-30%C2%BA%20lugar-gold)
![Score](https://img.shields.io/badge/Score-5.819,38-brightgreen)

> Serviço de detecção de fraude com busca vetorial para Rinha de Backend 2026.

## Sobre
Serviço de detecção de fraude escrito em Go 1.26 com mecanismo de busca vetorial IVF k-NN otimizado com SIMD.
Processa scores de fraude de transações de cartão de crédito vetorizando características e realizando busca por vizinhos mais próximos aproximados
em um dataset pré-computado de 100k transações, projetado para as restrições da Rinha de Backend 2026.

## Instalação

### Pré-requisitos
- Go 1.26+ com `GOEXPERIMENT=simd` habilitado
- CPU com suporte a AVX2 (necessário para kernels SIMD)
- Docker (para testar configuração de produção)
- Linux com capacidades `CAP_SYS_NICE` e `CAP_IPC_LOCK` (necessário para prioridade em tempo real `SCHED_FIFO` e travamento de memória `mlockall` fora do Docker)
- Make (opcional, para comandos de conveniência)

```bash
# Ativar experimentos do Go
export GOEXPERIMENT=simd

# Clonar repositório
git clone https://github.com/rlevidev/rinha-2026.git
cd rinha-2026

# Baixar dependências
go mod download
```

## Uso
```bash
# Construir todos os binários
make build

# Iniciar ambiente de desenvolvimento (LB + 2 workers de API)
make dev-up

# Enviar requisição de teste para o serviço local
make test-request

# Visualizar logs do serviço
make logs
```

## Variáveis de Ambiente
| Chave               | Valor Padrão | Descrição                                  |
|---------------------|--------------|--------------------------------------------|
| GOEXPERIMENT        | simd         | Ativar experimentos do Go para suporte a SIMD |
| (Nota: Configuração de runtime é via argumentos de linha de comando e volumes montados, não variáveis de ambiente.) |

## Arquitetura
```
┌─────────────────┐    SCM_RIGHTS    ┌──────────────┐    SCM_RIGHTS    ┌──────────────┐
│   Load Balancer │◄───────┐   ┌────►│   API Worker 1│◄───────┐   ┌────►│   API Worker 2│
│   (lb)          │        │   │    │  (server)     │        │   │    │  (server)     │
│                 │   ┌────┴───┤    │               │        │   │    │               │
│ 0.05 CPU, 8MB   │   │        │    │ 0.475 CPU,    │        │   │    │ 0.475 CPU,    │
│                 │   │ 171MB  │    │ 171MB         │        │   │    │ 171MB         │
└─────────────────┘   │        │    │               │        │   │    │               │
                      │        ▼    │               │        ▼   │    │               │
                      │  ┌──────────┴───────────────┴──────────┴────┴──────────────┐ │
                      │  │                    Shared Index (read-only)             │ │
                      │  │                                                        │ │
                      │  │  /index/                                               │ │
                      │  │  ├── index_p0.bin  (card_present=0, is_online=0)      │ │
                      │  │  ├── index_p1.bin  (card_present=0, is_online=1)      │ │
                      │  │  ├── index_p2.bin  (card_present=1, is_online=0)      │ │
                      │  │  ├── index_p3.bin  (card_present=1, is_online=1)      │ │
                      │  │  ├── index_p4.bin  (!known_merchant, has_last_tx=0)   │ │
                      │  │  ├── index_p5.bin  (!known_merchant, has_last_tx=1)   │ │
                      │  │  ├── index_p6.bin  (known_merchant, has_last_tx=0)    │ │
                      │  │  ├── index_p7.bin  (known_merchant, has_last_tx=1)    │ │
                      │  │  ├── index_p8.bin  (unknown_merchant, has_last_tx=0)  │ │
                      │  │  └── ...                                               │ │
                      │  └────────────────────────────────────────────────────────┘ │
                      │                                                            │
                      │  0.475 CPU, 171MB       0.475 CPU, 171MB                   │
                      │                                                            │
                      └────────────────────────────────────────────────────────────┘
```

## Desenvolvimento
### Executando testes
```bash
# Execute todos os testes com detector de corrida
go test -race ./...

# Execute testes para pacote específico
go test -race ./internal/fraud
go test -race ./internal/index
```

### Executando benchmarks
```bash
# Execute benchmarks
go test -bench=. ./...

# Execute benchmarks com perfil de alocação de memória
go test -bench=. -benchmem ./...
```

### Ambiente de desenvolvimento
```bash
# Inicie o ambiente de desenvolvimento (LB + 2 workers de API)
make dev-up

# Pare o ambiente de desenvolvimento
make down
```

## Licença
Este projeto está licenciado sob os termos da licença MIT. Veja o arquivo [LICENSE](./LICENSE) para mais detalhes.