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
```mermaid
flowchart TB
    subgraph LB[Load Balancer]
        direction TB
        lb[lb<br/>0.05 CPU, 8MB]
        style lb fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
    end
    
    subgraph Workers[API Workers]
        direction TB
        subgraph Worker1[API Worker 1]
            direction TB
            server1[server<br/>0.475 CPU, 171MB]
            style server1 fill:#fff3e0,stroke:#ef6c00,stroke-width:2px
        end
        subgraph Worker2[API Worker 2]
            direction TB
            server2[server<br/>0.475 CPU, 171MB]
            style server2 fill:#fff3e0,stroke:#ef6c00,stroke-width:2px
        end
    end
    
    subgraph Index[Shared Index (read-only)]
        direction TB
        index[/index/]
        style index fill:#f1f8e9,stroke:#33691e,stroke-width:2px
        subgraph Partitions[Partitions]
            direction TB
            p0[index_p0.bin]
            p1[index_p1.bin]
            p2[index_p2.bin]
            p3[index_p3.bin]
            p4[index_p4.bin]
            p5[index_p5.bin]
            p6[index_p6.bin]
            p7[index_p7.bin]
            p8[index_p8.bin]
            p9[index_p9.bin]
            p10[index_p10.bin]
            p11[index_p11.bin]
            style p0 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p1 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p2 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p3 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p4 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p5 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p6 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p7 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p8 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p9 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p10 fill:#f9f9f9,stroke:#333,stroke-width:1px
            style p11 fill:#f9f9f9,stroke:#333,stroke-width:1px
        end
    end
    
    lb -->|SCM_RIGHTS| server1
    lb -->|SCM_RIGHTS| server2
    server1 -->|reads| index
    server2 -->|reads| index
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