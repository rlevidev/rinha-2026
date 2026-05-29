# Stage 1: compila os binários Go
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/api          ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/lb           ./cmd/lb
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/build-index  ./cmd/build-index

# Stage 2: constrói o índice HNSW durante o docker build
# Roda uma vez, resultado embutido na imagem
# Startup da API: instantâneo (só mmap)
FROM golang:1.26-alpine AS index-builder
COPY --from=builder /bin/build-index /build-index
COPY resources/ /resources/
RUN /build-index \
    --input  /resources/references.json.gz \
    --output /index.bin \
    --m      8 \
    --ef-construct 200 \
    --ef     10
# Este stage leva ~5-10 minutos, mas roda uma vez no seu Mac
# O avaliador recebe a imagem com o índice já pronto

# Stage 3: imagem final mínima
FROM scratch
COPY --from=builder    /bin/api  /api
COPY --from=builder    /bin/lb   /lb
COPY --from=index-builder /index.bin /index.bin
COPY resources/mcc_risk.json    /resources/mcc_risk.json
COPY resources/normalization.json /resources/normalization.json

EXPOSE 8080 9999
